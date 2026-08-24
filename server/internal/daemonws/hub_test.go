package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/realtime"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestNotifyTaskAvailable(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime connection was not registered")
		}
		time.Sleep(10 * time.Millisecond)
	}

	hub.NotifyTaskAvailable("runtime-1", "task-1")

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	if msg.Type != protocol.EventDaemonTaskAvailable {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonTaskAvailable)
	}

	var payload protocol.TaskAvailablePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.RuntimeID != "runtime-1" || payload.TaskID != "task-1" {
		t.Fatalf("payload = %+v, want runtime/task IDs", payload)
	}
}

func TestRequestWorkdirFilesUsesRaftAgentWorkspaceFlow(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime connection was not registered")
		}
		time.Sleep(time.Millisecond)
	}

	type result struct {
		response *protocol.ListWorkdirFilesResponsePayload
		err      error
	}
	resultCh := make(chan result, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		response, requestErr := hub.RequestWorkdirFiles(ctx, protocol.ListWorkdirFilesRequestPayload{
			RequestID: "request-1",
			RuntimeID: "runtime-1",
			RelPath:   "workspaces/workspace-1/agents/agent-1",
		})
		resultCh <- result{response: response, err: requestErr}
	}()

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var request protocol.Message
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	if request.Type != protocol.EventAgentWorkspaceList {
		t.Fatalf("request type = %q, want %q", request.Type, protocol.EventAgentWorkspaceList)
	}

	responseFrame, err := json.Marshal(protocol.Message{
		Type: protocol.EventAgentWorkspaceFileTree,
		Payload: mustMarshalRaw(protocol.ListWorkdirFilesResponsePayload{
			RequestID: "request-1",
			Nodes:     []protocol.WorkdirFileNode{{Path: "MEMORY.md"}},
		}),
	})
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, responseFrame); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case got := <-resultCh:
		if got.err != nil {
			t.Fatalf("RequestWorkdirFiles: %v", got.err)
		}
		if len(got.response.Nodes) != 1 || got.response.Nodes[0].Path != "MEMORY.md" {
			t.Fatalf("response nodes = %#v", got.response.Nodes)
		}
	case <-time.After(time.Second):
		t.Fatal("RequestWorkdirFiles did not receive agent:workspace:file_tree")
	}
}

