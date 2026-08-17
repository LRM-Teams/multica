package computer

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55 is no
	// longer a supported direct self-upgrade source.
	previousPackageUpgradeBootstrap bool
}

// ErrComputerControlBusy is the Raft 1.0.16 Host busy signal. DaemonCore maps
// it onto computer:upgrade:done { error: "control_busy" }.
var ErrComputerControlBusy = errors.New("Computer Machine Upgrade is already running")

type hostMachineUpgrade struct {
	host   *Host
	config hostMachineUpgradeConfig

	installPath func() (string, error)

	mu                   sync.Mutex
	activeID             string
	initiatorWorkspaceID string
	restartBinary        string
	targetVersion        string
	manifestBaseURL      string
}

// hostMachineUpgradeJournal is the on-disk successor marker. Persist it before
// swapping the binary so every successfully activated successor can reconcile
// the original request on its new Binding socket. Its field names match Raft's
// upgrade-pending marker.
type hostMachineUpgradeJournal struct {
	RequestID     string `json:"requestId"`
	FromVersion   string `json:"fromVersion"`
	TargetVersion string `json:"targetVersion"`
	StartedAt     string `json:"startedAt"`
	SchemaVersion int    `json:"schemaVersion"`
}

const hostMachineUpgradeJournalSchemaVersion = 1

func newHostMachineUpgrade(host *Host, config hostMachineUpgradeConfig) *hostMachineUpgrade {
	return &hostMachineUpgrade{
		host: host, config: config,
		manifestBaseURL: strings.TrimSpace(config.releaseManifestURL),
		installPath:     cli.InstallPath,
	}
}

func (upgrade *hostMachineUpgrade) localRequestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !upgrade.authorized(r) {
			http.Error(w, "local control authentication failed", http.StatusUnauthorized)
			return
		}
		var request protocol.ComputerUpgradePayload
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestID := strings.TrimSpace(request.RequestID)
		if requestID == "" {
			http.Error(w, "requestId is required", http.StatusBadRequest)
			return
		}
		request.RequestID = requestID
		targetVersion := strings.TrimSpace(request.TargetVersion)
		if targetVersion == "" {
			targetVersion = "latest"
		}
		request.TargetVersion = targetVersion
		if err := upgrade.host.DeliverComputerUpgrade(r.Context(), request); err != nil {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"requestId": requestID, "phase": "starting"})
	}
}

func (upgrade *hostMachineUpgrade) authorized(r *http.Request) bool {
	if upgrade == nil || upgrade.host == nil || upgrade.host.control == nil {
		return false
	}
	expected := strings.TrimSpace(upgrade.host.control.token)
	provided := strings.TrimSpace(r.Header.Get("X-Multica-Control-Token"))
	return expected != "" && provided != "" && subtle.ConstantTimeCompare([]byte(expected), []byte(provided)) == 1
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
		// Connect-socket upgrades execute in the Binding that received
		// computer:upgrade. Host only prepares siblings / restarts.
		return nil
	}
	if ack.PendingRestart != nil {
		go upgrade.scheduleCurrentBinaryRestart()
	}
	return nil
}

func (upgrade *hostMachineUpgrade) prepareChildUpgrade(ctx context.Context, identity BindingChildIdentity, raw json.RawMessage) (BindingMachineUpgradePrepared, error) {
	if upgrade == nil || upgrade.host == nil {
		return BindingMachineUpgradePrepared{}, errors.New("Computer Machine Upgrade coordinator is unavailable")
	}
	var pending protocol.DaemonHeartbeatPendingMachineUpgrade
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &pending); err != nil {
			return BindingMachineUpgradePrepared{}, err
		}
	}
	if strings.TrimSpace(pending.ID) == "" {
		return BindingMachineUpgradePrepared{}, errors.New("Computer upgrade request identity is required")
	}
	upgrade.mu.Lock()
	if upgrade.activeID != "" && upgrade.activeID != pending.ID {
		upgrade.mu.Unlock()
		return BindingMachineUpgradePrepared{}, ErrComputerControlBusy
	}
	upgrade.activeID = pending.ID
	upgrade.initiatorWorkspaceID = identity.WorkspaceID
	manifestURL := upgrade.manifestBaseURL
	upgrade.mu.Unlock()
	// Host busy covers only this prepare call, matching Raft 1.0.16:
	// success, same-version, and sibling failure all release before return.
	// initiatorWorkspaceID stays so the successor restart can still observe
	// the Binding that actually swapped the binary.
	defer func() {
		upgrade.mu.Lock()
		if upgrade.activeID == pending.ID {
			upgrade.activeID = ""
		}
		upgrade.mu.Unlock()
	}()
	if err := upgrade.host.PrepareSiblingMachineUpgrade(ctx, identity.WorkspaceID); err != nil {
		upgrade.mu.Lock()
		if upgrade.initiatorWorkspaceID == identity.WorkspaceID {
			upgrade.initiatorWorkspaceID = ""
		}
		upgrade.mu.Unlock()
		return BindingMachineUpgradePrepared{}, err
	}
	runtimeIDs, workspaceIDs := upgrade.currentRuntimeAndWorkspaceIDs()
	return BindingMachineUpgradePrepared{RuntimeIDs: runtimeIDs, WorkspaceIDs: workspaceIDs, ManifestURL: manifestURL}, nil
}

