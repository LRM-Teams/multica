package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

const machineUpgradeGracefulDrain = 10 * time.Second

var fetchLatestMachineUpgradeRelease = cli.FetchLatestRelease

// machineUpgradeJournal is the daemon-local recovery record written before a
// release mutation. Its generation is deliberately independent of the server
// request id: the successor reads the same value after handoff and therefore
// cannot satisfy convergence with a newly-minted process identity.
type machineUpgradeJournal struct {
	ID                  string   `json:"id"`
	Generation          string   `json:"generation"`
	SourceVersion       string   `json:"source_version"`
	TargetVersion       string   `json:"target_version"`
	IncumbentGeneration uint64   `json:"incumbent_generation"`
	RollbackGeneration  string   `json:"rollback_generation,omitempty"`
	RuntimeIDs          []string `json:"runtime_ids"`
	Phase               string   `json:"phase"`
	UpdatedAt           string   `json:"updated_at"`
}

func (d *Daemon) machineUpgradeGenerationID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.TrimSpace(d.machineUpgradeGeneration) == "" {
		if journal, err := d.currentMachineUpgradeJournal(); err == nil && journal != nil && daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion) && (journal.Phase == "handoff" || journal.Phase == "candidate_ready") {
			d.machineUpgradeGeneration = journal.Generation
		}
	}
	if strings.TrimSpace(d.machineUpgradeGeneration) == "" {
		d.machineUpgradeGeneration = uuid.NewString()
	}
	return d.machineUpgradeGeneration
}

func (d *Daemon) markMachineUpgradeCandidateReady() error {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil {
		return err
	}
	if !daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion) || strings.TrimSpace(journal.Generation) != d.machineUpgradeGenerationID() {
		return nil
	}
	if journal.Phase == "candidate_ready" {
		return nil
	}
	if journal.Phase != "handoff" {
		return fmt.Errorf("machine upgrade journal phase %q cannot become candidate_ready", journal.Phase)
	}
	journal.Phase = "candidate_ready"
	return d.writeMachineUpgradeJournal(journal)
}

func (d *Daemon) machineUpgradeRollbackGenerationID() string {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil || journal.Phase != "rollback_pending" || !daemonVersionsMatch(d.cfg.CLIVersion, journal.SourceVersion) {
		return ""
	}
	if strings.TrimSpace(journal.RollbackGeneration) == "" {
		journal.RollbackGeneration = uuid.NewString()
		if d.writeMachineUpgradeJournal(journal) != nil {
			return ""
		}
	}
	return journal.RollbackGeneration
}

// handleMachineUpgrade accepts a machine operation once, journals its durable
// receipt, then either proves an already-current binary or stages/verifies and
// hands off a supervised successor. Completion remains server-owned sibling
// convergence and is never emitted from this incumbent process.
func (d *Daemon) handleMachineUpgrade(ctx context.Context, runtimeID string, upgrade *PendingMachineUpgrade) {
	if upgrade == nil {
		return
	}
	d.mu.Lock()
	d.machineUpgradeID = upgrade.ID
	d.machineUpgradeRuntimeID = runtimeID
	d.mu.Unlock()
	targetVersion, err := resolveMachineUpgradeTarget(upgrade.TargetVersion)
	if err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "target_resolution_failed", err)
		return
	}
	receipt, err := d.client.AcceptMachineUpgrade(ctx, runtimeID, upgrade.ID, d.machineUpgradeGenerationID(), d.cfg.CLIVersion, targetVersion)
	if err != nil {
		d.logger.Warn("machine upgrade acceptance failed", "runtime_id", runtimeID, "upgrade_id", upgrade.ID, "error", err)
		return
	}
	if daemonVersionsMatch(d.cfg.CLIVersion, targetVersion) {
		d.reregisterMachineUpgrade(ctx, runtimeID, upgrade.ID)
		return
	}
	journal, err := d.createMachineUpgradeJournal(receipt, d.cfg.CLIVersion, targetVersion)
	if err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "journal_persist_failed", err)
		return
	}
	// The handoff barrier is set before staging so no claim can cross the
	// mutation boundary unaccounted. Busy work is cancelled gracefully first,
	// then only daemon-owned processes still alive after the bounded drain may
	// be force-terminated.
	if err := d.beginMachineUpgradeHandoff(ctx); err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "handoff_failed", err)
		return
	}
	restartScheduled := false
	defer func() {
		if !restartScheduled {
			d.releaseClaimBarrier()
		}
	}()
	if err := d.client.ReportMachineUpgradeProgress(ctx, runtimeID, upgrade.ID, "staging", "", ""); err != nil {
		d.logger.Warn("machine upgrade staging progress rejected", "upgrade_id", upgrade.ID, "error", err)
		return
	}
	stagedOutput, err := d.runStageUpdate(targetVersion)
	if err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "stage_failed", err)
		return
	}
	journal.Phase = "staged"
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "journal_persist_failed", err)
		return
	}
	_ = d.client.ReportMachineUpgradeProgress(ctx, runtimeID, upgrade.ID, "verifying", "", "")
	if _, err := d.verifyStagedBinary(targetVersion, stagedOutput); err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "verification_failed", err)
		return
	}
	if err := d.client.ReportMachineUpgradeProgress(ctx, runtimeID, upgrade.ID, "handoff", "", ""); err != nil {
		d.logger.Warn("machine upgrade handoff progress rejected", "upgrade_id", upgrade.ID, "error", err)
		return
	}
	journal.Phase = "handoff"
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "journal_persist_failed", err)
		return
	}
	path, err := d.commitStagedActivation(ctx, upgrade.ID, targetVersion)
	if err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "activation_failed", err)
		return
	}
	d.restartBinary = path
	d.mu.Lock()
	d.machineUpgradeTarget = targetVersion
	d.mu.Unlock()
	restartScheduled = true
	d.triggerRestart()
}

