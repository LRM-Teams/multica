package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/internal/cli"
)

// recoverInterruptedMachineUpgrade resumes the first incomplete durable phase
// after authenticated startup. A phase is written only after its side effect
// succeeds, so recovery never repeats a completed stage or verification.
func (d *Daemon) recoverInterruptedMachineUpgrade(ctx context.Context) (bool, error) {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil {
		return false, err
	}
	if cleared, err := d.reconcileFailedPreActivationMachineUpgrade(ctx, journal); err != nil {
		return false, err
	} else if cleared {
		return false, nil
	}

	runningSource := daemonVersionsMatch(d.cfg.CLIVersion, journal.SourceVersion)
	runningTarget := daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion)
	switch journal.Phase {
	case "takeover_committed":
		if !runningTarget {
			return false, fmt.Errorf("takeover_committed journal requires target %s, running %s", journal.TargetVersion, d.cfg.CLIVersion)
		}
		// The detached coordinator resumes normal startup from this durable
		// marker. Recovery must not schedule a second activation or restart.
		return false, nil
	case "candidate_ready":
		if !runningTarget {
			if !runningSource {
				superseded, err := d.machineUpgradeJournalSupersededByActive(journal)
				if err != nil {
					return false, fmt.Errorf("inspect superseding Active generation: %w", err)
				}
				if superseded {
					if d.logger != nil {
						d.logger.Warn("retaining Machine Upgrade journal superseded by explicit activation",
							"journal_phase", journal.Phase,
							"source_version", journal.SourceVersion,
							"target_version", journal.TargetVersion,
							"incumbent_generation", journal.IncumbentGeneration,
							"running_version", d.cfg.CLIVersion,
						)
					}
					return false, nil
				}
			}
			return false, fmt.Errorf("candidate_ready journal requires target %s, running %s", journal.TargetVersion, d.cfg.CLIVersion)
		}
		return false, nil
	case "handoff":
		if runningTarget {
			return false, nil
		}
		if !runningSource {
			return false, fmt.Errorf("handoff journal requires source %s or target %s, running %s", journal.SourceVersion, journal.TargetVersion, d.cfg.CLIVersion)
		}
	case "accepted", "staged":
		if !runningSource {
			return false, fmt.Errorf("%s journal requires source %s, running %s", journal.Phase, journal.SourceVersion, d.cfg.CLIVersion)
		}
	case "rollback_pending":
		if strings.TrimSpace(journal.RollbackGeneration) == "" {
			return false, fmt.Errorf("rollback_pending journal has no rollback generation")
		}
		if runningSource {
			return false, d.resumeMachineUpgradeRollback(ctx, journal)
		}
		if !runningTarget {
			return false, fmt.Errorf("rollback_pending journal requires source %s or target %s, running %s", journal.SourceVersion, journal.TargetVersion, d.cfg.CLIVersion)
		}
		path, err := d.PrepareMachineUpgradeRollbackRestart(ctx)
		if err != nil {
			return false, err
		}
		if strings.TrimSpace(path) != "" {
			d.restartBinary = path
		}
		d.triggerRestart()
		return true, nil
	default:
		return false, fmt.Errorf("unknown Machine Upgrade recovery phase %q", journal.Phase)
	}
	if journal.TargetRestartAttempts >= machineUpgradeMaxRestartAttempts {
		return false, fmt.Errorf("target restart attempts exhausted (%d); explicit operator recovery required", journal.TargetRestartAttempts)
	}

	if err := d.beginMachineUpgradeHandoff(ctx); err != nil {
		return false, err
	}
	runtimeID := ""
	if len(journal.RuntimeIDs) > 0 {
		runtimeID = strings.TrimSpace(journal.RuntimeIDs[0])
	}

	stagedOutput := ""
	if journal.Phase == "accepted" {
		d.reportRecoveredMachineUpgradeProgress(ctx, runtimeID, journal.ID, "staging")
		stage := d.machineUpgradeStageFn
		if stage == nil {
			stage = d.runStageUpdate
		}
		stagedOutput, err = stage(journal.TargetVersion)
		if err != nil {
			return false, fmt.Errorf("resume staging: %w", err)
		}
		journal.Phase = "staged"
		if err := d.writeMachineUpgradeJournal(journal); err != nil {
			return false, fmt.Errorf("persist resumed staging: %w", err)
		}
	}

	if journal.Phase == "staged" {
		d.reportRecoveredMachineUpgradeProgress(ctx, runtimeID, journal.ID, "verifying")
		verify := d.machineUpgradeVerifyFn
		if verify == nil {
			verify = d.verifyStagedBinary
		}
		if _, err := verify(journal.TargetVersion, stagedOutput); err != nil {
			return false, fmt.Errorf("resume verification: %w", err)
		}
		d.reportRecoveredMachineUpgradeProgress(ctx, runtimeID, journal.ID, "handoff")
		journal.Phase = "handoff"
		if err := d.writeMachineUpgradeJournal(journal); err != nil {
			return false, fmt.Errorf("persist resumed handoff: %w", err)
		}
	}

	activate := d.activateStagedFn
	if activate == nil {
		activate = d.commitStagedActivation
	}
	path, err := activate(ctx, journal.ID, journal.TargetVersion)
	if err != nil {
		return false, fmt.Errorf("resume activation: %w", err)
	}
	if d.activateStagedFn == nil {
		if err := d.captureCommittedMachineUpgradeGeneration(journal); err != nil {
			return false, err
		}
	}
	if strings.TrimSpace(path) != "" {
		d.restartBinary = path
	}
	journal.TargetRestartAttempts++
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		return false, fmt.Errorf("persist target restart attempt: %w", err)
	}
	d.mu.Lock()
	d.machineUpgradeTarget = journal.TargetVersion
	d.mu.Unlock()
	d.triggerRestart()
	return true, nil
}

