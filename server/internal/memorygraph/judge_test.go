package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// ---------------------------------------------------------------------------
// fake judge backend
// ---------------------------------------------------------------------------

// fakeJudgeBackend plays the judge agent: it records the last prompt and
// options and completes immediately with the configured output.
type fakeJudgeBackend struct {
	mu     sync.Mutex
	output string
	err    error
	prompt string
	opts   agent.ExecOptions
	calls  int
}

func (f *fakeJudgeBackend) Execute(_ context.Context, prompt string, opts agent.ExecOptions) (*agent.Session, error) {
	f.mu.Lock()
	f.prompt = prompt
	f.opts = opts
	f.calls++
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	resultCh := make(chan agent.Result, 1)
	resultCh <- agent.Result{Status: "completed", Output: f.output}
	close(resultCh)
	return &agent.Session{Result: resultCh}, nil
}

func (f *fakeJudgeBackend) lastCall() (string, agent.ExecOptions, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.prompt, f.opts, f.calls
}

// ---------------------------------------------------------------------------
// judge tests
// ---------------------------------------------------------------------------

func TestJudgeParsesValidJSON(t *testing.T) {
	backend := &fakeJudgeBackend{
		output: "Evaluation follows.\n{\"score\":0.8,\"relevant_nodes\":[\"n1\",\"n2\"],\"rationale\":\"directly answers\"}",
	}
	j := NewJudge(backend, JudgeConfig{}, "pi")
	recall := &RecallResult{TraceID: "t1", Summary: "retry policy summary", NodeIDs: []string{"n1", "n2"}, Found: true, Rounds: 2}
	history := []Message{{Role: "user", Content: "why do batch jobs retry?"}}

	res, err := j.Judge(context.Background(), "why do batch jobs retry?", recall, history)
	if err != nil {
		t.Fatalf("Judge: %v", err)
	}
	if res.Score != 0.8 {
		t.Fatalf("Score = %v, want 0.8", res.Score)
	}
	if len(res.RelevantNodes) != 2 || res.RelevantNodes[0] != "n1" {
		t.Fatalf("RelevantNodes = %v, want [n1 n2]", res.RelevantNodes)
	}
	if res.Rationale != "directly answers" {
		t.Fatalf("Rationale = %q", res.Rationale)
	}

	prompt, opts, calls := backend.lastCall()
	if calls != 1 {
		t.Fatalf("backend calls = %d, want 1", calls)
	}
	// The prompt must be the well-formed user JSON payload.
	var payload judgePayload
	if err := json.Unmarshal([]byte(prompt), &payload); err != nil {
		t.Fatalf("prompt is not valid JSON: %v", err)
	}
	if payload.Query != "why do batch jobs retry?" || len(payload.CitedNodeIDs) != 2 || payload.RecallSummary != "retry policy summary" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	// The system prompt must carry the rubric and the injection defense.
	if !strings.Contains(opts.SystemPrompt, "untrusted data") {
		t.Fatalf("system prompt missing untrusted-data defense: %q", opts.SystemPrompt)
	}
	if !strings.Contains(opts.SystemPrompt, "0.5") || !strings.Contains(opts.SystemPrompt, `"relevant_nodes"`) {
		t.Fatalf("system prompt missing rubric/contract: %q", opts.SystemPrompt)
	}
}

func TestJudgeGarbageOutputErrors(t *testing.T) {
	backend := &fakeJudgeBackend{output: "I could not decide, sorry."}
	j := NewJudge(backend, JudgeConfig{}, "pi")
	recall := &RecallResult{TraceID: "t1", NodeIDs: []string{"n1"}}

	if _, err := j.Judge(context.Background(), "q", recall, nil); err == nil {
		t.Fatal("Judge with garbage output: expected error, got nil")
	}
}

func TestJudgeBackendErrorPropagates(t *testing.T) {
	backend := &fakeJudgeBackend{err: fmt.Errorf("execute failed")}
	j := NewJudge(backend, JudgeConfig{}, "pi")
	recall := &RecallResult{TraceID: "t1"}

	if _, err := j.Judge(context.Background(), "q", recall, nil); err == nil {
		t.Fatal("Judge with backend error: expected error, got nil")
	}
}

func TestJudgeTruncatesOversizedHistory(t *testing.T) {
	backend := &fakeJudgeBackend{
		output: `{"score":1,"relevant_nodes":["n1"],"rationale":"ok"}`,
	}
	j := NewJudge(backend, JudgeConfig{}, "pi")
	recall := &RecallResult{TraceID: "t1", NodeIDs: []string{"n1"}}

	// 20 messages of 1KB each ≈ 20KB total, over the 8KB budget.
	history := make([]Message, 0, 20)
	for i := range 20 {
		history = append(history, Message{
			Role:    "user",
			Content: fmt.Sprintf("msg-%02d-", i) + strings.Repeat("x", 1000),
		})
	}
	if _, err := j.Judge(context.Background(), "q", recall, history); err != nil {
		t.Fatalf("Judge: %v", err)
	}

	prompt, _, _ := backend.lastCall()
	var payload judgePayload
	if err := json.Unmarshal([]byte(prompt), &payload); err != nil {
		t.Fatalf("prompt is not valid JSON after truncation: %v", err)
	}
	total := 0
	for _, m := range payload.History {
		total += len(m.Role) + len(m.Content)
	}
	if total > maxJudgeHistoryBytes {
		t.Fatalf("history bytes = %d, budget %d", total, maxJudgeHistoryBytes)
	}
	if len(payload.History) == 0 || len(payload.History) >= len(history) {
		t.Fatalf("expected some oldest messages dropped, kept %d of %d", len(payload.History), len(history))
	}
	// The most recent message must survive truncation.
	last := payload.History[len(payload.History)-1]
	if !strings.HasPrefix(last.Content, "msg-19-") {
		t.Fatalf("newest message lost: %q", last.Content[:min(20, len(last.Content))])
	}
}

