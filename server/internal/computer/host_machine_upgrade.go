package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type hostMachineUpgradeConfig struct {
	identity           HostProcessIdentity
	releaseManifestURL string
	residentRoot       string
	cancel             context.CancelFunc
}

// ErrComputerControlBusy is the Raft 1.0.17 Host busy signal. DaemonCore maps
// it onto computer:upgrade:done { error: "control_busy" }.
var ErrComputerControlBusy = errors.New("Computer Machine Upgrade is already running")

type hostMachineUpgrade struct {
	host   *Host
	config hostMachineUpgradeConfig

	stageRelease   func(string, time.Duration, string) (string, error)
	verifyBinary   func(context.Context, string, string) error
	installPath    func() (string, error)
	swapExecutable func(string, string) error

	mu                   sync.Mutex
	activeID             string
	initiatorWorkspaceID string
	activeCancel         context.CancelFunc
	restartBinary        string
	targetVersion        string
	manifestBaseURL      string
}

// hostMachineUpgradeJournal is the durable successor handoff marker. Write it
// after staging and verification, immediately before swapping the binary. The
// successor reconciles the marker after restart and removes it after handling
// completion.
type hostMachineUpgradeJournal struct {
	RequestID                   string   `json:"requestId"`
	FromVersion                 string   `json:"fromVersion"`
	TargetVersion               string   `json:"targetVersion"`
	StartedAt                   string   `json:"startedAt"`
	SchemaVersion               int      `json:"schemaVersion"`
	SourceServicePID            int      `json:"sourceServicePid"`
	OldRunnerPIDs               []int    `json:"oldRunnerPids"`
	AcceptedManagedWorkspaceIDs []string `json:"acceptedManagedWorkspaceIds"`
	AcceptedManagedSetRevision  string   `json:"acceptedManagedSetRevision"`
	ObservedTargetGeneration    string   `json:"observedTargetGeneration,omitempty"`
	TargetServicePID            int      `json:"targetServicePid,omitempty"`
}

func newHostMachineUpgrade(host *Host, config hostMachineUpgradeConfig) *hostMachineUpgrade {
	return &hostMachineUpgrade{
		host: host, config: config,
		manifestBaseURL: strings.TrimSpace(config.releaseManifestURL),
		stageRelease:    cli.StageReleaseScratch, verifyBinary: verifyComputerBinary,
		installPath: cli.InstallPath, swapExecutable: cli.SwapExecutable,
	}
}

// startServiceUpgrade accepts a runner-delivered cloud command and returns
// after the service has claimed machine-upgrade ownership. All package work
// continues in the Computer service process.
func (upgrade *hostMachineUpgrade) startServiceUpgrade(identity BindingChildIdentity, command protocol.ComputerUpgradePayload) error {
	command.Canonicalize()
	operationID := strings.TrimSpace(command.Operation())
	if operationID == "" {
		return errors.New("Computer upgrade request identity is required")
	}
	upgrade.mu.Lock()
	if upgrade.activeID != "" {
		active := upgrade.activeID
		upgrade.mu.Unlock()
		if active == operationID {
			return nil
		}
		return ErrComputerControlBusy
	}
	upgrade.activeID = operationID
	upgrade.initiatorWorkspaceID = identity.WorkspaceID
	upgrade.mu.Unlock()
	go upgrade.executeServiceUpgrade(identity, command)
	return nil
}

func (upgrade *hostMachineUpgrade) status() map[string]string {
	upgrade.mu.Lock()
	defer upgrade.mu.Unlock()
	if upgrade.activeID == "" {
		return map[string]string{"phase": "idle"}
	}
	return map[string]string{"id": upgrade.activeID, "phase": "running"}
}

func (upgrade *hostMachineUpgrade) cancelActive() error {
	upgrade.mu.Lock()
	cancel := upgrade.activeCancel
	active := upgrade.activeID
	upgrade.mu.Unlock()
	if active == "" || cancel == nil {
		return errors.New("no active Computer Machine Upgrade")
	}
	cancel()
	return nil
}