// reconcileFailedPreActivationMachineUpgrade settles an operation that the
// server has already rejected before Active could be mutated. Later phases
// retain their marker because they require successor or rollback proof.
func (d *Daemon) reconcileFailedPreActivationMachineUpgrade(ctx context.Context, journal *machineUpgradeJournal) (bool, error) {
	if journal == nil || (journal.Phase != "accepted" && journal.Phase != "staged") {
		return false, nil
	}
	if d.client == nil || len(journal.RuntimeIDs) == 0 {
		return false, nil
	}
	receipt, err := d.client.GetMachineUpgradeReceipt(ctx, strings.TrimSpace(journal.RuntimeIDs[0]), journal.ID)
	if err != nil {
		return false, fmt.Errorf("read pre-activation failure receipt: %w", err)
	}
	if receipt == nil || receipt.Phase != "failed" {
		return false, nil
	}
	if !machineUpgradeFailureReceiptMatchesJournal(receipt, journal) {
		return false, fmt.Errorf("failed terminal receipt does not match pre-activation journal")
	}
	if err := d.compareAndClearMachineUpgradeJournal(journal); err != nil {
		return false, err
	}
	return true, nil
}

func machineUpgradeFailureReceiptMatchesJournal(receipt *MachineUpgradeReceipt, journal *machineUpgradeJournal) bool {
	return receipt != nil && journal != nil && receipt.ID == journal.ID &&
		receipt.AcceptedGeneration != nil && *receipt.AcceptedGeneration == journal.Generation &&
		receipt.ResolvedTarget != nil && daemonVersionsMatch(*receipt.ResolvedTarget, journal.TargetVersion) &&
		sameStringSet(receipt.AcceptedRuntimeIDs, journal.RuntimeIDs) &&
		sameStringSet(receipt.AcceptedWorkspaceIDs, journal.WorkspaceIDs)
}

