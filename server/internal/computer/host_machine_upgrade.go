package computer

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
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
	client *http.Client

	stageRelease   func(string, time.Duration, string) (string, error)
	verifyBinary   func(context.Context, string, string) error
	installPath    func() (string, error)
	swapExecutable func(string, string) error

	mu                   sync.Mutex
	activeID             string
	initiatorWorkspaceID string
	generationID         string
	restartBinary        string
	targetVersion        string
	manifestBaseURL      string
}

type hostMachineUpgradeReceipt struct {
	ID                   string   `json:"id"`
	RequestedTarget      string   `json:"requested_target"`
	ResolvedTarget       *string  `json:"resolved_target,omitempty"`
	Phase                string   `json:"phase"`
	AcceptedGeneration   *string  `json:"accepted_generation,omitempty"`
	AcceptedRuntimeIDs   []string `json:"accepted_runtime_ids,omitempty"`
	AcceptedWorkspaceIDs []string `json:"accepted_workspace_ids,omitempty"`
}

// hostMachineUpgradeJournal is the on-disk successor marker. Write it only
// after the binary swap succeeds. The new Host only needs the target so it
// can confirm it is the upgraded binary and delete the marker. Cloud
// completion is the new Binding socket reporting this version.
type hostMachineUpgradeJournal struct {
	Target    string `json:"target_version"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func newHostMachineUpgrade(host *Host, config hostMachineUpgradeConfig) *hostMachineUpgrade {
	return &hostMachineUpgrade{
		host: host, config: config, client: &http.Client{Timeout: 30 * time.Second},
		generationID: uuid.NewString(), manifestBaseURL: strings.TrimSpace(config.releaseManifestURL),
		stageRelease: cli.StageReleaseScratch, verifyBinary: verifyComputerBinary,
		installPath: cli.InstallPath, swapExecutable: cli.SwapExecutable,
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
		operationID := strings.TrimSpace(request.OperationID)
		if operationID == "" {
			http.Error(w, "operationId is required", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(request.RequestID) == "" {
			http.Error(w, "requestId is required", http.StatusBadRequest)
			return
		}
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
		_ = json.NewEncoder(w).Encode(map[string]string{"id": operationID, "phase": "starting"})
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
	if err != nil || journal == nil || strings.TrimSpace(journal.Target) == "" {
		return
	}
	path, err := upgrade.installPath()
	if err != nil {
		return
	}
	upgrade.mu.Lock()
	upgrade.restartBinary = path
	upgrade.targetVersion = journal.Target
	upgrade.mu.Unlock()
	if upgrade.config.cancel != nil {
		upgrade.config.cancel()
	}
}

func validateHostMachineUpgradeReceipt(receipt hostMachineUpgradeReceipt, operationID string, runtimeIDs, workspaceIDs []string) error {
	if strings.TrimSpace(receipt.ID) != strings.TrimSpace(operationID) || receipt.AcceptedGeneration == nil || strings.TrimSpace(*receipt.AcceptedGeneration) == "" {
		return errors.New("Computer Machine Upgrade acceptance receipt is incomplete")
	}
	if !sameHostStringSet(receipt.AcceptedRuntimeIDs, runtimeIDs) {
		return errors.New("accepted Runtime set does not match the current complete Computer set")
	}
	if !sameHostStringSet(receipt.AcceptedWorkspaceIDs, workspaceIDs) {
		return errors.New("accepted Workspace set does not match the current complete Computer set")
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
	if !versionsMatch(upgrade.config.identity.Version, journal.Target) {
		return fmt.Errorf("activated Machine Upgrade target %s does not match running Computer %s", journal.Target, upgrade.config.identity.Version)
	}
	// The successor only reports its current version. Cloud completion is
	// the new Binding socket, not a follow-up HTTP attest.
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
			hostMachineUpgradeJournal
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
		if newest == nil || candidate.UpdatedAt > newest.UpdatedAt {
			copy := candidate.hostMachineUpgradeJournal
			newest = &copy
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
	journal.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
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
	if strings.TrimSpace(journal.Target) == "" {
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

func (upgrade *hostMachineUpgrade) resolveTarget(requested, manifestBaseURL string) (string, error) {
	requested = strings.TrimSpace(requested)
	if requested != "latest" {
		if requested == "" {
			return "", errors.New("machine upgrade target is required")
		}
		return cli.NormalizeReleaseTag(requested), nil
	}
	release, err := cli.FetchReleaseForChannelWithOverride(upgrade.config.identity.releaseChannel(), manifestBaseURL)
	if err != nil {
		return "", err
	}
	if release == nil || strings.TrimSpace(release.TagName) == "" {
		return "", errors.New("resolved release is empty")
	}
	return cli.NormalizeReleaseTag(release.TagName), nil
}

func (upgrade *hostMachineUpgrade) reportProgress(ctx context.Context, runtimeID, token, upgradeID, phase, code, message string) error {
	return upgrade.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/progress", url.PathEscape(runtimeID), url.PathEscape(upgradeID)), token, map[string]string{
		"phase": phase, "error_code": code, "error_message": message,
	}, nil, nil)
}

func (upgrade *hostMachineUpgrade) reportFailure(ctx context.Context, runtimeID, token, upgradeID, code string, failure error) {
	message := ""
	if failure != nil {
		message = failure.Error()
	}
	_ = upgrade.reportProgress(ctx, runtimeID, token, upgradeID, "failed", code, message)
}

func (upgrade *hostMachineUpgrade) postJSON(ctx context.Context, path, token string, body, response any, headers map[string]string) error {
	base, err := hostHTTPBaseURL(upgrade.config.identity.ServerURL)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	result, err := upgrade.client.Do(request)
	if err != nil {
		return err
	}
	defer result.Body.Close()
	if result.StatusCode < 200 || result.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(result.Body, 4096))
		return fmt.Errorf("Computer server control returned %s: %s", result.Status, strings.TrimSpace(string(message)))
	}
	if response != nil {
		return json.NewDecoder(io.LimitReader(result.Body, 1<<20)).Decode(response)
	}
	return nil
}

func hostHTTPBaseURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "", fmt.Errorf("invalid Computer server URL %q", raw)
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	case "http", "https":
	default:
		return "", fmt.Errorf("unsupported Computer server URL scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
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