func (upgrade *hostMachineUpgrade) executeServiceUpgrade(identity BindingChildIdentity, command protocol.ComputerUpgradePayload) {
	operationID := command.Operation()
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
	defer func() {
		upgrade.mu.Lock()
		if upgrade.activeID == operationID {
			upgrade.activeID = ""
		}
		upgrade.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), cli.DefaultUpdateDownloadTimeout+30*time.Second)
	defer cancel()
	upgrade.mu.Lock()
	if upgrade.activeID == operationID {
		upgrade.activeCancel = cancel
	}
	upgrade.mu.Unlock()
	defer func() {
		upgrade.mu.Lock()
		if upgrade.activeID == operationID {
			upgrade.activeCancel = nil
		}
		upgrade.mu.Unlock()
	}()
	prepared := false
	defer func() {
		if !prepared {
			_ = upgrade.host.ReleaseMachineUpgrade(context.Background())
		}
	}()
	if err := upgrade.host.PrepareSiblingMachineUpgrade(ctx, identity.WorkspaceID); err != nil {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "prepare_failed"})
		return
	}
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "staging", Message: "Downloading release"})
	target, err := resolveMachineUpgradeTarget(command.TargetVersion, string(upgrade.config.identity.releaseChannel()), upgrade.manifestBaseURL)
	if err != nil {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "target_resolution_failed"})
		return
	}
	if versionsMatch(upgrade.config.identity.Version, target) {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: true, NewVersion: upgrade.config.identity.Version})
		return
	}
	staged, err := upgrade.stageRelease(target, cli.DefaultUpdateDownloadTimeout, upgrade.manifestBaseURL)
	if err != nil {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "stage_failed"})
		return
	}
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "verifying", Message: "Verifying binary"})
	if err := upgrade.verifyBinary(ctx, staged, target); err != nil {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "verification_failed"})
		return
	}
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "applying", Message: "Applying release"})
	installPath, err := upgrade.installPath()
	if err != nil {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "activation_failed"})
		return
	}
	_, managedWorkspaceIDs := upgrade.currentRuntimeAndWorkspaceIDs()
	if err := upgrade.writeJournal(hostMachineUpgradeJournal{
		RequestID: command.RequestID, FromVersion: upgrade.config.identity.Version,
		TargetVersion: target, StartedAt: startedAt, SchemaVersion: 1,
		SourceServicePID: os.Getpid(), OldRunnerPIDs: upgrade.runnerPIDs(managedWorkspaceIDs),
		AcceptedManagedWorkspaceIDs: managedWorkspaceIDs,
		AcceptedManagedSetRevision:  managedSetRevision(managedWorkspaceIDs),
	}); err != nil {
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "journal_persist_failed"})
		return
	}
	if err := upgrade.swapExecutable(installPath, staged); err != nil {
		_ = upgrade.removeJournal()
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "activation_failed"})
		return
	}
	prepared = true
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "restarting", Message: "Restarting Computer"})
	upgrade.mu.Lock()
	upgrade.restartBinary = installPath
	upgrade.targetVersion = target
	upgrade.mu.Unlock()
	if upgrade.config.cancel != nil {
		upgrade.config.cancel()
	}
}

func resolveMachineUpgradeTarget(requested, releaseChannel, manifestURL string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "latest" {
		if requested == "" {
			return "", errors.New("machine upgrade target is required")
		}
		return cli.NormalizeReleaseTag(requested), nil
	}
	channel, err := cli.NormalizeReleaseChannel(releaseChannel)
	if err != nil {
		return "", err
	}
	release, err := cli.FetchReleaseForChannelWithOverride(channel, manifestURL)
	if err != nil {
		return "", err
	}
	if release == nil || strings.TrimSpace(release.TagName) == "" {
		return "", errors.New("resolved release is empty")
	}
	return cli.NormalizeReleaseTag(release.TagName), nil
}

