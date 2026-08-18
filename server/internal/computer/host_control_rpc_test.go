package computer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHostControlRPCDispatchesCapacityOperation(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", StartIdentity: "start-2", PID: 1234}
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

func TestHostControlRPCRejectsStaleStartIdentity(t *testing.T) {
	active := BindingChildIdentity{WorkspaceID: "ws-1", StartIdentity: "start-2", PID: 1234}
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
		Identity:  BindingChildIdentity{WorkspaceID: "ws-1", StartIdentity: "start-1", PID: 1234},
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
	identity := BindingChildIdentity{WorkspaceID: "ws-1", StartIdentity: "start-2", PID: 1234}
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
			ComputerID: "computer-1", ServiceGeneration: "service-7", Environment: "test",
			Version: "v1.2.3", ServerURL: "https://test.example", DeviceName: "laptop",
		},
		startedAt: started, ready: true, desired: []string{"ws-1"},
	}
	result := (&Host{}).processHealthResult(state)
	for key, want := range map[string]string{
		"daemonId": "computer-1", "computerId": "computer-1", "serverUrl": "https://test.example",
		"deviceName": "laptop", "environment": "test", "cliVersion": "v1.2.3", "status": "running",
	} {
		if got, ok := result[key].(string); !ok || got != want {
			t.Errorf("health[%q] = %#v, want %q", key, result[key], want)
		}
	}
}
