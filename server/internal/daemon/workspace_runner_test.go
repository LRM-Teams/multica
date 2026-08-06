package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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

func TestWorkspaceRunnerOwnsOneProcessManagerPerWorkspace(t *testing.T) {
	d := New(Config{MaxAgentProcesses: 1}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	first := d.workspaceAgentProcessManager("ws-1")
	if got := d.workspaceAgentProcessManager("ws-1"); got != first {
		t.Fatal("same Workspace Runner did not retain its process manager")
	}
	second := d.workspaceAgentProcessManager("ws-2")
	if second == first {
		t.Fatal("different Workspace Runners unexpectedly share a process manager")
	}
	firstAck, err := first.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-1", StartDispatchID: "dispatch-1"})
	if err != nil {
		t.Fatalf("start in first manager: %v", err)
	}
	secondAck, err := second.Start(agentProcessStartRequest{AgentID: "agent-1", RuntimeID: "runtime-2", StartDispatchID: "dispatch-1"})
	if err != nil {
		t.Fatalf("start in second manager: %v", err)
	}
	if firstAck.LaunchID == secondAck.LaunchID || firstAck.QueueState != protocol.AgentStartQueueStarting || secondAck.QueueState != protocol.AgentStartQueueStarting {
		t.Fatalf("Workspace Runner managers were not isolated: first=%+v second=%+v", firstAck, secondAck)
	}
}

func TestWorkspaceRunnerAcceptsScopedStartAndReturnsAckThenStatus(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	frames := make(chan protocol.Message, 5)
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
		start, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStart, Payload: marshalRaw(protocol.WorkspaceRunnerAgentStartPayload{AgentID: "agent-1", RuntimeID: "runtime-1", StartDispatchID: "dispatch-1"})})
		if err := conn.WriteMessage(websocket.TextMessage, start); err != nil {
			t.Error(err)
			return
		}
		var accepted protocol.AgentStartAckPayload
		for i := 0; i < 3; i++ {
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
			frames <- msg
		}
		stop, _ := json.Marshal(protocol.Message{Type: protocol.EventDaemonAgentStop, Payload: marshalRaw(protocol.WorkspaceRunnerAgentStopPayload{AgentID: accepted.AgentID, LaunchID: accepted.LaunchID})})
		if err := conn.WriteMessage(websocket.TextMessage, stop); err != nil {
			t.Error(err)
			return
		}
		_, raw, err = conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		var inactive protocol.Message
		if err := json.Unmarshal(raw, &inactive); err != nil {
			t.Error(err)
			return
		}
		frames <- inactive
	}))
	defer server.Close()
	d := New(Config{ServerBaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.client.SetWorkspaceDaemonToken("ws-1", "workspace-token", time.Now().Add(time.Hour))
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "ws-1"}
	d.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.runWorkspaceRunnerConnection(ctx, "ws-1") }()
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
