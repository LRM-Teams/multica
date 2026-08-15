package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// BindingMachineUpgradeConfig is the Computer-owned child executor for one
// connect-socket computer:upgrade. DaemonCore only invokes it.
type BindingMachineUpgradeConfig struct {
	Identity       HostProcessIdentity
	ResidentRoot   string
	ControlURL     string
	ControlToken   string
	Child          BindingChildIdentity
	RuntimeID      string
	DaemonToken    string
	ManifestURL    string
	Drain          func(context.Context) error
	ReleaseDrain   func()
	Exit           func()
	StageRelease   func(string, time.Duration, string) (string, error)
	VerifyBinary   func(context.Context, string, string) error
	InstallPath    func() (string, error)
	SwapExecutable func(string, string) error
}

// BindingMachineUpgradeExecutor runs one machine-wide upgrade inside the
// Binding child that received computer:upgrade.
type BindingMachineUpgradeExecutor struct {
	config BindingMachineUpgradeConfig
	client *http.Client

	mu       sync.Mutex
	activeID string
}

func NewBindingMachineUpgradeExecutor(config BindingMachineUpgradeConfig) *BindingMachineUpgradeExecutor {
	if config.StageRelease == nil {
		config.StageRelease = cli.StageReleaseScratch
	}
	if config.VerifyBinary == nil {
		config.VerifyBinary = verifyComputerBinary
	}
	if config.InstallPath == nil {
		config.InstallPath = cli.InstallPath
	}
	if config.SwapExecutable == nil {
		config.SwapExecutable = cli.SwapExecutable
	}
	return &BindingMachineUpgradeExecutor{config: config, client: &http.Client{Timeout: 30 * time.Second}}
}

func (executor *BindingMachineUpgradeExecutor) Execute(ctx context.Context, command protocol.ComputerUpgradePayload) error {
	if executor == nil {
		return errors.New("Binding child Machine Upgrade executor is unavailable")
	}
	pending := protocol.DaemonHeartbeatPendingMachineUpgrade{
		ID: command.Operation(), TargetVersion: strings.TrimSpace(command.TargetVersion),
	}
	if pending.ID == "" {
		return errors.New("Computer upgrade request identity is required")
	}
	executor.mu.Lock()
	if executor.activeID != "" {
		activeID := executor.activeID
		executor.mu.Unlock()
		if activeID == pending.ID {
			return nil
		}
		return ErrComputerControlBusy
	}
	executor.activeID = pending.ID
	executor.mu.Unlock()
	defer func() {
		executor.mu.Lock()
		if executor.activeID == pending.ID {
			executor.activeID = ""
		}
		executor.mu.Unlock()
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	return executor.run(ctx, pending)
}

func (executor *BindingMachineUpgradeExecutor) run(ctx context.Context, pending protocol.DaemonHeartbeatPendingMachineUpgrade) error {
	if executor.config.Drain != nil {
		if err := executor.config.Drain(ctx); err != nil {
			return err
		}
		if executor.config.ReleaseDrain != nil {
			defer executor.config.ReleaseDrain()
		}
	}
	prepared, err := executor.prepareHost(ctx, pending)
	if err != nil {
		return err
	}
	target, err := resolveMachineUpgradeTarget(pending.TargetVersion, executor.config.Identity.ReleaseChannel, firstNonEmpty(executor.config.ManifestURL, prepared.ManifestURL))
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "target_resolution_failed", err)
		return err
	}
	generationID := uuid.NewString()
	var receipt hostMachineUpgradeReceipt
	err = executor.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/accept", url.PathEscape(executor.config.RuntimeID), url.PathEscape(pending.ID)), map[string]any{
		"generation_id": generationID, "cli_version": executor.config.Identity.Version, "resolved_target": target,
	}, &receipt)
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "acceptance_failed", err)
		return err
	}
	if err := validateHostMachineUpgradeReceipt(receipt, pending.ID, prepared.RuntimeIDs, prepared.WorkspaceIDs); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "acceptance_set_mismatch", err)
		return err
	}
	journal := hostMachineUpgradeJournal{
		ID: pending.ID, Generation: strings.TrimSpace(*receipt.AcceptedGeneration),
		Source: executor.config.Identity.Version, Target: target,
		RuntimeIDs: append([]string(nil), receipt.AcceptedRuntimeIDs...), WorkspaceIDs: append([]string(nil), receipt.AcceptedWorkspaceIDs...),
		Phase: "accepted",
	}
	if versionsMatch(executor.config.Identity.Version, target) {
		if err := writeMachineUpgradeJournal(executor.config.ResidentRoot, journal); err != nil {
			_ = executor.reportFailure(ctx, pending.ID, "journal_persist_failed", err)
			return err
		}
		if executor.config.Exit != nil {
			executor.config.Exit()
		}
		return nil
	}
	if err := writeMachineUpgradeJournal(executor.config.ResidentRoot, journal); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "journal_persist_failed", err)
		return err
	}
	if err := executor.reportProgress(ctx, pending.ID, "staging", "", ""); err != nil {
		return err
	}
	staged, err := executor.config.StageRelease(target, cli.DefaultUpdateDownloadTimeout, firstNonEmpty(executor.config.ManifestURL, prepared.ManifestURL))
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "stage_failed", err)
		_ = removeMachineUpgradeJournal(executor.config.ResidentRoot)
		return err
	}
	journal.Phase = "staged"
	if err := writeMachineUpgradeJournal(executor.config.ResidentRoot, journal); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "journal_persist_failed", err)
		return err
	}
	_ = executor.reportProgress(ctx, pending.ID, "verifying", "", "")
	if err := executor.config.VerifyBinary(ctx, staged, target); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "verification_failed", err)
		_ = removeMachineUpgradeJournal(executor.config.ResidentRoot)
		return err
	}
	if err := executor.reportProgress(ctx, pending.ID, "handoff", "", ""); err != nil {
		return err
	}
	installPath, err := executor.config.InstallPath()
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "activation_failed", err)
		return err
	}
	if err := executor.config.SwapExecutable(installPath, staged); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "activation_failed", err)
		return err
	}
	journal.Phase = "activated"
	if err := writeMachineUpgradeJournal(executor.config.ResidentRoot, journal); err != nil {
		_ = cli.RollbackExecutable(installPath)
		_ = executor.reportFailure(ctx, pending.ID, "journal_persist_failed", err)
		return err
	}
	if executor.config.Exit != nil {
		executor.config.Exit()
	}
	return nil
}

