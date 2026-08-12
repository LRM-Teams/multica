package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type captureResidentMessageRuntime struct {
	mu           sync.Mutex
	accepted     [][]agent.ResidentMessage
	done         chan error
	messages     chan agent.Message
	capture      chan agent.ResidentTurnCapture
	emitCapture  *agent.ResidentTurnCapture
	completeOnce sync.Once
}

func (r *captureResidentMessageRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *captureResidentMessageRuntime) AcceptMessageBatch(_ context.Context, messages []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	cloned := append([]agent.ResidentMessage(nil), messages...)
	r.mu.Lock()
	r.accepted = append(r.accepted, cloned)
	r.mu.Unlock()
	acceptance := agent.ResidentMessageAcceptance{Done: r.done, Messages: r.messages}
	if r.capture != nil {
		acceptance.Capture = r.capture
	}
	return acceptance, nil
}

func (r *captureResidentMessageRuntime) batches() [][]agent.ResidentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([][]agent.ResidentMessage, len(r.accepted))
	copy(out, r.accepted)
	return out
}

func (r *captureResidentMessageRuntime) finish(err error) {
	r.completeOnce.Do(func() {
		if r.messages != nil {
			close(r.messages)
		}
		if r.emitCapture != nil && r.capture != nil {
			r.capture <- *r.emitCapture
			close(r.capture)
		} else if r.capture != nil {
			close(r.capture)
		}
		r.done <- err
		close(r.done)
	})
}

