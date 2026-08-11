package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// recoverInterruptedMachineUpgrade resumes the first incomplete durable phase
// after authenticated startup. A phase is written only after its side effect
// succeeds, so recovery never repeats a completed stage or verification.
func (d *Daemon) recoverInterruptedMachineUpgrade(ctx context.Context) (bool, error) {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil {
		return false, err
	}

	runningSource := daemonVersionsMatch(d.cfg.CLIVersion, journal.SourceVersion)
	runningTarget := daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion)
	switch journal.Phase {
	case "candidate_ready":
		if !runningTarget {
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
			// The restored source now proceeds through normal authenticated runtime
			// registration, which owns server-side rolled_back attestation.
			return false, nil
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

func (d *Daemon) captureCommittedMachineUpgradeGeneration(journal *machineUpgradeJournal) error {
	if journal == nil {
		return fmt.Errorf("Machine Upgrade journal is required")
	}
	root, err := versionStoreRootFn()
	if err != nil {
		return err
	}
	store, err := openVersionStoreFn(root)
	if err != nil {
		return err
	}
	state, err := store.ReadActivationState()
	if err != nil {
		return err
	}
	if state.Generation == 0 || !daemonVersionsMatch(state.ActiveVersion, journal.TargetVersion) {
		return fmt.Errorf("committed Active does not match Machine Upgrade target")
	}
	journal.IncumbentGeneration = state.Generation - 1
	return d.writeMachineUpgradeJournal(journal)
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

func (d *Daemon) restoreMachineUpgradeSource(ctx context.Context, journal *machineUpgradeJournal) (string, error) {
	if journal == nil {
		return "", fmt.Errorf("Machine Upgrade rollback journal is required")
	}
	root, err := versionStoreRootFn()
	if err != nil {
		return "", err
	}
	store, err := openVersionStoreFn(root)
	if err != nil {
		return "", err
	}
	attemptID := "machine-upgrade-rollback:" + journal.ID + ":" + journal.RollbackGeneration
	_, path, err := store.RestoreMachineUpgradeSource(
		ctx,
		journal.IncumbentGeneration,
		journal.SourceVersion,
		journal.TargetVersion,
		attemptID,
	)
	return path, err
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
