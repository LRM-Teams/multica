package computer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/cli"
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
	// CreateLiveIntent authorizes the Computer owner and dispatches
	// computer:upgrade on the current Binding socket. The returned request_id
	// is the only identity; there is no cloud receipt.
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
	return v.probe(probeCtx, v.service)
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
	daemonID, _ := health["daemonId"].(string)
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
	operationRequestID, _ := operation["request_id"].(string)
	operationRequestID = strings.TrimSpace(operationRequestID)
	if operationRequestID == "" {
		return UpgradeResult{}, errors.New("Computer upgrade lifecycle intent is missing request id")
	}
	if operationRequestID != requestID {
		return UpgradeResult{}, fmt.Errorf("Computer upgrade lifecycle intent has request id %q, want %q", operationRequestID, requestID)
	}
	// The cloud POST already sent computer:upgrade on the current Binding
	// socket. Do not re-deliver through Computer local control.
	return UpgradeResult{
		Route:           UpgradeRouteLive,
		RequestedTarget: targetVersion,
		ResolvedTarget:  targetVersion,
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
