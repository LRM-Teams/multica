package computer

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestHostControlRPCDispatchesCapacityOperation(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
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

func TestHostControlRPCRejectsStaleDaemonInstanceID(t *testing.T) {
	active := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
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
		Identity:  BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-1", PID: 1234},
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
	identity := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
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

func TestHostControlClientUpgradeStartUsesCommandEnvelope(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
	command := protocol.ComputerUpgradePayload{RequestID: "upgrade-a", OperationID: "op-a", TargetVersion: "v9.9.9"}
	var got json.RawMessage
	endpoint := localControlTestServer(t, func(_ context.Context, operation string, _ map[string]string, raw json.RawMessage) (any, error) {
		if operation != LocalControlUpgradeStartOperation {
			t.Fatalf("operation = %q", operation)
		}
		got = append(json.RawMessage(nil), raw...)
		return map[string]string{"id": "op-a", "phase": "starting"}, nil
	})
	client := NewHostControlClient(endpoint, "control-token", identity)
	if err := client.RequestComputerUpgrade(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope["payload"]; ok {
		t.Fatalf("upgrade:start still wraps the command as payload: %s", got)
	}
	if string(envelope["identity"]) == "" || string(envelope["command"]) == "" {
		t.Fatalf("upgrade:start envelope = %s", got)
	}
	var decoded protocol.ComputerUpgradePayload
	if err := json.Unmarshal(envelope["command"], &decoded); err != nil {
		t.Fatalf("command decode: %v (%s)", err, envelope["command"])
	}
	if decoded.RequestID != command.RequestID || decoded.OperationID != command.OperationID || decoded.TargetVersion != command.TargetVersion {
		t.Fatalf("command = %+v, want %+v", decoded, command)
	}
	if !strings.Contains(string(envelope["command"]), `"requestId"`) || !strings.Contains(string(envelope["command"]), `"operationId"`) || !strings.Contains(string(envelope["command"]), `"targetVersion"`) {
		t.Fatalf("command JSON is not camelCase: %s", envelope["command"])
	}
}

func TestHostUpgradeStartDecodesCommandEnvelope(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
	var got protocol.ComputerUpgradePayload
	host := &Host{control: NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(gotIdentity BindingChildIdentity) bool { return gotIdentity == identity },
		ComputerUpgrade: func(_ context.Context, gotIdentity BindingChildIdentity, raw json.RawMessage) error {
			if gotIdentity != identity {
				t.Fatalf("identity = %+v", gotIdentity)
			}
			return json.Unmarshal(raw, &got)
		},
	})}
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	registry := host.LocalControlRegistry(&hostProcessState{})
	handler, ok := registry.handler(LocalControlUpgradeStartOperation)
	if !ok {
		t.Fatal("upgrade:start RPC was not registered")
	}
	args, err := json.Marshal(map[string]any{
		"identity": identity,
		"command":  protocol.ComputerUpgradePayload{RequestID: "upgrade-a", OperationID: "op-a", TargetVersion: "v9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestID != "upgrade-a" || got.OperationID != "op-a" || got.TargetVersion != "v9.9.9" {
		t.Fatalf("decoded command = %+v", got)
	}
	response, ok := result.(map[string]string)
	if !ok || response["id"] != "op-a" || response["phase"] != "starting" {
		t.Fatalf("upgrade:start result = %#v", result)
	}
}

func TestHostUpgradeStartReturnsRequestIDWhenOperationIDIsEmpty(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
	host := &Host{control: NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == identity },
		ComputerUpgrade: func(context.Context, BindingChildIdentity, json.RawMessage) error {
			return nil
		},
	})}
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	registry := host.LocalControlRegistry(&hostProcessState{})
	handler, ok := registry.handler(LocalControlUpgradeStartOperation)
	if !ok {
		t.Fatal("upgrade:start RPC was not registered")
	}
	args, err := json.Marshal(map[string]any{
		"identity": identity,
		"command":  protocol.ComputerUpgradePayload{RequestID: "upgrade-a", TargetVersion: "v9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := result.(map[string]string)
	if !ok || response["id"] != "upgrade-a" {
		t.Fatalf("upgrade:start result = %#v", result)
	}
}

func TestHostUpgradeStartRejectsStaleIdentity(t *testing.T) {
	active := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
	started := false
	host := &Host{control: NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{
		Current: func(got BindingChildIdentity) bool { return got == active },
		ComputerUpgrade: func(context.Context, BindingChildIdentity, json.RawMessage) error {
			started = true
			return nil
		},
	})}
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	registry := host.LocalControlRegistry(&hostProcessState{})
	handler, ok := registry.handler(LocalControlUpgradeStartOperation)
	if !ok {
		t.Fatal("upgrade:start RPC was not registered")
	}
	args, err := json.Marshal(map[string]any{
		"identity": BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-1", PID: 1234},
		"command":  protocol.ComputerUpgradePayload{RequestID: "upgrade-a", TargetVersion: "v9.9.9"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args); err == nil {
		t.Fatal("stale upgrade identity was accepted")
	}
	if started || host.upgrade.activeID != "" {
		t.Fatalf("stale upgrade still started (callback=%t active=%q)", started, host.upgrade.activeID)
	}
}

func TestHostUpgradeStartRejectsMissingCommand(t *testing.T) {
	host := &Host{control: NewHostControl("control-token", NewProcessCapacity(1), HostControlCallbacks{})}
	host.upgrade = newHostMachineUpgrade(host, hostMachineUpgradeConfig{})
	registry := host.LocalControlRegistry(&hostProcessState{})
	handler, ok := registry.handler(LocalControlUpgradeStartOperation)
	if !ok {
		t.Fatal("upgrade:start RPC was not registered")
	}
	args, err := json.Marshal(map[string]any{
		"identity": BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = handler(context.Background(), map[string]string{"X-Multica-Control-Token": "control-token"}, args)
	if err == nil || strings.Contains(err.Error(), "unexpected end of JSON input") {
		t.Fatalf("missing command error = %v", err)
	}
}

func TestBindingComputerUpgradeEventUsesCamelCaseEventType(t *testing.T) {
	identity := BindingChildIdentity{WorkspaceID: "ws-1", DaemonInstanceID: "start-2", PID: 1234}
	var got json.RawMessage
	var operation string
	endpoint := localControlTestServer(t, func(_ context.Context, name string, _ map[string]string, raw json.RawMessage) (any, error) {
		operation = name
		got = append(json.RawMessage(nil), raw...)
		return nil, nil
	})
	if err := RequestBindingComputerUpgradeEvent(context.Background(), endpoint, "control-token", identity, protocol.EventComputerUpgradeProgress, protocol.ComputerUpgradeProgressPayload{
		RequestID: "upgrade-a", Phase: "staging",
	}); err != nil {
		t.Fatal(err)
	}
	if operation != LocalControlUpgradeEventOperation {
		t.Fatalf("operation = %q, want %q", operation, LocalControlUpgradeEventOperation)
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(got, &envelope); err != nil {
		t.Fatal(err)
	}
	if _, ok := envelope["event_type"]; ok {
		t.Fatalf("upgrade event still uses event_type: %s", got)
	}
	if string(envelope["eventType"]) != `"computer:upgrade:progress"` {
		t.Fatalf("eventType = %s", envelope["eventType"])
	}
}
