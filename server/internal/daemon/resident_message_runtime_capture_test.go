package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
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

type captureBoundaryTurn struct {
	capture  agent.ResidentTurnCapture
	done     chan error
	captures chan agent.ResidentTurnCapture
}

type captureBoundaryPiRuntime struct {
	mu             sync.Mutex
	binding        agent.PiRunBinding
	boundarySerial int
	active         bool
	accepted       [][]agent.ResidentMessage
	turns          []*captureBoundaryTurn
	started        chan *captureBoundaryTurn
}

func newCaptureBoundaryPiRuntime(captures ...agent.ResidentTurnCapture) *captureBoundaryPiRuntime {
	turns := make([]*captureBoundaryTurn, 0, len(captures))
	for _, capture := range captures {
		turns = append(turns, &captureBoundaryTurn{
			capture: capture, done: make(chan error, 1), captures: make(chan agent.ResidentTurnCapture, 1),
		})
	}
	return &captureBoundaryPiRuntime{turns: turns, started: make(chan *captureBoundaryTurn, len(turns))}
}

func (r *captureBoundaryPiRuntime) Execute(context.Context, string, agent.ExecOptions) (*agent.Session, error) {
	return nil, nil
}

func (r *captureBoundaryPiRuntime) AcceptMessageBatch(_ context.Context, messages []agent.ResidentMessage) (agent.ResidentMessageAcceptance, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return agent.ResidentMessageAcceptance{}, agent.ErrPiRPCTurnBusy
	}
	if len(r.turns) == 0 {
		return agent.ResidentMessageAcceptance{}, fmt.Errorf("unexpected capture turn")
	}
	turn := r.turns[0]
	r.turns = r.turns[1:]
	r.accepted = append(r.accepted, append([]agent.ResidentMessage(nil), messages...))
	r.active = true
	r.started <- turn
	return agent.ResidentMessageAcceptance{Done: turn.done, Capture: turn.captures}, nil
}

func (r *captureBoundaryPiRuntime) PrepareMessageInput(context.Context, func(agent.Message)) error {
	return nil
}

func (r *captureBoundaryPiRuntime) AcceptIdleInboxNotice(context.Context, agent.ResidentPendingNotice) (agent.ResidentMessageAcceptance, error) {
	return agent.ResidentMessageAcceptance{}, nil
}

func (r *captureBoundaryPiRuntime) AcceptPendingNotice(context.Context, agent.ResidentPendingNotice) error {
	return nil
}

func (r *captureBoundaryPiRuntime) BindRunIdentity(identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.binding.SessionID == "" {
		r.boundarySerial = 1
		r.binding = agent.PiRunBinding{PiRunIdentity: identity, SessionID: "pi-session", CaptureBoundary: "capture-1"}
	} else if r.binding.PiRunIdentity != identity {
		return agent.PiRunBinding{}, agent.ErrPiRPCRunIdentityRequiresFreshSession
	}
	return r.binding, nil
}

func (r *captureBoundaryPiRuntime) PrepareRun(_ context.Context, identity agent.PiRunIdentity) (agent.PiRunBinding, error) {
	return r.BindRunIdentity(identity)
}

func (r *captureBoundaryPiRuntime) SettleRunTurn(identity agent.PiRunIdentity) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.binding.PiRunIdentity != identity {
		return fmt.Errorf("Pi turn settlement identity mismatch")
	}
	if r.active {
		return agent.ErrPiRPCTurnBusy
	}
	r.boundarySerial++
	r.binding.CaptureBoundary = fmt.Sprintf("capture-%d", r.boundarySerial)
	return nil
}

func (r *captureBoundaryPiRuntime) finish(turn *captureBoundaryTurn, err error) {
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
	turn.captures <- turn.capture
	close(turn.captures)
	turn.done <- err
	close(turn.done)
}

func (r *captureBoundaryPiRuntime) batches() [][]agent.ResidentMessage {
	r.mu.Lock()
	defer r.mu.Unlock()
	batches := make([][]agent.ResidentMessage, len(r.accepted))
	copy(batches, r.accepted)
	return batches
}

func (r *captureBoundaryPiRuntime) Close() {}

func (r *captureBoundaryPiRuntime) Compact(context.Context, string) (agent.PiCompactionResult, error) {
	return agent.PiCompactionResult{}, nil
}