func (upgrade *hostMachineUpgrade) observeInitiatorExit(identity BindingChildIdentity) {
	if upgrade == nil {
		return
	}
	upgrade.mu.Lock()
	initiator := upgrade.initiatorWorkspaceID
	upgrade.mu.Unlock()
	if initiator == "" || initiator != identity.WorkspaceID {
		return
	}
	journal, err := upgrade.readJournal()
	if err != nil || journal == nil || strings.TrimSpace(journal.TargetVersion) == "" {
		return
	}
	path, err := upgrade.installPath()
	if err != nil {
		return
	}
	upgrade.mu.Lock()
	upgrade.restartBinary = path
	upgrade.targetVersion = journal.TargetVersion
	upgrade.mu.Unlock()
	if upgrade.config.cancel != nil {
		if upgrade.host != nil && upgrade.host.logger != nil {
			upgrade.host.logger.Info("Computer shutdown requested",
				"source", "machine_upgrade",
				"action", "restart",
				"reason", "initiator_exit",
				"workspace_id", identity.WorkspaceID,
				"target_version", journal.TargetVersion,
			)
		}
		upgrade.config.cancel()
	}
}

func (upgrade *hostMachineUpgrade) recoverSuccessor(ctx context.Context) error {
	journal, err := upgrade.readJournal()
	if err != nil {
		return err
	}
	previousPackageJournalPath := ""
	if journal == nil && upgrade.config.previousPackageUpgradeBootstrap {
		// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55 is
		// no longer a supported direct self-upgrade source.
		journal, previousPackageJournalPath, err = upgrade.readPreviousPackageJournal()
		if err != nil {
			return err
		}
		if journal == nil {
			return errors.New("previous-package Computer successor has no matching handoff journal")
		}
	}
	if journal == nil {
		return nil
	}
	rolledBack := !versionsMatch(upgrade.config.identity.Version, journal.TargetVersion)
	if strings.TrimSpace(journal.RequestID) == "" {
		if rolledBack {
			return fmt.Errorf("activated Machine Upgrade target %s does not match running Computer %s", journal.TargetVersion, upgrade.config.identity.Version)
		}
	} else {
		if err := upgrade.host.DeliverComputerUpgradeDone(ctx, protocol.ComputerUpgradeDonePayload{
			RequestID:  journal.RequestID,
			OK:         !rolledBack,
			NewVersion: upgrade.config.identity.Version,
			RolledBack: rolledBack,
		}); err != nil {
			return fmt.Errorf("reconcile Computer Machine Upgrade completion: %w", err)
		}
	}
	if previousPackageJournalPath != "" {
		if err := os.Remove(previousPackageJournalPath); err != nil && !os.IsNotExist(err) && upgrade.host != nil && upgrade.host.logger != nil {
			upgrade.host.logger.Warn("could not remove previous-package Machine Upgrade marker after successor start",
				"path", previousPackageJournalPath, "error", err)
		}
	}
	return upgrade.removeJournal()
}

// readPreviousPackageJournal translates only the active handoff written by the
// immediately previous package. It is used solely when that package launches
// this process with its bootstrap marker; ordinary current Host startup never
// reads the retired journal directory.
// TODO(previous-package-bootstrap): Remove after v0.4.24-alpha.55 is no longer
// a supported direct self-upgrade source.
func (upgrade *hostMachineUpgrade) readPreviousPackageJournal() (*hostMachineUpgradeJournal, string, error) {
	root, err := cli.MachineStateRoot()
	if err != nil {
		return nil, "", err
	}
	dir := filepath.Join(root, "machine-upgrades")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	var newest *hostMachineUpgradeJournal
	newestPath := ""
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			continue
		}
		var candidate struct {
			ID           string   `json:"id"`
			Source       string   `json:"source_version"`
			Target       string   `json:"target_version"`
			UpdatedAt    string   `json:"updated_at"`
			RuntimeIDs   []string `json:"runtime_ids"`
			WorkspaceIDs []string `json:"workspace_ids"`
		}
		if json.Unmarshal(data, &candidate) != nil ||
			!versionsMatch(upgrade.config.identity.Version, candidate.Target) {
			continue
		}
		if strings.TrimSpace(candidate.Target) == "" {
			return nil, "", fmt.Errorf("previous-package Machine Upgrade marker %s is incomplete", path)
		}
		if newest == nil || candidate.UpdatedAt > newest.StartedAt {
			newest = &hostMachineUpgradeJournal{
				RequestID: candidate.ID, FromVersion: candidate.Source,
				TargetVersion: candidate.Target, StartedAt: candidate.UpdatedAt,
				SchemaVersion: hostMachineUpgradeJournalSchemaVersion,
			}
			newestPath = path
		}
	}
	return newest, newestPath, nil
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
	if strings.TrimSpace(journal.StartedAt) == "" {
		journal.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	journal.SchemaVersion = hostMachineUpgradeJournalSchemaVersion
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
		// TODO(raft-upgrade-marker): Remove the target_version reader after
		// v0.4.24-alpha.81 is no longer a supported direct upgrade source.
		var legacy struct {
			TargetVersion string `json:"target_version"`
			UpdatedAt     string `json:"updated_at"`
		}
		if json.Unmarshal(data, &legacy) == nil {
			journal.TargetVersion = strings.TrimSpace(legacy.TargetVersion)
			journal.StartedAt = strings.TrimSpace(legacy.UpdatedAt)
		}
	}
	if strings.TrimSpace(journal.TargetVersion) == "" {
		return nil, errors.New("Computer Machine Upgrade journal is incomplete")
	}
	return &journal, nil
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
