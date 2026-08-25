package computer

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LocalControlRegistry builds ComputerCore's production control surface. The
// HTTP mux is deliberately not involved in this path.
func (computerCore *ComputerCore) LocalControlRegistry(state *computerProcessState) *LocalControlRegistry {
	registry := NewLocalControlRegistry()
	register := func(name string, fn LocalControlHandler) {
		if err := registry.Register(name, fn); err != nil {
			panic(err)
		}
	}
	register(LocalControlServiceStatusOperation, func(context.Context, map[string]string, json.RawMessage) (any, error) {
		return computerCore.processHealthResult(state), nil
	})
	register(LocalControlMachineAttestationOperation, func(context.Context, map[string]string, json.RawMessage) (any, error) {
		state.mu.RLock()
		identity := state.identity
		desired := append([]string(nil), state.desired...)
		state.mu.RUnlock()
		ids := normalizedWorkspaceIDs(desired)
		return MachineAttestation{
			ComputerVersion: identity.Version, ServiceGeneration: identity.ServiceGeneration,
			ServicePID: os.Getpid(), SourceServicePID: identity.SourceServicePID,
			ManagedWorkspaceIDs: ids, ManagedSetRevision: managedSetRevision(ids),
		}, nil
	})
	register(LocalControlRestartServiceOperation, func(_ context.Context, headers map[string]string, _ json.RawMessage) (any, error) {
		if !computerCore.authorizeLocal(headers) {
			return nil, errors.New("local control authentication failed")
		}
		go state.cancel()
		return map[string]string{"status": "shutting down"}, nil
	})
	register(LocalControlWorkspaceEnvironmentOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if !computerCore.authorizeLocal(headers) {
			return nil, errors.New("local control authentication failed")
		}
		var request struct {
			Prepare bool `json:"prepare"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		var err error
		if request.Prepare {
			err = computerCore.PrepareEnvironmentSwitch(ctx)
		} else {
			err = computerCore.ReleaseEnvironmentSwitch(ctx)
		}
		return map[string]string{"status": "prepared"}, err
	})
	register(LocalControlUpgradeStartOperation, func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if !computerCore.authorizeLocal(headers) {
			return nil, errors.New("local control authentication failed")
		}
		var request struct {
			Identity WorkspaceDaemonIdentity `json:"identity"`
			Command  json.RawMessage         `json:"command"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if len(request.Command) == 0 {
			return nil, errors.New("Computer upgrade command is required")
		}
		var command protocol.ComputerUpgradePayload
		if err := json.Unmarshal(request.Command, &command); err != nil {
			return nil, err
		}
		if request.Identity.Validate() != nil || !computerCore.control.current(request.Identity) {
			return nil, errors.New("inactive WorkspaceDaemon process")
		}
		if computerCore.control.callbacks.ComputerUpgrade == nil {
			return nil, errors.New("Computer upgrade is unavailable")
		}
		if err := computerCore.control.callbacks.ComputerUpgrade(ctx, request.Identity, request.Command); err != nil {
			return nil, err
		}
		return map[string]string{"id": command.Operation(), "phase": "starting"}, nil
	})
	// Status/cancel are retained as explicit RPC operations; their journal
	// implementation is owned by the upgrade coordinator.
	register(LocalControlUpgradeStatusOperation, func(context.Context, map[string]string, json.RawMessage) (any, error) {
		return computerCore.upgrade.status(), nil
	})
	register(LocalControlUpgradeCancelOperation, func(_ context.Context, headers map[string]string, _ json.RawMessage) (any, error) {
		if !computerCore.authorizeLocal(headers) {
			return nil, errors.New("local control authentication failed")
		}
		if err := computerCore.upgrade.cancelActive(); err != nil {
			return nil, err
		}
		return computerCore.upgrade.status(), nil
	})
	computerCore.control.RegisterRPCHandlers(registry)
	return registry
}

func (computerCore *ComputerCore) processHealthResult(state *computerProcessState) map[string]any {
	state.mu.RLock()
	identity, ready, desired := state.identity, state.ready, append([]string(nil), state.desired...)
	started := state.startedAt
	state.mu.RUnlock()
	status := "starting"
	if ready {
		status = "running"
	}
	return map[string]any{
		"status": status, "pid": os.Getpid(), "os": runtime.GOOS,
		"uptime":   time.Since(started).Truncate(time.Second).String(),
		"daemonId": identity.ComputerID, "computerId": identity.ComputerID,
		"serviceGeneration": identity.ServiceGeneration,
		"deviceName":        identity.DeviceName, "serverUrl": identity.ServerURL,
		"environment": identity.Environment, "releaseChannel": identity.releaseChannel(),
		"cliVersion": identity.Version, "connected": ready && len(desired) > 0,
		"workspaces": desired,
	}
}

func (computerCore *ComputerCore) authorizeLocal(headers map[string]string) bool {
	provided := strings.TrimSpace(headers["X-Multica-Control-Token"])
	return computerCore != nil && computerCore.control != nil && provided != "" && computerCore.control.token != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(computerCore.control.token)) == 1
}
