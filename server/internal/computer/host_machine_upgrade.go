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
	activePhase          string
	activeMessage        string
	activePhases         []string
	initiatorWorkspaceID string
	activeCancel         context.CancelFunc
	restartBinary        string
	restartHandoff       *ComputerRestartHandoff
	targetVersion        string
	manifestBaseURL      string
	lastStatus           MachineUpgradeStatus
}

// MachineUpgradeStatus is the read-only local progress projection used by
// lifecycle clients. It reports only phases the resident has actually
// entered; terminal status remains available until the next upgrade starts.
type MachineUpgradeStatus struct {
	ID            string   `json:"id,omitempty"`
	Phase         string   `json:"phase"`
	Message       string   `json:"message,omitempty"`
	TargetVersion string   `json:"targetVersion,omitempty"`
	NewVersion    string   `json:"newVersion,omitempty"`
	Error         string   `json:"error,omitempty"`
	Phases        []string `json:"phases,omitempty"`
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
	Phase                       string   `json:"phase,omitempty"`
}

const (
	MachineUpgradePhaseAccepted       = "accepted"
	MachineUpgradePhaseStartingTarget = "starting_target"
	MachineUpgradePhaseTargetReady    = "target_ready"
	MachineUpgradePhaseRollingBack    = "rolling_back"
)

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
	upgrade.activePhase = "accepted"
	upgrade.activeMessage = "Upgrade request accepted"
	upgrade.activePhases = []string{"accepted"}
	upgrade.targetVersion = strings.TrimSpace(command.TargetVersion)
	upgrade.lastStatus = MachineUpgradeStatus{}
	upgrade.initiatorWorkspaceID = identity.WorkspaceID
	upgrade.mu.Unlock()
	go upgrade.executeServiceUpgrade(identity, command)
	return nil
}

func (upgrade *hostMachineUpgrade) status() MachineUpgradeStatus {
	upgrade.mu.Lock()
	defer upgrade.mu.Unlock()
	if upgrade.activeID == "" {
		if upgrade.lastStatus.ID != "" {
			return upgrade.lastStatus
		}
		return MachineUpgradeStatus{Phase: "idle"}
	}
	phase := upgrade.activePhase
	if phase == "" {
		phase = "running"
	}
	return MachineUpgradeStatus{
		ID: upgrade.activeID, Phase: phase, Message: upgrade.activeMessage,
		TargetVersion: upgrade.targetVersion, Phases: append([]string(nil), upgrade.activePhases...),
	}
}

func (upgrade *hostMachineUpgrade) recordProgress(operationID, phase, message string) {
	upgrade.mu.Lock()
	defer upgrade.mu.Unlock()
	if upgrade.activeID != operationID {
		return
	}
	upgrade.activePhase = phase
	upgrade.activeMessage = message
	if len(upgrade.activePhases) == 0 || upgrade.activePhases[len(upgrade.activePhases)-1] != phase {
		upgrade.activePhases = append(upgrade.activePhases, phase)
	}
}

