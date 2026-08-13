package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
)

const machineUpgradeGracefulDrain = 10 * time.Second

const machineUpgradeMaxRestartAttempts = 2

var fetchMachineUpgradeRelease = cli.FetchReleaseForChannelWithOverride

// machineUpgradeJournal is the daemon-local recovery record written before a
// release mutation. Its generation is deliberately independent of the server
// request id: the successor reads the same value after handoff and therefore
// cannot satisfy convergence with a newly-minted process identity.
type machineUpgradeJournal struct {
	ID                  string `json:"id"`
	Generation          string `json:"generation"`
	SourceVersion       string `json:"source_version"`
	TargetVersion       string `json:"target_version"`
	IncumbentGeneration uint64 `json:"incumbent_generation"`
	// IncumbentGenerationKnown is retained on the journal for older readers.
	// PATH swap no longer has a VersionStore generation.
	IncumbentGenerationKnown bool `json:"incumbent_generation_known,omitempty"`
	// PredecessorComputerGeneration identifies the exact resident process
	// generation that committed the handoff.
	PredecessorComputerGeneration int64    `json:"predecessor_computer_generation"`
	RollbackGeneration            string   `json:"rollback_generation,omitempty"`
	TargetRestartAttempts         int      `json:"target_restart_attempts,omitempty"`
	RollbackRestartAttempts       int      `json:"rollback_restart_attempts,omitempty"`
	RuntimeIDs                    []string `json:"runtime_ids"`
	WorkspaceIDs                  []string `json:"workspace_ids"`
	Phase                         string   `json:"phase"`
	UpdatedAt                     string   `json:"updated_at"`
}

func (d *Daemon) machineUpgradeGenerationID() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if strings.TrimSpace(d.machineUpgradeGeneration) == "" {
		if journal, err := d.currentMachineUpgradeJournal(); err == nil && journal != nil && daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion) && (journal.Phase == "handoff" || journal.Phase == "takeover_committed" || journal.Phase == "candidate_ready") {
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
	if journal.Phase != "handoff" && journal.Phase != "takeover_committed" {
		return fmt.Errorf("machine upgrade journal phase %q cannot become candidate_ready", journal.Phase)
	}
	journal.Phase = "candidate_ready"
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		return err
	}
	d.appendMachineUpgradeEvent(machineUpgradeEvent{
		Event:         machineUpgradeEventCandidateReady,
		UpgradeID:     journal.ID,
		Generation:    journal.Generation,
		SourceVersion: journal.SourceVersion,
		TargetVersion: journal.TargetVersion,
	})
	return nil
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
	targetVersion, err := resolveMachineUpgradeTargetForChannel(upgrade.TargetVersion, d.cfg.ReleaseChannel, d.releaseManifestBaseURLOverride())
	if err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "target_resolution_failed", err)
		return
	}
	receipt, err := d.client.AcceptMachineUpgrade(ctx, runtimeID, upgrade.ID, d.machineUpgradeGenerationID(), d.cfg.CLIVersion, targetVersion)
	if err != nil {
		d.logger.Warn("machine upgrade acceptance failed", "runtime_id", runtimeID, "upgrade_id", upgrade.ID, "error", err)
		d.appendMachineUpgradeEvent(machineUpgradeEvent{
			Event:         machineUpgradeEventFailed,
			UpgradeID:     upgrade.ID,
			SourceVersion: d.cfg.CLIVersion,
			TargetVersion: targetVersion,
			ErrorCode:     "acceptance_failed",
			Error:         err.Error(),
		})
		return
	}
	if daemonVersionsMatch(d.cfg.CLIVersion, targetVersion) {
		if err := d.refreshStableLauncher(); err != nil {
			d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "launcher_refresh_failed", err)
			return
		}
		d.appendMachineUpgradeEvent(machineUpgradeEvent{
			Event:         machineUpgradeEventAlreadyCurrent,
			UpgradeID:     upgrade.ID,
			SourceVersion: d.cfg.CLIVersion,
			TargetVersion: targetVersion,
		})
		if err := d.reregisterMachineUpgrade(ctx, runtimeID, upgrade.ID); err != nil {
			d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "already_current_registration_failed", err)
			return
		}
		if err := d.attestAlreadyCurrentMachineUpgrade(ctx, receipt); err != nil {
			d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "already_current_attestation_failed", err)
		}
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
	d.appendMachineUpgradeEvent(machineUpgradeEvent{
		Event:         machineUpgradeEventStaged,
		UpgradeID:     journal.ID,
		Generation:    journal.Generation,
		SourceVersion: journal.SourceVersion,
		TargetVersion: journal.TargetVersion,
	})
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
	d.appendMachineUpgradeEvent(machineUpgradeEvent{
		Event:         machineUpgradeEventHandoff,
		UpgradeID:     journal.ID,
		Generation:    journal.Generation,
		SourceVersion: journal.SourceVersion,
		TargetVersion: journal.TargetVersion,
	})
	path, err := d.commitStagedActivation(ctx, upgrade.ID, targetVersion)
	if err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "activation_failed", err)
		return
	}
	if err := d.captureCommittedMachineUpgradeGeneration(journal); err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "activation_state_mismatch", err)
		return
	}
	d.appendMachineUpgradeEvent(machineUpgradeEvent{
		Event:         machineUpgradeEventActivated,
		UpgradeID:     journal.ID,
		Generation:    journal.Generation,
		SourceVersion: journal.SourceVersion,
		TargetVersion: journal.TargetVersion,
	})
	journal.TargetRestartAttempts++
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		d.failMachineUpgrade(ctx, runtimeID, upgrade.ID, "journal_persist_failed", err)
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
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		return err
	}
	d.appendMachineUpgradeEvent(machineUpgradeEvent{
		Event:         machineUpgradeEventRollbackPending,
		UpgradeID:     journal.ID,
		Generation:    journal.Generation,
		SourceVersion: journal.SourceVersion,
		TargetVersion: journal.TargetVersion,
	})
	return nil
}

