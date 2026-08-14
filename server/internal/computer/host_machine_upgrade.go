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

type hostMachineUpgrade struct {
	host   *Host
	config hostMachineUpgradeConfig
	client *http.Client

	stageRelease   func(string, time.Duration, string) (string, error)
	verifyBinary   func(context.Context, string, string) error
	installPath    func() (string, error)
	swapExecutable func(string, string) error

	mu              sync.Mutex
	activeID        string
	generationID    string
	restartBinary   string
	targetVersion   string
	manifestBaseURL string
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

type hostMachineUpgradeJournal struct {
	ID           string   `json:"id"`
	Generation   string   `json:"generation"`
	Source       string   `json:"source_version"`
	Target       string   `json:"target_version"`
	RuntimeIDs   []string `json:"runtime_ids"`
	WorkspaceIDs []string `json:"workspace_ids"`
	Phase        string   `json:"phase"`
	UpdatedAt    string   `json:"updated_at"`
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
		runtime, token, ok := upgrade.firstCurrentRuntime()
		if !ok {
			http.Error(w, "Computer has no ready Binding Runtime for Machine Upgrade", http.StatusConflict)
			return
		}
		go upgrade.execute(context.Background(), runtime, token, protocol.DaemonHeartbeatPendingMachineUpgrade{
			ID: operationID, TargetVersion: targetVersion,
		})
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": operationID, "phase": "queued"})
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
	runtime, token, ok := upgrade.currentRuntime(identity, ack.RuntimeID)
	if !ok {
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
		pending := *ack.PendingMachineUpgrade
		go upgrade.execute(context.Background(), runtime, token, pending)
	}
	if ack.PendingRestart != nil {
		go upgrade.scheduleCurrentBinaryRestart()
	}
	return nil
}

func (upgrade *hostMachineUpgrade) execute(ctx context.Context, runtime hostBindingRuntime, token string, pending protocol.DaemonHeartbeatPendingMachineUpgrade) {
	upgrade.mu.Lock()
	// Every Binding child can observe the same machine-scoped operation on its
	// heartbeat. Exactly one goroutine may accept and execute it, including when
	// the repeated notification carries the same operation ID.
	if upgrade.activeID != "" {
		upgrade.mu.Unlock()
		return
	}
	upgrade.activeID = pending.ID
	manifestBaseURL := upgrade.manifestBaseURL
	upgrade.mu.Unlock()

	succeeded := false
	prepared := false
	journalPersisted := false
	defer func() {
		if !succeeded {
			if prepared {
				releaseCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
				_ = upgrade.host.ReleaseMachineUpgrade(releaseCtx)
				cancel()
			}
			upgrade.mu.Lock()
			if upgrade.activeID == pending.ID {
				upgrade.activeID = ""
			}
			upgrade.mu.Unlock()
			if journalPersisted {
				_ = upgrade.removeJournal()
			}
		}
	}()

	target, err := upgrade.resolveTarget(pending.TargetVersion, manifestBaseURL)
	if err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "target_resolution_failed", err)
		return
	}
	var receipt hostMachineUpgradeReceipt
	err = upgrade.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/accept", url.PathEscape(runtime.ID), url.PathEscape(pending.ID)), token, map[string]any{
		"generation_id": upgrade.generationID, "cli_version": upgrade.config.identity.Version, "resolved_target": target,
	}, &receipt, nil)
	if err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "acceptance_failed", err)
		return
	}
	runtimeIDs, workspaceIDs := upgrade.currentRuntimeAndWorkspaceIDs()
	if err := validateHostMachineUpgradeReceipt(receipt, pending.ID, runtimeIDs, workspaceIDs); err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "acceptance_set_mismatch", err)
		return
	}
	if versionsMatch(upgrade.config.identity.Version, target) {
		if err := upgrade.host.ReregisterBindings(ctx, receipt.AcceptedWorkspaceIDs); err != nil {
			upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "already_current_registration_failed", err)
			return
		}
		if err := upgrade.attest(ctx, receipt); err != nil {
			upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "already_current_attestation_failed", err)
			return
		}
		succeeded = true
		return
	}
	journal := hostMachineUpgradeJournal{
		ID: pending.ID, Generation: strings.TrimSpace(*receipt.AcceptedGeneration), Source: upgrade.config.identity.Version, Target: target,
		RuntimeIDs: append([]string(nil), receipt.AcceptedRuntimeIDs...), WorkspaceIDs: append([]string(nil), receipt.AcceptedWorkspaceIDs...),
		Phase: "accepted",
	}
	if err := upgrade.writeJournal(journal); err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "journal_persist_failed", err)
		return
	}
	journalPersisted = true
	if err := upgrade.host.PrepareMachineUpgrade(ctx); err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "handoff_failed", err)
		return
	}
	prepared = true
	if err := upgrade.reportProgress(ctx, runtime.ID, token, pending.ID, "staging", "", ""); err != nil {
		return
	}
	staged, err := upgrade.stageRelease(target, cli.DefaultUpdateDownloadTimeout, manifestBaseURL)
	if err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "stage_failed", err)
		return
	}
	journal.Phase = "staged"
	if err := upgrade.writeJournal(journal); err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "journal_persist_failed", err)
		return
	}
	_ = upgrade.reportProgress(ctx, runtime.ID, token, pending.ID, "verifying", "", "")
	if err := upgrade.verifyBinary(ctx, staged, target); err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "verification_failed", err)
		return
	}
	if err := upgrade.reportProgress(ctx, runtime.ID, token, pending.ID, "handoff", "", ""); err != nil {
		return
	}
	installPath, err := upgrade.installPath()
	if err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "activation_failed", err)
		return
	}
	if err := upgrade.swapExecutable(installPath, staged); err != nil {
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "activation_failed", err)
		return
	}
	journal.Phase = "activated"
	if err := upgrade.writeJournal(journal); err != nil {
		_ = cli.RollbackExecutable(installPath)
		upgrade.reportFailure(ctx, runtime.ID, token, pending.ID, "journal_persist_failed", err)
		return
	}
	upgrade.mu.Lock()
	upgrade.restartBinary = installPath
	upgrade.targetVersion = target
	upgrade.mu.Unlock()
	succeeded = true
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
	if journal.Phase != "activated" {
		if versionsMatch(upgrade.config.identity.Version, journal.Source) {
			if runtime, token, ok := upgrade.firstCurrentRuntime(); ok {
				if err := upgrade.reportProgress(ctx, runtime.ID, token, journal.ID, "failed", "interrupted_before_activation", "Computer restarted before Machine Upgrade activation"); err != nil {
					return fmt.Errorf("report interrupted Computer Machine Upgrade: %w", err)
				}
			} else {
				return errors.New("restored Computer has no current Runtime for rollback report")
			}
			return upgrade.removeJournal()
		}
		return fmt.Errorf("Machine Upgrade journal phase %q does not match running Computer %s", journal.Phase, upgrade.config.identity.Version)
	}
	if !versionsMatch(upgrade.config.identity.Version, journal.Target) {
		return fmt.Errorf("activated Machine Upgrade target %s does not match running Computer %s", journal.Target, upgrade.config.identity.Version)
	}
	upgrade.generationID = journal.Generation
	if err := upgrade.host.ReregisterBindings(ctx, journal.WorkspaceIDs); err != nil {
		return fmt.Errorf("re-register successor Binding set: %w", err)
	}
	if err := upgrade.attestJournal(ctx, *journal); err != nil {
		return err
	}
	if previousPackageJournalPath != "" {
		if err := os.Remove(previousPackageJournalPath); err != nil && !os.IsNotExist(err) && upgrade.host != nil && upgrade.host.logger != nil {
			upgrade.host.logger.Warn("could not remove previous-package Machine Upgrade handoff journal after attestation",
				"path", previousPackageJournalPath, "error", err)
		}
		return nil
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
		var candidate hostMachineUpgradeJournal
		if json.Unmarshal(data, &candidate) != nil || candidate.Phase != "handoff" ||
			!versionsMatch(upgrade.config.identity.Version, candidate.Target) {
			continue
		}
		if strings.TrimSpace(candidate.ID) == "" || strings.TrimSpace(candidate.Generation) == "" ||
			len(candidate.RuntimeIDs) == 0 || len(candidate.WorkspaceIDs) == 0 {
			return nil, "", fmt.Errorf("previous-package Machine Upgrade handoff journal %s is incomplete", path)
		}
		if newest == nil || candidate.UpdatedAt > newest.UpdatedAt {
			copy := candidate
			copy.Phase = "activated"
			newest = &copy
			newestPath = path
		}
	}
	return newest, newestPath, nil
}

