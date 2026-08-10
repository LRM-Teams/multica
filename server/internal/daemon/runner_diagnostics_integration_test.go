package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestWorkspaceRunnerCanonicalMessageDiagnosticsFollowRealDeliveryPath(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
		channelID   = "44444444-4444-4444-8444-444444444444"
		messageID   = "55555555-5555-4555-8555-555555555555"
	)
	deliveryID := "message:" + messageID + ":agent:" + agentID

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	acknowledged := make(chan struct{}, 1)
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
		var recovery protocol.AgentRecoveryRequest
		for recovery.RecoveryID == "" {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) == nil && frame.Type == protocol.EventAgentRecoveryRequest {
				_ = json.Unmarshal(frame.Payload, &recovery)
			}
		}
		recoveryPage, _ := json.Marshal(protocol.Message{Type: protocol.EventAgentRecoveryPage, Payload: marshalRaw(protocol.AgentRecoveryPage{
			AgentID: agentID, RecoveryID: recovery.RecoveryID, SnapshotID: "snapshot-1", HighWatermark: "snapshot-1",
		})})
		if err := conn.WriteMessage(websocket.TextMessage, recoveryPage); err != nil {
			t.Error(err)
			return
		}
		delivery, _ := json.Marshal(protocol.Message{Type: protocol.EventAgentDeliver, Payload: marshalRaw(protocol.AgentDeliverPayload{
			AgentID: agentID, Target: "channel:" + channelID, Seq: 7, DeliveryID: deliveryID,
			Message: protocol.AgentMessageProjection{ID: messageID, ChannelID: channelID, Target: "channel:" + channelID, Seq: 7, Content: "diagnostic-content-canary"},
		})})
		if err := conn.WriteMessage(websocket.TextMessage, delivery); err != nil {
			t.Error(err)
			return
		}
		for {
			_, raw, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var frame protocol.Message
			if json.Unmarshal(raw, &frame) != nil || frame.Type != protocol.EventAgentDeliverAck {
				continue
			}
			acknowledged <- struct{}{}
			return
		}
	}))
	t.Cleanup(server.Close)

	root := filepath.Join(t.TempDir(), "logs")
	store, err := diagnosticlog.Open(diagnosticlog.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	d := New(Config{ServerBaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runnerInstanceID = "runner-generation-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: diagnosticlog.EnvironmentProduction, runnerGeneration: d.runnerInstanceID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}
	d.client.SetWorkspaceDaemonToken(workspaceID, "workspace-token", time.Now().Add(time.Hour))
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.handoffIdleMessageBatch(ctx, agentID, runtimeID, messages)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.messageCoordinators[agentID] = coordinator
	d.messageRuntimeIDs[agentID] = runtimeID
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: &idleMessageFakeRuntime{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.runWorkspaceRunnerConnection(ctx, workspaceID) }()
	select {
	case <-acknowledged:
	case <-ctx.Done():
		t.Fatal("timed out waiting for canonical Message acknowledgement")
	}
	<-errCh

	path := filepath.Join(root, "runners", "production", workspaceID+".log")
	var records []map[string]any
	deadline := time.Now().Add(time.Second)
	for {
		records = readRunnerDiagnosticRecords(t, path)
		found := make(map[string]bool)
		for _, record := range records {
			phase, _ := record["phase"].(string)
			found[phase] = true
		}
		if (found["provider_finished"] && found["context_boundary_persisted"]) || time.Now().After(deadline) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	phases := make(map[string]map[string]any)
	for _, record := range records {
		phase, _ := record["phase"].(string)
		phases[phase] = record
		if _, leaked := record["content"]; leaked {
			t.Fatalf("diagnostic record contains Message content field: %#v", record)
		}
	}
	for _, phase := range []string{
		"runner_received", "coordinator_accepted", "ack_sent",
		"runtime_handoff_accepted", "context_boundary_persisted", "provider_finished",
	} {
		record := phases[phase]
		if record == nil {
			t.Fatalf("missing %s diagnostic in %#v", phase, phases)
		}
		if record["agent_id"] != agentID || record["runtime_id"] != runtimeID || record["message_id"] != messageID || record["channel_id"] != channelID {
			t.Fatalf("%s identities = %#v", phase, record)
		}
		if record["runner_generation"] != d.runnerInstanceID || record["workspace_id"] != workspaceID {
			t.Fatalf("%s Runner scope = %#v", phase, record)
		}
	}
	if body, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if string(body) == "" || strings.Contains(string(body), "diagnostic-content-canary") {
		t.Fatalf("diagnostic stream leaked Message content: %s", body)
	}
}

func TestStandaloneChatDiagnosticsFollowInboxExecutionPath(t *testing.T) {
	const (
		workspaceID  = "11111111-1111-4111-8111-111111111111"
		agentID      = "22222222-2222-4222-8222-222222222222"
		runtimeID    = "33333333-3333-4333-8333-333333333333"
		eventID      = "44444444-4444-4444-8444-444444444444"
		deliveryID   = "55555555-5555-4555-8555-555555555555"
		conversation = "66666666-6666-4666-8666-666666666666"
		sourceID     = "77777777-7777-4777-8777-777777777777"
		chatSession  = "88888888-8888-4888-8888-888888888888"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/daemon/runtimes/" + runtimeID + "/agent-inbox/drain":
			_, _ = w.Write([]byte(`{"events":[{` +
				`"id":"` + eventID + `",` +
				`"delivery_id":"` + deliveryID + `",` +
				`"conversation_id":"` + conversation + `",` +
				`"source_message_id":"` + sourceID + `",` +
				`"lease_token":"99999999-9999-4999-8999-999999999999",` +
				`"lease_expires_at":"2026-08-10T12:00:00Z",` +
				`"seq_from":9,"seq_to":9,"requires_wake":true,` +
				`"reason":"chat_session","response_mode":"public_response",` +
				`"task":{"id":"` + eventID + `","agent_id":"` + agentID + `",` +
				`"runtime_id":"` + runtimeID + `","workspace_id":"` + workspaceID + `",` +
				`"chat_session_id":"` + chatSession + `"}}]}`))
		case "/api/daemon/agent-inbox/events/" + eventID + "/renew",
			"/api/daemon/agent-inbox/events/" + eventID + "/execution",
			"/api/daemon/agent-memory-writes":
			_, _ = w.Write([]byte(`{"ok":true}`))
		case "/api/daemon/tasks/" + eventID + "/status":
			_, _ = w.Write([]byte(`{"status":"running"}`))
		case "/api/daemon/agent-inbox/events/" + eventID + "/complete":
			_, _ = w.Write([]byte(`{"ok":true,"acked_seq":9,"terminal_outcome":"completed"}`))
		default:
			t.Errorf("unexpected standalone Chat path: %s", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	root := filepath.Join(t.TempDir(), "logs")
	store, err := diagnosticlog.Open(diagnosticlog.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := New(Config{ServerBaseURL: server.URL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runnerInstanceID = "runner-generation-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: diagnosticlog.EnvironmentProduction, runnerGeneration: d.runnerInstanceID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID, Provider: "claude"}
	d.cancelPollInterval = time.Hour
	d.runner = taskRunnerFunc(func(_ context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
		return TaskResult{Status: "completed", Comment: "content-must-not-enter-diagnostics"}, nil
	})

	task, err := d.drainInboxTask(context.Background(), runtimeID)
	if err != nil {
		t.Fatal(err)
	}
	if task == nil {
		t.Fatal("standalone Chat drain returned no task")
	}
	d.handleTask(context.Background(), *task, 1)

	path := filepath.Join(root, "runners", "production", workspaceID+".log")
	records := readRunnerDiagnosticRecords(t, path)
	phases := make(map[string]map[string]any)
	for _, record := range records {
		phase, _ := record["phase"].(string)
		phases[phase] = record
	}
	for _, phase := range []string{"lease_acquired", "execution_started", "provider_finished", "terminal_accepted"} {
		record := phases[phase]
		if record == nil {
			t.Fatalf("missing %s standalone Chat checkpoint in %#v", phase, phases)
		}
		if record["task_id"] != eventID || record["delivery_id"] != deliveryID ||
			record["agent_id"] != agentID || record["runtime_id"] != runtimeID ||
			record["chat_session_id"] != chatSession || record["conversation_id"] != conversation ||
			record["source_message_id"] != sourceID || record["response_mode"] != "public_response" {
			t.Fatalf("%s standalone Chat identities = %#v", phase, record)
		}
	}
	executionID, _ := phases["execution_started"]["execution_id"].(string)
	if executionID == "" || phases["provider_finished"]["execution_id"] != executionID || phases["terminal_accepted"]["execution_id"] != executionID {
		t.Fatalf("execution identity did not span provider lifecycle: %#v", phases)
	}
	if phases["terminal_accepted"]["acked_seq"] != float64(9) {
		t.Fatalf("terminal receipt missing acked_seq: %#v", phases["terminal_accepted"])
	}
	if body, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(body), "content-must-not-enter-diagnostics") {
		t.Fatalf("standalone Chat diagnostics leaked output content: %s", body)
	}
}

func readRunnerDiagnosticRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return records
}
