package daemonws

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
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

func TestWorkspaceRunnerRoutesOnlyWithinDaemonWorkspaceScope(t *testing.T) {
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
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: mustMarshalRaw(protocol.WorkspaceRunnerReadyPayload{
			WorkspaceID: workspaceID, DaemonInstanceID: "instance-" + workspaceID,
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
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

	if !hub.NotifyWorkspaceRunner("daemon-1", "workspace-a", protocol.EventDaemonAgentStart, protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-a", RuntimeID: "runtime-1", LaunchID: "dispatch-a"}) {
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

func TestWorkspaceRunnerAllowsNoProviderRegistrations(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1"})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: mustMarshalRaw(protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
		t.Fatal(err)
	}
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	if !hub.NotifyWorkspaceRunner("daemon-1", "workspace-1", protocol.EventAgentAttach, protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	}) {
		t.Fatal("zero-Runtime Runner did not accept Attachment command")
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read zero-Runtime Attachment command: %v", err)
	}
	var command protocol.Message
	if err := json.Unmarshal(raw, &command); err != nil || command.Type != protocol.EventAgentAttach {
		t.Fatalf("zero-Runtime command=%+v err=%v", command, err)
	}
}

func TestWorkspaceRunnerCapabilityBelongsOnlyToCurrentReadyConnection(t *testing.T) {
	hub := NewHub()
	key := workspaceRunnerKey{daemonID: "daemon-1", workspaceID: "workspace-1"}
	old := &client{runnerCapabilities: map[string]struct{}{protocol.DaemonCapabilityWorkspaceRunnerAttachment: {}}}
	current := &client{runnerCapabilities: map[string]struct{}{protocol.DaemonCapabilityAgentLifecycleActions: {}}}
	hub.mu.Lock()
	hub.byRunner[key] = old
	hub.mu.Unlock()
	if !hub.WorkspaceRunnerSupportsCapability("daemon-1", "workspace-1", protocol.DaemonCapabilityWorkspaceRunnerAttachment) {
		t.Fatal("current Runner Attachment capability was not visible")
	}
	hub.mu.Lock()
	hub.byRunner[key] = current
	hub.mu.Unlock()
	if hub.WorkspaceRunnerSupportsCapability("daemon-1", "workspace-1", protocol.DaemonCapabilityWorkspaceRunnerAttachment) {
		t.Fatal("replaced Runner lent its Attachment capability to the current connection")
	}
	if !hub.WorkspaceRunnerSupportsCapability("daemon-1", "workspace-1", protocol.DaemonCapabilityAgentLifecycleActions) {
		t.Fatal("current Runner capability was not visible")
	}
}

func TestWorkspaceRunnerReadyReplacesConnectionAndFencesInboundFrames(t *testing.T) {
	hub := NewHub()
	var accepted atomic.Int64
	var runnerReady atomic.Int64
	var attachmentReceipts atomic.Int64
	hub.SetWorkspaceRunnerHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, _ json.RawMessage) error {
		if eventType == protocol.EventAgentStartAck {
			accepted.Add(1)
		}
		if eventType == protocol.EventWorkspaceRunnerReady {
			runnerReady.Add(1)
		}
		if eventType == protocol.EventAgentAttached {
			attachmentReceipts.Add(1)
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
	write(first, protocol.EventAgentStartAck, protocol.AgentStartAckPayload{AgentID: "agent-a", LaunchID: "launch-a", QueueState: protocol.AgentStartQueueQueued})
	time.Sleep(20 * time.Millisecond)
	if accepted.Load() != 0 {
		t.Fatal("unready connection mutated Runner state")
	}
	write(first, protocol.EventWorkspaceRunnerReady, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	write(first, protocol.EventAgentStartAck, protocol.AgentStartAckPayload{AgentID: "agent-a", LaunchID: "launch-a", QueueState: protocol.AgentStartQueueQueued})
	deadline := time.Now().Add(time.Second)
	for accepted.Load() != 1 {
		if time.Now().After(deadline) {
			t.Fatal("ready Runner acknowledgement was not delivered")
		}
		time.Sleep(time.Millisecond)
	}
	second := dial()
	defer second.Close()
	write(second, protocol.EventWorkspaceRunnerReady, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-2",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})
	for runnerReady.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("replacement Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	// The replaced socket either rejects this write locally or the Hub drops it
	// at the current-ready fence. It must never reach the receipt handler.
	staleReceipt, err := json.Marshal(protocol.Message{Type: protocol.EventAgentAttached, Payload: mustMarshalRaw(protocol.WorkspaceRunnerAgentAttachedPayload{
		AgentID: "agent-a", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	})})
	if err != nil {
		t.Fatal(err)
	}
	_ = first.WriteMessage(websocket.TextMessage, staleReceipt)
	time.Sleep(20 * time.Millisecond)
	if attachmentReceipts.Load() != 0 {
		t.Fatal("replaced Runner receipt bypassed current-ready fence")
	}
	if !hub.NotifyWorkspaceRunner("daemon-1", "workspace-1", protocol.EventWorkspaceRunnerPing, protocol.WorkspaceRunnerPingPayload{PingID: "ping-1"}) {
		t.Fatal("current Runner did not receive ping")
	}
	second.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err := second.ReadMessage(); err != nil {
		t.Fatalf("current Runner did not receive ping: %v", err)
	}
}

func TestWorkspaceRunnerRoutesAttachmentReplayFramesOnlyAfterReady(t *testing.T) {
	hub := NewHub()
	var seenMu sync.Mutex
	seen := make([]string, 0, 4)
	hub.SetWorkspaceRunnerHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, _ json.RawMessage) error {
		seenMu.Lock()
		seen = append(seen, eventType)
		seenMu.Unlock()
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
	attachment := protocol.WorkspaceRunnerAgentAttachedPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	// A valid frame is still ignored before the ready fence.
	write(protocol.EventAgentAttachmentReplayReq, protocol.WorkspaceRunnerAttachmentReplayRequest{RuntimeCursors: map[string]int64{"runtime-1": 0}})
	time.Sleep(20 * time.Millisecond)
	seenMu.Lock()
	if len(seen) != 0 {
		seenMu.Unlock()
		t.Fatalf("unready Attachment replay was dispatched: %v", seen)
	}
	seenMu.Unlock()
	write(protocol.EventWorkspaceRunnerReady, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	write(protocol.EventAgentAttachmentReplayReq, protocol.WorkspaceRunnerAttachmentReplayRequest{RuntimeCursors: map[string]int64{"runtime-1": 0}})
	write(protocol.EventAgentAttached, attachment)
	write(protocol.EventAgentDetached, protocol.WorkspaceRunnerAgentDetachedPayload(attachment))
	write(protocol.EventAgentAttachmentReplayAck, protocol.WorkspaceRunnerAttachmentReplayAck{RuntimeCursors: map[string]int64{"runtime-1": 1}})
	deadline := time.Now().Add(time.Second)
	for {
		seenMu.Lock()
		got := append([]string(nil), seen...)
		seenMu.Unlock()
		if len(got) == 5 { // ready plus all four Attachment frames
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Attachment replay frames were not dispatched: %v", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkspaceRunnerReceivesLiveAttachmentCommandsOnRunnerScope(t *testing.T) {
	hub := NewHub()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.HandleWebSocket(w, r, ClientIdentity{DaemonID: "daemon-1", WorkspaceID: "workspace-1", RuntimeIDs: []string{"runtime-1"}})
	}))
	defer server.Close()
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ready, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: mustMarshalRaw(protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1", ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, ready); err != nil {
		t.Fatal(err)
	}
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	attach := protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	}
	hub.NotifyAgentAttachmentAdded("workspace-1", "daemon-1", attach)
	hub.NotifyAgentAttachmentRemoved("workspace-1", "daemon-1", protocol.WorkspaceRunnerAgentDetachPayload(attach))
	for _, wantType := range []string{protocol.EventAgentAttach, protocol.EventAgentDetach} {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatal(err)
		}
		var frame protocol.Message
		if err := json.Unmarshal(raw, &frame); err != nil || frame.Type != wantType {
			t.Fatalf("live Attachment frame = %+v, want %q; err=%v", frame, wantType, err)
		}
	}
}

func TestCloseWorkspaceRunnerFencesReplacementDaemonInstance(t *testing.T) {
	hub := NewHub()
	var readyCount atomic.Int64
	hub.SetWorkspaceRunnerHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, _ json.RawMessage) error {
		if eventType == protocol.EventWorkspaceRunnerReady {
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
	write := func(conn *websocket.Conn, payload protocol.WorkspaceRunnerReadyPayload) {
		t.Helper()
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: mustMarshalRaw(payload)})
		if err != nil {
			t.Fatal(err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, frame); err != nil {
			t.Fatal(err)
		}
	}
	first := dial()
	defer first.Close()
	write(first, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})
	waitForRunner(t, hub, "daemon-1", "workspace-1")
	second := dial()
	defer second.Close()
	write(second, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-2",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
	})
	deadline := time.Now().Add(time.Second)
	for readyCount.Load() != 2 {
		if time.Now().After(deadline) {
			t.Fatal("replacement Runner did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	if hub.CloseWorkspaceRunner("daemon-1", "workspace-1", "instance-1") {
		t.Fatal("stale daemon instance closed the replacement Runner")
	}
	if hub.WorkspaceRunnerConnectionCount("daemon-1", "workspace-1") != 1 {
		t.Fatal("stale close removed the current Runner")
	}
	if !hub.CloseWorkspaceRunner("daemon-1", "workspace-1", "instance-2") {
		t.Fatal("current daemon instance was not closed")
	}
	deadline = time.Now().Add(time.Second)
	for hub.WorkspaceRunnerConnectionCount("daemon-1", "workspace-1") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("current Runner remained registered after close")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkspaceRunnerDisconnectCallbackOnlyObservesCurrentRunner(t *testing.T) {
	hub := NewHub()
	disconnected := make(chan string, 2)
	hub.SetWorkspaceRunnerDisconnectHandler(func(_ context.Context, _ ClientIdentity, daemonInstanceID string) error {
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
		frame, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: mustMarshalRaw(protocol.WorkspaceRunnerReadyPayload{
			WorkspaceID: "workspace-1", DaemonInstanceID: instanceID,
			ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
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
	if !hub.IsCurrentWorkspaceRunner("daemon-1", "workspace-1", "instance-1") {
		t.Fatal("first ready connection was not current")
	}

	second := dial()
	defer second.Close()
	ready(second, "instance-2")
	deadline := time.Now().Add(time.Second)
	for !hub.IsCurrentWorkspaceRunner("daemon-1", "workspace-1", "instance-2") {
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

	if !hub.CloseWorkspaceRunner("daemon-1", "workspace-1", "instance-2") {
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
	if hub.IsCurrentWorkspaceRunner("daemon-1", "workspace-1", "instance-2") {
		t.Fatal("closed Runner remained current")
	}
}

func waitForRunner(t *testing.T, hub *Hub, daemonID, workspaceID string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for hub.WorkspaceRunnerConnectionCount(daemonID, workspaceID) != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("Workspace Runner %s/%s was not ready", daemonID, workspaceID)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWorkspaceRunnerDeliveryRetriesUntilCurrentRunnerAcknowledges(t *testing.T) {
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
	ready, err := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerReady, Payload: mustMarshalRaw(protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1",
		ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment},
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

func TestRelayNotifierPublishesAttachmentToWorkspaceRunnerScope(t *testing.T) {
	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)
	payload := protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 2, LifecycleSeq: 3,
	}
	notifier.NotifyAgentAttachmentAdded("workspace-1", "daemon-1", payload)
	if relay.scopeType != realtime.ScopeDaemonWorkspaceRunner || relay.scopeID != workspaceRunnerRelayScopeID("daemon-1", "workspace-1") {
		t.Fatalf("Attachment relay scope = %q/%q", relay.scopeType, relay.scopeID)
	}
	if relay.eventID != "attachment:3" {
		t.Fatalf("Attachment relay event ID = %q, want lifecycle identity", relay.eventID)
	}
	var frame protocol.Message
	if err := json.Unmarshal(relay.frame, &frame); err != nil || frame.Type != protocol.EventAgentAttach {
		t.Fatalf("Attachment relay frame = %+v, err=%v", frame, err)
	}
}

func TestRelayNotifierPublishesReminderAsTransientAgentDeliveryToWorkspaceRunnerScope(t *testing.T) {
	relay := &recordingRelayPublisher{}
	notifier := NewRelayNotifier(nil, relay)
	payload := protocol.ReminderOwnerInputPayload{
		WorkspaceID: "workspace-1", AgentID: "agent-1", RuntimeID: "runtime-1",
		PlacementGeneration: 2, ReminderID: "reminder-1", Version: 3,
	}

	if !notifier.NotifyReminderOwnerInput("workspace-1", "daemon-1", payload) {
		t.Fatal("Reminder transient delivery was not published")
	}
	if relay.scopeType != realtime.ScopeDaemonWorkspaceRunner || relay.scopeID != workspaceRunnerRelayScopeID("daemon-1", "workspace-1") {
		t.Fatalf("Reminder relay scope = %q/%q", relay.scopeType, relay.scopeID)
	}
	var frame protocol.Message
	if err := json.Unmarshal(relay.frame, &frame); err != nil {
		t.Fatalf("unmarshal Reminder relay frame: %v", err)
	}
	if frame.Type != protocol.EventAgentDeliver {
		t.Fatalf("Reminder relay event = %q, want %q", frame.Type, protocol.EventAgentDeliver)
	}
	var input protocol.AgentTransientDeliverPayload
	if err := json.Unmarshal(frame.Payload, &input); err != nil {
		t.Fatalf("unmarshal Reminder transient input: %v", err)
	}
	if input.Kind != protocol.AgentTransientDeliverKindReminder || !input.Transient || input.Reminder != payload {
		t.Fatalf("Reminder transient input = %+v", input)
	}
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
		Type: protocol.EventReminderSnapshotRequest,
		Payload: mustMarshalRaw(protocol.ReminderSnapshotRequestPayload{
			AgentID: "agent-1", RuntimeID: "runtime-1", PlacementGeneration: 1,
		}),
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

// TestReminderFireAttemptTransientErrorKeepsConnectionOpenForLocalRetry pins
// task #68's hub.go fix: unlike the snapshot path above, a transient
// fire-attempt processing failure must NOT force-close the connection. The
// daemon now keeps a locally-retryable in-flight record and resends the
// fire_attempt itself on a short backoff (reminder_cache.go), so tearing
// down the WS connection here would only add an unnecessary reconnect on
// top of a retry that was already going to happen.
func TestReminderFireAttemptTransientErrorKeepsConnectionOpenForLocalRetry(t *testing.T) {
	hub := NewHub()
	hub.SetReminderHandlers(nil, func(context.Context, ClientIdentity, protocol.ReminderFireAttemptPayload) (*protocol.ReminderFireResultPayload, error) {
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
		Type: protocol.EventReminderFireAttempt,
		Payload: mustMarshalRaw(protocol.ReminderFireAttemptPayload{
			AgentID: "agent-1", RuntimeID: "runtime-1", PlacementGeneration: 1, ReminderID: "reminder-1", Version: 1,
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

func TestReminderProjectionReplayDrainsMoreThanSendBufferInOrder(t *testing.T) {
	hub := NewHub()
	const eventCount = 40
	hub.SetReminderProjectionHandlers(func(_ context.Context, identity ClientIdentity, payload protocol.ReminderProjectionRequestPayload) ([]protocol.ReminderProjectionEvent, protocol.ReminderProjectionReplayEndPayload, error) {
		if len(identity.RuntimeIDs) != 1 || identity.RuntimeIDs[0] != "runtime-1" {
			t.Fatalf("identity runtimes = %#v", identity.RuntimeIDs)
		}
		if payload.RuntimeCursors["runtime-1"] != 0 {
			t.Fatalf("request cursors = %#v", payload.RuntimeCursors)
		}
		events := make([]protocol.ReminderProjectionEvent, 0, eventCount)
		for i := 1; i <= eventCount; i++ {
			events = append(events, protocol.ReminderProjectionEvent{
				Seq: int64(i), RuntimeID: "runtime-1", AgentID: "agent-1", EventType: "upsert",
				ReminderID: "reminder-" + strconv.Itoa(i), Version: 1,
				FireAt: time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano),
			})
		}
		return events, protocol.ReminderProjectionReplayEndPayload{RuntimeCursors: map[string]int64{"runtime-1": eventCount}}, nil
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
		Type:    protocol.EventReminderProjectionReq,
		Payload: mustMarshalRaw(protocol.ReminderProjectionRequestPayload{RuntimeCursors: map[string]int64{"runtime-1": 0}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, request); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= eventCount; i++ {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Fatalf("read projection event %d: %v", i, err)
		}
		var msg protocol.Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != protocol.EventReminderProjection {
			t.Fatalf("event %d type = %q", i, msg.Type)
		}
		var event protocol.ReminderProjectionEvent
		if err := json.Unmarshal(msg.Payload, &event); err != nil {
			t.Fatal(err)
		}
		if event.Seq != int64(i) {
			t.Fatalf("event %d seq = %d", i, event.Seq)
		}
	}
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read projection replay end: %v", err)
	}
	var end protocol.Message
	if err := json.Unmarshal(raw, &end); err != nil {
		t.Fatal(err)
	}
	if end.Type != protocol.EventReminderProjectionEnd {
		t.Fatalf("final type = %q", end.Type)
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

func TestWorkspaceRunnerMixedRunActivityAcknowledgesOnlyCommittedTransition(t *testing.T) {
	hub := NewHub()
	handled := make(chan string, 2)
	hub.SetWorkspaceRunnerHandler(func(_ context.Context, _ ClientIdentity, _ string, eventType string, raw json.RawMessage) error {
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
	write(protocol.EventWorkspaceRunnerReady, protocol.WorkspaceRunnerReadyPayload{WorkspaceID: "workspace-1", DaemonInstanceID: "instance-1", ActiveCapabilities: []string{protocol.DaemonCapabilityWorkspaceRunnerAttachment}})
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
