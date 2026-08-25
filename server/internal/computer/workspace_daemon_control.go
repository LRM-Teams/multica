package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type WorkspaceDaemonControlRequest struct {
	Identity WorkspaceDaemonIdentity `json:"identity"`
}

// RequestWorkspaceDaemonDrain closes a WorkspaceDaemon's admission barrier
// and cancels in-flight work. DaemonCore still owns terminating the process.
func RequestWorkspaceDaemonDrain(ctx context.Context, controlEndpoint, token string, identity WorkspaceDaemonIdentity) error {
	return requestWorkspaceDaemonControl(ctx, controlEndpoint, token, LocalControlRunnerDrainOperation, identity)
}

func RequestWorkspaceDaemonReleaseMachineUpgrade(ctx context.Context, controlEndpoint, token string, identity WorkspaceDaemonIdentity) error {
	return requestWorkspaceDaemonControl(ctx, controlEndpoint, token, LocalControlRunnerReleaseOperation, identity)
}

func RequestWorkspaceDaemonPrepareEnvironmentSwitch(ctx context.Context, controlURL, token string, identity WorkspaceDaemonIdentity) error {
	return requestWorkspaceDaemonEnvironmentControl(ctx, controlURL, token, LocalControlWorkspaceEnvironmentOperation, "prepare", identity)
}

func RequestWorkspaceDaemonReleaseEnvironmentSwitch(ctx context.Context, controlURL, token string, identity WorkspaceDaemonIdentity) error {
	return requestWorkspaceDaemonEnvironmentControl(ctx, controlURL, token, LocalControlWorkspaceEnvironmentOperation, "release", identity)
}

func RequestWorkspaceDaemonReregisterRuntime(ctx context.Context, controlURL, token string, identity WorkspaceDaemonIdentity) error {
	return requestWorkspaceDaemonControl(ctx, controlURL, token, LocalControlRunnerReadyOperation, identity)
}

func RequestWorkspaceDaemonComputerUpgrade(ctx context.Context, controlEndpoint, token string, identity WorkspaceDaemonIdentity, command protocol.ComputerUpgradePayload) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("WorkspaceDaemon control is not configured")
	}
	body, err := json.Marshal(upgradeStartControlRequest{Identity: identity, Command: command})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, LocalControlUpgradeStartOperation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func RequestWorkspaceDaemonComputerUpgradeEvent(ctx context.Context, controlEndpoint, token string, identity WorkspaceDaemonIdentity, eventType string, payload any) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("WorkspaceDaemon control is not configured")
	}
	body, err := json.Marshal(struct {
		Identity  WorkspaceDaemonIdentity `json:"identity"`
		EventType string                  `json:"eventType"`
		Payload   any                     `json:"payload"`
	}{identity, eventType, payload})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, LocalControlUpgradeEventOperation, 5*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func requestWorkspaceDaemonControl(ctx context.Context, controlEndpoint, token, operation string, identity WorkspaceDaemonIdentity) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("WorkspaceDaemon control is not configured")
	}
	body, err := json.Marshal(WorkspaceDaemonControlRequest{Identity: identity})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, operation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func requestWorkspaceDaemonEnvironmentControl(ctx context.Context, controlEndpoint, token, operation, action string, identity WorkspaceDaemonIdentity) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("WorkspaceDaemon control is not configured")
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, operation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		struct {
			Identity WorkspaceDaemonIdentity `json:"identity"`
			Action   string                  `json:"action"`
		}{Identity: identity, Action: action}, nil)
}
