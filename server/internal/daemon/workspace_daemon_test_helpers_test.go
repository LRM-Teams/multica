package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (runner *WorkspaceDaemon) startManagedAgent(ctx context.Context, payload protocol.AgentStartPayload) (protocol.AgentStartAckPayload, protocol.AgentStatusPayload, protocol.AgentSessionPayload, error) {
	ack, callback, replayed, err := runner.registerManagedAgentStartOnce(payload)
	if err != nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, err
	}
	if replayed {
		return ack, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, nil
	}
	failed := false
	defer func() {
		if failed {
			runner.processes.completeFailedManagedStart(callback)
		} else {
			runner.processes.completeManagedStart(callback)
		}
	}()
	outcome, err := runner.completeManagedAgentStart(ctx, payload, callback, ack)
	if err != nil {
		failed = true
		runner.publishManagedAgentStartFailure(payload, callback, outcome)
		return ack, outcome.status, outcome.session, err
	}
	if err := runner.processes.publishManagedStart(callback, func() error {
		return runner.establishManagedAgentStart(payload, callback.AgentInstanceID, outcome)
	}); err != nil {
		failed = errors.Is(err, errManagedAgentStartStopped)
		return ack, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, err
	}
	runner.broadcastActivity(payload.AgentID, payload.RuntimeID, "starting")
	runner.flushManagedAgentStartMessages(ctx, payload, ack)
	runner.observeResidentRuntimeReady(payload.AgentID, payload.RuntimeID)
	return ack, outcome.status, outcome.session, nil
}

func attachTestWorkspaceDaemon(t *testing.T, d *Daemon, workspaceID string, send func(string, any) error) (*WorkspaceDaemon, *DaemonConnection) {
	t.Helper()
	if send == nil {
		send = func(string, any) error { return nil }
	}
	prepareHeadlessWorkspaceDaemonTestDaemon(d, "")
	runner, err := d.newWorkspaceDaemon(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	connection := newDaemonConnection(workspaceID, context.Background(), send, func() {})
	runner.replaceConnection(connection)
	d.attachWorkspaceDaemon(runner)
	t.Cleanup(func() {
		d.detachWorkspaceDaemon(runner)
		runner.releaseConnection(connection)
		runner.Close()
		runner.inboxes.Close()
	})
	return runner, connection
}

func registerTestInbox(t *testing.T, d *Daemon, key InboxKey, runtimeID string, coordinator *MessageCoordinator) *WorkspaceDaemon {
	t.Helper()
	runner := d.currentWorkspaceDaemon(key.WorkspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceDaemon(t, d, key.WorkspaceID, nil)
	}
	registerTestWorkspaceDaemonInbox(t, runner, key, runtimeID, coordinator)
	return runner
}

func installTestAgentActivityProducer(t *testing.T, d *Daemon, workspaceID string, producer *agentActivityProducer) *WorkspaceDaemon {
	t.Helper()
	runner := d.currentWorkspaceDaemon(workspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceDaemon(t, d, workspaceID, nil)
	}
	runner.activity = producer
	return runner
}

func markTestLaunchRunning(t *testing.T, runner *WorkspaceDaemon, agentID string) {
	t.Helper()
	instance, ok := runner.processes.Snapshot(agentID)
	if !ok {
		t.Fatalf("APM launch for %q is missing", agentID)
	}
	callback := agentProcessCallback{AgentID: agentID, AgentInstanceID: instance.AgentInstanceID, ProcessInstanceID: "test-process-" + agentID}
	if err := runner.processes.ProcessSpawned(callback); err != nil {
		t.Fatalf("ProcessSpawned(%s): %v", agentID, err)
	}
	if err := runner.processes.RuntimeReady(callback); err != nil {
		t.Fatalf("RuntimeReady(%s): %v", agentID, err)
	}
}

func startTestManagedAgent(t *testing.T, runner *WorkspaceDaemon, agentID, runtimeID, _ string) agentProcessStartAcceptance {
	t.Helper()
	accepted, err := runner.processes.Start(agentProcessStartRequest{AgentID: agentID, RuntimeID: runtimeID})
	if err != nil {
		t.Fatalf("start test managed Agent: %v", err)
	}
	if runner.residency != nil {
		runner.residency.rememberLaunch(agentID, runtimeID, accepted.AgentInstanceID)
	}
	return accepted
}

func currentTestAgentProcessCallback(t *testing.T, runner *WorkspaceDaemon, agentID string) agentProcessCallback {
	t.Helper()
	current, found := runner.processes.Snapshot(agentID)
	if !found {
		t.Fatalf("APM Agent instance for %q is missing", agentID)
	}
	return agentProcessCallback{AgentID: agentID, AgentInstanceID: current.AgentInstanceID, ProcessInstanceID: current.ProcessInstanceID}
}

func registerTestWorkspaceDaemonInbox(t *testing.T, runner *WorkspaceDaemon, key InboxKey, runtimeID string, coordinator *MessageCoordinator) {
	t.Helper()
	if runner == nil || runner.inboxes == nil {
		t.Fatal("test WorkspaceDaemon has no Inbox registry")
	}
	if runner.config.WorkspaceID != key.WorkspaceID {
		t.Fatalf("Runner Workspace %q cannot register Inbox %+v", runner.config.WorkspaceID, key)
	}
	runner.inboxes.mu.Lock()
	if previous := runner.inboxes.inboxes[key.AgentID]; previous.coordinator != nil && previous.coordinator != coordinator {
		previous.coordinator.Close()
	}
	runner.inboxes.inboxes[key.AgentID] = inboxRegistryEntry{runtimeID: runtimeID, coordinator: coordinator}
	runner.inboxes.mu.Unlock()
	if runner.processes != nil {
		accepted, err := runner.processes.Start(agentProcessStartRequest{AgentID: key.AgentID, RuntimeID: runtimeID})
		if err != nil {
			t.Fatalf("register test APM launch: %v", err)
		}
		if runner.residency != nil {
			runner.residency.rememberLaunch(key.AgentID, runtimeID, accepted.AgentInstanceID)
		}
	}
}

func resolveTestInbox(t *testing.T, d *Daemon, key InboxKey) (*MessageCoordinator, string) {
	t.Helper()
	runner := d.currentWorkspaceDaemon(key.WorkspaceID)
	if runner == nil || runner.inboxes == nil {
		t.Fatalf("WorkspaceDaemon %q is unavailable", key.WorkspaceID)
	}
	coordinator, runtimeID, ok := runner.inboxes.Resolve(key.AgentID)
	if !ok {
		t.Fatalf("Inbox %+v is unavailable", key)
	}
	return coordinator, runtimeID
}

func prepareHeadlessWorkspaceDaemonTestDaemon(d *Daemon, workspacesRoot string) {
	if d.cfg.DaemonID == "" {
		d.cfg.DaemonID = "daemon-test"
	}
	if d.cfg.WorkspacesRoot == "" {
		d.cfg.WorkspacesRoot = workspacesRoot
	}
	if d.instanceID == "" {
		d.instanceID = "runner-test"
	}
	if d.canonicalRuntimes == nil {
		d.canonicalRuntimes = newAgentRuntimePool()
	}
	if d.client == nil {
		d.client = NewClient("")
	}
	if d.runtimeIndex == nil {
		d.runtimeIndex = make(map[string]Runtime)
	}
	if d.workspaces == nil {
		d.workspaces = make(map[string]*workspaceState)
	}
	if d.workspaceDaemons == nil {
		d.workspaceDaemons = make(map[string]*WorkspaceDaemon)
	}
}