func (upgrade *hostMachineUpgrade) attestJournal(ctx context.Context, journal hostMachineUpgradeJournal) error {
	runtime, token, ok := upgrade.firstCurrentRuntime()
	if !ok {
		return errors.New("Computer successor has no current Runtime for upgrade attestation")
	}
	return upgrade.postJSON(ctx, fmt.Sprintf("/api/daemon/computer/machine-upgrades/%s/attest", url.PathEscape(journal.ID)), token, map[string]any{
		"daemon_id": upgrade.config.identity.ComputerID, "generation_id": journal.Generation,
		"cli_version": upgrade.config.identity.Version, "runtime_ids": journal.RuntimeIDs, "workspace_ids": journal.WorkspaceIDs,
	}, nil, map[string]string{"X-Workspace-ID": runtime.WorkspaceID})
}

func (upgrade *hostMachineUpgrade) journalPath() string {
	root := strings.TrimSpace(upgrade.config.residentRoot)
	if root == "" {
		return ""
	}
	return filepath.Join(root, "machine-upgrade-host.json")
}

func (upgrade *hostMachineUpgrade) writeJournal(journal hostMachineUpgradeJournal) error {
	path := upgrade.journalPath()
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

func (upgrade *hostMachineUpgrade) readJournal() (*hostMachineUpgradeJournal, error) {
	path := upgrade.journalPath()
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
	if strings.TrimSpace(journal.ID) == "" || strings.TrimSpace(journal.Generation) == "" || strings.TrimSpace(journal.Target) == "" {
		return nil, errors.New("Computer Machine Upgrade journal is incomplete")
	}
	return &journal, nil
}

func (upgrade *hostMachineUpgrade) removeJournal() error {
	path := upgrade.journalPath()
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
	channel, err := cli.NormalizeReleaseChannel(upgrade.config.identity.ReleaseChannel)
	if err != nil {
		return "", err
	}
	release, err := cli.FetchReleaseForChannelWithOverride(channel, manifestBaseURL)
	if err != nil {
		return "", err
	}
	if release == nil || strings.TrimSpace(release.TagName) == "" {
		return "", errors.New("resolved release is empty")
	}
	return cli.NormalizeReleaseTag(release.TagName), nil
}

func (upgrade *hostMachineUpgrade) attest(ctx context.Context, receipt hostMachineUpgradeReceipt) error {
	generation := upgrade.generationID
	if receipt.AcceptedGeneration != nil && strings.TrimSpace(*receipt.AcceptedGeneration) != "" {
		generation = strings.TrimSpace(*receipt.AcceptedGeneration)
	}
	runtimeIDs, workspaceIDs := upgrade.currentRuntimeAndWorkspaceIDs()
	if len(receipt.AcceptedWorkspaceIDs) > 0 {
		workspaceIDs = append([]string(nil), receipt.AcceptedWorkspaceIDs...)
	}
	runtime, token, ok := upgrade.firstCurrentRuntime()
	if !ok {
		return errors.New("Computer has no current Runtime for upgrade attestation")
	}
	return upgrade.postJSON(ctx, fmt.Sprintf("/api/daemon/computer/machine-upgrades/%s/attest", url.PathEscape(receipt.ID)), token, map[string]any{
		"daemon_id": upgrade.config.identity.ComputerID, "generation_id": generation,
		"cli_version": upgrade.config.identity.Version, "runtime_ids": runtimeIDs, "workspace_ids": workspaceIDs,
	}, nil, map[string]string{"X-Workspace-ID": runtime.WorkspaceID})
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
	request.Header.Set("X-Computer-Generation", fmt.Sprintf("%d", upgrade.config.identity.ComputerGeneration))
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
	for _, runtime := range report.Runtimes {
		if runtime.ID == strings.TrimSpace(runtimeID) && runtime.WorkspaceID == identity.WorkspaceID {
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

// MarkMachineUpgradeRollbackPending lets the foreground launcher record that
// successor startup failed after activation and the retained previous binary
// was restored. The restored Host reports the failure and clears the journal.
func (host *Host) MarkMachineUpgradeRollbackPending() error {
	if host == nil || host.upgrade == nil {
		return errors.New("Computer Machine Upgrade coordinator is unavailable")
	}
	journal, err := host.upgrade.readJournal()
	if err != nil || journal == nil {
		return err
	}
	journal.Phase = "rollback_pending"
	return host.upgrade.writeJournal(*journal)
}
