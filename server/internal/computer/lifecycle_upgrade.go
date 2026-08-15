package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type UpgradeRoute string

const (
	UpgradeRouteLive    UpgradeRoute = "live"
	UpgradeRouteOffline UpgradeRoute = "offline"
)

// UpgradeOptions is the complete caller input for one explicit Computer
// upgrade. An empty target follows the package source fixed by the active
// production/test environment.
type UpgradeOptions struct {
	TargetVersion   string
	RequestID       string
	DownloadTimeout time.Duration
	// CreateLiveIntent records the human-authorized server operation before a
	// running Computer is asked to execute it. The server operation ID is the
	// shared identity used by both the Daemon WS and loopback delivery paths.
	CreateLiveIntent func(context.Context, string, string, string) (map[string]any, error)
}

// UpgradeResult distinguishes a live canonical operation from an offline
// installation. Offline Active state is progress for the next start, never
// proof that a successor is running or converged.
type UpgradeResult struct {
	Route           UpgradeRoute
	RequestedTarget string
	ResolvedTarget  string
	Operation       map[string]any
	ActiveVersion   string
	PreviousVersion string
	Generation      uint64
	BinaryPath      string
	AlreadyCurrent  bool
}

// Upgrade implements Raft-style service-first routing behind the Computer
// lifecycle seam. A live resident is the sole mutation owner. Only a proven
// absent resident permits a locked offline install; an unreachable live owner
// fails closed without swapping the on-PATH Computer.
func (l *Lifecycle) Upgrade(ctx context.Context, options UpgradeOptions) (UpgradeResult, error) {
	requestedTarget, err := normalizeUpgradeTarget(options.TargetVersion)
	if err != nil {
		return UpgradeResult{}, err
	}
	requestID := strings.TrimSpace(options.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}

	if health := l.upgradeHealth(ctx); Alive(health) {
		return l.requestLiveUpgrade(ctx, health, requestID, requestedTarget, options.CreateLiveIntent)
	}
	if err := rejectLivePIDWithoutControl(l.view().pidPath); err != nil {
		return UpgradeResult{}, err
	}

	downloadTimeout := options.DownloadTimeout
	if downloadTimeout <= 0 {
		downloadTimeout = cli.DefaultUpdateDownloadTimeout
	}

	var result UpgradeResult
	var ownerBecameLive bool
	err = cli.WithMachineMutationLock(ctx, func() error {
		startLease, err := acquireStartLease(ctx, RootDir(""))
		if err != nil {
			return fmt.Errorf("serialize Computer start with offline upgrade: %w", err)
		}
		defer startLease.Close()

		if health := l.upgradeHealth(ctx); Alive(health) {
			ownerBecameLive = true
			return nil
		}
		if err := rejectLivePIDWithoutControl(l.view().pidPath); err != nil {
			return err
		}

		leaseCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		residentLease, err := AcquireResidentLease(leaseCtx, RootDir(""))
		cancel()
		if err != nil {
			return fmt.Errorf("upgrade_service_unreachable: Computer resident ownership is held but its local control surface is unavailable; refusing offline activation")
		}
		defer residentLease.Close()

		result, err = installOfflineUpgrade(ctx, requestID, requestedTarget, downloadTimeout)
		return err
	})
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("offline Computer upgrade: %w", err)
	}
	if ownerBecameLive {
		health := l.upgradeHealth(ctx)
		if !Alive(health) {
			return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: Computer ownership changed while routing the upgrade; retry")
		}
		return l.requestLiveUpgrade(ctx, health, requestID, requestedTarget, options.CreateLiveIntent)
	}
	return result, nil
}

func normalizeUpgradeTarget(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" || strings.EqualFold(target, "latest") {
		return "latest", nil
	}
	if !cli.IsReleaseVersion(target) {
		return "", fmt.Errorf("invalid --target-version %q: expected vX.Y.Z or vX.Y.Z-(alpha|beta|rc).N", raw)
	}
	return cli.NormalizeReleaseTag(target), nil
}

func (l *Lifecycle) upgradeHealth(ctx context.Context) map[string]any {
	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	v := l.view()
	return v.probe(probeCtx, v.health)
}

func rejectLivePIDWithoutControl(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("upgrade_service_unreachable: inspect Computer PID state: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return fmt.Errorf("upgrade_service_unreachable: Computer PID state is invalid; refusing offline activation")
	}
	alive, known := processAlive(pid)
	if !known {
		return fmt.Errorf("upgrade_service_unreachable: Computer PID %d liveness is unknown; refusing offline activation", pid)
	}
	if alive {
		return fmt.Errorf("upgrade_service_unreachable: Computer PID %d is alive but its local control surface is unavailable; refusing offline activation", pid)
	}
	return nil
}