// resumeMachineUpgradeRollback repairs the crash window where the local source
// was restored before the server entered rollback_pending. The restored owner
// first publishes the rollback generation, then repeats the accepted Workspace
// registrations so the server can collect its normal per-Runtime proof.
func (d *Daemon) resumeMachineUpgradeRollback(ctx context.Context, journal *machineUpgradeJournal) error {
	if journal == nil || journal.Phase != "rollback_pending" || len(journal.RuntimeIDs) == 0 || strings.TrimSpace(journal.RollbackGeneration) == "" {
		return fmt.Errorf("rollback recovery identity is incomplete")
	}
	if d.client == nil {
		return fmt.Errorf("rollback recovery client is unavailable")
	}
	runtimeID := strings.TrimSpace(journal.RuntimeIDs[0])
	receipt, err := d.client.GetMachineUpgradeReceipt(ctx, runtimeID, journal.ID)
	if err != nil {
		return fmt.Errorf("read rollback recovery receipt: %w", err)
	}
	if receipt != nil && receipt.Phase == "failed" {
		// A pre-CAS candidate rejection is already terminal. Local source restore
		// is sufficient; do not manufacture a remote rollback transition.
		return nil
	}
	if err := d.client.ReportMachineUpgradeRollback(ctx, runtimeID, journal.ID, journal.RollbackGeneration, "candidate_takeover_failed", "restored source resumed rollback"); err != nil {
		return fmt.Errorf("publish restored rollback generation: %w", err)
	}
	for _, workspaceID := range journal.WorkspaceIDs {
		if _, err := d.registerRuntimesForWorkspace(ctx, workspaceID); err != nil {
			return fmt.Errorf("re-register restored Workspace %s: %w", workspaceID, err)
		}
	}
	return nil
}

// machineUpgradeJournalSupersededByActive distinguishes an explicit
// VersionStore activation from an arbitrary executable replacement. A later
// Active generation matching this process proves another serialized installer
// mutation superseded the retained operation. The old journal remains intact
// for diagnosis and terminal-receipt reconciliation; recovery only stops
// replaying its obsolete side effects.
func (d *Daemon) machineUpgradeJournalSupersededByActive(journal *machineUpgradeJournal) (bool, error) {
	_ = journal
	return false, nil
}

func (d *Daemon) captureCommittedMachineUpgradeGeneration(journal *machineUpgradeJournal) error {
	if journal == nil {
		return fmt.Errorf("Machine Upgrade journal is required")
	}
	return nil
}

// PrepareMachineUpgradeRollbackRestart exact-restores the retained source and
// durably reserves one bounded restart attempt. The caller owns launching or
// re-execing the returned immutable binary path.
func (d *Daemon) PrepareMachineUpgradeRollbackRestart(ctx context.Context) (string, error) {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil {
		return "", err
	}
	if journal == nil || journal.Phase != "rollback_pending" || strings.TrimSpace(journal.RollbackGeneration) == "" {
		return "", fmt.Errorf("rollback_pending Machine Upgrade identity is unavailable")
	}
	if journal.RollbackRestartAttempts >= machineUpgradeMaxRestartAttempts {
		return "", fmt.Errorf("rollback restart attempts exhausted (%d); explicit operator recovery required", journal.RollbackRestartAttempts)
	}
	restore := d.machineUpgradeRollbackFn
	if restore == nil {
		restore = d.restoreMachineUpgradeSource
	}
	path, err := restore(ctx, journal)
	if err != nil {
		return "", fmt.Errorf("restore exact Machine Upgrade source: %w", err)
	}
	journal.RollbackRestartAttempts++
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		return "", fmt.Errorf("persist rollback restart attempt: %w", err)
	}
	d.mu.Lock()
	d.machineUpgradeTarget = journal.SourceVersion
	d.mu.Unlock()
	return path, nil
}

func (d *Daemon) restoreMachineUpgradeSource(_ context.Context, journal *machineUpgradeJournal) (string, error) {
	if journal == nil {
		return "", fmt.Errorf("Machine Upgrade rollback journal is required")
	}
	installPath, err := cli.InstallPath()
	if err != nil {
		return "", err
	}
	if err := cli.RollbackExecutable(installPath); err != nil {
		return "", err
	}
	return installPath, nil
}

