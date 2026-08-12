package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func completeTestWorkspaceRunnerAttachmentReplay(conn *websocket.Conn) error {
	_, raw, err := conn.ReadMessage()
	if err != nil {
		return err
	}
	var frame protocol.Message
	var request protocol.WorkspaceRunnerAttachmentReplayRequest
	if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != protocol.EventAgentAttachmentReplayReq {
		return fmt.Errorf("invalid Attachment replay request frame: %s", raw)
	}
	if err := json.Unmarshal(frame.Payload, &request); err != nil || request.Validate() != nil {
		return fmt.Errorf("invalid Attachment replay request payload: %s", frame.Payload)
	}
	end, err := json.Marshal(protocol.Message{
		Type:    protocol.EventAgentAttachmentReplayEnd,
		Payload: marshalRaw(protocol.WorkspaceRunnerAttachmentReplayEnd{RuntimeCursors: request.RuntimeCursors}),
	})
	if err != nil {
		return err
	}
	if err := conn.WriteMessage(websocket.TextMessage, end); err != nil {
		return err
	}
	_, raw, err = conn.ReadMessage()
	if err != nil {
		return err
	}
	var ack protocol.WorkspaceRunnerAttachmentReplayAck
	if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != protocol.EventAgentAttachmentReplayAck {
		return fmt.Errorf("invalid Attachment replay acknowledgement frame: %s", raw)
	}
	if err := json.Unmarshal(frame.Payload, &ack); err != nil || ack.Validate() != nil || !reflect.DeepEqual(ack.RuntimeCursors, request.RuntimeCursors) {
		return fmt.Errorf("invalid Attachment replay acknowledgement payload: %s", frame.Payload)
	}
	return nil
}

func attachTestWorkspaceRunner(t *testing.T, d *Daemon, workspaceID string, send func(string, any) error) (*WorkspaceRunner, *workspaceRunnerConnection) {
	t.Helper()
	if send == nil {
		send = func(string, any) error { return nil }
	}
	prepareHeadlessWorkspaceRunnerTestDaemon(d, "")
	runner, err := d.newWorkspaceRunner(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	connection := &workspaceRunnerConnection{
		workspaceID: workspaceID,
		ctx:         ctx,
		cancel:      cancel,
		write:       send,
		close:       func() {},
	}
	runner.replaceConnection(connection)
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.releaseConnection(connection)
		runner.inboxes.Close()
	})
	return runner, connection
}

func registerTestInbox(t *testing.T, d *Daemon, key InboxKey, runtimeID string, coordinator *MessageCoordinator) *WorkspaceRunner {
	t.Helper()
	runner := d.currentWorkspaceRunner(key.WorkspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceRunner(t, d, key.WorkspaceID, nil)
	}
	registerTestRunnerInbox(t, runner, key, runtimeID, coordinator)
	return runner
}

func installTestRunnerActivity(t *testing.T, d *Daemon, workspaceID string, producer *agentActivityProducer) *WorkspaceRunner {
	t.Helper()
	runner := d.currentWorkspaceRunner(workspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceRunner(t, d, workspaceID, nil)
	}
	runner.activity = producer
	return runner
}

func registerTestRunnerInbox(t *testing.T, runner *WorkspaceRunner, key InboxKey, runtimeID string, coordinator *MessageCoordinator) {
	t.Helper()
	if runner == nil || runner.inboxes == nil {
		t.Fatal("test Workspace Runner has no Inbox registry")
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
		if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: key.AgentID, RuntimeID: runtimeID, LaunchID: "test-launch-" + key.AgentID}); err != nil {
			t.Fatalf("register test APM launch: %v", err)
		}
	}
}

func resolveTestInbox(t *testing.T, d *Daemon, key InboxKey) (*MessageCoordinator, string) {
	t.Helper()
	runner := d.currentWorkspaceRunner(key.WorkspaceID)
	if runner == nil || runner.inboxes == nil {
		t.Fatalf("Workspace Runner %q is unavailable", key.WorkspaceID)
	}
	coordinator, runtimeID, ok := runner.inboxes.Resolve(key.AgentID)
	if !ok {
		t.Fatalf("Inbox %+v is unavailable", key)
	}
	return coordinator, runtimeID
}

func prepareHeadlessWorkspaceRunnerTestDaemon(d *Daemon, workspacesRoot string) {
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
	if d.client == nil {
		d.client = NewClient("")
	}
	if d.runtimeIndex == nil {
		d.runtimeIndex = make(map[string]Runtime)
	}
	if d.workspaces == nil {
		d.workspaces = make(map[string]*workspaceState)
	}
	if d.agentAttachments == nil {
		d.agentAttachments = newLocalAgentAttachmentRegistry(workspacesRoot, d.logger)
	}
	if d.workspaceRunners == nil {
		d.workspaceRunners = make(map[string]*WorkspaceRunner)
	}
}