// MarkMachineUpgradeRollbackPending retains the operation marker while the
// launcher restores the previous Active generation after a failed detached
// candidate. It does not claim server-side rollback completion.
func (d *Daemon) MarkMachineUpgradeRollbackPending() error {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil {
		return err
	}
	journal.Phase = "rollback_pending"
	if strings.TrimSpace(journal.RollbackGeneration) == "" {
		journal.RollbackGeneration = uuid.NewString()
	}
	return d.writeMachineUpgradeJournal(journal)
}

func resolveMachineUpgradeTarget(requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "latest" {
		if requested == "" {
			return "", fmt.Errorf("machine upgrade target is required")
		}
		return requested, nil
	}
	release, err := fetchLatestMachineUpgradeRelease()
	if err != nil {
		return "", fmt.Errorf("resolve latest machine upgrade target: %w", err)
	}
	if release == nil || strings.TrimSpace(release.TagName) == "" {
		return "", fmt.Errorf("resolve latest machine upgrade target: empty release")
	}
	return strings.TrimSpace(release.TagName), nil
}

func (d *Daemon) registerMachineUpgradeTask(slot int, cancel context.CancelFunc) {
	if cancel == nil {
		return
	}
	d.machineUpgradeTaskMu.Lock()
	if d.machineUpgradeTaskCancels == nil {
		d.machineUpgradeTaskCancels = make(map[int64]context.CancelFunc)
	}
	d.machineUpgradeTaskCancels[int64(slot)] = cancel
	d.machineUpgradeTaskMu.Unlock()
}

func (d *Daemon) unregisterMachineUpgradeTask(slot int) {
	d.machineUpgradeTaskMu.Lock()
	delete(d.machineUpgradeTaskCancels, int64(slot))
	d.machineUpgradeTaskMu.Unlock()
}

func (d *Daemon) requestManagedTaskTermination() {
	d.machineUpgradeTaskMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(d.machineUpgradeTaskCancels))
	for _, cancel := range d.machineUpgradeTaskCancels {
		cancels = append(cancels, cancel)
	}
	d.machineUpgradeTaskMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (d *Daemon) forceTerminateManagedAgentProcesses() error {
	if d.canonicalRuntimes != nil {
		if err := d.canonicalRuntimes.forceTerminateAll(); err != nil {
			return fmt.Errorf("canonical managed runtime: %w", err)
		}
	}
	return nil
}

func (d *Daemon) machineUpgradeTimeNow() time.Time {
	if d.machineUpgradeNow != nil {
		return d.machineUpgradeNow()
	}
	return time.Now()
}

