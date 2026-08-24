package daemon

import (
	"context"
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func (d *WorkspaceDaemonCore) attachWorkspaceSession(runner *workspaceSession) {
	if d == nil || runner == nil || runner.WorkspaceID() == "" {
		return
	}
	d.workspaceSessionMu.Lock()
	d.workspaceSession = runner
	d.workspaceSessionMu.Unlock()
}

func (runner *workspaceSession) startManagedAgent(ctx context.Context, payload protocol.WorkspaceDaemonAgentStartPayload) (protocol.AgentStartAckPayload, protocol.AgentStatusPayload, protocol.AgentSessionPayload, error) {
	ack, err := runner.registerManagedAgentStart(payload)
	if err != nil {
		return protocol.AgentStartAckPayload{}, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, err
	}
	callback := agentProcessCallback{AgentID: payload.AgentID, LaunchID: payload.LaunchID}
	failed := false
	defer func() {
		if failed {
			runner.processes.completeFailedManagedStart(callback)
		} else {
			runner.processes.completeManagedStart(callback)
		}
	}()
	outcome, err := runner.completeManagedAgentStart(ctx, payload, ack)
	if err != nil {
		failed = true
		runner.publishManagedAgentStartFailure(payload, outcome)
		return ack, outcome.status, outcome.session, err
	}
	if err := runner.processes.publishManagedStart(callback, func() error {
		return runner.establishManagedAgentStart(payload, outcome)
	}); err != nil {
		failed = errors.Is(err, errManagedAgentStartStopped)
		return ack, protocol.AgentStatusPayload{}, protocol.AgentSessionPayload{}, err
	}
	runner.broadcastActivity(payload.AgentID, payload.RuntimeID, "starting")
	runner.flushManagedAgentStartMessages(ctx, payload, ack)
	runner.observeResidentRuntimeReady(payload.AgentID, payload.RuntimeID)
	return ack, outcome.status, outcome.session, nil
}

func attachTestWorkspaceDaemon(t *testing.T, d *WorkspaceDaemonCore, workspaceID string, send func(string, any) error) (*workspaceSession, *DaemonConnection) {
	t.Helper()
	if send == nil {
		send = func(string, any) error { return nil }
	}
	prepareHeadlessWorkspaceDaemonTestDaemon(d, "")
	runner, err := d.newWorkspaceSession(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	connection := newDaemonConnection(workspaceID, context.Background(), send, func() {})
	runner.replaceConnection(connection)
	d.attachWorkspaceSession(runner)
	t.Cleanup(func() {
		d.detachWorkspaceSession(runner)
		runner.releaseConnection(connection)
		runner.Close()
		runner.inboxes.Close()
	})
	return runner, connection
}

func registerTestInbox(t *testing.T, d *WorkspaceDaemonCore, key InboxKey, runtimeID string, coordinator *MessageCoordinator) *workspaceSession {
	t.Helper()
	runner := d.currentWorkspaceSession(key.WorkspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceDaemon(t, d, key.WorkspaceID, nil)
	}
	registerTestRunnerInbox(t, runner, key, runtimeID, coordinator)
	return runner
}

func installTestRunnerActivity(t *testing.T, d *WorkspaceDaemonCore, workspaceID string, producer *agentActivityProducer) *workspaceSession {
	t.Helper()
	runner := d.currentWorkspaceSession(workspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceDaemon(t, d, workspaceID, nil)
	}
	runner.activity = producer
	return runner
}

func markTestLaunchRunning(t *testing.T, runner *workspaceSession, agentID string) {
	t.Helper()
	launch, ok := runner.processes.Snapshot(agentID)
	if !ok {
		t.Fatalf("APM launch for %q is missing", agentID)
	}
	callback := agentProcessCallback{AgentID: agentID, LaunchID: launch.LaunchID, ProcessInstanceID: "test-process-" + agentID}
	if err := runner.processes.ProcessSpawned(callback); err != nil {
		t.Fatalf("ProcessSpawned(%s): %v", agentID, err)
	}
	if err := runner.processes.RuntimeReady(callback); err != nil {
		t.Fatalf("RuntimeReady(%s): %v", agentID, err)
	}
}

func registerTestRunnerInbox(t *testing.T, runner *workspaceSession, key InboxKey, runtimeID string, coordinator *MessageCoordinator) {
	t.Helper()
	if runner == nil || runner.inboxes == nil {
		t.Fatal("test WorkspaceDaemon has no Inbox registry")
	}
	if runner.config.WorkspaceID != key.WorkspaceID {
		t.Fatalf("WorkspaceDaemon Workspace %q cannot register Inbox %+v", runner.config.WorkspaceID, key)
	}
	runner.inboxes.mu.Lock()
	if previous := runner.inboxes.inboxes[key.AgentID]; previous.coordinator != nil && previous.coordinator != coordinator {
		previous.coordinator.Close()
	}
	runner.inboxes.inboxes[key.AgentID] = inboxRegistryEntry{runtimeID: runtimeID, coordinator: coordinator}
	runner.inboxes.mu.Unlock()
	if runner.processes != nil {
		if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: key.AgentID, RuntimeID: runtimeID, LaunchID: "test-launch-" + key.AgentID, StartDispatchID: "test-launch-" + key.AgentID + "-dispatch"}); err != nil {
			t.Fatalf("register test APM launch: %v", err)
		}
		if runner.residency != nil {
			startStopEpoch, _ := runner.processes.startStopEpoch(agentProcessCallback{AgentID: key.AgentID, LaunchID: "test-launch-" + key.AgentID})
			runner.residency.rememberLaunch(key.AgentID, runtimeID, "test-launch-"+key.AgentID, "test-launch-"+key.AgentID+"-dispatch", startStopEpoch)
		}
	}
}

func resolveTestInbox(t *testing.T, d *WorkspaceDaemonCore, key InboxKey) (*MessageCoordinator, string) {
	t.Helper()
	runner := d.currentWorkspaceSession(key.WorkspaceID)
	if runner == nil || runner.inboxes == nil {
		t.Fatalf("WorkspaceDaemon %q is unavailable", key.WorkspaceID)
	}
	coordinator, runtimeID, ok := runner.inboxes.Resolve(key.AgentID)
	if !ok {
		t.Fatalf("Inbox %+v is unavailable", key)
	}
	return coordinator, runtimeID
}

func prepareHeadlessWorkspaceDaemonTestDaemon(d *WorkspaceDaemonCore, workspacesRoot string) {
	if d.cfg.DaemonID == "" {
		d.cfg.DaemonID = "daemon-test"
	}
	if d.cfg.WorkspacesRoot == "" {
		d.cfg.WorkspacesRoot = workspacesRoot
	}
	if d.runnerInstanceID == "" {
		d.runnerInstanceID = "runner-test"
	}
	if d.canonicalRuntimes == nil {
		d.canonicalRuntimes = newCanonicalAgentRuntimePool()
	}
	if d.processAdmission == nil {
		d.processAdmission = d.canonicalRuntimes.managedProcessAdmission()
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
}
