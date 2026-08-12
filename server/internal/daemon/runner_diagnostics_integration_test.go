package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
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
		if err := completeTestWorkspaceRunnerAttachmentReplay(conn); err != nil {
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
	d := New(Config{ServerBaseURL: server.URL, DaemonID: "daemon-1"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runnerInstanceID = "runner-generation-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: diagnosticlog.EnvironmentProduction, runnerGeneration: d.runnerInstanceID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}
	d.client.SetWorkspaceDaemonToken(workspaceID, "workspace-token", time.Now().Add(time.Hour))
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(ctx context.Context, messages []protocol.AgentMessageProjection) error {
		return d.handoffIdleMessageBatch(ctx, agentID, runtimeID, messages)
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
	d.canonicalRuntimes.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: &idleMessageFakeRuntime{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	errCh := make(chan error, 1)
	runner, err := d.newWorkspaceRunner(workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	registerTestRunnerInbox(t, runner, InboxKey{WorkspaceID: workspaceID, AgentID: agentID}, runtimeID, coordinator)
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.inboxes.Close()
	})
	go func() { errCh <- runner.runConnection(ctx) }()
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
		if (found["provider_finished"] && found["context_boundary_persisted"] && found["ack_sent"]) || time.Now().After(deadline) {
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
		if _, found := record["task_id"]; found {
			t.Fatalf("ordinary Message delivery invented task_id: %#v", record)
		}
		if _, found := record["execution_id"]; found {
			t.Fatalf("ordinary Message delivery invented execution_id: %#v", record)
		}
	}
	for _, phase := range []string{
		"runner_received", "coordinator_accepted", "ack_attempted", "ack_sent",
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
	assertDiagnosticPhaseOrder(t, records, "lease_acquired", "execution_started", "provider_finished", "terminal_accepted")
	executionID, _ := phases["execution_started"]["execution_id"].(string)
	if executionID == "" || phases["provider_finished"]["execution_id"] != executionID || phases["terminal_accepted"]["execution_id"] != executionID {
		t.Fatalf("execution identity did not span provider lifecycle: %#v", phases)
	}
	if phases["terminal_accepted"]["acked_seq"] != float64(9) {
		t.Fatalf("terminal receipt missing acked_seq: %#v", phases["terminal_accepted"])
	}
	if body, err := os.ReadFile(path); err != nil {
		t.Fatal(err)
	} else if strings.Contains(string(body), "content-must-not-enter-diagnostics") || strings.Contains(string(body), "99999999-9999-4999-8999-999999999999") {
		t.Fatalf("standalone Chat diagnostics leaked content or lease token: %s", body)
	}
}

func TestStandaloneChatDiagnosticsRecordLeaseLossBeforeExecution(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/renew") {
			http.Error(w, "lease-token-and-body-must-not-enter-diagnostics", http.StatusConflict)
			return
		}
		t.Errorf("unexpected request after rejected lease renewal: %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
	d.runner = taskRunnerFunc(func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		providerCalls.Add(1)
		return TaskResult{}, nil
	})
	d.handleTask(context.Background(), task, 1)

	if providerCalls.Load() != 0 {
		t.Fatal("provider ran after the standalone Chat lease was rejected")
	}
	records := readRunnerDiagnosticRecords(t, path)
	record := requireDiagnosticPhase(t, records, "result_discarded")
	assertDiagnosticOutcome(t, record, "discarded", "lease_lost_before_execution")
	if _, found := record["execution_id"]; found {
		t.Fatalf("pre-execution lease loss invented execution_id: %#v", record)
	}
	assertDiagnosticFileExcludes(t, path, "lease-token-and-body-must-not-enter-diagnostics")
}

func TestStandaloneChatDiagnosticsDoNotPublishRejectedExecutionIdentity(t *testing.T) {
	var providerCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/renew"):
			w.WriteHeader(http.StatusOK)
		case strings.HasSuffix(r.URL.Path, "/execution"):
			http.Error(w, "execution-ledger-body-must-not-enter-diagnostics", http.StatusBadRequest)
		case strings.HasSuffix(r.URL.Path, "/fail"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
	d.runner = taskRunnerFunc(func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		providerCalls.Add(1)
		return TaskResult{}, nil
	})
	d.handleTask(context.Background(), task, 1)

	if providerCalls.Load() != 0 {
		t.Fatal("provider ran after the execution ledger rejected the start")
	}
	records := readRunnerDiagnosticRecords(t, path)
	rejected := requireDiagnosticPhase(t, records, "execution_start_rejected")
	assertDiagnosticOutcome(t, rejected, "rejected", "execution_ledger_error")
	terminal := requireDiagnosticPhase(t, records, "terminal_accepted")
	if terminal["status"] != "failed" {
		t.Fatalf("failure terminal = %#v", terminal)
	}
	for _, record := range records {
		if _, found := record["execution_id"]; found {
			t.Fatalf("rejected local execution candidate escaped into diagnostics: %#v", record)
		}
	}
	assertDiagnosticFileExcludes(t, path, "execution-ledger-body-must-not-enter-diagnostics")
}

func TestStandaloneChatDiagnosticsRecordProviderFailureWithBoundedReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/renew"), strings.HasSuffix(r.URL.Path, "/execution"), strings.HasSuffix(r.URL.Path, "/fail"):
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
	d.runner = taskRunnerFunc(func(context.Context, Task, string, int, *slog.Logger) (TaskResult, error) {
		return TaskResult{}, errors.New("provider-secret-output-must-not-enter-diagnostics")
	})
	d.handleTask(context.Background(), task, 1)

	records := readRunnerDiagnosticRecords(t, path)
	started := requireDiagnosticPhase(t, records, "execution_started")
	provider := requireDiagnosticPhase(t, records, "provider_finished")
	terminal := requireDiagnosticPhase(t, records, "terminal_accepted")
	if provider["outcome"] != "failed" || provider["reason_code"] == "" || terminal["status"] != "failed" {
		t.Fatalf("provider failure checkpoints = %#v", records)
	}
	executionID := started["execution_id"]
	if executionID == "" || provider["execution_id"] != executionID || terminal["execution_id"] != executionID {
		t.Fatalf("accepted execution identity did not span the failure lifecycle: %#v", records)
	}
	assertDiagnosticFileExcludes(t, path, "provider-secret-output-must-not-enter-diagnostics")
}

func TestStandaloneChatDiagnosticsExplainDiscardedProviderResult(t *testing.T) {
	t.Run("lease lost during execution", func(t *testing.T) {
		var renewCalls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/renew"):
				if renewCalls.Add(1) == 1 {
					w.WriteHeader(http.StatusOK)
					return
				}
				http.Error(w, "lost", http.StatusConflict)
			case strings.HasSuffix(r.URL.Path, "/execution"):
				w.WriteHeader(http.StatusOK)
			case strings.HasSuffix(r.URL.Path, "/status"):
				_, _ = w.Write([]byte(`{"status":"running"}`))
			default:
				t.Errorf("unexpected request: %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)

		d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
		d.cancelPollInterval = 5 * time.Millisecond
		d.runner = taskRunnerFunc(func(ctx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
			<-ctx.Done()
			return TaskResult{Status: "aborted", Comment: "discarded-provider-output-canary"}, nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		d.handleTask(ctx, task, 1)

		records := readRunnerDiagnosticRecords(t, path)
		requireDiagnosticPhase(t, records, "provider_finished")
		discarded := requireDiagnosticPhase(t, records, "result_discarded")
		assertDiagnosticOutcome(t, discarded, "discarded", "lease_lost_during_execution")
		assertNoDiagnosticPhase(t, records, "terminal_accepted")
		assertDiagnosticFileExcludes(t, path, "discarded-provider-output-canary")
	})

	t.Run("task cancelled during execution", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/renew"), strings.HasSuffix(r.URL.Path, "/execution"):
				w.WriteHeader(http.StatusOK)
			case strings.HasSuffix(r.URL.Path, "/status"):
				_, _ = w.Write([]byte(`{"status":"cancelled"}`))
			default:
				t.Errorf("unexpected request: %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)

		d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
		d.cancelPollInterval = 5 * time.Millisecond
		d.runner = taskRunnerFunc(func(ctx context.Context, _ Task, _ string, _ int, _ *slog.Logger) (TaskResult, error) {
			<-ctx.Done()
			return TaskResult{Status: "cancelled", Comment: "cancelled-provider-output-canary"}, nil
		})
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		d.handleTask(ctx, task, 1)

		records := readRunnerDiagnosticRecords(t, path)
		requireDiagnosticPhase(t, records, "provider_finished")
		discarded := requireDiagnosticPhase(t, records, "result_discarded")
		assertDiagnosticOutcome(t, discarded, "discarded", "task_cancelled_during_execution")
		assertNoDiagnosticPhase(t, records, "terminal_accepted")
		assertDiagnosticFileExcludes(t, path, "cancelled-provider-output-canary")
	})
}

func TestStandaloneChatDiagnosticsDistinguishTerminalRejections(t *testing.T) {
	previousSchedule := defaultTerminalRetrySchedule
	defaultTerminalRetrySchedule = []time.Duration{time.Nanosecond, time.Nanosecond}
	t.Cleanup(func() { defaultTerminalRetrySchedule = previousSchedule })
	defer noSleepRetry(t)()

	t.Run("transient exhaustion leaves result recoverable", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/complete") {
				http.Error(w, "retry-body-must-not-enter-diagnostics", http.StatusBadGateway)
				return
			}
			t.Errorf("unexpected request: %s", r.URL.Path)
			http.NotFound(w, r)
		}))
		t.Cleanup(server.Close)

		d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
		task.InboxEvent.ExecutionID = "99999999-9999-4999-8999-999999999999"
		d.reportTaskResultForTask(context.Background(), task, TaskResult{
			Status: "completed", Comment: "terminal-output-canary", ExecutionID: task.InboxEvent.ExecutionID,
		}, slog.Default())

		records := readRunnerDiagnosticRecords(t, path)
		rejected := requireDiagnosticPhase(t, records, "terminal_rejected")
		assertDiagnosticOutcome(t, rejected, "rejected", "terminal_transient_error")
		assertNoDiagnosticPhase(t, records, "terminal_accepted")
		assertDiagnosticFileExcludes(t, path, "retry-body-must-not-enter-diagnostics", "terminal-output-canary")
	})

	t.Run("permanent rejection falls back to accepted failure", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case strings.HasSuffix(r.URL.Path, "/complete"):
				http.Error(w, "permanent-body-must-not-enter-diagnostics", http.StatusBadRequest)
			case strings.HasSuffix(r.URL.Path, "/fail"):
				w.WriteHeader(http.StatusOK)
			default:
				t.Errorf("unexpected request: %s", r.URL.Path)
				http.NotFound(w, r)
			}
		}))
		t.Cleanup(server.Close)

		d, task, path := newStandaloneChatDiagnosticDaemon(t, server.URL)
		task.InboxEvent.ExecutionID = "99999999-9999-4999-8999-999999999999"
		d.reportTaskResultForTask(context.Background(), task, TaskResult{
			Status: "completed", Comment: "terminal-output-canary", ExecutionID: task.InboxEvent.ExecutionID,
		}, slog.Default())

		records := readRunnerDiagnosticRecords(t, path)
		rejected := requireDiagnosticPhase(t, records, "terminal_rejected")
		assertDiagnosticOutcome(t, rejected, "rejected", "terminal_permanent_error")
		accepted := requireDiagnosticPhase(t, records, "terminal_accepted")
		if accepted["status"] != "failed" || accepted["execution_id"] != task.InboxEvent.ExecutionID {
			t.Fatalf("fallback terminal = %#v", accepted)
		}
		assertDiagnosticFileExcludes(t, path, "permanent-body-must-not-enter-diagnostics", "terminal-output-canary")
	})
}