func (upgrade *hostMachineUpgrade) emitRunnerEvent(identity BindingChildIdentity, eventType string, payload any) {
	if identity.Validate() != nil {
		targets := upgrade.host.supervisor.availableMachineControlTargets()
		if len(targets) == 0 {
			return
		}
		identity = targets[0].identity
	}
	_ = upgrade.host.supervisor.DeliverComputerUpgradeEvent(context.Background(), upgrade.host.control.token, identity, eventType, payload)
}

func (upgrade *hostMachineUpgrade) handleChildAction(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage) error {
	if upgrade == nil {
		return errors.New("Computer Machine Upgrade coordinator is unavailable")
	}
	var ack protocol.DaemonHeartbeatAckPayload
	if err := json.Unmarshal(raw, &ack); err != nil {
		return err
	}
	if _, _, ok := upgrade.currentRuntime(identity, ack.RuntimeID); !ok {
		return errors.New("machine action Runtime belongs to another Binding child")
	}
	if ack.RuntimeGone || ack.PendingModelList != nil || ack.PendingLocalSkills != nil || ack.PendingLocalSkillImport != nil || len(ack.PendingLocalSkillImports) > 0 || ack.PendingMemoryCuration != nil {
		return errors.New("Binding child attempted to forward a Workspace execution action")
	}
	upgrade.mu.Lock()
	if manifest := strings.TrimSpace(ack.ReleaseManifestBaseURL); manifest != "" {
		upgrade.manifestBaseURL = manifest
	}
	upgrade.mu.Unlock()
	if ack.PendingMachineUpgrade != nil {
		// The connect socket already forwarded computer:upgrade to the Host's
		// single service-owned executor.
		return nil
	}
	if ack.PendingRestart != nil {
		go upgrade.scheduleCurrentBinaryRestart()
	}
	return nil
}

func sameHostStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	sort.Strings(leftCopy)
	sort.Strings(rightCopy)
	for i := range leftCopy {
		if leftCopy[i] != rightCopy[i] {
			return false
		}
	}
	return true
}

func (upgrade *hostMachineUpgrade) recoverSuccessor(ctx context.Context) error {
	journal, err := upgrade.readJournal()
	if err != nil {
		return err
	}
	if journal == nil {
		return nil
	}
	if !versionsMatch(upgrade.config.identity.Version, journal.TargetVersion) {
		return fmt.Errorf("activated Machine Upgrade target %s does not match running Computer %s", journal.TargetVersion, upgrade.config.identity.Version)
	}
	if upgrade.config.identity.SourceServicePID != journal.SourceServicePID || journal.SourceServicePID < 1 {
		return fmt.Errorf("successor source service pid %d does not match upgrade source %d", upgrade.config.identity.SourceServicePID, journal.SourceServicePID)
	}
	if strings.TrimSpace(upgrade.config.identity.ServiceGeneration) == "" {
		return errors.New("successor service generation is empty")
	}
	_, managedWorkspaceIDs := upgrade.currentRuntimeAndWorkspaceIDs()
	if !sameHostStringSet(managedWorkspaceIDs, journal.AcceptedManagedWorkspaceIDs) || managedSetRevision(managedWorkspaceIDs) != journal.AcceptedManagedSetRevision {
		return errors.New("successor managed runner set has not converged")
	}
	if journal.ObservedTargetGeneration != upgrade.config.identity.ServiceGeneration || journal.TargetServicePID != os.Getpid() {
		journal.ObservedTargetGeneration = upgrade.config.identity.ServiceGeneration
		journal.TargetServicePID = os.Getpid()
		if err := upgrade.writeJournal(*journal); err != nil {
			return err
		}
	}
	go upgrade.removeJournalAfterPredecessorsExit(ctx, *journal)
	return nil
}

func (upgrade *hostMachineUpgrade) runnerPIDs(workspaceIDs []string) []int {
	pids := make([]int, 0, len(workspaceIDs))
	for _, workspaceID := range workspaceIDs {
		_, pid, ok := upgrade.host.Snapshot(workspaceID)
		if ok && pid > 0 {
			pids = append(pids, pid)
		}
	}
	sort.Ints(pids)
	return pids
}