func TestResidentMessageRuntimeCapture_UploadsTrustedBatchAtTurnEnd(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
		runID       = "run-1"
		runAgentID  = "run-agent-1"
	)
	var (
		mu      sync.Mutex
		uploads []protocol.TurnCaptureUpload
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "POST /api/v1/env-dispatch/runs/"+runID+"/turn-captures" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer durable-agent-token" {
			t.Errorf("Authorization = %q", got)
		}
		var payload protocol.TurnCaptureUpload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upload: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		uploads = append(uploads, payload)
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(protocol.TurnCaptureUploadResponse{Accepted: true, TurnID: payload.Turn.TurnID})
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: upstream.URL}
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token",
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}

	capturePayload := &agent.ResidentTurnCapture{
		RunID: runID, RunAgentID: runAgentID, PiSessionID: "pi-session-1",
		CaptureBoundary: "boundary-1", TurnID: "turn-1", CaptureBatchID: "batch-1",
		TurnOrdinal: 1, Complete: true,
		StartedAt: time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.August, 12, 15, 0, 8, 0, time.UTC),
		ProviderCalls: []agent.ResidentProviderCallCapture{{
			CallID: "C1", CallOrdinal: 1, Provider: "pi", Model: "test", APIKind: "messages",
			RawProviderRequest: json.RawMessage(`{"messages":[]}`), FinalAssistantMessage: json.RawMessage(`{"role":"assistant"}`),
			Status: "completed", StopReason: "stop", ResponseComplete: true,
			RequestHash: "sha256:req", ResponseHash: "sha256:resp",
			StartedAt: time.Date(2026, time.August, 12, 15, 0, 1, 0, time.UTC),
			CompletedAt: time.Date(2026, time.August, 12, 15, 0, 4, 0, time.UTC),
		}},
		PayloadHash: "sha256:batch-1",
	}
	backend := &captureResidentMessageRuntime{
		done: make(chan error, 1), messages: make(chan agent.Message, 1),
		capture: make(chan agent.ResidentTurnCapture, 1), emitCapture: capturePayload,
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{mode: canonicalRuntimeResident, backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 8)
	d := &Daemon{
		cfg: cfg, client: NewClient(upstream.URL),
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID}},
		mixedRunActivityReporter: func(payload protocol.MixedRunActivityTransitionPayload) bool {
			reports <- payload
			return true
		},
	}

	messages := []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello",
		RunID: runID, RunAgentID: runAgentID, DeliveryID: "delivery-1",
	}}
	if err := d.handoffIdleMessageBatch(context.Background(), agentID, runtimeID, messages); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	backend.finish(nil)

	deadline := time.After(2 * time.Second)
	counts := map[string]int{}
	for counts[protocol.MixedRunActivityUnfinishedCaptureBatch+"-1"] == 0 {
		select {
		case report := <-reports:
			counts[report.Dimension+fmt.Sprint(report.Delta)]++
		case <-deadline:
			t.Fatalf("timed out waiting for capture-end accounting: %v", counts)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 1 {
		t.Fatalf("turn-end uploads = %d, want 1", len(uploads))
	}
	if uploads[0].CaptureBatchID != "batch-1" || len(uploads[0].ProviderCalls) != 1 || uploads[0].ProviderCalls[0].CallID != "C1" {
		t.Fatalf("unexpected upload payload: %+v", uploads[0])
	}
	if counts[protocol.MixedRunActivityUnfinishedCaptureBatch+"-1"] != 1 {
		t.Fatalf("unfinished capture was not released after accepted upload: %v", counts)
	}
}

func TestResidentMessageRuntimeCapture_ToolLifecycleDuringIdleInput(t *testing.T) {
	const agentID, runtimeID = "agent-1", "runtime-1"
	backend := &captureResidentMessageRuntime{
		done: make(chan error, 1), messages: make(chan agent.Message, 4),
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{mode: canonicalRuntimeResident, backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 16)
	d := &Daemon{
		cfg:               Config{WorkspacesRoot: t.TempDir()},
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: "workspace-1"}},
		mixedRunActivityReporter: func(payload protocol.MixedRunActivityTransitionPayload) bool {
			reports <- payload
			return true
		},
	}
	if err := d.handoffIdleMessageBatch(context.Background(), agentID, runtimeID, []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "use a tool",
		RunID: "run-1", RunAgentID: "run-agent-1", DeliveryID: "delivery-1",
	}}); err != nil {
		t.Fatalf("handoff: %v", err)
	}

	backend.messages <- agent.Message{Type: agent.MessageToolUse, Tool: "bash", CallID: "tool-1"}
	backend.messages <- agent.Message{Type: agent.MessageToolResult, Tool: "bash", CallID: "tool-1"}
	backend.messages <- agent.Message{Type: agent.MessageToolUse, Tool: "read", CallID: "tool-2"}
	backend.messages <- agent.Message{Type: agent.MessageToolResult, Tool: "read", CallID: "tool-2"}
	backend.finish(nil)

	deadline := time.After(2 * time.Second)
	counts := map[string]int{}
	for counts[protocol.MixedRunActivityInflightTool+"1"] < 2 || counts[protocol.MixedRunActivityInflightTool+"-1"] < 2 {
		select {
		case report := <-reports:
			counts[report.Dimension+fmt.Sprint(report.Delta)]++
		case <-deadline:
			t.Fatalf("timed out waiting for tool start/end during idle input: %v", counts)
		}
	}
	if counts[protocol.MixedRunActivityInflightTool+"1"] != 2 || counts[protocol.MixedRunActivityInflightTool+"-1"] != 2 {
		t.Fatalf("tool lifecycle counts = %v, want paired start/end for two tools", counts)
	}
	if counts[protocol.MixedRunActivityActiveTurn+"1"] != 1 || counts[protocol.MixedRunActivityActiveTurn+"-1"] != 1 {
		t.Fatalf("active-turn lifecycle = %v", counts)
	}
}

func TestResidentMessageRuntimeCapture_MissingBatchReportsCaptureGap(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
		runID       = "run-gap"
		runAgentID  = "run-agent-gap"
	)
	var (
		mu   sync.Mutex
		gaps []protocol.TurnCaptureGapReport
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/env-dispatch/runs/" + runID + "/turn-capture-gaps":
			var payload protocol.TurnCaptureGapReport
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			mu.Lock()
			gaps = append(gaps, payload)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(protocol.TurnCaptureGapResponse{Accepted: true})
		case "POST /api/v1/env-dispatch/runs/" + runID + "/turn-captures":
			t.Fatal("incomplete capture must not upload a trusted batch")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: t.TempDir(), ServerBaseURL: upstream.URL}
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token",
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}

	backend := &captureResidentMessageRuntime{
		done: make(chan error, 1), messages: make(chan agent.Message),
		capture: make(chan agent.ResidentTurnCapture, 1),
		emitCapture: &agent.ResidentTurnCapture{
			RunID: runID, RunAgentID: runAgentID, PiSessionID: "pi-session",
			CaptureBoundary: "boundary-gap", TurnID: "turn-gap", CaptureBatchID: "batch-gap",
			TurnOrdinal: 1, Complete: false, PayloadHash: "sha256:incomplete",
		},
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{mode: canonicalRuntimeResident, backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 8)
	d := &Daemon{
		cfg: cfg, client: NewClient(upstream.URL),
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID}},
		mixedRunActivityReporter: func(payload protocol.MixedRunActivityTransitionPayload) bool {
			reports <- payload
			return true
		},
	}
	if err := d.handoffIdleMessageBatch(context.Background(), agentID, runtimeID, []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "hello",
		RunID: runID, RunAgentID: runAgentID, DeliveryID: "delivery-1",
	}}); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	backend.finish(nil)

	deadline := time.After(2 * time.Second)
	for {
		select {
		case report := <-reports:
			if report.Dimension == protocol.MixedRunActivityUnfinishedCaptureBatch && report.Delta == -1 {
				mu.Lock()
				defer mu.Unlock()
				if len(gaps) != 1 {
					t.Fatalf("capture gaps = %d, want 1; payload=%+v", len(gaps), gaps)
				}
				if gaps[0].Reason == "" || gaps[0].RunAgentID != runAgentID {
					t.Fatalf("unexpected gap report: %+v", gaps[0])
				}
				return
			}
		case <-deadline:
			t.Fatal("missing-batch path did not report a capture gap and release unfinished capture")
		}
	}
}