func resolveMachineUpgradeTarget(requested string) (string, error) {
	return resolveMachineUpgradeTargetForChannel(requested, string(cli.ReleaseChannelLatest), "")
}

func resolveMachineUpgradeTargetForChannel(requested, rawChannel, serverDispatched string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "latest" {
		if requested == "" {
			return "", fmt.Errorf("machine upgrade target is required")
		}
		return requested, nil
	}
	channel, err := cli.NormalizeReleaseChannel(rawChannel)
	if err != nil {
		return "", err
	}
	release, err := fetchMachineUpgradeRelease(channel, serverDispatched)
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
	d.claimMu.Lock()
	claimsInFlight := d.claimsInFlight
	d.claimMu.Unlock()
	if claimsInFlight > 0 {
		return fmt.Errorf("claim handoff deadline elapsed with %d claims still in flight", claimsInFlight)
	}
	if err := d.forceTerminateManagedAgentProcesses(); err != nil {
		return fmt.Errorf("force handoff: %w", err)
	}
	succeeded = true
	return nil
}

func (d *Daemon) reregisterMachineUpgrade(ctx context.Context, runtimeID, upgradeID string) error {
	workspaceID := d.workspaceIDForRuntime(runtimeID)
	if workspaceID == "" {
		return fmt.Errorf("machine upgrade accepted but runtime workspace is unavailable: runtime %s", runtimeID)
	}
	if _, err := d.registerRuntimesForWorkspace(ctx, workspaceID); err != nil {
		return fmt.Errorf("machine upgrade convergence registration for Workspace %s: %w", workspaceID, err)
	}
	return nil
}

// attestAlreadyCurrentMachineUpgrade closes the generation-aware Computer
// operation when the requested target already matches the resident. No new
// process will start in this path, so the normal successor-startup attestation
// cannot run. Runtime re-registration above proves the accepting Workspace;
// this Computer-level proof covers the complete captured Workspace set,
// including zero-Agent connections.
func (d *Daemon) attestAlreadyCurrentMachineUpgrade(ctx context.Context, receipt *MachineUpgradeReceipt) error {
	if receipt == nil || strings.TrimSpace(receipt.ID) == "" || receipt.AcceptedGeneration == nil || strings.TrimSpace(*receipt.AcceptedGeneration) == "" {
		return fmt.Errorf("already-current machine upgrade acceptance receipt is incomplete")
	}
	workspaceIDs := d.workspaceRunnerWorkspaceIDs()
	if !sameStringSet(receipt.AcceptedWorkspaceIDs, workspaceIDs) {
		return fmt.Errorf("already-current Workspace connection set does not match accepted complete set")
	}
	return d.client.AttestComputerUpgrade(
		ctx,
		d.cfg.DaemonID,
		receipt.ID,
		strings.TrimSpace(*receipt.AcceptedGeneration),
		d.cfg.CLIVersion,
		d.allRuntimeIDs(),
		workspaceIDs,
	)
}