func TestTruncateHistorySingleOversizedMessage(t *testing.T) {
	history := []Message{{Role: "user", Content: strings.Repeat("y", 3*maxJudgeHistoryBytes)}}
	kept := truncateHistory(history, maxJudgeHistoryBytes)
	if len(kept) != 1 {
		t.Fatalf("kept %d messages, want 1", len(kept))
	}
	total := len(kept[0].Role) + len(kept[0].Content)
	if total > maxJudgeHistoryBytes+3 { // +3 for the "..." suffix
		t.Fatalf("single message bytes = %d, budget %d", total, maxJudgeHistoryBytes)
	}
}

// ---------------------------------------------------------------------------
// query recorder tests
// ---------------------------------------------------------------------------

func newQueryRecorderStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "memory_graph"))
	if err := store.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return store
}

func TestQueryRecorderRecallJudgeRoundTrip(t *testing.T) {
	store := newQueryRecorderStore(t)
	rec := NewQueryRecorder(store, "w1")

	entry := QueryLogEntry{
		TraceID:   "trace-a",
		Query:     "why retries?",
		Timestamp: time.Now().UTC(),
		Version:   1,
		NodeIDs:   []string{"n1"},
		Rounds:    2,
		AgentRuns: 1,
		Found:     true,
	}
	if err := rec.RecordRecall(entry); err != nil {
		t.Fatalf("RecordRecall: %v", err)
	}

	found, err := rec.ApplyJudge("trace-a", &JudgeResult{Score: 0.9, RelevantNodes: []string{"n1"}, Rationale: "relevant"}, BaselineSignal{Covered: true, TopK: []string{"n1", "n2"}})
	if err != nil {
		t.Fatalf("ApplyJudge: %v", err)
	}
	if !found {
		t.Fatal("ApplyJudge: entry not found")
	}

	entries, err := store.ReadQueryLog("w1")
	if err != nil {
		t.Fatalf("ReadQueryLog: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	got := entries[0]
	if !got.JudgeDone || got.JudgeScore != 0.9 || !got.BaselineCovered {
		t.Fatalf("judge write-back wrong: %+v", got)
	}
	if len(got.BaselineTopK) != 2 || got.BaselineTopK[0] != "n1" || got.BaselineTopK[1] != "n2" {
		t.Fatalf("BaselineTopK = %v, want [n1 n2]", got.BaselineTopK)
	}
	if len(got.RelevantNodes) != 1 || got.RelevantNodes[0] != "n1" {
		t.Fatalf("RelevantNodes = %v, want [n1]", got.RelevantNodes)
	}
	// Recall fields must survive the judge write-back untouched.
	if got.Query != "why retries?" || got.Rounds != 2 || !got.Found {
		t.Fatalf("recall fields clobbered: %+v", got)
	}

	// Unknown trace: found=false, no error.
	found, err = rec.ApplyJudge("trace-unknown", &JudgeResult{Score: 0.1}, BaselineSignal{})
	if err != nil || found {
		t.Fatalf("ApplyJudge unknown: found=%v err=%v", found, err)
	}
}

func TestQueryRecorderQueriesBetweenFilters(t *testing.T) {
	store := newQueryRecorderStore(t)
	rec := NewQueryRecorder(store, "w1")

	now := time.Now().UTC()
	entries := []QueryLogEntry{
		{TraceID: "v1-judged", Query: "q1", Timestamp: now, Version: 1, Found: true},
		{TraceID: "v2-judged", Query: "q2", Timestamp: now, Version: 2, Found: true},
		{TraceID: "v2-unjudged", Query: "q3", Timestamp: now, Version: 2, Found: true},
		{TraceID: "v3-judged", Query: "q4", Timestamp: now, Version: 3, Found: true},
	}
	for _, e := range entries {
		if err := rec.RecordRecall(e); err != nil {
			t.Fatalf("RecordRecall %s: %v", e.TraceID, err)
		}
	}
	for _, id := range []string{"v1-judged", "v2-judged", "v3-judged"} {
		if _, err := rec.ApplyJudge(id, &JudgeResult{Score: 0.8}, BaselineSignal{Covered: true}); err != nil {
			t.Fatalf("ApplyJudge %s: %v", id, err)
		}
	}

	// (1, 2]: only the judged version-2 entry.
	got, err := rec.QueriesBetween(1, 2)
	if err != nil {
		t.Fatalf("QueriesBetween: %v", err)
	}
	if len(got) != 1 || got[0].TraceID != "v2-judged" {
		t.Fatalf("QueriesBetween(1,2) = %v", traceIDs(got))
	}

	// (0, 3]: both judged entries of v1..v3; the unjudged one is excluded.
	got, err = rec.QueriesBetween(0, 3)
	if err != nil {
		t.Fatalf("QueriesBetween: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("QueriesBetween(0,3) = %v", traceIDs(got))
	}

	// Empty range.
	got, err = rec.QueriesBetween(2, 2)
	if err != nil {
		t.Fatalf("QueriesBetween: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("QueriesBetween(2,2) = %v", traceIDs(got))
	}
}

func traceIDs(entries []*QueryLogEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.TraceID)
	}
	return ids
}
