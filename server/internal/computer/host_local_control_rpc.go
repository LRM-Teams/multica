package computer

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LocalControlRegistry builds the Computer's production control surface. The
// HTTP mux is deliberately not involved in this path.
func (host *Host) LocalControlRegistry(state *hostProcessState) *LocalControlRegistry {
	registry := NewLocalControlRegistry()
	register := func(name string, fn LocalControlHandler) {
		if err := registry.Register(name, fn); err != nil {
			panic(err)
		}
	}
	register("service-status", func(context.Context, map[string]string, json.RawMessage) (any, error) {
		return host.processHealthResult(state), nil
	})
	register("machine-attestation", func(context.Context, map[string]string, json.RawMessage) (any, error) {
		return host.processAttestationResult(state), nil
	})
	register("restart-service", func(_ context.Context, headers map[string]string, _ json.RawMessage) (any, error) {
		if !host.authorizeLocal(headers) {
			return nil, errors.New("local control authentication failed")
		}
		go state.cancel()
		return map[string]string{"status": "shutting down"}, nil
	})
	register("workspace-environment", func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if !host.authorizeLocal(headers) {
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
			err = host.PrepareEnvironmentSwitch(ctx)
		} else {
			err = host.ReleaseEnvironmentSwitch(ctx)
		}
		return map[string]string{"status": "prepared"}, err
	})
	register("upgrade-start", func(ctx context.Context, headers map[string]string, raw json.RawMessage) (any, error) {
		if !host.authorizeLocal(headers) {
			return nil, errors.New("local control authentication failed")
		}
		var request struct {
			Identity BindingChildIdentity `json:"identity"`
			Command  json.RawMessage      `json:"command"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		var command protocol.ComputerUpgradePayload
		if err := json.Unmarshal(request.Command, &command); err != nil {
			return nil, err
		}
		if request.Identity.Validate() == nil && host.control.current(request.Identity) && host.control.callbacks.ComputerUpgrade != nil {
			return nil, host.control.callbacks.ComputerUpgrade(ctx, request.Identity, request.Command)
		}
		if err := host.upgrade.startServiceUpgrade(BindingChildIdentity{}, command); err != nil {
			return nil, err
		}
		return map[string]string{"id": command.OperationID, "phase": "starting"}, nil
	})
	// Status/cancel are retained as explicit RPC operations; their journal
	// implementation is owned by the upgrade coordinator.
	register("upgrade-status", func(context.Context, map[string]string, json.RawMessage) (any, error) {
		return host.upgrade.status(), nil
	})
	register("upgrade-cancel", func(context.Context, map[string]string, json.RawMessage) (any, error) {
		if err := host.upgrade.cancelActive(); err != nil {
			return nil, err
		}
		return host.upgrade.status(), nil
	})
	host.control.RegisterRPCHandlers(registry)
	return registry
}

func (host *Host) processHealthResult(state *hostProcessState) map[string]any {
	state.mu.RLock()
	identity, ready, desired := state.identity, state.ready, append([]string(nil), state.desired...)
	started := state.startedAt
	state.mu.RUnlock()
	status := "starting"
	if ready {
		status = "running"
	}
	return map[string]any{"status": status, "pid": os.Getpid(), "os": runtime.GOOS, "uptime": time.Since(started).Truncate(time.Second).String(), "computer_id": identity.ComputerID, "computer_generation": identity.ComputerGeneration, "environment": identity.Environment, "cli_version": identity.Version, "connected": ready && len(desired) > 0, "workspaces": desired}
}

func (host *Host) processAttestationResult(state *hostProcessState) MachineAttestation {
	state.mu.RLock()
	identity, started, workspaces := state.identity, state.startedAt, append([]string(nil), state.desired...)
	state.mu.RUnlock()
	return MachineAttestation{ComputerVersion: identity.Version, ServiceGeneration: fmt.Sprintf("computer-%d", identity.ComputerGeneration), ComputerGeneration: identity.ComputerGeneration, ServicePID: os.Getpid(), ManagedWorkspaceIDs: workspaces, ManagedSetRevision: started.UTC().Format(time.RFC3339Nano) + ":" + strings.Join(workspaces, ","), SourceServicePID: identity.MachineAttestationFrom}
}

func (host *Host) authorizeLocal(headers map[string]string) bool {
	provided := strings.TrimSpace(headers["X-Multica-Control-Token"])
	return host != nil && host.control != nil && provided != "" && host.control.token != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(host.control.token)) == 1
}