func TestResidentMessageRuntimeCapture_NoHistoryReplayAfterNewBoundary(t *testing.T) {
	const agentID, runtimeID = "agent-1", "runtime-1"
	backend := newSettlementBlockingPiRuntime()
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		mode: canonicalRuntimeResident, backend: backend,
	}
	identity := agent.PiRunIdentity{RunID: "run-1", RunAgentID: "run-agent-1"}
	if _, err := pool.bindResidentPiRunIdentity(context.Background(), agentID, runtimeID, identity); err != nil {
		t.Fatalf("bind Pi run identity: %v", err)
	}

	firstBatch := []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "first turn",
		RunID: "run-1", RunAgentID: "run-agent-1",
	}}
	firstComplete := make(chan error, 1)
	if err := pool.handoffIdleMessages(
		context.Background(), agentID, runtimeID, firstBatch,
		nil, nil, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { firstComplete <- err },
	); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	firstTurn := <-backend.accepted
	backend.complete(firstTurn, nil)
	backend.releaseSettlement()
	select {
	case err := <-firstComplete:
		if err != nil {
			t.Fatalf("first completion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first turn did not settle")
	}

	recording := &captureResidentMessageRuntime{done: make(chan error, 1), messages: make(chan agent.Message)}
	pool.slots[agentID+"\x00"+runtimeID].mu.Lock()
	// After settlement the capture boundary advanced. Replace with a recorder that
	// proves the next idle input receives only the new batch, never prior history.
	priorBoundary := backend.binding.CaptureBoundary
	pool.slots[agentID+"\x00"+runtimeID].backend = recording
	pool.slots[agentID+"\x00"+runtimeID].mu.Unlock()
	if priorBoundary == "capture-1" {
		t.Fatal("settlement did not advance capture boundary before the next turn")
	}

	secondBatch := []protocol.AgentMessageProjection{{
		ID: "message-2", Target: "channel:one", Seq: 2, Content: "second turn only",
		RunID: "run-1", RunAgentID: "run-agent-1",
	}}
	secondComplete := make(chan error, 1)
	if err := pool.handoffIdleMessages(
		context.Background(), agentID, runtimeID, secondBatch,
		nil, nil, nil, func(err error, _ uint64, _ *agent.ResidentTurnCapture) { secondComplete <- err },
	); err != nil {
		t.Fatalf("second handoff: %v", err)
	}
	recording.finish(nil)
	select {
	case err := <-secondComplete:
		if err != nil {
			t.Fatalf("second completion: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("second turn did not complete")
	}

	batches := recording.batches()
	if len(batches) != 1 {
		t.Fatalf("post-boundary AcceptMessageBatch calls = %d, want 1", len(batches))
	}
	if len(batches[0]) != 1 || batches[0][0].ID != "message-2" || batches[0][0].Content != "second turn only" {
		t.Fatalf("new boundary replayed history: %+v", batches[0])
	}
	for _, message := range batches[0] {
		if message.ID == "message-1" || message.Content == "first turn" {
			t.Fatalf("history from before the new capture boundary was replayed: %+v", message)
		}
	}
}
