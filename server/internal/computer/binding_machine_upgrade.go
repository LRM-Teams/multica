package computer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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
	ManifestURL    string
	Drain          func(context.Context) error
	ReleaseDrain   func()
	Exit           func()
	Emit           func(string, any)
	Prepare        func(context.Context, protocol.DaemonHeartbeatPendingMachineUpgrade) (BindingMachineUpgradePrepared, error)
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
	emit   func(string, any)

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
	return &BindingMachineUpgradeExecutor{config: config, client: &http.Client{Timeout: 30 * time.Second}, emit: config.Emit}
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
	startedAt := time.Now().UTC().Format(time.RFC3339Nano)
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
	target, err := resolveMachineUpgradeTarget(pending.TargetVersion, string(executor.config.Identity.releaseChannel()), firstNonEmpty(executor.config.ManifestURL, prepared.ManifestURL))
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "target_resolution_failed", err)
		return err
	}
	if versionsMatch(executor.config.Identity.Version, target) {
		executor.emitUpgradeDone(pending.ID, true, executor.config.Identity.Version, "")
		if executor.config.Exit != nil {
			executor.config.Exit()
		}
		return nil
	}
	executor.emitUpgradeProgress(pending.ID, "downloading", "Downloading release")
	staged, err := executor.config.StageRelease(target, cli.DefaultUpdateDownloadTimeout, firstNonEmpty(executor.config.ManifestURL, prepared.ManifestURL))
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "stage_failed", err)
		return err
	}
	executor.emitUpgradeProgress(pending.ID, "verifying", "Verifying binary")
	if err := executor.config.VerifyBinary(ctx, staged, target); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "verification_failed", err)
		return err
	}
	installPath, err := executor.config.InstallPath()
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "activation_failed", err)
		return err
	}
	journal := hostMachineUpgradeJournal{
		RequestID: pending.ID, FromVersion: executor.config.Identity.Version,
		TargetVersion: target, StartedAt: startedAt,
	}
	if err := writeMachineUpgradeJournal(executor.config.ResidentRoot, journal); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "journal_persist_failed", err)
		return err
	}
	executor.emitUpgradeProgress(pending.ID, "applying", "Swapping binary")
	if err := executor.config.SwapExecutable(installPath, staged); err != nil {
		_ = removeMachineUpgradeJournal(executor.config.ResidentRoot)
		_ = executor.reportFailure(ctx, pending.ID, "activation_failed", err)
		return err
	}
	executor.emitUpgradeProgress(pending.ID, "restarting", "Restarting Computer")
	if executor.config.Exit != nil {
		executor.config.Exit()
	}
	return nil
}

type BindingMachineUpgradePrepared struct {
	RuntimeIDs   []string `json:"runtime_ids"`
	WorkspaceIDs []string `json:"workspace_ids"`
	ManifestURL  string   `json:"manifest_url,omitempty"`
}

func (executor *BindingMachineUpgradeExecutor) prepareHost(ctx context.Context, pending protocol.DaemonHeartbeatPendingMachineUpgrade) (BindingMachineUpgradePrepared, error) {
	if executor.config.Prepare != nil {
		return executor.config.Prepare(ctx, pending)
	}
	if strings.TrimSpace(executor.config.ControlURL) == "" || strings.TrimSpace(executor.config.ControlToken) == "" {
		return BindingMachineUpgradePrepared{}, errors.New("Binding child Host control is unavailable")
	}
	body, err := json.Marshal(struct {
		Identity BindingChildIdentity                          `json:"identity"`
		Payload  protocol.DaemonHeartbeatPendingMachineUpgrade `json:"payload"`
	}{Identity: executor.config.Child, Payload: pending})
	if err != nil {
		return BindingMachineUpgradePrepared{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(executor.config.ControlURL, "/")+bindingChildPrepareUpgradePath, strings.NewReader(string(body)))
	if err != nil {
		return BindingMachineUpgradePrepared{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Multica-Control-Token", executor.config.ControlToken)
	response, err := executor.client.Do(request)
	if err != nil {
		return BindingMachineUpgradePrepared{}, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusConflict {
		var failure struct {
			Code string `json:"code"`
		}
		_ = json.NewDecoder(io.LimitReader(response.Body, 1024)).Decode(&failure)
		if failure.Code == bindingChildControlBusyCode {
			return BindingMachineUpgradePrepared{}, ErrComputerControlBusy
		}
		return BindingMachineUpgradePrepared{}, fmt.Errorf("Computer Host prepare returned %s", response.Status)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return BindingMachineUpgradePrepared{}, fmt.Errorf("Computer Host prepare returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	var prepared BindingMachineUpgradePrepared
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&prepared); err != nil {
		return BindingMachineUpgradePrepared{}, err
	}
	return prepared, nil
}

func (executor *BindingMachineUpgradeExecutor) emitUpgradeProgress(requestID, phase, message string) {
	if executor == nil || executor.emit == nil {
		return
	}
	executor.emit(protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{
		RequestID: requestID, Phase: phase, Message: message,
	})
}

func (executor *BindingMachineUpgradeExecutor) emitUpgradeDone(requestID string, ok bool, newVersion, errText string) {
	if executor == nil || executor.emit == nil {
		return
	}
	executor.emit(protocol.EventComputerUpgradeDone, protocol.ComputerUpgradeDonePayload{
		RequestID: requestID, OK: ok, NewVersion: newVersion, Error: errText,
	})
}

func (executor *BindingMachineUpgradeExecutor) reportFailure(_ context.Context, upgradeID, code string, failure error) error {
	message := code
	if failure != nil && strings.TrimSpace(failure.Error()) != "" {
		message = failure.Error()
	}
	executor.emitUpgradeDone(upgradeID, false, "", message)
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
