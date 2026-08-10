package computer

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Health is the replaceable local liveness/readiness seam. Callers use this
// instead of knowing the Computer's singleton port or probing it directly.
func (l *Lifecycle) Health(ctx context.Context) map[string]any {
	v := l.view()
	return v.probe(ctx, v.health)
}

type RestartResult struct {
	Stop  StopResult
	Start StartResult
}

// Restart preserves the one-resident invariant by completing the stop before
// allocating and launching a fresh generation.
func (l *Lifecycle) Restart(options StartOptions) (RestartResult, error) {
	result := RestartResult{Stop: l.Stop()}
	if result.Stop.Err != nil {
		return result, result.Stop.Err
	}
	started, err := l.StartBackground(options)
	result.Start = started
	return result, err
}

// UpgradeTransport is the service boundary needed by the local Computer
// lifecycle. The CLI owns authentication/HTTP construction; the Computer owns
// identity, request shape, polling, and terminal-phase semantics.
type UpgradeTransport interface {
	PostJSON(context.Context, string, any, any) error
	GetJSON(context.Context, string, any) error
}

type UpgradeOptions struct {
	TargetVersion string
	RequestID     string
	Wait          bool
	PollInterval  time.Duration
}

func (l *Lifecycle) Upgrade(ctx context.Context, client UpgradeTransport, options UpgradeOptions) (map[string]any, error) {
	if client == nil {
		return nil, fmt.Errorf("Computer upgrade transport is required")
	}
	identity, err := l.Identity()
	if err != nil {
		return nil, fmt.Errorf("resolve local Computer identity: %w", err)
	}
	requestID := strings.TrimSpace(options.RequestID)
	if requestID == "" {
		requestID = uuid.NewString()
	}
	body := map[string]any{
		"target_version": strings.TrimSpace(options.TargetVersion),
		"request_id":     requestID,
	}
	upgrade := make(map[string]any)
	if err := client.PostJSON(ctx, "/api/daemons/"+identity+"/upgrades", body, &upgrade); err != nil {
		return nil, fmt.Errorf("create Computer upgrade: %w", err)
	}
	if !options.Wait {
		return upgrade, nil
	}
	upgradeID, _ := upgrade["id"].(string)
	if strings.TrimSpace(upgradeID) == "" {
		return nil, fmt.Errorf("Computer upgrade response is missing id")
	}
	interval := options.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	for {
		phase, _ := upgrade["phase"].(string)
		if terminalUpgradePhase(phase) {
			return upgrade, nil
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, fmt.Errorf("timed out waiting for Computer upgrade (last phase: %s): %w", phase, ctx.Err())
		case <-timer.C:
		}
		if err := client.GetJSON(ctx, "/api/daemons/"+identity+"/upgrades/"+upgradeID, &upgrade); err != nil {
			return nil, fmt.Errorf("get Computer upgrade status: %w", err)
		}
	}
}

func terminalUpgradePhase(phase string) bool {
	switch phase {
	case "completed", "failed", "rolled_back", "timeout", "cancelled":
		return true
	default:
		return false
	}
}