func (upgrade *hostMachineUpgrade) recordDone(operationID, newVersion, upgradeError string) {
	upgrade.mu.Lock()
	defer upgrade.mu.Unlock()
	if upgrade.activeID != operationID {
		return
	}
	phase := "done"
	message := "Upgrade complete"
	if upgradeError != "" {
		phase = "failed"
		message = "Upgrade failed"
	}
	upgrade.lastStatus = MachineUpgradeStatus{
		ID: operationID, Phase: phase, Message: message,
		TargetVersion: upgrade.targetVersion, NewVersion: newVersion, Error: upgradeError,
		Phases: append(append([]string(nil), upgrade.activePhases...), phase),
	}
	upgrade.activeID = ""
	upgrade.activePhase = ""
	upgrade.activeMessage = ""
	upgrade.activePhases = nil
	upgrade.activeCancel = nil
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
		upgrade.recordDone(operationID, "", "prepare_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "prepare_failed"})
		return
	}
	target, err := resolveMachineUpgradeTarget(command.TargetVersion, string(upgrade.config.identity.releaseChannel()), upgrade.manifestBaseURL)
	if err != nil {
		upgrade.recordDone(operationID, "", "target_resolution_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "target_resolution_failed"})
		return
	}
	upgrade.mu.Lock()
	if upgrade.activeID == operationID {
		upgrade.targetVersion = target
	}
	upgrade.mu.Unlock()
	if versionsMatch(upgrade.config.identity.Version, target) {
		upgrade.recordDone(operationID, upgrade.config.identity.Version, "")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: true, NewVersion: upgrade.config.identity.Version})
		return
	}
	upgrade.recordProgress(operationID, "staging", "Downloading release")
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "staging", Message: "Downloading release"})
	staged, err := upgrade.stageRelease(target, cli.DefaultUpdateDownloadTimeout, upgrade.manifestBaseURL)
	if err != nil {
		upgrade.recordDone(operationID, "", "stage_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "stage_failed"})
		return
	}
	upgrade.recordProgress(operationID, "verifying", "Verifying binary")
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "verifying", Message: "Verifying binary"})
	if err := upgrade.verifyBinary(ctx, staged, target); err != nil {
		upgrade.recordDone(operationID, "", "verification_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "verification_failed"})
		return
	}
	upgrade.recordProgress(operationID, "applying", "Installing release")
	upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{RequestID: command.RequestID, Phase: "applying", Message: "Applying release"})
	installPath, err := upgrade.installPath()
	if err != nil {
		upgrade.recordDone(operationID, "", "activation_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "activation_failed"})
		return
	}
	managedWorkspaceIDs := upgrade.acceptedManagedWorkspaceIDs()
	if err := upgrade.writeJournal(hostMachineUpgradeJournal{
		RequestID: command.RequestID, FromVersion: upgrade.config.identity.Version,
		TargetVersion: target, StartedAt: startedAt, SchemaVersion: 1,
		SourceServicePID: os.Getpid(), OldRunnerPIDs: upgrade.runnerPIDs(managedWorkspaceIDs),
		AcceptedManagedWorkspaceIDs: managedWorkspaceIDs,
		AcceptedManagedSetRevision:  managedSetRevision(managedWorkspaceIDs),
		Phase:                       MachineUpgradePhaseAccepted,
	}); err != nil {
		upgrade.recordDone(operationID, "", "journal_persist_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "journal_persist_failed"})
		return
	}
	if err := upgrade.swapExecutable(installPath, staged); err != nil {
		_ = upgrade.removeJournal()
		upgrade.recordDone(operationID, "", "activation_failed")
		upgrade.emitRunnerEvent(identity, protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{RequestID: command.RequestID, OK: false, Error: "activation_failed"})
		return
	}
	prepared = true
	upgrade.recordProgress(operationID, "restarting", "Restarting Computer")
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
	if versionsMatch(upgrade.config.identity.Version, journal.FromVersion) && !versionsMatch(upgrade.config.identity.Version, journal.TargetVersion) {
		if journal.Phase == "" || journal.Phase == MachineUpgradePhaseAccepted || journal.Phase == MachineUpgradePhaseRollingBack {
			return upgrade.removeJournal()
		}
		return fmt.Errorf("Computer Machine Upgrade %s is still active", journal.Phase)
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
	if err := requirePredecessorsDead(*journal); err != nil {
		return err
	}
	managedWorkspaceIDs := upgrade.acceptedManagedWorkspaceIDs()
	if !sameHostStringSet(managedWorkspaceIDs, journal.AcceptedManagedWorkspaceIDs) || managedSetRevision(managedWorkspaceIDs) != journal.AcceptedManagedSetRevision {
		return errors.New("successor managed runner set has not converged")
	}
	if journal.ObservedTargetGeneration != upgrade.config.identity.ServiceGeneration || journal.TargetServicePID != os.Getpid() || journal.Phase != MachineUpgradePhaseTargetReady {
		journal.ObservedTargetGeneration = upgrade.config.identity.ServiceGeneration
		journal.TargetServicePID = os.Getpid()
		journal.Phase = MachineUpgradePhaseTargetReady
		if err := upgrade.writeJournal(*journal); err != nil {
			return err
		}
	}
	return nil
}

func predecessorPIDs(journal hostMachineUpgradeJournal) []int {
	return append([]int{journal.SourceServicePID}, journal.OldRunnerPIDs...)
}

func requirePredecessorsDead(journal hostMachineUpgradeJournal) error {
	for _, pid := range predecessorPIDs(journal) {
		if pid < 1 {
			continue
		}
		alive, known := processAlive(pid)
		if !known || alive {
			return fmt.Errorf("predecessor process %d is still live", pid)
		}
	}
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

// PendingMachineUpgradeHandoff is the durable predecessor/successor contract
// the detached coordinator waits on before spawning the next Computer.
type PendingMachineUpgradeHandoff struct {
	RequestID                   string
	FromVersion                 string
	TargetVersion               string
	SourceServicePID            int
	OldRunnerPIDs               []int
	AcceptedManagedWorkspaceIDs []string
	AcceptedManagedSetRevision  string
	Phase                       string
	KeepOwnedProcess            bool
}

// ComputerRestartHandoff is the in-memory predecessor snapshot used to
// restart the current binary without pretending that an upgrade is active.
type ComputerRestartHandoff struct {
	Version                     string   `json:"version"`
	SourceServicePID            int      `json:"sourceServicePid"`
	OldBindingPIDs              []int    `json:"oldBindingPids,omitempty"`
	AcceptedManagedWorkspaceIDs []string `json:"acceptedManagedWorkspaceIds"`
	AcceptedManagedSetRevision  string   `json:"acceptedManagedSetRevision"`
}

// ComputerRestartPlan is the one post-shutdown launch decision returned by
// Host. CurrentBinaryHandoff is nil for a real Machine Upgrade.
type ComputerRestartPlan struct {
	BinaryPath           string
	CurrentBinaryHandoff *ComputerRestartHandoff
}

func WritePendingMachineUpgradeHandoffForTest(root string, handoff PendingMachineUpgradeHandoff) error {
	requestID := strings.TrimSpace(handoff.RequestID)
	if requestID == "" {
		requestID = "test-upgrade"
	}
	phase := strings.TrimSpace(handoff.Phase)
	if phase == "" {
		phase = MachineUpgradePhaseAccepted
	}
	return writeMachineUpgradeJournal(root, hostMachineUpgradeJournal{
		RequestID: requestID, FromVersion: handoff.FromVersion, TargetVersion: handoff.TargetVersion,
		StartedAt: "2026-08-18T00:00:00Z", SchemaVersion: 1, SourceServicePID: handoff.SourceServicePID,
		OldRunnerPIDs:               append([]int(nil), handoff.OldRunnerPIDs...),
		AcceptedManagedWorkspaceIDs: append([]string(nil), handoff.AcceptedManagedWorkspaceIDs...),
		AcceptedManagedSetRevision:  handoff.AcceptedManagedSetRevision,
		Phase:                       phase,
	})
}

func ReadPendingMachineUpgradeHandoff(root string) (*PendingMachineUpgradeHandoff, error) {
	journal, err := readMachineUpgradeJournal(root)
	if err != nil || journal == nil {
		return nil, err
	}
	return &PendingMachineUpgradeHandoff{
		RequestID: journal.RequestID, FromVersion: journal.FromVersion, TargetVersion: journal.TargetVersion,
		SourceServicePID: journal.SourceServicePID, OldRunnerPIDs: append([]int(nil), journal.OldRunnerPIDs...),
		AcceptedManagedWorkspaceIDs: append([]string(nil), journal.AcceptedManagedWorkspaceIDs...),
		AcceptedManagedSetRevision:  journal.AcceptedManagedSetRevision,
		Phase:                       journal.Phase,
	}, nil
}

func MarkPendingMachineUpgradePhase(root, phase string) error {
	journal, err := readMachineUpgradeJournal(root)
	if err != nil {
		return err
	}
	if journal == nil {
		return errors.New("Computer Machine Upgrade journal is missing")
	}
	journal.Phase = strings.TrimSpace(phase)
	return writeMachineUpgradeJournal(root, *journal)
}

func FinalizePendingMachineUpgrade(root string) error {
	return removeMachineUpgradeJournal(root)
}

func WaitForMachineUpgradePredecessors(ctx context.Context, handoff PendingMachineUpgradeHandoff) error {
	if ctx == nil {
		ctx = context.Background()
	}
	journal := hostMachineUpgradeJournal{
		SourceServicePID: handoff.SourceServicePID, OldRunnerPIDs: handoff.OldRunnerPIDs,
	}
	if err := requirePredecessorsDead(journal); err == nil {
		return nil
	}
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			if err := requirePredecessorsDead(journal); err != nil {
				return err
			}
			return nil
		case <-ticker.C:
			if err := requirePredecessorsDead(journal); err == nil {
				return nil
			}
		}
	}
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

func (upgrade *hostMachineUpgrade) acceptedManagedWorkspaceIDs() []string {
	if upgrade == nil || upgrade.host == nil {
		return nil
	}
	return normalizedWorkspaceIDs(upgrade.host.DesiredWorkspaceIDs())
}

func (upgrade *hostMachineUpgrade) scheduleCurrentBinaryRestart() {
	path, err := upgrade.installPath()
	if err != nil {
		return
	}
	upgrade.mu.Lock()
	if upgrade.restartBinary == "" {
		workspaceIDs := upgrade.acceptedManagedWorkspaceIDs()
		upgrade.restartBinary = path
		upgrade.restartHandoff = &ComputerRestartHandoff{
			Version: upgrade.config.identity.Version, SourceServicePID: os.Getpid(),
			OldBindingPIDs:              upgrade.runnerPIDs(workspaceIDs),
			AcceptedManagedWorkspaceIDs: workspaceIDs,
			AcceptedManagedSetRevision:  managedSetRevision(workspaceIDs),
		}
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

// RestartPlan atomically returns the activated launcher and, for a same-binary
// restart, its predecessor snapshot. Real Machine Upgrade uses its journal.
func (host *Host) RestartPlan() ComputerRestartPlan {
	if host == nil || host.upgrade == nil {
		return ComputerRestartPlan{}
	}
	host.upgrade.mu.Lock()
	defer host.upgrade.mu.Unlock()
	plan := ComputerRestartPlan{BinaryPath: host.upgrade.restartBinary}
	if host.upgrade.restartHandoff == nil {
		return plan
	}
	handoff := *host.upgrade.restartHandoff
	handoff.OldBindingPIDs = append([]int(nil), handoff.OldBindingPIDs...)
	handoff.AcceptedManagedWorkspaceIDs = append([]string(nil), handoff.AcceptedManagedWorkspaceIDs...)
	plan.CurrentBinaryHandoff = &handoff
	return plan
}