func (upgrade *hostMachineUpgrade) removeJournalAfterPredecessorsExit(ctx context.Context, journal hostMachineUpgradeJournal) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		allDead := true
		for _, pid := range append([]int{journal.SourceServicePID}, journal.OldRunnerPIDs...) {
			alive, known := processAlive(pid)
			if !known || alive {
				allDead = false
				break
			}
		}
		if allDead {
			current, err := upgrade.readJournal()
			if err == nil && current != nil && current.ObservedTargetGeneration == journal.ObservedTargetGeneration && current.TargetServicePID == journal.TargetServicePID {
				_ = upgrade.removeJournal()
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (upgrade *hostMachineUpgrade) journalPath() string {
	return machineUpgradeJournalPath(upgrade.config.residentRoot)
}

func (upgrade *hostMachineUpgrade) writeJournal(journal hostMachineUpgradeJournal) error {
	return writeMachineUpgradeJournal(upgrade.config.residentRoot, journal)
}

func (upgrade *hostMachineUpgrade) readJournal() (*hostMachineUpgradeJournal, error) {
	return readMachineUpgradeJournal(upgrade.config.residentRoot)
}

func (upgrade *hostMachineUpgrade) removeJournal() error {
	return removeMachineUpgradeJournal(upgrade.config.residentRoot)
}

func machineUpgradeJournalPath(root string) string {
	root = strings.TrimSpace(root)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "machine-upgrade-host.json")
}

func writeMachineUpgradeJournal(root string, journal hostMachineUpgradeJournal) error {
	path := machineUpgradeJournalPath(root)
	if path == "" {
		return errors.New("Computer Machine Upgrade journal root is unavailable")
	}
	data, err := json.MarshalIndent(journal, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".machine-upgrade-host-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func readMachineUpgradeJournal(root string) (*hostMachineUpgradeJournal, error) {
	path := machineUpgradeJournalPath(root)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var journal hostMachineUpgradeJournal
	if err := json.Unmarshal(data, &journal); err != nil {
		return nil, fmt.Errorf("parse Computer Machine Upgrade journal: %w", err)
	}
	if strings.TrimSpace(journal.TargetVersion) == "" {
		return nil, errors.New("Computer Machine Upgrade journal is incomplete")
	}
	if journal.SchemaVersion != 1 || strings.TrimSpace(journal.RequestID) == "" || strings.TrimSpace(journal.FromVersion) == "" || strings.TrimSpace(journal.StartedAt) == "" || journal.SourceServicePID < 1 || strings.TrimSpace(journal.AcceptedManagedSetRevision) == "" {
		return nil, errors.New("Computer Machine Upgrade journal schema is unsupported")
	}
	return &journal, nil
}

// PendingMachineUpgradeSourceServicePID returns the predecessor identity from
// the one durable upgrade handoff contract. Normal starts have no predecessor.
func PendingMachineUpgradeSourceServicePID(root string) (int, error) {
	journal, err := readMachineUpgradeJournal(root)
	if err != nil || journal == nil {
		return 0, err
	}
	return journal.SourceServicePID, nil
}

func removeMachineUpgradeJournal(root string) error {
	path := machineUpgradeJournalPath(root)
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func (upgrade *hostMachineUpgrade) currentRuntime(identity BindingChildIdentity, runtimeID string) (hostBindingRuntime, string, bool) {
	upgrade.host.runtimeMu.RLock()
	defer upgrade.host.runtimeMu.RUnlock()
	report, ok := upgrade.host.runtimeSets[identity.WorkspaceID]
	if !ok || report.Identity != identity || report.DaemonToken == "" || (!report.ExpiresAt.IsZero() && time.Now().After(report.ExpiresAt)) {
		return hostBindingRuntime{}, "", false
	}
	requestedRuntimeID := strings.TrimSpace(runtimeID)
	for _, runtime := range report.Runtimes {
		// Connect-socket machine actions are scoped to the authenticated Binding,
		// not to one Runtime heartbeat. Resolve an omitted Runtime ID only within
		// that current child; an explicit ID must still match exactly.
		if runtime.WorkspaceID == identity.WorkspaceID && (requestedRuntimeID == "" || runtime.ID == requestedRuntimeID) {
			return runtime, report.DaemonToken, true
		}
	}
	return hostBindingRuntime{}, "", false
}

func (upgrade *hostMachineUpgrade) firstCurrentRuntime() (hostBindingRuntime, string, bool) {
	upgrade.host.runtimeMu.RLock()
	defer upgrade.host.runtimeMu.RUnlock()
	workspaces := make([]string, 0, len(upgrade.host.runtimeSets))
	for workspaceID := range upgrade.host.runtimeSets {
		workspaces = append(workspaces, workspaceID)
	}
	sort.Strings(workspaces)
	for _, workspaceID := range workspaces {
		report := upgrade.host.runtimeSets[workspaceID]
		if report.DaemonToken == "" || (!report.ExpiresAt.IsZero() && time.Now().After(report.ExpiresAt)) || !upgrade.host.Current(report.Identity) {
			continue
		}
		if len(report.Runtimes) > 0 {
			return report.Runtimes[0], report.DaemonToken, true
		}
	}
	return hostBindingRuntime{}, "", false
}

func (upgrade *hostMachineUpgrade) currentRuntimeAndWorkspaceIDs() ([]string, []string) {
	upgrade.host.runtimeMu.RLock()
	defer upgrade.host.runtimeMu.RUnlock()
	var runtimeIDs []string
	var workspaceIDs []string
	for workspaceID, report := range upgrade.host.runtimeSets {
		if !upgrade.host.Current(report.Identity) {
			continue
		}
		workspaceIDs = append(workspaceIDs, workspaceID)
		for _, runtime := range report.Runtimes {
			runtimeIDs = append(runtimeIDs, runtime.ID)
		}
	}
	sort.Strings(runtimeIDs)
	sort.Strings(workspaceIDs)
	return runtimeIDs, workspaceIDs
}

func (upgrade *hostMachineUpgrade) scheduleCurrentBinaryRestart() {
	path, err := upgrade.installPath()
	if err != nil {
		return
	}
	upgrade.mu.Lock()
	if upgrade.restartBinary == "" {
		upgrade.restartBinary = path
		upgrade.targetVersion = upgrade.config.identity.Version
	}
	upgrade.mu.Unlock()
	if upgrade.config.cancel != nil {
		if upgrade.host != nil && upgrade.host.logger != nil {
			upgrade.host.logger.Info("Computer shutdown requested",
				"source", "machine_upgrade",
				"action", "restart",
				"reason", "current_binary_restart",
				"target_version", upgrade.config.identity.Version,
			)
		}
		upgrade.config.cancel()
	}
}

func verifyComputerBinary(ctx context.Context, path, target string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := exec.CommandContext(verifyCtx, path, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("verify staged Computer: %w", err)
	}
	if !strings.Contains(strings.ToLower(string(output)), strings.ToLower(strings.TrimPrefix(target, "v"))) {
		return fmt.Errorf("staged Computer version %q does not match %s", strings.TrimSpace(string(output)), target)
	}
	return nil
}

func versionsMatch(left, right string) bool {
	normalize := func(value string) string { return strings.TrimPrefix(strings.TrimSpace(strings.ToLower(value)), "v") }
	return normalize(left) != "" && normalize(left) == normalize(right)
}

// RestartBinary returns the exact activated Computer launcher selected by a
// Host-owned Machine Upgrade.
func (host *Host) RestartBinary() string {
	if host == nil || host.upgrade == nil {
		return ""
	}
	host.upgrade.mu.Lock()
	defer host.upgrade.mu.Unlock()
	return host.upgrade.restartBinary
}

func (host *Host) MachineUpgradeTarget() string {
	if host == nil || host.upgrade == nil {
		return ""
	}
	host.upgrade.mu.Lock()
	defer host.upgrade.mu.Unlock()
	return host.upgrade.targetVersion
}
