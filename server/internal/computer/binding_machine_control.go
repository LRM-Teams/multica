package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type BindingMachineControlRequest struct {
	Identity BindingChildIdentity `json:"identity"`
}

func RequestBindingPrepareMachineUpgrade(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlEndpoint, token, LocalControlRunnerDrainOperation, identity)
}

func RequestBindingReleaseMachineUpgrade(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlEndpoint, token, LocalControlRunnerReleaseOperation, identity)
}

func RequestBindingPrepareEnvironmentSwitch(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingEnvironmentControl(ctx, controlURL, token, LocalControlWorkspaceEnvironmentOperation, "prepare", identity)
}

func RequestBindingReleaseEnvironmentSwitch(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingEnvironmentControl(ctx, controlURL, token, LocalControlWorkspaceEnvironmentOperation, "release", identity)
}

func RequestBindingReregisterRuntime(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlURL, token, LocalControlRunnerReadyOperation, identity)
}

func RequestBindingComputerUpgrade(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity, command protocol.ComputerUpgradePayload) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	body, err := json.Marshal(upgradeStartControlRequest{Identity: identity, Command: command})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, LocalControlUpgradeStartOperation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func RequestBindingComputerUpgradeEvent(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity, eventType string, payload any) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding runner machine control is not configured")
	}
	body, err := json.Marshal(struct {
		Identity  BindingChildIdentity `json:"identity"`
		EventType string               `json:"eventType"`
		Payload   any                  `json:"payload"`
	}{identity, eventType, payload})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, LocalControlUpgradeEventOperation, 5*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func requestBindingMachineControl(ctx context.Context, controlEndpoint, token, operation string, identity BindingChildIdentity) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	body, err := json.Marshal(BindingMachineControlRequest{Identity: identity})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, operation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func requestBindingEnvironmentControl(ctx context.Context, controlEndpoint, token, operation, action string, identity BindingChildIdentity) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, operation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		struct {
			Identity BindingChildIdentity `json:"identity"`
			Action   string               `json:"action"`
		}{Identity: identity, Action: action}, nil)
}