func TestCredentialProxyResponseDiagnosticsUseBoundedOutcomes(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
		channelID   = "44444444-4444-4444-8444-444444444444"
		messageID   = "55555555-5555-4555-8555-555555555555"
	)
	root := filepath.Join(t.TempDir(), "logs")
	store, err := diagnosticlog.Open(diagnosticlog.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runnerInstanceID = "runner-generation-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: diagnosticlog.EnvironmentProduction, runnerGeneration: d.runnerInstanceID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error { return nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	registerTestInbox(t, d, InboxKey{WorkspaceID: workspaceID, AgentID: agentID}, runtimeID, coordinator)

	tests := []struct {
		requestID string
		phase     string
		outcome   string
		reason    string
		response  map[string]any
	}{
		{"66666666-6666-4666-8666-666666666661", "response_accepted", "accepted", "", map[string]any{"message": map[string]any{"id": messageID, "channel_id": channelID, "content": "reply-content-canary"}}},
		{"66666666-6666-4666-8666-666666666662", "response_send", "held", "freshness_unknown", nil},
		{"66666666-6666-4666-8666-666666666663", "response_send", "held", "local_pending", nil},
		{"66666666-6666-4666-8666-666666666664", "response_send", "held", "server_race", map[string]any{"heldMessages": []any{"held-message-content-canary"}}},
		{"66666666-6666-4666-8666-666666666665", "response_send", "failed", "service_send_failed", nil},
		{"66666666-6666-4666-8666-666666666666", "response_accepted", "degraded", "draft_cleanup_failed", map[string]any{"message": map[string]any{"id": messageID, "channel_id": channelID}}},
	}
	for _, test := range tests {
		d.recordAgentMessageResponse(workspaceID, agentID, test.requestID, "channel:"+channelID, test.response, test.phase, test.outcome, test.reason)
	}

	path := filepath.Join(root, "runners", "production", workspaceID+".log")
	records := readRunnerDiagnosticRecords(t, path)
	if len(records) != len(tests) {
		t.Fatalf("response diagnostics = %d, want %d: %#v", len(records), len(tests), records)
	}
	for index, test := range tests {
		record := records[index]
		gotReason, _ := record["reason_code"].(string)
		if record["phase"] != test.phase || record["outcome"] != test.outcome || gotReason != test.reason {
			t.Fatalf("response diagnostic %d = %#v", index, record)
		}
		if record["request_id"] != test.requestID || record["agent_id"] != agentID || record["runtime_id"] != runtimeID || record["channel_id"] != channelID {
			t.Fatalf("response diagnostic identities %d = %#v", index, record)
		}
		if _, found := record["task_id"]; found {
			t.Fatalf("Credential Proxy response invented task_id: %#v", record)
		}
		if _, found := record["execution_id"]; found {
			t.Fatalf("Credential Proxy response invented execution_id: %#v", record)
		}
	}
	assertDiagnosticFileExcludes(t, path, "reply-content-canary", "held-message-content-canary")
}

