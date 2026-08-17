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
	Identity        HostProcessIdentity
	ResidentRoot    string
	ServiceEndpoint string
	ControlToken    string
	Child           BindingChildIdentity
	RuntimeID       string
	DaemonToken     string
	ManifestURL     string
	Drain           func(context.Context) error
	ReleaseDrain    func()
	Exit            func()
	Emit            func(string, any)
	StageRelease    func(string, time.Duration, string) (string, error)
	VerifyBinary    func(context.Context, string, string) error
	InstallPath     func() (string, error)
	SwapExecutable  func(string, string) error
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
	executor.emitUpgradeProgress(pending.ID, "staging", "Downloading release")
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
	executor.emitUpgradeProgress(pending.ID, "handoff", "Swapping binary")
	installPath, err := executor.config.InstallPath()
	if err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "activation_failed", err)
		return err
	}
	if err := executor.config.SwapExecutable(installPath, staged); err != nil {
		_ = executor.reportFailure(ctx, pending.ID, "activation_failed", err)
		return err
	}
	journal := hostMachineUpgradeJournal{
		RequestID: pending.ID, FromVersion: executor.config.Identity.Version,
		TargetVersion: target, StartedAt: time.Now().UTC().Format(time.RFC3339Nano), SchemaVersion: 1,
	}
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

type BindingMachineUpgradePrepared struct {
	RuntimeIDs   []string `json:"runtime_ids"`
	WorkspaceIDs []string `json:"workspace_ids"`
	ManifestURL  string   `json:"manifest_url,omitempty"`
}

func (executor *BindingMachineUpgradeExecutor) prepareHost(ctx context.Context, pending protocol.DaemonHeartbeatPendingMachineUpgrade) (BindingMachineUpgradePrepared, error) {
	if strings.TrimSpace(executor.config.ServiceEndpoint) == "" || strings.TrimSpace(executor.config.ControlToken) == "" {
		return BindingMachineUpgradePrepared{}, errors.New("Binding child Host control is unavailable")
	}
	body, err := json.Marshal(struct {
		Identity BindingChildIdentity                          `json:"identity"`
		Payload  protocol.DaemonHeartbeatPendingMachineUpgrade `json:"payload"`
	}{Identity: executor.config.Child, Payload: pending})
	if err != nil {
		return BindingMachineUpgradePrepared{}, err
	}
	var prepared BindingMachineUpgradePrepared
	if err := callLocalJSONAt(ctx, executor.config.ServiceEndpoint, "runner-drain", bindingChildPrepareUpgradePath, 30*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": executor.config.ControlToken},
		json.RawMessage(body), &prepared); err != nil {
		if strings.Contains(err.Error(), bindingChildControlBusyCode) {
			return BindingMachineUpgradePrepared{}, ErrComputerControlBusy
		}
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