func (d *Daemon) reportRecoveredMachineUpgradeProgress(ctx context.Context, runtimeID, upgradeID, phase string) {
	if d.client == nil || runtimeID == "" {
		return
	}
	if err := d.client.ReportMachineUpgradeProgress(ctx, runtimeID, upgradeID, phase, "", ""); err != nil && d.logger != nil {
		d.logger.Warn("recovered Machine Upgrade progress rejected", "upgrade_id", upgradeID, "phase", phase, "error", err)
	}
}

// reconcileMachineUpgradeTerminalJournal removes only the exact operation
// marker for which the server has returned terminal proof and this process can
// independently prove the matching live version, generation, and runtime set.
func (d *Daemon) reconcileMachineUpgradeTerminalJournal(ctx context.Context) error {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil {
		return err
	}
	if journal.Phase != "candidate_ready" && journal.Phase != "rollback_pending" {
		return nil
	}
	if d.client == nil || len(journal.RuntimeIDs) == 0 {
		return fmt.Errorf("terminal receipt client identity is unavailable")
	}
	runtimeID := strings.TrimSpace(journal.RuntimeIDs[0])
	receipt, err := d.client.GetMachineUpgradeReceipt(ctx, runtimeID, journal.ID)
	if err != nil {
		return err
	}
	if receipt == nil || receipt.ID != journal.ID || !sameStringSet(receipt.AcceptedRuntimeIDs, journal.RuntimeIDs) || !sameStringSet(d.allRuntimeIDs(), journal.RuntimeIDs) {
		return fmt.Errorf("terminal receipt operation or live runtime set mismatch")
	}
	switch receipt.Phase {
	case "completed":
		if journal.Phase != "candidate_ready" || receipt.AcceptedGeneration == nil || *receipt.AcceptedGeneration != journal.Generation ||
			receipt.ResolvedTarget == nil || !daemonVersionsMatch(*receipt.ResolvedTarget, journal.TargetVersion) ||
			!daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion) || d.cfg.ComputerGeneration <= journal.PredecessorComputerGeneration ||
			!sameStringSet(receipt.AttestedRuntimeIDs, journal.RuntimeIDs) || !sameStringSet(receipt.AcceptedWorkspaceIDs, journal.WorkspaceIDs) ||
			!sameStringSet(receipt.AttestedWorkspaceIDs, journal.WorkspaceIDs) {
			return fmt.Errorf("completed terminal receipt does not match live successor proof")
		}
	case "rolled_back":
		if journal.Phase != "rollback_pending" || receipt.RollbackGeneration == nil || *receipt.RollbackGeneration != journal.RollbackGeneration ||
			receipt.SourceVersion == nil || !daemonVersionsMatch(*receipt.SourceVersion, journal.SourceVersion) ||
			!daemonVersionsMatch(d.cfg.CLIVersion, journal.SourceVersion) || !sameStringSet(receipt.RollbackRuntimeIDs, journal.RuntimeIDs) {
			return fmt.Errorf("rolled_back terminal receipt does not match live restored proof")
		}
	case "failed":
		if journal.Phase != "rollback_pending" || !daemonVersionsMatch(d.cfg.CLIVersion, journal.SourceVersion) {
			return fmt.Errorf("failed terminal receipt does not match locally restored source")
		}
	default:
		return nil
	}
	return d.compareAndClearMachineUpgradeJournal(journal)
}

func (d *Daemon) compareAndClearMachineUpgradeJournal(expected *machineUpgradeJournal) error {
	if expected == nil || strings.TrimSpace(expected.ID) == "" || filepath.Base(expected.ID) != expected.ID {
		return fmt.Errorf("exact Machine Upgrade marker identity is required")
	}
	dir, err := d.machineUpgradeJournalDir()
	if err != nil {
		return err
	}
	path := filepath.Join(dir, expected.ID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var current machineUpgradeJournal
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	if current.ID != expected.ID || current.Generation != expected.Generation || current.RollbackGeneration != expected.RollbackGeneration || current.Phase != expected.Phase || current.UpdatedAt != expected.UpdatedAt {
		return fmt.Errorf("Machine Upgrade marker changed before compare-and-clear")
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	if handle, err := os.Open(dir); err == nil {
		defer handle.Close()
		return handle.Sync()
	}
	return nil
}
