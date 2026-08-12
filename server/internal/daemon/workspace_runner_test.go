package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerURLIsScopedWithoutRuntimeIDs(t *testing.T) {
	got, err := workspaceRunnerURL("https://api.example.com/multica", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	const want = "wss://api.example.com/multica/api/daemon/ws?workspace_id=workspace-1"
	if got != want {
		t.Fatalf("workspaceRunnerURL() = %q, want %q", got, want)
	}
}

func TestWorkspaceRunnerReadyPingAndReconnectUseFixedIdentity(t *testing.T) {
	type observation struct {
		ready protocol.WorkspaceRunnerReadyPayload
		pong  protocol.WorkspaceRunnerPongPayload
	}
	observations := make(chan observation, 2)
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var readyFrame protocol.Message
		if err := json.Unmarshal(raw, &readyFrame); err != nil {
			t.Error(err)
			return
		}
		var ready protocol.WorkspaceRunnerReadyPayload
		if readyFrame.Type != protocol.EventWorkspaceRunnerReady || json.Unmarshal(readyFrame.Payload, &ready) != nil {
			t.Errorf("invalid ready frame: %+v", readyFrame)
			return
		}
		if err := completeTestWorkspaceRunnerAttachmentReplay(conn); err != nil {
			t.Error(err)
			return
		}
		ping, _ := json.Marshal(protocol.Message{Type: protocol.EventWorkspaceRunnerPing, Payload: marshalRaw(protocol.WorkspaceRunnerPingPayload{PingID: "ping-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, ping); err != nil {
			t.Error(err)
			return
		}
		_, raw, err = conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var pongFrame protocol.Message
		var pong protocol.WorkspaceRunnerPongPayload
		if json.Unmarshal(raw, &pongFrame) != nil || pongFrame.Type != protocol.EventWorkspaceRunnerPong || json.Unmarshal(pongFrame.Payload, &pong) != nil {
			t.Errorf("invalid pong frame: %s", raw)
			return
		}
		observations <- observation{ready: ready, pong: pong}
	}))
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runnerInstanceID = "instance-1"
	d.client.SetWorkspaceDaemonToken("workspace-1", "workspace-token", time.Now().Add(time.Minute))
	runner, err := d.newWorkspaceRunner("workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	go runner.Run(ctx)
	for attempt := 0; attempt < 2; attempt++ {
		select {
		case got := <-observations:
			if got.ready.WorkspaceID != "workspace-1" || got.ready.DaemonInstanceID != "instance-1" || got.pong.PingID != "ping-1" {
				t.Fatalf("reconnect observation = %+v", got)
			}
			capabilities := make(map[string]bool, len(got.ready.ActiveCapabilities))
			for _, capability := range got.ready.ActiveCapabilities {
				capabilities[capability] = true
			}
			if !capabilities[protocol.DaemonCapabilityWorkspaceRunnerAttachment] || !capabilities[protocol.DaemonCapabilityReminderTransientInput] {
				t.Fatalf("Runner capabilities = %v, want Attachment and Reminder transient input", got.ready.ActiveCapabilities)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Runner connection %d", attempt+1)
		}
	}
}

func TestWorkspaceRunnerOwnsOneProcessManagerPerWorkspace(t *testing.T) {
	d := New(Config{DaemonID: "daemon-1", MaxAgentProcesses: 1}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	firstRunner, err := d.newWorkspaceRunner("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	secondRunner, err := d.newWorkspaceRunner("ws-2")
	if err != nil {
		t.Fatal(err)
	}
	first := firstRunner.processes
	second := secondRunner.processes
	if first == nil || second == nil || second == first {
		t.Fatal("different Workspace Runners unexpectedly share a process manager")
	}
	firstAck, err := first.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "dispatch-1"})
	if err != nil {
		t.Fatalf("start in first manager: %v", err)
	}
	secondAck, err := second.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-2", LaunchID: "dispatch-1"})
	if err != nil {
		t.Fatalf("start in second manager: %v", err)
	}
	if firstAck.LaunchID != "dispatch-1" || secondAck.LaunchID != "dispatch-1" || firstAck.QueueState != protocol.AgentStartQueueStarting || secondAck.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("Workspace Runner managers were not isolated: first=%+v second=%+v", firstAck, secondAck)
	}
}

func TestWorkspaceRunnerAcceptsScopedStartAndReturnsAckThenStatus(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	frames := make(chan protocol.Message, 6)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("workspace_id") != "ws-1" || r.Header.Get("Authorization") != "Bearer workspace-token" {
			http.Error(w, "unexpected runner scope", http.StatusForbidden)
			return
		}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, raw, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var ready protocol.Message
		if err := json.Unmarshal(raw, &ready); err != nil {
			t.Error(err)
			return
		}
		frames <- ready
		if err := completeTestWorkspaceRunnerAttachmentReplay(conn); err != nil {
			t.Error(err)
			return
		}
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1", LaunchID: "dispatch-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		var accepted protocol.AgentStartAckPayload
		for responses := 0; responses < 3; {
			_, raw, err = conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var msg protocol.Message
			if err := json.Unmarshal(raw, &msg); err != nil {
				t.Error(err)
				return
			}
			if msg.Type == protocol.EventAgentStartAck {
				if err := json.Unmarshal(msg.Payload, &accepted); err != nil {
					t.Error(err)
					return
				}
			}
			if msg.Type == protocol.EventAgentRecoveryRequest {
				continue
			}
			frames <- msg
			responses++
		}
		stop, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStop, Payload: marshalRaw(protocol.WorkspaceRunnerAgentStopPayload{AgentID: accepted.AgentID, LaunchID: accepted.LaunchID})})
		if err := conn.WriteMessage(websocket.TextMessage, stop); err != nil {
			t.Error(err)
			return
		}
		for i := 0; i < 1; i++ {
			_, raw, err = conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var stoppedFrame protocol.Message
			if err := json.Unmarshal(raw, &stoppedFrame); err != nil {
				t.Error(err)
				return
			}
			frames <- stoppedFrame
		}
	}))
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1", WorkspacesRoot: t.TempDir()}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	runner, err := d.newWorkspaceRunner("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	runner.ensureResidentRuntime = func(context.Context, string, string, *agent.PiRunIdentity) error { return nil }
	if _, err := runner.applyAttachmentAttach(protocol.WorkspaceRunnerAgentAttachPayload{
		AgentID: "agent-1", RuntimeID: "runtime-1", AttachmentGeneration: 1, LifecycleSeq: 1,
	}); err != nil {
		t.Fatalf("attach scoped Agent before start: %v", err)
	}
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.inboxes.Close()
	})
	go func() { errCh <- runner.runConnection(ctx) }()
	var ready, ack, status, inactive, session protocol.Message
	for i := 0; i < 5; i++ {
		select {
		case msg := <-frames:
			switch msg.Type {
			case protocol.EventWorkspaceRunnerReady:
				ready = msg
			case protocol.EventAgentStartAck:
				ack = msg
			case protocol.EventAgentStatus:
				var candidate protocol.AgentStatusPayload
				if err := json.Unmarshal(msg.Payload, &candidate); err != nil {
					t.Fatal(err)
				}
				if candidate.Status == protocol.AgentStatusActive {
					status = msg
				} else {
					inactive = msg
				}
			case protocol.EventAgentSession:
				session = msg
			case protocol.EventAgentActivity:
				t.Fatalf("managed start/stop invented lifecycle Activity: %+v", msg)
			}
		case <-ctx.Done():
			t.Fatal("timed out waiting for Runner frames")
		}
	}
	if ready.Type != protocol.EventWorkspaceRunnerReady {
		t.Fatalf("ready frame = %+v", ready)
	}
	var accepted protocol.AgentStartAckPayload
	if err := json.Unmarshal(ack.Payload, &accepted); err != nil {
		t.Fatal(err)
	}
	var active protocol.AgentStatusPayload
	if err := json.Unmarshal(status.Payload, &active); err != nil {
		t.Fatal(err)
	}
	var reportedSession protocol.AgentSessionPayload
	if err := json.Unmarshal(session.Payload, &reportedSession); err != nil {
		t.Fatal(err)
	}
	if accepted.LaunchID == "" || active.LaunchID != accepted.LaunchID || active.Status != protocol.AgentStatusActive || reportedSession.LaunchID != accepted.LaunchID {
		t.Fatalf("ack=%+v status=%+v session=%+v", accepted, active, reportedSession)
	}
	var stopped protocol.AgentStatusPayload
	if err := json.Unmarshal(inactive.Payload, &stopped); err != nil {
		t.Fatal(err)
	}
	if stopped.LaunchID != accepted.LaunchID || stopped.Status != protocol.AgentStatusInactive {
		t.Fatalf("inactive status=%+v, want launch %q", stopped, accepted.LaunchID)
	}
	<-errCh
}