func newStandaloneChatDiagnosticDaemon(t *testing.T, serverURL string) (*Daemon, Task, string) {
	t.Helper()
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
	root := filepath.Join(t.TempDir(), "logs")
	store, err := diagnosticlog.Open(diagnosticlog.Config{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	d := New(Config{ServerBaseURL: serverURL}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	d.runnerInstanceID = "runner-generation-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{
		store: store, environment: diagnosticlog.EnvironmentProduction, runnerGeneration: d.runnerInstanceID,
		loggers: make(map[string]*diagnosticlog.Logger), failed: make(map[string]struct{}),
	}
	d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID, Provider: "claude"}
	d.cancelPollInterval = time.Hour
	task := Task{
		ID: eventID, AgentID: agentID, RuntimeID: runtimeID, WorkspaceID: workspaceID, ChatSessionID: chatSession,
		Agent: &AgentData{Name: "diagnostic-test-agent"},
		InboxEvent: &AgentInboxLease{
			ID: eventID, DeliveryID: deliveryID, ConversationID: conversation, SourceMessageID: sourceID,
			ResponseMode: "public_response", LeaseToken: "lease-secret-canary", SeqFrom: 9, SeqTo: 9,
			RequiresWake: true, Reason: protocol.AgentInboxReasonChatSession, RuntimeID: runtimeID,
		},
	}
	return d, task, filepath.Join(root, "runners", "production", workspaceID+".log")
}

func requireDiagnosticPhase(t *testing.T, records []map[string]any, phase string) map[string]any {
	t.Helper()
	for _, record := range records {
		if record["phase"] == phase {
			return record
		}
	}
	t.Fatalf("missing %s diagnostic in %#v", phase, records)
	return nil
}

func assertNoDiagnosticPhase(t *testing.T, records []map[string]any, phase string) {
	t.Helper()
	for _, record := range records {
		if record["phase"] == phase {
			t.Fatalf("unexpected %s diagnostic: %#v", phase, record)
		}
	}
}

func assertDiagnosticPhaseOrder(t *testing.T, records []map[string]any, phases ...string) {
	t.Helper()
	next := 0
	for _, record := range records {
		if next < len(phases) && record["phase"] == phases[next] {
			next++
		}
	}
	if next != len(phases) {
		t.Fatalf("diagnostic phase order did not contain %v: %#v", phases, records)
	}
}

func assertDiagnosticOutcome(t *testing.T, record map[string]any, outcome, reason string) {
	t.Helper()
	if record["outcome"] != outcome || record["reason_code"] != reason {
		t.Fatalf("diagnostic outcome = %#v, want outcome=%q reason_code=%q", record, outcome, reason)
	}
}

func assertDiagnosticFileExcludes(t *testing.T, path string, values ...string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range values {
		if strings.Contains(string(body), value) {
			t.Fatalf("diagnostic stream leaked %q: %s", value, body)
		}
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
