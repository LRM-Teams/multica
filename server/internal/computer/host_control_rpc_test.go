package computer

import (
	"context"
	"encoding/json"
	"testing"
)

func TestHostControlRPCDispatchesCapacityOperation(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", RunnerGeneration: 2, PID: 1234}
	control := NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
	})
	registry := NewLocalControlRegistry()
	control.RegisterRPCHandlers(registry)
	handler, ok := registry.handler("workspace-capacity")
	if !ok {
		t.Fatal("workspace-capacity RPC was not registered")
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
	handler, ok := registry.handler("workspace-capacity")
	if !ok {
		t.Fatal("workspace-capacity RPC was not registered")
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