func (d *Daemon) waitMachineUpgrade(ctx context.Context, delay time.Duration) error {
	if d.machineUpgradeWait != nil {
		return d.machineUpgradeWait(ctx, delay)
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// beginMachineUpgradeHandoff owns the claim barrier until it succeeds. Every
// error releases it before returning; only a successful handoff transfers the
// held barrier to the caller through process restart. Context cancellation is
// therefore a failure boundary, never a path that can accidentally both reopen
// claims and commit activation.
func (d *Daemon) beginMachineUpgradeHandoff(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	d.setClaimBarrier()
	succeeded := false
	defer func() {
		if !succeeded {
			d.releaseClaimBarrier()
		}
	}()
	d.requestManagedTaskTermination()
	deadline := d.machineUpgradeTimeNow().Add(machineUpgradeGracefulDrain)
	for !d.claimBarrierDrained() {
		remaining := deadline.Sub(d.machineUpgradeTimeNow())
		if remaining <= 0 {
			break
		}
		if remaining > 100*time.Millisecond {
			remaining = 100 * time.Millisecond
		}
		if err := d.waitMachineUpgrade(ctx, remaining); err != nil {
			return fmt.Errorf("graceful handoff drain: %w", err)
		}
	}
	if d.claimBarrierDrained() {
		succeeded = true
		return nil
	}
	if err := d.forceTerminateManagedAgentProcesses(); err != nil {
		return fmt.Errorf("force handoff: %w", err)
	}
	succeeded = true
	return nil
}

func (d *Daemon) reregisterMachineUpgrade(ctx context.Context, runtimeID, upgradeID string) {
	workspaceID := d.workspaceIDForRuntime(runtimeID)
	if workspaceID == "" {
		d.logger.Warn("machine upgrade accepted but runtime workspace is unavailable", "runtime_id", runtimeID, "upgrade_id", upgradeID)
		return
	}
	if _, err := d.registerRuntimesForWorkspace(ctx, workspaceID); err != nil {
		d.logger.Warn("machine upgrade convergence registration failed", "workspace_id", workspaceID, "upgrade_id", upgradeID, "error", err)
	}
}

func (d *Daemon) failMachineUpgrade(ctx context.Context, runtimeID, upgradeID, code string, cause error) {
	d.logger.Error("machine upgrade failed", "runtime_id", runtimeID, "upgrade_id", upgradeID, "code", code, "error", cause)
	_ = d.client.ReportMachineUpgradeProgress(ctx, runtimeID, upgradeID, "failed", code, cause.Error())
}

func (d *Daemon) machineUpgradeJournalDir() (string, error) {
	root, err := versionStoreRootFn()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "machine-upgrades"), nil
}

func (d *Daemon) createMachineUpgradeJournal(receipt *MachineUpgradeReceipt, source, target string) (*machineUpgradeJournal, error) {
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" || receipt.AcceptedGeneration == nil || strings.TrimSpace(*receipt.AcceptedGeneration) == "" {
		return nil, fmt.Errorf("machine upgrade acceptance receipt is incomplete")
	}
	var incumbent uint64
	if root, err := versionStoreRootFn(); err == nil {
		if store, err := openVersionStoreFn(root); err == nil {
			if state, err := store.ReadActivationState(); err == nil {
				incumbent = state.Generation
			}
		}
	}
	journal := &machineUpgradeJournal{ID: receipt.ID, Generation: *receipt.AcceptedGeneration, SourceVersion: source, TargetVersion: target, IncumbentGeneration: incumbent, RuntimeIDs: append([]string(nil), receipt.AcceptedRuntimeIDs...), Phase: "accepted"}
	return journal, d.writeMachineUpgradeJournal(journal)
}

func (d *Daemon) writeMachineUpgradeJournal(journal *machineUpgradeJournal) error {
	if journal == nil || strings.TrimSpace(journal.ID) == "" {
		return fmt.Errorf("machine upgrade journal id is required")
	}
	dir, err := d.machineUpgradeJournalDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	journal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	data, err := json.Marshal(journal)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+journal.ID+"-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(data, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, filepath.Join(dir, journal.ID+".json"))
}

func (d *Daemon) currentMachineUpgradeJournal() (*machineUpgradeJournal, error) {
	dir, err := d.machineUpgradeJournalDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var newest *machineUpgradeJournal
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var candidate machineUpgradeJournal
		if json.Unmarshal(data, &candidate) != nil || (candidate.Phase != "handoff" && candidate.Phase != "candidate_ready" && candidate.Phase != "rollback_pending") {
			continue
		}
		if newest == nil || candidate.UpdatedAt > newest.UpdatedAt {
			newest = &candidate
		}
	}
	return newest, nil
}

func (d *Daemon) workspaceIDForRuntime(runtimeID string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	for workspaceID, state := range d.workspaces {
		for _, id := range state.runtimeIDs {
			if id == runtimeID {
				return workspaceID
			}
		}
	}
	return ""
}

func daemonVersionsMatch(left, right string) bool {
	left = strings.TrimPrefix(strings.TrimSpace(left), "v")
	right = strings.TrimPrefix(strings.TrimSpace(right), "v")
	return left != "" && left == right
}