func (r *captureBoundaryPiRuntime) SetAutoCompaction(context.Context, bool) error { return nil }

func (r *captureBoundaryPiRuntime) RuntimeStats(context.Context) (*agent.RuntimeTokenStats, error) {
	return nil, nil
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

	cfg := Config{WorkspacesRoot: isolatedWorkspacesRoot(t), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}

	capturePayload := &agent.ResidentTurnCapture{
		RunID: runID, RunAgentID: runAgentID, PiSessionID: "pi-session-1",
		CaptureBoundary: "boundary-1", TurnID: "turn-1", CaptureBatchID: "batch-1",
		TurnOrdinal: 1, Complete: true,
		StartedAt:   time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC),
		CompletedAt: time.Date(2026, time.August, 12, 15, 0, 8, 0, time.UTC),
		ProviderCalls: []agent.ResidentProviderCallCapture{{
			CallID: "C1", CallOrdinal: 1, Provider: "pi", Model: "test", APIKind: "messages",
			RawProviderRequest: json.RawMessage(`{"messages":[]}`), FinalAssistantMessage: json.RawMessage(`{"role":"assistant"}`),
			Status: "completed", StopReason: "stop", ResponseComplete: true,
			RequestHash: "sha256:req", ResponseHash: "sha256:resp",
			StartedAt:   time.Date(2026, time.August, 12, 15, 0, 1, 0, time.UTC),
			CompletedAt: time.Date(2026, time.August, 12, 15, 0, 4, 0, time.UTC),
		}},
		PayloadHash: "sha256:batch-1",
	}
	backend := &captureResidentMessageRuntime{
		done: make(chan error, 1), messages: make(chan agent.Message, 1),
		capture: make(chan agent.ResidentTurnCapture, 1), emitCapture: capturePayload,
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 8)
	d := &WorkspaceDaemonCore{
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
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, messages); err != nil {
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

func TestResidentMessageRuntimeCapture_BindsProxyActionToProviderCallAndDrainsTurn(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
		runID       = "run-1"
		runAgentID  = "run-agent-1"
		canonicalID = "70000000-0000-4000-8000-000000000371"
	)
	var (
		mu      sync.Mutex
		uploads []protocol.TurnCaptureUpload
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/messages/send":
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"created":true,"message":{"id":"`+canonicalID+`"}}`)
		case "/api/v1/env-dispatch/runs/" + runID + "/turn-captures":
			var upload protocol.TurnCaptureUpload
			if err := json.NewDecoder(r.Body).Decode(&upload); err != nil {
				t.Fatalf("decode upload: %v", err)
			}
			mu.Lock()
			uploads = append(uploads, upload)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(protocol.TurnCaptureUploadResponse{Accepted: true, TurnID: upload.Turn.TurnID})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: isolatedWorkspacesRoot(t), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	capture := residentTurnCaptureForBoundaryTest(runID, runAgentID, "boundary-1", "turn-1", "batch-1", "provider-call-1", "first")
	capture.ProviderCalls[0].FinalAssistantMessage = json.RawMessage(`{"role":"assistant","blocks":[{"type":"toolCall","id":"tool-1","name":"multica"}]}`)
	backend := &captureResidentMessageRuntime{
		done: make(chan error, 1), messages: make(chan agent.Message, 2),
		capture: make(chan agent.ResidentTurnCapture, 1), emitCapture: &capture,
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{backend: backend}
	d := &WorkspaceDaemonCore{
		cfg: cfg, client: NewClient(upstream.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID}},
		mixedRunActivityReporter: func(protocol.MixedRunActivityTransitionPayload) bool {
			return true
		},
	}
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "send a message",
		RunID: runID, RunAgentID: runAgentID, DeliveryID: "delivery-1",
	}}); err != nil {
		t.Fatalf("handoff: %v", err)
	}
	backend.messages <- agent.Message{Type: agent.MessageToolUse, Tool: "multica", CallID: "tool-1"}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		state := provenanceStateFor(d)
		state.mu.Lock()
		active := state.active[agentID].ToolCallID == "tool-1"
		state.mu.Unlock()
		if active {
			break
		}
		time.Sleep(time.Millisecond)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/agent/messages/send", strings.NewReader(`{}`))
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Workspace-ID", workspaceID)
	rec := httptest.NewRecorder()
	d.credentialProxyAgentAPIHandler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("proxy status = %d body=%s", rec.Code, rec.Body.String())
	}
	backend.messages <- agent.Message{Type: agent.MessageToolResult, Tool: "multica", CallID: "tool-1"}
	backend.finish(nil)
	waitForCaptureUploads(t, &mu, &uploads, 1)

	second := residentTurnCaptureForBoundaryTest(runID, runAgentID, "boundary-2", "turn-2", "batch-2", "provider-call-2", "second")
	second.ProviderCalls[0].FinalAssistantMessage = capture.ProviderCalls[0].FinalAssistantMessage
	secondTurnToken := d.beginCanonicalActionTurn(agentID)
	if !d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, "activity-turn-2", secondTurnToken, nil, &second) {
		t.Fatal("second capture was not accepted")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 2 {
		t.Fatalf("uploads = %d, want 2", len(uploads))
	}
	if len(uploads[0].VisibleActions) != 1 {
		t.Fatalf("first visible actions = %+v, want one", uploads[0].VisibleActions)
	}
	action := uploads[0].VisibleActions[0]
	if action.CanonicalID != canonicalID || action.ProducerCallID != "provider-call-1" || action.ActionOrdinal != 1 || action.SucceededAt == "" {
		t.Fatalf("first visible action = %+v", action)
	}
	if len(uploads[1].VisibleActions) != 0 {
		t.Fatalf("second turn replayed visible actions: %+v", uploads[1].VisibleActions)
	}
}

func TestResidentMessageRuntimeCapture_ToolLifecycleDuringIdleInput(t *testing.T) {
	const agentID, runtimeID = "agent-1", "runtime-1"
	backend := &captureResidentMessageRuntime{
		done: make(chan error, 1), messages: make(chan agent.Message, 4),
	}
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 16)
	d := &WorkspaceDaemonCore{
		cfg:               Config{WorkspacesRoot: isolatedWorkspacesRoot(t)},
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: "workspace-1"}},
		mixedRunActivityReporter: func(payload protocol.MixedRunActivityTransitionPayload) bool {
			reports <- payload
			return true
		},
	}
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, []protocol.AgentMessageProjection{{
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
	for counts[protocol.MixedRunActivityInflightTool+"1"] < 2 ||
		counts[protocol.MixedRunActivityInflightTool+"-1"] < 2 ||
		counts[protocol.MixedRunActivityActiveTurn+"-1"] < 1 {
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

	cfg := Config{WorkspacesRoot: isolatedWorkspacesRoot(t), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt,
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
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{backend: backend}
	reports := make(chan protocol.MixedRunActivityTransitionPayload, 8)
	d := &WorkspaceDaemonCore{
		cfg: cfg, client: NewClient(upstream.URL),
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID}},
		mixedRunActivityReporter: func(payload protocol.MixedRunActivityTransitionPayload) bool {
			reports <- payload
			return true
		},
	}
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, []protocol.AgentMessageProjection{{
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
				if associations := d.ObservedCanonicalActionAssociations(agentID); len(associations) != 0 {
					t.Fatalf("capture gap retained turn associations: %+v", associations)
				}
				return
			}
		case <-deadline:
			t.Fatal("missing-batch path did not report a capture gap and release unfinished capture")
		}
	}
}

func TestResidentMessageRuntimeCapture_ActionOverflowReportsGapWithoutUpload(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
		runID       = "run-overflow"
		runAgentID  = "run-agent-overflow"
	)
	var (
		mu      sync.Mutex
		uploads int
		gaps    []protocol.TurnCaptureGapReport
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/env-dispatch/runs/" + runID + "/turn-captures":
			mu.Lock()
			uploads++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(protocol.TurnCaptureUploadResponse{Accepted: true})
		case "/api/v1/env-dispatch/runs/" + runID + "/turn-capture-gaps":
			var gap protocol.TurnCaptureGapReport
			if err := json.NewDecoder(r.Body).Decode(&gap); err != nil {
				t.Errorf("decode gap: %v", err)
			}
			mu.Lock()
			gaps = append(gaps, gap)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(protocol.TurnCaptureGapResponse{Accepted: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: isolatedWorkspacesRoot(t), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	d := &WorkspaceDaemonCore{cfg: cfg, client: NewClient(upstream.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	turnToken := d.beginCanonicalActionTurn(agentID)
	toolContext := ActiveProviderToolContext{AgentID: agentID, CallID: "tool-overflow", ToolCallID: "tool-overflow", TurnToken: turnToken}
	d.SetActiveProviderToolContext(toolContext)
	for index := 0; index <= canonicalActionAssociationLimitPerTurn; index++ {
		canonicalID := fmt.Sprintf("70000000-0000-4000-8000-%012x", index+1)
		d.observeCanonicalActionOutcome(toolContext, "message", canonicalID, true)
	}
	capture := residentTurnCaptureForBoundaryTest(runID, runAgentID, "boundary-overflow", "turn-overflow", "batch-overflow", "provider-call", "overflow")
	if !d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, "activity-overflow", turnToken, nil, &capture) {
		t.Fatal("overflow gap was not accepted")
	}

	mu.Lock()
	defer mu.Unlock()
	if uploads != 0 {
		t.Fatalf("overflow emitted %d complete uploads", uploads)
	}
	if len(gaps) != 1 || gaps[0].Reason != "canonical_action_overflow" {
		t.Fatalf("overflow gaps = %+v", gaps)
	}
	if drained := d.endCanonicalActionTurn(agentID, turnToken); len(drained.Associations) != 0 || drained.Overflow {
		t.Fatalf("overflow state survived terminal gap: %+v", drained)
	}
}

func TestResidentMessageRuntimeCapture_AmbiguousContextReportsGapAndRecovers(t *testing.T) {
	const (
		workspaceID = "11111111-1111-1111-1111-111111111111"
		runtimeID   = "22222222-2222-2222-2222-222222222222"
		agentID     = "33333333-3333-3333-3333-333333333333"
		runID       = "run-ambiguous"
		runAgentID  = "run-agent-ambiguous"
	)
	var (
		mu      sync.Mutex
		uploads []protocol.TurnCaptureUpload
		gaps    []protocol.TurnCaptureGapReport
	)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/env-dispatch/runs/" + runID + "/turn-captures":
			var upload protocol.TurnCaptureUpload
			_ = json.NewDecoder(r.Body).Decode(&upload)
			mu.Lock()
			uploads = append(uploads, upload)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(protocol.TurnCaptureUploadResponse{Accepted: true})
		case "/api/v1/env-dispatch/runs/" + runID + "/turn-capture-gaps":
			var gap protocol.TurnCaptureGapReport
			_ = json.NewDecoder(r.Body).Decode(&gap)
			mu.Lock()
			gaps = append(gaps, gap)
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(protocol.TurnCaptureGapResponse{Accepted: true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	cfg := Config{WorkspacesRoot: isolatedWorkspacesRoot(t), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("write credential: %v", err)
	}
	d := &WorkspaceDaemonCore{cfg: cfg, client: NewClient(upstream.URL), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	turn1 := d.beginCanonicalActionTurn(agentID)
	turn2 := d.beginCanonicalActionTurn(agentID)
	for index, token := range []canonicalActionTurnToken{turn1, turn2} {
		capture := residentTurnCaptureForBoundaryTest(runID, runAgentID, fmt.Sprintf("boundary-%d", index+1), fmt.Sprintf("turn-%d", index+1), fmt.Sprintf("batch-%d", index+1), fmt.Sprintf("provider-%d", index+1), "ambiguous")
		if !d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, fmt.Sprintf("activity-%d", index+1), token, nil, &capture) {
			t.Fatalf("ambiguous gap %d was not accepted", index+1)
		}
	}

	turn3 := d.beginCanonicalActionTurn(agentID)
	context3 := ActiveProviderToolContext{AgentID: agentID, CallID: "tool-3", ToolCallID: "tool-3", TurnToken: turn3}
	d.SetActiveProviderToolContext(context3)
	d.observeCanonicalActionOutcome(context3, "message", "70000000-0000-4000-8000-000000000379", true)
	recovery := residentTurnCaptureForBoundaryTest(runID, runAgentID, "boundary-3", "turn-3", "batch-3", "provider-3", "recovery")
	recovery.ProviderCalls[0].FinalAssistantMessage = json.RawMessage(`{"role":"assistant","blocks":[{"type":"toolCall","id":"tool-3"}]}`)
	if !d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, "activity-3", turn3, nil, &recovery) {
		t.Fatal("recovery upload was not accepted")
	}
	parallelTurn := d.beginCanonicalActionTurn(agentID)
	d.SetActiveProviderToolContext(ActiveProviderToolContext{AgentID: agentID, CallID: "parallel-1", ToolCallID: "parallel-1", TurnToken: parallelTurn})
	d.SetActiveProviderToolContext(ActiveProviderToolContext{AgentID: agentID, CallID: "parallel-2", ToolCallID: "parallel-2", TurnToken: parallelTurn})
	parallelCapture := residentTurnCaptureForBoundaryTest(runID, runAgentID, "boundary-4", "turn-4", "batch-4", "provider-4", "parallel")
	if !d.reportResidentTurnCapture(workspaceID, agentID, runtimeID, runID, runAgentID, "activity-4", parallelTurn, nil, &parallelCapture) {
		t.Fatal("parallel-tool ambiguity gap was not accepted")
	}
	postParallelTurn := d.beginCanonicalActionTurn(agentID)
	postParallelContext := ActiveProviderToolContext{AgentID: agentID, CallID: "tool-5", ToolCallID: "tool-5", TurnToken: postParallelTurn}
	d.SetActiveProviderToolContext(postParallelContext)
	if active, ok := d.activeProviderToolContextSnapshot(agentID); !ok || active.TurnToken != postParallelTurn {
		t.Fatalf("parallel-tool cleanup did not recover: %+v, ok=%v", active, ok)
	}
	d.endCanonicalActionTurn(agentID, postParallelTurn)

	mu.Lock()
	defer mu.Unlock()
	if len(gaps) != 3 || gaps[0].Reason != "canonical_action_context_ambiguous" ||
		gaps[1].Reason != "canonical_action_context_ambiguous" || gaps[2].Reason != "canonical_action_context_ambiguous" {
		t.Fatalf("ambiguous gaps = %+v", gaps)
	}
	if len(uploads) != 1 || len(uploads[0].VisibleActions) != 1 || uploads[0].VisibleActions[0].ProducerCallID != "provider-3" {
		t.Fatalf("recovery uploads = %+v", uploads)
	}
}

func TestResidentMessageRuntimeCapture_NoHistoryReplayAfterNewBoundary(t *testing.T) {
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
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/env-dispatch/runs/" + runID + "/turn-captures":
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
		case "POST /api/v1/env-dispatch/runs/" + runID + "/turn-capture-gaps":
			t.Fatal("complete captures must upload rather than report gaps")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	cfg := Config{WorkspacesRoot: isolatedWorkspacesRoot(t), ServerBaseURL: upstream.URL}
	expiresAt := time.Now().Add(2 * time.Hour).UTC().Format(time.RFC3339Nano)
	if _, err := writeCachedAgentCredential(cfg, workspaceID, runtimeID, agentID, AgentCredentialResponse{
		ID: "credential-1", AgentID: agentID, Token: "durable-agent-token", ExpiresAt: &expiresAt,
	}, time.Now()); err != nil {
		t.Fatalf("writeCachedAgentCredential: %v", err)
	}
	firstCapture := residentTurnCaptureForBoundaryTest(runID, runAgentID, "capture-1", "turn-1", "batch-1", "call-1", "first")
	secondCapture := residentTurnCaptureForBoundaryTest(runID, runAgentID, "capture-2", "turn-2", "batch-2", "call-2", "second")
	backend := newCaptureBoundaryPiRuntime(firstCapture, secondCapture)
	pool := newCanonicalAgentRuntimePool()
	pool.slots[agentID+"\x00"+runtimeID] = &canonicalAgentRuntimeSlot{
		backend: backend,
	}
	d := &WorkspaceDaemonCore{
		cfg: cfg, client: NewClient(upstream.URL),
		logger:            slog.New(slog.NewTextHandler(io.Discard, nil)),
		canonicalRuntimes: pool,
		runtimeIndex:      map[string]Runtime{runtimeID: {ID: runtimeID, WorkspaceID: workspaceID}},
	}

	firstBatch := []protocol.AgentMessageProjection{{
		ID: "message-1", Target: "channel:one", Seq: 1, Content: "first turn",
		RunID: runID, RunAgentID: runAgentID,
	}}
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, firstBatch); err != nil {
		t.Fatalf("first handoff: %v", err)
	}
	backend.finish(<-backend.started, nil)
	waitForCaptureUploads(t, &mu, &uploads, 1)
	backend.mu.Lock()
	boundaryAfterFirstTurn := backend.binding.CaptureBoundary
	backend.mu.Unlock()
	if boundaryAfterFirstTurn == "capture-1" {
		t.Fatal("settlement did not advance capture boundary before the next turn")
	}

	secondBatch := []protocol.AgentMessageProjection{{
		ID: "message-2", Target: "channel:one", Seq: 2, Content: "second turn only",
		RunID: runID, RunAgentID: runAgentID,
	}}
	if err := d.deliverIdleMessageBatch(context.Background(), agentID, runtimeID, secondBatch); err != nil {
		t.Fatalf("second handoff: %v", err)
	}
	backend.finish(<-backend.started, nil)
	waitForCaptureUploads(t, &mu, &uploads, 2)

	batches := backend.batches()
	if len(batches) != 2 {
		t.Fatalf("AcceptMessageBatch calls = %d, want 2", len(batches))
	}
	if len(batches[1]) != 1 || batches[1][0].ID != "message-2" || batches[1][0].Content != "second turn only" {
		t.Fatalf("new boundary replayed history: %+v", batches[1])
	}
	for _, message := range batches[1] {
		if message.ID == "message-1" || message.Content == "first turn" {
			t.Fatalf("history from before the new capture boundary was replayed: %+v", message)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads[1].ProviderCalls) != 1 || uploads[1].ProviderCalls[0].CallID != "call-2" {
		t.Fatalf("second upload calls = %+v, want only call-2", uploads[1].ProviderCalls)
	}
	for _, call := range uploads[1].ProviderCalls {
		if call.CallID == "call-1" || string(call.RawProviderRequest) == `{"call":"first"}` {
			t.Fatalf("second upload replayed first-turn capture: %+v", uploads[1].ProviderCalls)
		}
	}
}

func residentTurnCaptureForBoundaryTest(runID, runAgentID, boundary, turnID, batchID, callID, call string) agent.ResidentTurnCapture {
	startedAt := time.Date(2026, time.August, 12, 15, 0, 0, 0, time.UTC)
	return agent.ResidentTurnCapture{
		RunID: runID, RunAgentID: runAgentID, PiSessionID: "pi-session", CaptureBoundary: boundary,
		TurnID: turnID, CaptureBatchID: batchID, TurnOrdinal: 1, Complete: true,
		StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second), PayloadHash: "sha256:" + batchID,
		ProviderCalls: []agent.ResidentProviderCallCapture{{
			CallID: callID, CallOrdinal: 1, Provider: "pi", Model: "test", APIKind: "messages",
			RawProviderRequest: json.RawMessage(`{"call":"` + call + `"}`), FinalAssistantMessage: json.RawMessage(`{"role":"assistant"}`),
			Status: "completed", StopReason: "stop", ResponseComplete: true,
			RequestHash: "sha256:req-" + call, ResponseHash: "sha256:resp-" + call,
			StartedAt: startedAt, CompletedAt: startedAt.Add(time.Second),
		}},
	}
}

// isolatedWorkspacesRoot is a TempDir that retries RemoveAll. The capture
// turn finishes its assertions before reportAgentMemoryWrites finishes writing
// .multica/memory-sync-state.json; t.TempDir() then fails Linux CI cleanup
// with "directory not empty".
func isolatedWorkspacesRoot(t *testing.T) string {
	t.Helper()
	root, err := os.MkdirTemp("", "multica-capture-ws-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		deadline := time.Now().Add(2 * time.Second)
		var removeErr error
		for {
			removeErr = os.RemoveAll(root)
			if removeErr == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Errorf("cleanup workspaces root %s: %v", root, removeErr)
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	})
	return root
}

func waitForCaptureUploads(t *testing.T, mu *sync.Mutex, uploads *[]protocol.TurnCaptureUpload, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		length := len(*uploads)
		mu.Unlock()
		if length >= count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("uploads = %d, want at least %d", len(*uploads), count)
}