func TestWorkspaceDaemonRoutesOnlyWithinDaemonWorkspaceScope(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		workspaceID := r.URL.Query().Get("workspace")
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: workspaceID, RuntimeIDs: []string{"runtime-1", "runtime-2"}})
	}))
	defer server.Close()
	dial := func(workspaceID string) *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"?workspace="+workspaceID, nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	ready := func(conn *websocket.Conn, workspaceID string) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: mustMarshalRaw(protocol.WorkspaceReadyPayload{
			WorkspaceID: workspaceID, DaemonInstanceID: "instance-" + workspaceID,
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	workspaceA := dial("workspace-a")
	defer workspaceA.Close()
	workspaceB := dial("workspace-b")
	defer workspaceB.Close()
	ready(workspaceA, "workspace-a")
	ready(workspaceB, "workspace-b")
	waitForRunner(t, hub, "daemon-1", "workspace-a")
	waitForRunner(t, hub, "daemon-1", "workspace-b")

	if !hub.NotifyWorkspaceDaemon("daemon-1", "workspace-a", protocol.EventDaemonAgentStart, protocol.AgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-1"}) {
		t.Fatal("workspace-a command was not routed")
	}
	workspaceA.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := workspaceA.ReadMessage()
	if err != nil {
		t.Fatalf("read workspace-a command: %v", err)
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.EventDaemonAgentStart {
		t.Fatalf("workspace-a event = %q", message.Type)
	}
	workspaceB.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if _, _, err := workspaceB.ReadMessage(); err == nil {
		t.Fatal("workspace-b received a workspace-a command")
	}
}

func TestLegacyControlPlaneRunnerCanBecomeReadyOnlyForRollingUpgrade(t *testing.T) {
	hub := NewHub()
	ready := make(chan struct{}, 1)
	hub.SetHeartbeatHandler(func(_ context.Context, _ ClientIdentity, payload protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		return &protocol.DaemonHeartbeatAckPayload{
			RuntimeID: payload.RuntimeID,
			Status:    "ok",
		}, nil
	})
	hub.SetWorkspaceDaemonHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, _ json.RawMessage) error {
		if eventType == protocol.EventWorkspaceDaemonReady {
			ready <- struct{}{}
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: mustMarshalRaw(protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "legacy-instance-1",
		ActiveCapabilities: []string{"workspace_daemon_attachment_v1", protocol.DaemonCapabilityWorkspaceDaemonControlPlane},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatal(err)
	}

	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("legacy control-plane Runner could not become ready to receive its machine upgrade")
	}
	replayRequest, err := json.Marshal(protocol.Message{Type: "agent:attachment.replay_request", Payload: mustMarshalRaw(struct {
		RuntimeCursors map[string]int64 `json:"runtimeCursors"`
	}{RuntimeCursors: map[string]int64{"runtime-1": 3}})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, replayRequest); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("legacy control-plane Runner did not finish its retired replay handshake: %v", err)
	}
	var replayEnd protocol.Message
	if err := json.Unmarshal(raw, &replayEnd); err != nil {
		t.Fatal(err)
	}
	var replayEndPayload struct {
		RuntimeCursors map[string]int64 `json:"runtimeCursors"`
	}
	if replayEnd.Type != "agent:attachment.replay_end" || json.Unmarshal(replayEnd.Payload, &replayEndPayload) != nil || replayEndPayload.RuntimeCursors["runtime-1"] != 3 {
		t.Fatalf("replay end = %s, want echoed legacy cursor", raw)
	}
	heartbeat, err := json.Marshal(protocol.Message{Type: protocol.EventDaemonHeartbeat, Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{
		RuntimeID: "runtime-1",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, heartbeat); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err = conn.ReadMessage()
	if err != nil {
		t.Fatalf("legacy control-plane Runner did not receive heartbeat ack: %v", err)
	}
	var ack protocol.Message
	if err := json.Unmarshal(raw, &ack); err != nil {
		t.Fatal(err)
	}
	var heartbeatAck protocol.DaemonHeartbeatAckPayload
	if ack.Type != protocol.EventDaemonHeartbeatAck || json.Unmarshal(ack.Payload, &heartbeatAck) != nil || heartbeatAck.RuntimeID != "runtime-1" || heartbeatAck.Status != "ok" {
		t.Fatalf("heartbeat ack = %s, want control-plane heartbeat ack", raw)
	}
	if hub.NotifyAgentRestartCommand("workspace-1", "daemon-1", protocol.EventDaemonAgentStart, "dispatch-1", protocol.AgentStartPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1"}) {
		t.Fatal("legacy rolling-upgrade Runner received a new Agent process command")
	}
}

func TestWorkspaceDaemonCapabilityBelongsOnlyToCurrentReadyConnection(t *testing.T) {
	hub := NewHub()
	key := workspaceDaemonKey{daemonID: "daemon-1", workspaceID: "workspace-1"}
	old := &client{runnerCapabilities: map[string]struct{}{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess: {}}}
	current := &client{runnerCapabilities: map[string]struct{}{protocol.DaemonCapabilityWorkspaceDaemonAgentReset: {}}}
	hub.mu.Lock()
	hub.byRunner[key] = old
	hub.mu.Unlock()
	if !hub.WorkspaceDaemonSupportsCapability("daemon-1", "workspace-1", protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) {
		t.Fatal("current Runner Attachment capability was not visible")
	}
	hub.mu.Lock()
	hub.byRunner[key] = current
	hub.mu.Unlock()
	if hub.WorkspaceDaemonSupportsCapability("daemon-1", "workspace-1", protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) {
		t.Fatal("replaced Runner lent its Attachment capability to the current connection")
	}
	if !hub.WorkspaceDaemonSupportsCapability("daemon-1", "workspace-1", protocol.DaemonCapabilityWorkspaceDaemonAgentReset) {
		t.Fatal("current Runner capability was not visible")
	}
}

func TestWorkspaceDaemonReadyReplacesConnectionAndFencesInboundFrames(t *testing.T) {
	hub := NewHub()
	var accepted atomic.Int64
	var runnerReady atomic.Int64
	hub.SetWorkspaceDaemonHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, _ json.RawMessage) error {
		if eventType == protocol.EventAgentStartAck {
			accepted.Add(1)
		}
		if eventType == protocol.EventWorkspaceDaemonReady {
			runnerReady.Add(1)
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	dial := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	write := func(conn *websocket.Conn, eventType string, payload any) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	first := dial()
	defer first.Close()
	// An acknowledgement before ready must not reach the new boundary.
	write(first, protocol.EventAgentStartAck, protocol.AgentStartAckPayload{AgentID: "agent-a", QueueState: protocol.AgentStartQueueQueued})
	time.Sleep(20 * time.Millisecond)
	if accepted.Load() != 0 {
		t.Fatal("unready connection mutated Runner state")
	}
	write(first, protocol.EventWorkspaceDaemonReady, protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	write(first, protocol.EventAgentStartAck, protocol.AgentStartAckPayload{AgentID: "agent-a", QueueState: protocol.AgentStartQueueQueued})
	deadline := time.Now().Add(time.Second)
	for accepted.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("ready Runner acknowledgement was not delivered")
		}
		time.Sleep(time.Millisecond)
	}
	second := dial()
	defer second.Close()
	write(second, protocol.EventWorkspaceDaemonReady, protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-2",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	for runnerReady.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("replacement Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	// The replaced socket either rejects this write locally or the Hub drops it
	// at the current-ready fence. It must never reach the start receipt handler.
	staleReceipt, err := json.Marshal(protocol.Message{Type: protocol.EventAgentStartAck, Payload: mustMarshalRaw(protocol.AgentStartAckPayload{
		AgentID: "agent-a", QueueState: protocol.AgentStartQueueQueued,
	})})
	if err != nil {
		t.Fatal(err)
	}
	_ = first.WriteMessage(websocket.TextMessage, staleReceipt)
	time.Sleep(20 * time.Millisecond)
	if accepted.Load() != 1 {
		t.Fatal("replaced Runner start receipt bypassed current-ready fence")
	}
	if !hub.NotifyWorkspaceDaemon("daemon-1", "workspace-1", protocol.EventWorkspaceDaemonPing, protocol.WorkspacePingPayload{PingID: "ping-1"}) {
		t.Fatal("current Runner did not receive ping")
	}
	second.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := second.ReadMessage(); err != nil {
		t.Fatalf("current Runner did not receive ping: %v", err)
	}
}

func TestWorkspaceDaemonReadyDispatchesStatusAfterCurrentSlotIsClaimed(t *testing.T) {
	hub := NewHub()
	var order []string
	hub.SetWorkspaceDaemonHandler(func(_ context.Context, _ ClientIdentity, daemonInstanceID, eventType string, _ json.RawMessage) error {
		if eventType == protocol.EventWorkspaceDaemonReady {
			// Mimic the production ready handler: it does DB work before the
			// Computer can replay agent:status on the same socket.
			time.Sleep(30 * time.Millisecond)
		}
		order = append(order, eventType+":"+daemonInstanceID)
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	write := func(eventType string, payload any) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	write(protocol.EventWorkspaceDaemonReady, protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	write(protocol.EventAgentStatus, protocol.AgentStatusPayload{
		AgentID: "agent-1", Status: protocol.AgentStatusActive,
	})
	deadline := time.Now().Add(time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatalf("ready/status order = %v, want ready then active status", order)
		}
		if len(order) >= 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if order[0] != protocol.EventWorkspaceDaemonReady+":instance-1" || order[1] != protocol.EventAgentStatus+":instance-1" {
		t.Fatalf("ready/status order = %v, want ready then active status on the claimed slot", order)
	}
}

func TestCloseWorkspaceDaemonFencesReplacementDaemonInstance(t *testing.T) {
	hub := NewHub()
	var readyCount atomic.Int64
	hub.SetWorkspaceDaemonHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, _ json.RawMessage) error {
		if eventType == protocol.EventWorkspaceDaemonReady {
			readyCount.Add(1)
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()
	endpoint := "ws" + strings.TrimPrefix(server.URL, "http")
	dial := func() *websocket.Conn {
		conn, _, err := websocket.DefaultDialer.Dial(endpoint, nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	write := func(conn *websocket.Conn, payload protocol.WorkspaceReadyPayload) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: mustMarshalRaw(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	first := dial()
	defer first.Close()
	write(first, protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	second := dial()
	defer second.Close()
	write(second, protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-2",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})
	deadline := time.Now().Add(time.Second)
	for readyCount.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("replacement Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	if hub.CloseWorkspaceDaemon("daemon-1", "workspace-1", "instance-1") {
		t.Fatal("stale daemon instance closed the replacement Runner")
	}
	if hub.WorkspaceDaemonConnectionCount("daemon-1", "workspace-1") != 1 {
		t.Fatal("stale close removed the current Runner")
	}
	if !hub.CloseWorkspaceDaemon("daemon-1", "workspace-1", "instance-2") {
		t.Fatal("current daemon instance was not closed")
	}
	deadline = time.Now().Add(time.Second)
	for hub.WorkspaceDaemonConnectionCount("daemon-1", "workspace-1") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("current Runner remained registered after close")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkspaceDaemonDisconnectCallbackOnlyObservesCurrentRunner(t *testing.T) {
	hub := NewHub()
	disconnected := make(chan string, 2)
	hub.SetWorkspaceDaemonDisconnectHandler(func(_ context.Context, _ ClientIdentity, daemonInstanceID string) error {
		disconnected <- daemonInstanceID
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()

	dial := func() *websocket.Conn {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		if err != nil {
			t.Fatal(err)
		}
		return conn
	}
	ready := func(conn *websocket.Conn, instanceID string) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: mustMarshalRaw(protocol.WorkspaceReadyPayload{
			WorkspaceID: "workspace-1", DaemonInstanceID: instanceID,
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
		})})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}

	first := dial()
	defer first.Close()
	ready(first, "instance-1")
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	if !hub.IsCurrentWorkspaceDaemon("daemon-1", "workspace-1", "instance-1") {
		t.Fatal("first ready connection was not current")
	}
	if !hub.HasWorkspaceDaemon("daemon-1", "workspace-1") {
		t.Fatal("current Runner socket must count as Computer/Workspace liveness")
	}

	second := dial()
	defer second.Close()
	ready(second, "instance-2")
	deadline := time.Now().Add(time.Second)
	for !hub.IsCurrentWorkspaceDaemon("daemon-1", "workspace-1", "instance-2") {
		if time.Now().After(deadline) {
			t.Fatal("replacement Runner did not become current")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case instanceID := <-disconnected:
		t.Fatalf("replacement teardown emitted a disconnect for %q", instanceID)
	case <-time.After(50 * time.Millisecond):
	}

	if !hub.CloseWorkspaceDaemon("daemon-1", "workspace-1", "instance-2") {
		t.Fatal("current Runner did not close")
	}
	select {
	case instanceID := <-disconnected:
		if instanceID != "instance-2" {
			t.Fatalf("disconnect instance = %q, want instance-2", instanceID)
		}
	case <-time.After(time.Second):
		t.Fatal("current Runner disconnect callback was not delivered")
	}
	if hub.IsCurrentWorkspaceDaemon("daemon-1", "workspace-1", "instance-2") {
		t.Fatal("closed Runner remained current")
	}
	if hub.HasWorkspaceDaemon("daemon-1", "workspace-1") {
		t.Fatal("closed Runner socket must count as Computer/Workspace offline")
	}
}

func waitForRunner(t *testing.T, hub *Hub, daemonID, workspaceID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceDaemonConnectionCount(daemonID, workspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("WorkspaceDaemon %s/%s was not ready", daemonID, workspaceID)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkspaceDaemonDeliveryRetriesUntilCurrentRunnerAcknowledges(t *testing.T) {
	hub := NewHub()
	retries := make(chan func(), 2)
	hub.scheduleAgentDeliveryRetry = func(_ time.Duration, retry func()) { retries <- retry }
	hub.SetAgentDeliveryAckHandler(func(_ context.Context, identity ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
		if identity.DaemonID != "daemon-1" || identity.WorkspaceID != "workspace-1" || ack.AgentID != "agent-1" {
			t.Fatalf("unexpected Runner acknowledgement: identity=%+v ack=%+v", identity, ack)
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ready, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceDaemonReady, Payload: mustMarshalRaw(protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	delivery := protocol.AgentDeliverPayload{AgentID: "agent-1", Target: "channel:one", Seq: 7, DeliveryID: "delivery-1", Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 7, Content: "hello"}}
	if !hub.NotifyWorkspaceAgentDelivery("workspace-1", "daemon-1", delivery) {
		t.Fatal("Runner delivery was not queued")
	}
	readDelivery := func() protocol.AgentDeliverPayload {
		t.Helper()
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil || msg.Type != protocol.EventAgentDeliver {
			t.Fatalf("Runner delivery frame = %+v, err=%v", msg, err)
		}
		var got protocol.AgentDeliverPayload
		if err := json.Unmarshal(msg.Payload, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := readDelivery(); got.DeliveryID != delivery.DeliveryID {
		t.Fatalf("initial Runner delivery = %+v", got)
	}
	(<-retries)()
	if got := readDelivery(); got.DeliveryID != delivery.DeliveryID {
		t.Fatalf("retried Runner delivery = %+v", got)
	}
	ack, err := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliverAck, Payload: mustMarshalRaw(protocol.AgentDeliverAckPayload{AgentID: delivery.AgentID, Seq: delivery.Seq, DeliveryID: delivery.DeliveryID})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for {
		hub.agentDeliveryMu.Lock()
		pending := len(hub.pendingAgentDeliveries)
		hub.agentDeliveryMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Runner acknowledgement did not clear pending delivery")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestAgentDeliveryRetriesUntilAuthorizedAcknowledgement(t *testing.T) {
	hub := NewHub()
	retries := make(chan func(), 4)
	hub.scheduleAgentDeliveryRetry = func(_ time.Duration, retry func()) {
		retries <- retry
	}
	hub.SetAgentDeliveryAckHandler(func(_ context.Context, identity ClientIdentity, ack protocol.AgentDeliverAckPayload) error {
		if identity.WorkspaceID != "workspace-1" || ack.AgentID != "agent-1" || ack.Seq != 7 || ack.DeliveryID != "delivery-1" {
			t.Fatalf("unexpected acknowledgement: identity=%+v ack=%+v", identity, ack)
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	deadline := time.Now().Add(time.Second)
	for hub.RuntimeConnectionCount("runtime-1") == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime connection was not registered")
		}
		time.Sleep(time.Millisecond)
	}
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 7, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 7, Content: "hello"},
	}
	if !hub.NotifyAgentDelivery("runtime-1", delivery) {
		t.Fatal("initial delivery was not queued")
	}
	readAgentDelivery := func() protocol.AgentDeliverPayload {
		t.Helper()
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var envelope protocol.Message
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Type != protocol.EventAgentDeliver {
			t.Fatalf("event type = %q", envelope.Type)
		}
		var got protocol.AgentDeliverPayload
		if err := json.Unmarshal(envelope.Payload, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := readAgentDelivery(); got.DeliveryID != delivery.DeliveryID {
		t.Fatalf("initial delivery = %+v", got)
	}

	firstRetry := <-retries
	firstRetry()
	if got := readAgentDelivery(); got.DeliveryID != delivery.DeliveryID {
		t.Fatalf("retry delivery changed identity: %+v", got)
	}
	secondRetry := <-retries

	ack, err := json.Marshal(protocol.Message{
		Type: protocol.EventAgentDeliverAck,
		Payload: mustMarshalRaw(protocol.AgentDeliverAckPayload{
			AgentID: delivery.AgentID, Seq: delivery.Seq, DeliveryID: delivery.DeliveryID,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ack); err != nil {
		t.Fatal(err)
	}
	for {
		hub.agentDeliveryMu.Lock()
		pending := len(hub.pendingAgentDeliveries)
		hub.agentDeliveryMu.Unlock()
		if pending == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("authorized acknowledgement did not stop retry state")
		}
		time.Sleep(time.Millisecond)
	}
	secondRetry()
	if err := conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatal("delivery retried after acknowledgement")
	}
}

func TestRelayNotifierPublishesDaemonRuntimeScope(t *testing.T) {
	M.Reset()
	defer M.Reset()

	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)

	notifier.NotifyTaskAvailable("runtime-1", "task-1")

	if relay.scopeType != realtime.ScopeDaemonRuntime {
		t.Fatalf("scopeType = %q, want %q", relay.scopeType, realtime.ScopeDaemonRuntime)
	}
	if relay.scopeID != "task-1" {
		t.Fatalf("scopeID = %q, want task_id shard key", relay.scopeID)
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	if M.WakeupPublishedTotal.Load() != 1 {
		t.Fatalf("published metric = %d, want 1", M.WakeupPublishedTotal.Load())
	}

	var msg protocol.Message
	if err := json.Unmarshal(relay.frame, &msg); err != nil {
		t.Fatalf("unmarshal frame: %v", err)
	}
	if msg.Type != protocol.EventDaemonTaskAvailable {
		t.Fatalf("message type = %q, want %q", msg.Type, protocol.EventDaemonTaskAvailable)
	}
	var payload protocol.TaskAvailablePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.RuntimeID != "runtime-1" || payload.TaskID != "task-1" {
		t.Fatalf("payload = %+v, want runtime/task IDs", payload)
	}
}

func attachDaemonTestClient(hub *Hub, runtimeID string) *client {
	c := &client{
		send:     make(chan []byte, 2),
		done:     make(chan struct{}),
		runtimes: map[string]struct{}{runtimeID: {}},
	}
	hub.mu.Lock()
	hub.clients[c] = true
	hub.byRuntime[runtimeID] = map[*client]bool{c: true}
	hub.mu.Unlock()
	return c
}

type recordingRelayPublisher struct {
	scopeType string
	scopeID   string
	exclude   string
	frame     []byte
	eventID   string
}

func (r *recordingRelayPublisher) PublishWithID(scopeType, scopeID, exclude string, frame []byte, id string) error {
	r.scopeType = scopeType
	r.scopeID = scopeID
	r.exclude = exclude
	r.frame = append([]byte(nil), frame...)
	r.eventID = id
	return nil
}

type localFirstDaemonRelayPublisher struct {
	t      *testing.T
	client *client

	called     bool
	scopeType  string
	scopeID    string
	exclude    string
	frame      []byte
	eventID    string
	localFrame []byte
}

func (p *localFirstDaemonRelayPublisher) PublishWithID(scopeType, scopeID, exclude string, frame []byte, id string) error {
	p.called = true
	p.scopeType = scopeType
	p.scopeID = scopeID
	p.exclude = exclude
	p.frame = append([]byte(nil), frame...)
	p.eventID = id
	select {
	case p.localFrame = <-p.client.send:
	default:
		p.t.Fatal("expected local fanout to happen before relay publish")
	}
	return nil
}

func TestRelayNotifierDedupsLocalRedisLoopback(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	client := attachDaemonTestClient(hub, "runtime-1")
	relay := &localFirstDaemonRelayPublisher{t: t, client: client}
	notifier := NewRelayNotifier(hub, relay)

	notifier.NotifyTaskAvailable("runtime-1", "task-1")

	if !relay.called {
		t.Fatal("expected relay publish to be invoked")
	}
	if relay.eventID == "" {
		t.Fatal("expected event id")
	}
	if M.WakeupDeliveredHit.Load() != 1 {
		t.Fatalf("delivered hit metric = %d, want 1", M.WakeupDeliveredHit.Load())
	}

	hub.DeliverDaemonRuntime(relay.scopeID, relay.frame, relay.eventID)

	select {
	case duplicate := <-client.send:
		t.Fatalf("expected redis loopback to be deduped, got duplicate %s", duplicate)
	case <-time.After(20 * time.Millisecond):
	}
	if M.WakeupDeliveredHit.Load() != 1 {
		t.Fatalf("delivered hit metric after loopback = %d, want 1", M.WakeupDeliveredHit.Load())
	}
	if M.WakeupDeliveredMiss.Load() != 0 {
		t.Fatalf("delivered miss metric after dedup = %d, want 0", M.WakeupDeliveredMiss.Load())
	}
}

func TestAgentDeliveryRelayDedupsLocalRedisLoopbackWithoutDroppingRetry(t *testing.T) {
	hub := NewHub()
	hub.scheduleAgentDeliveryRetry = func(time.Duration, func()) {}
	client := attachDaemonTestClient(hub, "runtime-1")
	relay := &localFirstDaemonRelayPublisher{t: t, client: client}
	notifier := NewRelayNotifier(hub, relay)
	delivery := protocol.AgentDeliverPayload{
		AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
		Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"},
	}
	if !notifier.NotifyAgentDelivery("runtime-1", delivery) {
		t.Fatal("delivery was not accepted by local/relay notifier")
	}
	if !relay.called || relay.eventID == "" {
		t.Fatalf("relay publish = %+v", relay)
	}

	hub.DeliverDaemonRuntime("runtime-1", relay.frame, relay.eventID)
	select {
	case duplicate := <-client.send:
		t.Fatalf("expected agent delivery redis loopback to be deduped, got %s", duplicate)
	default:
	}
	hub.agentDeliveryMu.Lock()
	pending := hub.pendingAgentDeliveries[delivery.DeliveryID]
	hub.agentDeliveryMu.Unlock()
	if pending == nil {
		t.Fatal("deduped relay loopback dropped pending retry state")
	}
}

// TestHeartbeatRoundTrip pins the WS heartbeat contract: a daemon:heartbeat
// frame invokes the registered HeartbeatHandler with the runtime ID, and the
// hub serializes the returned ack as a daemon:heartbeat_ack on the wire.
func TestHeartbeatRoundTrip(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	var calls atomic.Int32
	hub.SetHeartbeatHandler(func(_ context.Context, identity ClientIdentity, payload protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		calls.Add(1)
		if identity.WorkspaceID != "ws-1" {
			t.Errorf("identity workspace = %q, want ws-1", identity.WorkspaceID)
		}
		if !payload.SupportsMemoryCuration || payload.ActiveMemoryCurationRunID != "run-1" {
			t.Errorf("memory curation heartbeat fields = %+v", payload)
		}
		return &protocol.DaemonHeartbeatAckPayload{
			RuntimeID: payload.RuntimeID,
			Status:    "ok",
			PendingUpdate: &protocol.DaemonHeartbeatPendingUpdate{
				ID:            "update-1",
				TargetVersion: "0.1.99",
			},
		}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{
			WorkspaceID: "ws-1",
			RuntimeIDs:  []string{"runtime-1"},
		})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	hbFrame, err := json.Marshal(protocol.Message{
		Type: protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{
			RuntimeID: "runtime-1", SupportsMemoryCuration: true, ActiveMemoryCurationRunID: "run-1",
		}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, hbFrame); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}

	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal ack envelope: %v", err)
	}
	if msg.Type != protocol.EventDaemonHeartbeatAck {
		t.Fatalf("ack type = %q, want %q", msg.Type, protocol.EventDaemonHeartbeatAck)
	}
	var ack protocol.DaemonHeartbeatAckPayload
	if err := json.Unmarshal(msg.Payload, &ack); err != nil {
		t.Fatalf("unmarshal ack payload: %v", err)
	}
	if ack.RuntimeID != "runtime-1" {
		t.Fatalf("ack runtime_id = %q, want runtime-1", ack.RuntimeID)
	}
	if ack.PendingUpdate == nil || ack.PendingUpdate.ID != "update-1" {
		t.Fatalf("ack pending_update = %+v, want update-1", ack.PendingUpdate)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("HeartbeatHandler invocations = %d, want 1", got)
	}
}

func TestWorkspaceDaemonControlPlaneHeartbeatRoundTrip(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	var calls atomic.Int32
	hub.SetHeartbeatHandler(func(_ context.Context, identity ClientIdentity, payload protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		calls.Add(1)
		if identity.DaemonID != "computer-1" || identity.WorkspaceID != "workspace-1" {
			t.Errorf("identity = %+v, want current Computer and WorkspaceDaemon", identity)
		}
		return &protocol.DaemonHeartbeatAckPayload{RuntimeID: payload.RuntimeID, Status: "ok"}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "computer-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	readyFrame, err := json.Marshal(protocol.Message{
		Type: protocol.EventWorkspaceDaemonReady,
		Payload: mustMarshalRaw(protocol.WorkspaceReadyPayload{
			WorkspaceID:      "workspace-1",
			DaemonInstanceID: "instance-1",
			ActiveCapabilities: []string{
				protocol.DaemonCapabilityWorkspaceDaemonAgentProcess,
				protocol.DaemonCapabilityWorkspaceDaemonControlPlane,
			},
		}),
	})
	if err != nil {
		t.Fatalf("marshal ready: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, readyFrame); err != nil {
		t.Fatalf("write ready: %v", err)
	}

	deadline := time.Now().Add(time.Second)
	for !hub.IsCurrentWorkspaceDaemon("computer-1", "workspace-1", "instance-1") {
		if time.Now().After(deadline) {
			t.Fatal("WorkspaceDaemon did not become current")
		}
		time.Sleep(10 * time.Millisecond)
	}

	heartbeatFrame, err := json.Marshal(protocol.Message{
		Type: protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{
			RuntimeID: "runtime-1",
		}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, heartbeatFrame); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read WorkspaceDaemon heartbeat ack: %v", err)
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatalf("unmarshal heartbeat ack: %v", err)
	}
	if message.Type != protocol.EventDaemonHeartbeatAck {
		t.Fatalf("message type = %q, want %q", message.Type, protocol.EventDaemonHeartbeatAck)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("HeartbeatHandler calls = %d, want 1", got)
	}
}

func TestLivenessProbeAcknowledgesWithoutInvokingHeartbeatActions(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	var heartbeatCalls atomic.Int32
	hub.SetHeartbeatHandler(func(context.Context, ClientIdentity, protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		heartbeatCalls.Add(1)
		return &protocol.DaemonHeartbeatAckPayload{Status: "ok"}, nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	probe, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonLivenessProbe})
	if err := conn.WriteMessage(websocket.TextMessage, probe); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var ack protocol.Message
	if json.Unmarshal(raw, &ack) != nil || ack.Type != protocol.EventDaemonLivenessAck {
		t.Fatalf("liveness response = %s", raw)
	}
	if calls := heartbeatCalls.Load(); calls != 0 {
		t.Fatalf("liveness probe invoked HeartbeatHandler %d times", calls)
	}
}

// TestHeartbeatHandlerCtxNotTimeBounded pins the PopPending invariant: the
// hub must not wrap the handler ctx with a short WithTimeout, otherwise the
// Redis Lua claim script can be cancelled mid-flight after its side effects
// have already landed. We assert by stalling the handler past any timeout
// the hub might be tempted to add and verifying the ack still arrives.
func TestHeartbeatHandlerCtxNotTimeBounded(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	const stall = 250 * time.Millisecond
	hub.SetHeartbeatHandler(func(ctx context.Context, _ ClientIdentity, payload protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		select {
		case <-time.After(stall):
		case <-ctx.Done():
			t.Errorf("handler ctx was cancelled (deadline=%v) — PopPending invariant violated", ctx.Err())
			return nil, ctx.Err()
		}
		if _, ok := ctx.Deadline(); ok {
			t.Errorf("handler ctx must not carry a deadline; PopPending side effects cannot be safely un-run")
		}
		return &protocol.DaemonHeartbeatAckPayload{RuntimeID: payload.RuntimeID, Status: "ok"}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	hbFrame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{RuntimeID: "runtime-1"}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, hbFrame); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(stall + 2*time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	var msg protocol.Message
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("unmarshal ack: %v", err)
	}
	if msg.Type != protocol.EventDaemonHeartbeatAck {
		t.Fatalf("ack type = %q, want %q", msg.Type, protocol.EventDaemonHeartbeatAck)
	}
}

// TestHeartbeatRejectsUnauthorizedRuntime verifies that a heartbeat for a
// runtime outside the connection's authenticated set is dropped silently —
// no handler call, no ack frame.
func TestHeartbeatRejectsUnauthorizedRuntime(t *testing.T) {
	M.Reset()
	defer M.Reset()

	hub := NewHub()
	var called atomic.Bool
	hub.SetHeartbeatHandler(func(context.Context, ClientIdentity, protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		called.Store(true)
		return &protocol.DaemonHeartbeatAckPayload{Status: "ok"}, nil
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()

	hbFrame, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{RuntimeID: "runtime-other"}),
	})
	if err != nil {
		t.Fatalf("marshal heartbeat: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, hbFrame); err != nil {
		t.Fatalf("WriteMessage: %v", err)
	}

	if err := conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected no ack for unauthorized runtime, got message")
	}
	if called.Load() {
		t.Fatalf("HeartbeatHandler invoked for unauthorized runtime")
	}
}

func TestReminderSnapshotTransientErrorClosesConnectionWithoutTerminalStop(t *testing.T) {
	hub := NewHub()
	hub.SetReminderHandlers(func(context.Context, ClientIdentity, protocol.ReminderSnapshotRequestPayload) (*protocol.ReminderSnapshotPayload, error) {
		return nil, errors.New("transient snapshot query failure")
	}, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request, err := json.Marshal(protocol.Message{
		Type:    protocol.EventReminderSnapshotRequest,
		Payload: mustMarshalRaw(protocol.ReminderSnapshotRequestPayload{RuntimeID: "runtime-1"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, raw, err := conn.ReadMessage(); err == nil {
		var msg protocol.Message
		if json.Unmarshal(raw, &msg) == nil && msg.Type == protocol.EventDaemonAgentStop {
			t.Fatalf("transient snapshot error emitted terminal stop: %s", raw)
		}
		t.Fatalf("transient snapshot error left connection open: %s", raw)
	}
}

// TestReminderFireRequestTransientErrorKeepsConnectionOpenForLocalRetry pins
// task #68's hub.go fix: unlike the snapshot path above, a transient
// fire-attempt processing failure must NOT force-close the connection. The
// daemon now keeps a locally-retryable in-flight record and resends the
// fire_request itself on a short backoff (reminder_cache.go), so tearing
// down the WS connection here would only add an unnecessary reconnect on
// top of a retry that was already going to happen.
func TestReminderFireRequestTransientErrorKeepsConnectionOpenForLocalRetry(t *testing.T) {
	hub := NewHub()
	hub.SetReminderHandlers(nil, func(context.Context, ClientIdentity, protocol.ReminderFireRequestPayload) (*protocol.ReminderFireRequestResultPayload, error) {
		return nil, errors.New("transient fire processing failure")
	})
	hub.SetHeartbeatHandler(func(context.Context, ClientIdentity, protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
		return &protocol.DaemonHeartbeatAckPayload{RuntimeID: "runtime-1", Status: "ok"}, nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request, err := json.Marshal(protocol.Message{
		Type: protocol.EventReminderFireRequest,
		Payload: mustMarshalRaw(protocol.ReminderFireRequestPayload{
			AgentID: "agent-1", ReminderID: "reminder-1", Version: 1, RequestID: "request-1",
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		t.Fatal(err)
	}
	// A single read call after both writes, not two separate reads: gorilla's
	// ReadMessage is not reliably reusable after a prior call returns any
	// error (including a deadline timeout) — this proves both "no frame for
	// the failure itself" and "connection survives" in one read, avoiding
	// that pitfall. The heartbeat ack is the only frame that should arrive.
	heartbeat, err := json.Marshal(protocol.Message{
		Type:    protocol.EventDaemonHeartbeat,
		Payload: mustMarshalRaw(protocol.DaemonHeartbeatRequestPayload{RuntimeID: "runtime-1"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, heartbeat); err != nil {
		t.Fatalf("connection unusable after transient fire-attempt failure: %v", err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, raw, err := conn.ReadMessage(); err != nil {
		t.Fatalf("connection closed instead of surviving to answer a heartbeat: %v", err)
	} else {
		var msg protocol.Message
		if json.Unmarshal(raw, &msg) != nil || msg.Type != protocol.EventDaemonHeartbeatAck {
			t.Fatalf("post-failure frame = %s, want a heartbeat ack proving the connection survived", raw)
		}
	}
}

func TestWorkspaceDaemonMixedRunActivityAcknowledgesOnlyCommittedTransition(t *testing.T) {
	hub := NewHub()
	handled := make(chan string, 2)
	hub.SetWorkspaceDaemonHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, raw json.RawMessage) error {
		if eventType != protocol.EventMixedRunActivityTransition {
			return nil
		}
		var payload protocol.MixedRunActivityTransitionPayload
		if err := json.Unmarshal(raw, &payload); err != nil {
			return err
		}
		handled <- payload.TransitionID
		if payload.TransitionID == "failed-transition" {
			return errors.New("injected transaction failure")
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	write := func(eventType string, payload any) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	write(protocol.EventWorkspaceDaemonReady, protocol.WorkspaceReadyPayload{WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1", ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess}})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	transition := protocol.MixedRunActivityTransitionPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", RunID: "run-1", RunAgentID: "run-agent-1",
		TransitionID: "failed-transition", Dimension: protocol.MixedRunActivityActiveTurn, Delta: 1,
	}
	write(protocol.EventMixedRunActivityTransition, transition)
	if got := <-handled; got != transition.TransitionID {
		t.Fatalf("failed transition handled as %q", got)
	}
	transition.TransitionID = "committed-transition"
	write(protocol.EventMixedRunActivityTransition, transition)
	if got := <-handled; got != transition.TransitionID {
		t.Fatalf("committed transition handled as %q", got)
	}
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var message protocol.Message
	if err := json.Unmarshal(raw, &message); err != nil {
		t.Fatal(err)
	}
	if message.Type != protocol.EventMixedRunActivityAck {
		t.Fatalf("ack frame type = %q", message.Type)
	}
	var ack protocol.MixedRunActivityTransitionAckPayload
	if err := json.Unmarshal(message.Payload, &ack); err != nil {
		t.Fatal(err)
	}
	if ack.RunID != transition.RunID || ack.TransitionID != transition.TransitionID {
		t.Fatalf("ack = %+v, want committed transition only", ack)
	}
}

func TestWorkspaceDaemonAgentResetCommandAndReceiptUseCurrentCapableRunner(t *testing.T) {
	hub := NewHub()
	received := make(chan string, 1)
	hub.SetWorkspaceDaemonHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, raw json.RawMessage) error {
		if eventType == protocol.EventAgentResetWorkspaceResult {
			var result protocol.AgentWorkspaceResetResultPayload
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			received <- result.OperationID
		}
		return nil
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	write := func(eventType string, payload any) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: eventType, Payload: mustMarshalRaw(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	write(protocol.EventWorkspaceDaemonReady, protocol.WorkspaceReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceDaemonAgentProcess, protocol.DaemonCapabilityWorkspaceDaemonAgentReset},
	})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	command := protocol.AgentWorkspaceResetPayload{
		OperationID: "operation-1", AgentID: "agent-1",
	}
	if !hub.NotifyAgentRestartCommand("workspace-1", "daemon-1", protocol.EventDaemonAgentResetWorkspace, command.OperationID, command) {
		t.Fatal("reset command was not delivered")
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	var frame protocol.Message
	if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != protocol.EventDaemonAgentResetWorkspace {
		t.Fatalf("reset frame = %+v, err=%v", frame, err)
	}
	write(protocol.EventAgentResetWorkspaceResult, protocol.AgentWorkspaceResetResultPayload{
		OperationID: command.OperationID, AgentID: command.AgentID, Status: protocol.AgentResetWorkspaceSucceeded,
	})
	select {
	case operationID := <-received:
		if operationID != command.OperationID {
			t.Fatalf("receipt operation_id = %q", operationID)
		}
	case <-time.After(time.Second):
		t.Fatal("workspace reset result was not forwarded")
	}
}
