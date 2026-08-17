package computer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	BindingPrepareMachineUpgradePath    = "/computer-control/prepare-machine-upgrade"
	BindingReleaseMachineUpgradePath    = "/computer-control/release-machine-upgrade"
	BindingComputerUpgradePath          = "/computer-control/computer-upgrade"
	BindingComputerUpgradeEventPath     = "/computer-control/computer-upgrade-event"
	BindingPrepareEnvironmentSwitchPath = "/computer-control/prepare-environment-switch"
	BindingReleaseEnvironmentSwitchPath = "/computer-control/release-environment-switch"
	BindingReregisterRuntimePath        = "/computer-control/reregister-runtime"
)

type BindingMachineControlRequest struct {
	Identity BindingChildIdentity `json:"identity"`
}

func RequestBindingPrepareMachineUpgrade(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlEndpoint, token, BindingPrepareMachineUpgradePath, identity)
}

func RequestBindingReleaseMachineUpgrade(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlEndpoint, token, BindingReleaseMachineUpgradePath, identity)
}

func RequestBindingPrepareEnvironmentSwitch(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlURL, token, BindingPrepareEnvironmentSwitchPath, identity)
}

func RequestBindingReleaseEnvironmentSwitch(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlURL, token, BindingReleaseEnvironmentSwitchPath, identity)
}

func RequestBindingReregisterRuntime(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlURL, token, BindingReregisterRuntimePath, identity)
}

func RequestBindingComputerUpgrade(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity, command protocol.ComputerUpgradePayload) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	body, err := json.Marshal(struct {
		Identity BindingChildIdentity            `json:"identity"`
		Command  protocol.ComputerUpgradePayload `json:"command"`
	}{Identity: identity, Command: command})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, "upgrade-start", 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func RequestBindingComputerUpgradeEvent(ctx context.Context, controlEndpoint, token string, identity BindingChildIdentity, eventType string, payload any) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding runner machine control is not configured")
	}
	body, err := json.Marshal(struct {
		Identity  BindingChildIdentity `json:"identity"`
		EventType string               `json:"event_type"`
		Payload   any                  `json:"payload"`
	}{identity, eventType, payload})
	if err != nil {
		return err
	}
	return callLocalJSONWithTimeout(ctx, controlEndpoint, "upgrade-status", 5*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}

func requestBindingMachineControl(ctx context.Context, controlEndpoint, token, path string, identity BindingChildIdentity) error {
	if !validLocalControlEndpoint(controlEndpoint) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	input := any(BindingMachineControlRequest{Identity: identity})
	if path == BindingPrepareEnvironmentSwitchPath || path == BindingReleaseEnvironmentSwitchPath {
		action := "prepare"
		if path == BindingReleaseEnvironmentSwitchPath {
			action = "release"
		}
		input = struct {
			Identity BindingChildIdentity `json:"identity"`
			Action   string               `json:"action"`
		}{Identity: identity, Action: action}
	}
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	operation := localControlOperationForPath(path)
	return callLocalJSONWithTimeout(ctx, controlEndpoint, operation, 35*time.Second,
		map[string]string{"Content-Type": "application/json", "X-Multica-Control-Token": strings.TrimSpace(token)},
		json.RawMessage(body), nil)
}