func (d *Daemon) attestComputerMachineUpgrade(ctx context.Context, workspaceIDs []string) error {
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil {
		return err
	}
	// candidate_ready means this exact successor already completed the remote
	// attestation. Workspace sync is periodic, so treating it as pending would
	// turn every later sync into a duplicate completion request.
	if journal.Phase == "candidate_ready" {
		return nil
	}
	if journal.Phase != "handoff" && journal.Phase != "takeover_committed" {
		return nil
	}
	if !daemonVersionsMatch(d.cfg.CLIVersion, journal.TargetVersion) || journal.Generation != d.machineUpgradeGenerationID() {
		return nil
	}
	if len(journal.WorkspaceIDs) > 0 && !sameStringSet(journal.WorkspaceIDs, workspaceIDs) {
		return fmt.Errorf("successor Workspace connection set does not match accepted complete set")
	}
	runtimeIDs := d.allRuntimeIDs()
	sort.Strings(runtimeIDs)
	if !sameStringSet(journal.RuntimeIDs, runtimeIDs) {
		return fmt.Errorf("successor Runtime set does not match accepted complete set")
	}
	// A detached candidate may register only after the predecessor-to-candidate
	// Computer generation CAS is durably committed. Before that point Run is
	// blocked in the takeover module and cannot reach this function.
	if d.cfg.DetachedMachineUpgradeCandidate && journal.Phase != "takeover_committed" {
		return nil
	}
	if err := d.client.AttestComputerUpgrade(ctx, d.cfg.DaemonID, journal.ID, journal.Generation, d.cfg.CLIVersion, runtimeIDs, workspaceIDs); err != nil {
		return err
	}
	journal.Phase = "candidate_ready"
	return d.writeMachineUpgradeJournal(journal)
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := make(map[string]int, len(left))
	for _, value := range left {
		seen[value]++
	}
	for _, value := range right {
		if seen[value] == 0 {
			return false
		}
		seen[value]--
	}
	return true
}

func (d *Daemon) failMachineUpgrade(ctx context.Context, runtimeID, upgradeID, code string, cause error) {
	d.logger.Error("machine upgrade failed", "runtime_id", runtimeID, "upgrade_id", upgradeID, "code", code, "error", cause)
	event := machineUpgradeEvent{
		Event:         machineUpgradeEventFailed,
		UpgradeID:     upgradeID,
		SourceVersion: d.cfg.CLIVersion,
		ErrorCode:     code,
	}
	if cause != nil {
		event.Error = cause.Error()
	}
	d.appendMachineUpgradeEvent(event)
	reportError := ""
	if cause != nil {
		reportError = cause.Error()
	}
	if err := d.client.ReportMachineUpgradeProgress(ctx, runtimeID, upgradeID, "failed", code, reportError); err != nil {
		if d.logger != nil {
			d.logger.Warn("machine upgrade failure report rejected", "upgrade_id", upgradeID, "error", err)
		}
		return
	}
	journal, err := d.currentMachineUpgradeJournal()
	if err != nil || journal == nil || journal.ID != upgradeID || (journal.Phase != "accepted" && journal.Phase != "staged") {
		return
	}
	if err := d.compareAndClearMachineUpgradeJournal(journal); err != nil && d.logger != nil {
		d.logger.Warn("could not clear acknowledged pre-activation Machine Upgrade journal", "upgrade_id", upgradeID, "error", err)
	}
}

// appendMachineUpgradeEvent records one Machine Upgrade lifecycle transition
// in the machine-local upgrade history. It never fails the caller: append
// errors are logged at debug level and swallowed.
func (d *Daemon) appendMachineUpgradeEvent(event machineUpgradeEvent) {
	if d == nil || d.machineUpgradeLog == nil {
		return
	}
	if err := d.machineUpgradeLog.Append(event); err != nil && d.logger != nil {
		d.logger.Debug("machine upgrade event append failed", "event", event.Event, "error", err)
	}
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
	journal := &machineUpgradeJournal{ID: receipt.ID, Generation: *receipt.AcceptedGeneration, SourceVersion: source, TargetVersion: target, IncumbentGeneration: 0, IncumbentGenerationKnown: true, PredecessorComputerGeneration: d.cfg.ComputerGeneration, RuntimeIDs: append([]string(nil), receipt.AcceptedRuntimeIDs...), WorkspaceIDs: append([]string(nil), receipt.AcceptedWorkspaceIDs...), Phase: "accepted"}
	if err := d.writeMachineUpgradeJournal(journal); err != nil {
		return nil, err
	}
	d.appendMachineUpgradeEvent(machineUpgradeEvent{
		Event:               machineUpgradeEventAccepted,
		UpgradeID:           journal.ID,
		Generation:          journal.Generation,
		SourceVersion:       source,
		TargetVersion:       target,
		IncumbentGeneration: 0,
	})
	return journal, nil
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
		// A clean machine has no upgrade journal directory until its first
		// Machine Upgrade. That means there is nothing to attest, not that
		// ordinary Workspace synchronization failed.
		if os.IsNotExist(err) {
			return nil, nil
		}
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
		if json.Unmarshal(data, &candidate) != nil || (candidate.Phase != "accepted" && candidate.Phase != "staged" && candidate.Phase != "handoff" && candidate.Phase != "takeover_committed" && candidate.Phase != "candidate_ready" && candidate.Phase != "rollback_pending") {
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