type bindingMachineUpgradePrepared struct {
	RuntimeIDs   []string `json:"runtime_ids"`
	WorkspaceIDs []string `json:"workspace_ids"`
	ManifestURL  string   `json:"manifest_url,omitempty"`
}

func (executor *BindingMachineUpgradeExecutor) prepareHost(ctx context.Context, pending protocol.DaemonHeartbeatPendingMachineUpgrade) (bindingMachineUpgradePrepared, error) {
	if strings.TrimSpace(executor.config.ControlURL) == "" || strings.TrimSpace(executor.config.ControlToken) == "" {
		return bindingMachineUpgradePrepared{}, errors.New("Binding child Host control is unavailable")
	}
	body, err := json.Marshal(struct {
		Identity BindingChildIdentity                          `json:"identity"`
		Payload  protocol.DaemonHeartbeatPendingMachineUpgrade `json:"payload"`
	}{Identity: executor.config.Child, Payload: pending})
	if err != nil {
		return bindingMachineUpgradePrepared{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(executor.config.ControlURL, "/")+bindingChildPrepareUpgradePath, strings.NewReader(string(body)))
	if err != nil {
		return bindingMachineUpgradePrepared{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Multica-Control-Token", executor.config.ControlToken)
	response, err := executor.client.Do(request)
	if err != nil {
		return bindingMachineUpgradePrepared{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		var failure struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&failure)
		if failure.Code == bindingChildControlBusyCode {
			return bindingMachineUpgradePrepared{}, ErrComputerControlBusy
		}
		return bindingMachineUpgradePrepared{}, fmt.Errorf("Computer Host prepare returned %s", response.Status)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return bindingMachineUpgradePrepared{}, fmt.Errorf("Computer Host prepare returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var prepared bindingMachineUpgradePrepared
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&prepared); err != nil {
		return bindingMachineUpgradePrepared{}, err
	}
	return prepared, nil
}

func (executor *BindingMachineUpgradeExecutor) reportProgress(ctx context.Context, upgradeID, phase, code, message string) error {
	return executor.postJSON(ctx, fmt.Sprintf("/api/daemon/runtimes/%s/machine-upgrades/%s/progress", url.PathEscape(executor.config.RuntimeID), url.PathEscape(upgradeID)), map[string]string{
		"phase": phase, "error_code": code, "error_message": message,
	}, nil)
}

func (executor *BindingMachineUpgradeExecutor) reportFailure(ctx context.Context, upgradeID, code string, failure error) error {
	message := ""
	if failure != nil {
		message = failure.Error()
	}
	return executor.reportProgress(ctx, upgradeID, "failed", code, message)
}

func (executor *BindingMachineUpgradeExecutor) postJSON(ctx context.Context, path string, body, response any) error {
	base, err := hostHTTPBaseURL(executor.config.Identity.ServerURL)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base+path, strings.NewReader(string(encoded)))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(executor.config.DaemonToken))
	request.Header.Set("X-Computer-Generation", fmt.Sprintf("%d", executor.config.Identity.ComputerGeneration))
	result, err := executor.client.Do(request)
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}


