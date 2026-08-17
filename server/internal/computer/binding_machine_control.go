package computer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	BindingPrepareMachineUpgradePath    = "/computer-control/prepare-machine-upgrade"
	BindingReleaseMachineUpgradePath    = "/computer-control/release-machine-upgrade"
	BindingComputerUpgradePath          = "/computer-control/computer-upgrade"
	BindingComputerUpgradeDonePath      = "/computer-control/computer-upgrade-done"
	BindingPrepareEnvironmentSwitchPath = "/computer-control/prepare-environment-switch"
	BindingReleaseEnvironmentSwitchPath = "/computer-control/release-environment-switch"
	BindingReregisterRuntimePath        = "/computer-control/reregister-runtime"
)

type BindingMachineControlRequest struct {
	Identity BindingChildIdentity `json:"identity"`
}

func RequestBindingPrepareMachineUpgrade(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlURL, token, BindingPrepareMachineUpgradePath, identity)
}

func RequestBindingReleaseMachineUpgrade(ctx context.Context, controlURL, token string, identity BindingChildIdentity) error {
	return requestBindingMachineControl(ctx, controlURL, token, BindingReleaseMachineUpgradePath, identity)
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

func RequestBindingComputerUpgrade(ctx context.Context, controlURL, token string, identity BindingChildIdentity, command protocol.ComputerUpgradePayload) error {
	if !validBindingChildControlURL(controlURL) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	body, err := json.Marshal(struct {
		Identity BindingChildIdentity            `json:"identity"`
		Command  protocol.ComputerUpgradePayload `json:"command"`
	}{Identity: identity, Command: command})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controlURL, "/")+BindingComputerUpgradePath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Multica-Control-Token", strings.TrimSpace(token))
	response, err := (&http.Client{Timeout: 35 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Binding child machine control %s returned %s: %s", BindingComputerUpgradePath, response.Status, strings.TrimSpace(string(message)))
	}
	return nil
}

func RequestBindingComputerUpgradeDone(ctx context.Context, controlURL, token string, identity BindingChildIdentity, done protocol.ComputerUpgradeDonePayload) error {
	if !validBindingChildControlURL(controlURL) || strings.TrimSpace(token) == "" || identity.Validate() != nil || done.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	body, err := json.Marshal(struct {
		Identity BindingChildIdentity                `json:"identity"`
		Done     protocol.ComputerUpgradeDonePayload `json:"done"`
	}{Identity: identity, Done: done})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controlURL, "/")+BindingComputerUpgradeDonePath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Multica-Control-Token", strings.TrimSpace(token))
	response, err := (&http.Client{Timeout: 35 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Binding child machine control %s returned %s: %s", BindingComputerUpgradeDonePath, response.Status, strings.TrimSpace(string(message)))
	}
	return nil
}

func requestBindingMachineControl(ctx context.Context, controlURL, token, path string, identity BindingChildIdentity) error {
	if !validBindingChildControlURL(controlURL) || strings.TrimSpace(token) == "" || identity.Validate() != nil {
		return fmt.Errorf("Binding child machine control is not configured")
	}
	body, err := json.Marshal(BindingMachineControlRequest{Identity: identity})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(controlURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Multica-Control-Token", strings.TrimSpace(token))
	response, err := (&http.Client{Timeout: 35 * time.Second}).Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Binding child machine control %s returned %s: %s", path, response.Status, strings.TrimSpace(string(message)))
	}
	return nil
}
