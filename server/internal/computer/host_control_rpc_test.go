package computer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHostControlRPCDispatchesCapacityOperation(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", RunnerGeneration: 2, PID: 1234}
	control := NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
	})
	registry := NewLocalControlRegistry()
	control.RegisterRPCHandlers(registry)
	handler, ok := registry.handler(LocalControlWorkspaceCapacityOperation)
	if !ok {
		t.Fatal("workspace:capacity RPC was not registered")
	}
	args, err := json.Marshal(capacityControlRequest{
		Identity: identity, Operation: "acquire", WorkspaceID: identity.WorkspaceID,
		AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(capacityControlResponse)
	if !ok || !response.Admitted || response.Grant.LaunchID != "launch-1" {
		t.Fatalf("result = %#v", result)
	}
}

func TestHostControlRPCRejectsStaleRunnerGeneration(t *testing.T) {
	active := BindingChildIdentity{WorkspaceID: "ws-1", RunnerGeneration: 2, PID: 1234}
	control := NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == active },
	})
	registry := NewLocalControlRegistry()
	control.RegisterRPCHandlers(registry)
	handler, ok := registry.handler(LocalControlWorkspaceCapacityOperation)
	if !ok {
		t.Fatal("workspace:capacity RPC was not registered")
	}
	args, err := json.Marshal(capacityControlRequest{
		Identity:  BindingChildIdentity{WorkspaceID: "ws-1", RunnerGeneration: 1, PID: 1234},
		Operation: "active", Grant: ProcessCapacityGrant{LaunchID: "launch-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args); err == nil {
		t.Fatal("stale runner generation was accepted")
	}
}

func TestHostControlClientUsesLocalRPCTransport(t *testing.T) {
	root := t.TempDir()
	identity := BindingChildIdentity{WorkspaceID: "ws-1", RunnerGeneration: 2, PID: 1234}
	control := NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
	})
	registry := NewLocalControlRegistry()
	control.RegisterRPCHandlers(registry)
	endpoint := ServiceControlEndpoint(root)
	listener, err := ListenLocalControl(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = ServeLocalControlRPC(ctx, listener, registry) }()
	client := NewHostControlClient(endpoint, "control-token", identity)
	grant, admitted, err := client.AcquireCapacity(context.Background(), ProcessCapacityRequest{
		WorkspaceID: identity.WorkspaceID, AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "launch-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !admitted || grant.LaunchID != "launch-1" {
		t.Fatalf("grant = %#v admitted=%v", grant, admitted)
	}
	_ = listener.Close()
}

func TestHostControlRPCPrepareReturnsCallbackResult(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", RunnerGeneration: 2, PID: 1234}
	control := NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
		PrepareUpgrade: func(context.Context, BindingChildIdentity, json.RawMessage) (any, error) {
			return map[string]string{"workspace_id": "ws-1", "prepared": "true"}, nil
		},
	})
	registry := NewLocalControlRegistry()
	control.RegisterRPCHandlers(registry)
	handler, ok := registry.handler(LocalControlRunnerPrepareOperation)
	if !ok {
		t.Fatal("runner:prepare RPC was not registered")
	}
	args, err := json.Marshal(map[string]any{
		"identity": identity,
		"payload":  json.RawMessage(`{"target_version":"v1.2.3"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := result.(map[string]string); !ok || got["prepared"] != "true" {
		t.Fatalf("prepare result = %#v", result)
	}
}

func TestHostControlRPCCancelRequiresToken(t *testing.T) {
	control := NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{})
	host := &Host{control: control}
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	registry := host.LocalControlRegistry(&hostProcessState{})
	handler, ok := registry.handler(LocalControlUpgradeCancelOperation)
	if !ok {
		t.Fatal("upgrade:cancel RPC was not registered")
	}
	_, err := handler(context.Background(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "authentication") {
		t.Fatalf("unauthenticated cancel error = %v", err)
	}
}

func TestHostProcessHealthRPCPreservesLifecycleFields(t *testing.T) {
	started := time.Now().Add(-time.Second)
	state := &hostProcessState{
		identity: HostProcessIdentity{
			ComputerID: "computer-1", ComputerGeneration: 7, Environment: "test",
			Version: "v1.2.3", ServerURL: "https://test.example", DeviceName: "laptop",
		},
		startedAt: started, ready: true, desired: []string{"ws-1"},
	}
	result := (&Host{}).processHealthResult(state)
	for key, want := range map[string]string{
		"daemon_id": "computer-1", "computer_id": "computer-1", "server_url": "https://test.example",
		"device_name": "laptop", "environment": "test", "cli_version": "v1.2.3", "status": "running",
	} {
		if got, ok := result[key].(string); !ok || got != want {
			t.Errorf("health[%q] = %#v, want %q", key, result[key], want)
		}
	}
}