func (l *Lifecycle) requestLiveUpgrade(
	ctx context.Context,
	health map[string]any,
	requestID, targetVersion string,
	createIntent func(context.Context, string, string, string) (map[string]any, error),
) (UpgradeResult, error) {
	daemonID, _ := health["daemon_id"].(string)
	daemonID = strings.TrimSpace(daemonID)
	if daemonID == "" {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: live Computer did not prove its machine identity")
	}
	if createIntent == nil {
		return UpgradeResult{}, errors.New("live Computer upgrade requires a human-authorized server lifecycle intent")
	}
	operation, err := createIntent(ctx, daemonID, requestID, targetVersion)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("create Computer upgrade lifecycle intent: %w", err)
	}
	operationID, _ := operation["id"].(string)
	operationID = strings.TrimSpace(operationID)
	if operationID == "" {
		return UpgradeResult{}, errors.New("Computer upgrade lifecycle intent is missing operation id")
	}
	if operationComputerID, _ := operation["daemon_id"].(string); strings.TrimSpace(operationComputerID) != "" && !strings.EqualFold(strings.TrimSpace(operationComputerID), daemonID) {
		return UpgradeResult{}, errors.New("Computer upgrade lifecycle intent belongs to another Computer")
	}
	operationRequestID, _ := operation["request_id"].(string)
	if strings.TrimSpace(operationRequestID) != requestID {
		return UpgradeResult{}, fmt.Errorf("Computer upgrade lifecycle intent has request id %q, want %q", operationRequestID, requestID)
	}
	operationTarget, _ := operation["requested_target"].(string)
	if strings.TrimSpace(operationTarget) != targetVersion {
		return UpgradeResult{}, fmt.Errorf("Computer upgrade lifecycle intent has target %q, want %q", operationTarget, targetVersion)
	}
	phase, _ := operation["phase"].(string)
	if strings.TrimSpace(phase) == "" {
		return UpgradeResult{}, errors.New("Computer upgrade lifecycle intent is missing operation phase")
	}
	resolvedTarget, _ := operation["resolved_target"].(string)
	if strings.TrimSpace(resolvedTarget) == "" {
		resolvedTarget = operationTarget
	}
	controlToken, err := ReadControlToken("")
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: read owner control credential: %w", err)
	}
	body, err := json.Marshal(protocol.ComputerUpgradePayload{
		RequestID: requestID, OperationID: operationID, TargetVersion: targetVersion,
	})
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: encode owner request: %w", err)
	}
	port := l.view().health
	url := fmt.Sprintf("http://127.0.0.1:%d/machine-upgrades", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: build owner request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Multica-Control-Token", controlToken)
	client := &http.Client{Timeout: 30 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: request upgrade through live owner: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		if response.StatusCode == http.StatusConflict {
			return UpgradeResult{}, fmt.Errorf("machine upgrade request rejected: %s", strings.TrimSpace(string(message)))
		}
		return UpgradeResult{}, fmt.Errorf(
			"upgrade_service_unreachable: local control returned %s: %s",
			response.Status,
			strings.TrimSpace(string(message)),
		)
	}
	var acceptance map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&acceptance); err != nil {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: decode live owner response: %w", err)
	}
	acceptedOperationID, _ := acceptance["id"].(string)
	if strings.TrimSpace(acceptedOperationID) == "" {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: live owner response is missing operation id")
	}
	if strings.TrimSpace(acceptedOperationID) != operationID {
		return UpgradeResult{}, fmt.Errorf("upgrade_service_unreachable: live owner accepted operation %q, want %q", acceptedOperationID, operationID)
	}
	return UpgradeResult{
		Route:           UpgradeRouteLive,
		RequestedTarget: targetVersion,
		ResolvedTarget:  resolvedTarget,
		Operation:       operation,
	}, nil
}

func installOfflineUpgrade(
	ctx context.Context,
	requestID, requestedTarget string,
	downloadTimeout time.Duration,
) (UpgradeResult, error) {
	_ = ctx
	_ = requestID
	targetVersion := requestedTarget
	if targetVersion == "latest" {
		cfg, err := cli.LoadCLIConfigForProfile("")
		if err != nil {
			return UpgradeResult{}, fmt.Errorf("load service environment: %w", err)
		}
		channel, err := cli.ResolveReleaseChannel(cfg)
		if err != nil {
			return UpgradeResult{}, fmt.Errorf("resolve package source: %w", err)
		}
		release, err := cli.FetchReleaseForChannelWithOverride(channel, "")
		if err != nil {
			return UpgradeResult{}, fmt.Errorf("resolve upgrade target: %w", err)
		}
		targetVersion = cli.NormalizeReleaseTag(release.TagName)
	}

	installPath, err := cli.InstallPath()
	if err != nil {
		return UpgradeResult{}, err
	}
	if cli.IsReleaseVersion(cli.ClientVersion) && cli.NormalizeReleaseTag(cli.ClientVersion) == targetVersion {
		return UpgradeResult{
			Route:           UpgradeRouteOffline,
			RequestedTarget: requestedTarget,
			ResolvedTarget:  targetVersion,
			ActiveVersion:   targetVersion,
			BinaryPath:      installPath,
			AlreadyCurrent:  true,
		}, nil
	}

	staged, err := cli.StageReleaseScratch(targetVersion, downloadTimeout, "")
	if err != nil {
		return UpgradeResult{}, fmt.Errorf("stage release: %w", err)
	}
	if err := cli.SwapExecutable(installPath, staged); err != nil {
		return UpgradeResult{}, fmt.Errorf("swap PATH computer: %w", err)
	}
	return UpgradeResult{
		Route:           UpgradeRouteOffline,
		RequestedTarget: requestedTarget,
		ResolvedTarget:  targetVersion,
		ActiveVersion:   targetVersion,
		BinaryPath:      installPath,
	}, nil
}