func TestWorkspaceRunnerAcknowledgesCanonicalMessageDeliveryWithoutRuntime(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	acknowledgements := make(chan protocol.AgentDeliverAckPayload, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, _, err := conn.ReadMessage(); err != nil {
			t.Error(err)
			return
		}
		if err := completeTestWorkspaceRunnerAttachmentReplay(conn); err != nil {
			t.Error(err)
			return
		}
		delivery, _ := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: marshalRaw(protocol.AgentDeliverPayload{
			AgentID: "agent-1", Target: "channel:one", Seq: 1, DeliveryID: "delivery-1",
			Message: protocol.AgentMessageProjection{ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello"},
		})})
		for attempt := 0; attempt < 2; attempt++ {
			if err := conn.WriteMessage(websocket.TextMessage, delivery); err != nil {
				t.Error(err)
				return
			}
			for {
				_, raw, err := conn.ReadMessage()
				if err != nil {
					t.Error(err)
					return
				}
				var message protocol.Message
				if err := json.Unmarshal(raw, &message); err != nil {
					t.Error(err)
					return
				}
				if message.Type != protocol.EventAgentDeliverAck {
					continue
				}
				var ack protocol.AgentDeliverAckPayload
				if err := json.Unmarshal(message.Payload, &ack); err != nil {
					t.Error(err)
					return
				}
				acknowledgements <- ack
				break
			}
		}
	}))
	defer server.Close()

	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		return errors.New("resident Runtime unavailable")
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	completeCoordinatorRecovery(t, coordinator)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	// Delivery acknowledgement depends only on coordinator acceptance. No
	// resident Runtime slot or provider client exists in this transport test.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	runner, err := d.newWorkspaceRunner("ws-1")
	if err != nil {
		t.Fatal(err)
	}
	registerTestRunnerInbox(t, runner, InboxKey{WorkspaceID: "ws-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.inboxes.Close()
	})
	go func() { errCh <- runner.runConnection(ctx) }()
	for attempt := 0; attempt < 2; attempt++ {
		var ack protocol.AgentDeliverAckPayload
		select {
		case ack = <-acknowledgements:
		case <-ctx.Done():
			t.Fatalf("timed out waiting for Runner delivery acknowledgement %d", attempt+1)
		}
		if ack.AgentID != "agent-1" || ack.Seq != 1 || ack.DeliveryID != "delivery-1" {
			t.Fatalf("delivery acknowledgement=%+v", ack)
		}
	}
	<-errCh
	coordinator.mu.Lock()
	pending := coordinator.pendingBatchLocked()
	coordinator.mu.Unlock()
	if len(pending) != 1 || pending[0].ID != "message-1" {
		t.Fatalf("Pending after duplicate delivery = %+v, want exactly message-1", pending)
	}
	if boundary := coordinator.Boundaries()["channel:one"]; boundary != 0 {
		t.Fatalf("Context Boundary after ACK = %d, want 0", boundary)
	}
}
