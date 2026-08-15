package memorygraph

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// fakeIngestBackend is a scriptable AgentBackend double for the ingester.
type fakeIngestBackend struct {
	mu         sync.Mutex
	calls      int
	lastPrompt string
	output     string
	err        error
}

func (f *fakeIngestBackend) Execute(_ context.Context, prompt string, _ agent.ExecOptions) (*agent.Session, error) {
	f.mu.Lock()
	f.calls++
	f.lastPrompt = prompt
	f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return exploreCompletedSession(f.output), nil
}

func (f *fakeIngestBackend) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeIngestBackend) prompt() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastPrompt
}

func newIngestStore(t *testing.T) *Store {
	t.Helper()
	store := NewStore(t.TempDir())
	if err := store.Init(); err != nil {
		t.Fatalf("init store: %v", err)
	}
	return store
}

func testTrajectory(contents ...string) json.RawMessage {
	type msg struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	msgs := make([]msg, 0, len(contents))
	for i, c := range contents {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, msg{Role: role, Content: c})
	}
	b, err := json.Marshal(msgs)
	if err != nil {
		panic(err)
	}
	return b
}

// TestIngest_HappyPath: valid strict-JSON output -> staging file carries the
// summary, entities, tags, and fallback:false frontmatter.
func TestIngest_HappyPath(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: `{"summary":"Fixed the auth token refresh bug.","entities":["auth.go","token-service"],"tags":["bugfix","auth"]}`}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	seg := SegmentExport{
		SegmentID:    "sess-1-7",
		AgentRunID:   "run-1",
		Trajectory:   testTrajectory("please fix the auth bug", "done, patched auth.go"),
		ClosingEvent: "completion",
	}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if backend.callCount() != 1 {
		t.Fatalf("backend calls = %d, want 1", backend.callCount())
	}

	b, err := store.ReadStagingSegment("sess-1-7")
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		`segment_id: sess-1-7`,
		`agent_run_id: run-1`,
		`closing_event: completion`,
		`fallback: false`,
		"Fixed the auth token refresh bug.",
		"- auth.go",
		"- token-service",
		"- bugfix",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("staging file missing %q:\n%s", want, content)
		}
	}
}

// TestIngest_FallbackOnGarbageOutput: an unparseable final response falls back
// to the deterministic extractive summary (fallback:true) and ingest still
// succeeds.
func TestIngest_FallbackOnGarbageOutput(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: "I summarized things but produced no JSON."}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	last := "all done, deployed the fix to production"
	seg := SegmentExport{
		SegmentID:  "sess-2-1",
		AgentRunID: "run-2",
		Trajectory: testTrajectory("deploy the fix", last),
	}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	b, err := store.ReadStagingSegment("sess-2-1")
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}
	content := string(b)
	for _, want := range []string{
		`fallback: true`,
		"[extractive fallback] trajectory contained 2 messages.",
		last,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("staging file missing %q:\n%s", want, content)
		}
	}
}

// TestIngest_FallbackOnBackendFailure covers nil backend and execute error:
// both yield the extractive fallback rather than an ingest error.
func TestIngest_FallbackOnBackendFailure(t *testing.T) {
	for name, backend := range map[string]*fakeIngestBackend{
		"nil backend":   nil,
		"execute error": {err: errors.New("backend down")},
	} {
		t.Run(name, func(t *testing.T) {
			store := newIngestStore(t)
			var b AgentBackend
			if backend != nil {
				b = backend
			}
			ing := NewIngester(store, b, "pi", "test-model", 0)
			seg := SegmentExport{SegmentID: "seg-x", AgentRunID: "run-x", Trajectory: testTrajectory("hi", "hello")}
			if err := ing.Ingest(context.Background(), seg); err != nil {
				t.Fatalf("ingest: %v", err)
			}
			content, err := store.ReadStagingSegment("seg-x")
			if err != nil {
				t.Fatalf("read staging: %v", err)
			}
			if !strings.Contains(string(content), `fallback: true`) {
				t.Errorf("expected fallback frontmatter:\n%s", content)
			}
		})
	}
}

// TestIngest_IdempotentSecondIngest: a second ingest of the same segment is a
// no-op (staging is immutable, Q24) and does not call the backend again.
func TestIngest_IdempotentSecondIngest(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: `{"summary":"S","entities":["e"],"tags":["t"]}`}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	seg := SegmentExport{SegmentID: "sess-3-2", AgentRunID: "run-3", Trajectory: testTrajectory("q", "a")}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("first ingest: %v", err)
	}
	first, err := store.ReadStagingSegment("sess-3-2")
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}

	backend.output = `{"summary":"DIFFERENT","entities":["x"],"tags":["y"]}`
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("second ingest: %v", err)
	}
	if backend.callCount() != 1 {
		t.Fatalf("backend calls = %d, want 1 (second ingest must be a no-op)", backend.callCount())
	}
	second, err := store.ReadStagingSegment("sess-3-2")
	if err != nil {
		t.Fatalf("read staging: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("staging file changed across idempotent re-ingest")
	}
}

// TestIngest_ToleratesEmptyTrajectory: the trained seam paths pass no
// trajectory bytes; ingest must still succeed.
func TestIngest_ToleratesEmptyTrajectory(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: `{"summary":"no trajectory available","entities":[],"tags":[]}`}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	seg := SegmentExport{SegmentID: "sess-4-1", AgentRunID: "run-4", ClosingEvent: "delegation"}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if !strings.Contains(backend.prompt(), "(none available)") {
		t.Errorf("prompt should note the missing trajectory:\n%s", backend.prompt())
	}
	if _, err := store.ReadStagingSegment("sess-4-1"); err != nil {
		t.Fatalf("read staging: %v", err)
	}
}

// TestIngest_TrajectoryTruncation: trajectory text is capped at the prompt
// budget, dropping the oldest messages first.
func TestIngest_TrajectoryTruncation(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: `{"summary":"S","entities":[],"tags":[]}`}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	const markerOld = "OLDEST-MESSAGE-MARKER"
	const markerNew = "NEWEST-MESSAGE-MARKER"
	// ~24KB across 6 messages -> the oldest must be dropped to fit 16KB.
	contents := []string{markerOld + " " + strings.Repeat("x", 4*1024)}
	for i := 0; i < 5; i++ {
		contents = append(contents, strings.Repeat("x", 4*1024))
	}
	contents = append(contents, markerNew)

	seg := SegmentExport{SegmentID: "sess-5-1", AgentRunID: "run-5", Trajectory: testTrajectory(contents...)}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	prompt := backend.prompt()
	if strings.Contains(prompt, markerOld) {
		t.Errorf("prompt must drop the oldest messages once over budget")
	}
	if !strings.Contains(prompt, markerNew) {
		t.Errorf("prompt must keep the newest messages")
	}
	if len(prompt) > ingestTrajectoryBudgetBytes+4096 {
		t.Errorf("prompt size %d exceeds budget + overhead", len(prompt))
	}
}

// TestIngest_TaskMessageSnapshotTrajectory: the allowlisted task_message
// snapshot shape (type/content entries) is extracted as role+content pairs.
func TestIngest_TaskMessageSnapshotTrajectory(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: `{"summary":"snapshot summary","entities":[],"tags":[]}`}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	raw := json.RawMessage(`[
		{"sequence":1,"type":"user","tool":"","content":"build the feature","input":"","output":""},
		{"sequence":2,"type":"assistant","tool":"","content":"feature built","input":"","output":""}
	]`)
	seg := SegmentExport{SegmentID: "multica:run-6", AgentRunID: "run-6", Trajectory: raw}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	prompt := backend.prompt()
	if !strings.Contains(prompt, "build the feature") || !strings.Contains(prompt, "feature built") {
		t.Errorf("prompt should embed task_message contents:\n%s", prompt)
	}
}

// ---------------------------------------------------------------------------
// ingest outcome metrics (design §7, review R14)
// ---------------------------------------------------------------------------

// fakeIngestMetrics records RecordGraphMemoryIngest results.
type fakeIngestMetrics struct {
	mu      sync.Mutex
	results []string
}

func (f *fakeIngestMetrics) RecordGraphMemoryIngest(result string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results = append(f.results, result)
}

func (f *fakeIngestMetrics) captured() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.results))
	copy(out, f.results)
	return out
}

// TestIngest_TrajectoryDrivesSummaryPrompt (review R1): the real trajectory
// bytes from the seam are parsed into messages and embedded in the
// summarizer prompt — the summary is built from actual segment content, not
// a contentless fallback.
func TestIngest_TrajectoryDrivesSummaryPrompt(t *testing.T) {
	store := newIngestStore(t)
	backend := &fakeIngestBackend{output: `{"summary":"Refactored the retry loop.","entities":["retry.go"],"tags":["refactor"]}`}
	ing := NewIngester(store, backend, "pi", "test-model", 0)

	seg := SegmentExport{
		SegmentID:    "sess-9-3",
		AgentRunID:   "run-9",
		Trajectory:   testTrajectory("refactor the retry loop in retry.go", "done, extracted backoff helper"),
		ClosingEvent: "completion",
	}
	if err := ing.Ingest(context.Background(), seg); err != nil {
		t.Fatalf("ingest: %v", err)
	}
	prompt := backend.prompt()
	for _, want := range []string{"[user]", "refactor the retry loop in retry.go", "[assistant]", "done, extracted backoff helper"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("summarizer prompt missing trajectory content %q:\n%s", want, prompt)
		}
	}
}

// TestIngest_MetricOutcomes: fresh ingests record ok / fallback by outcome;
// the idempotent repeat records nothing.
func TestIngest_MetricOutcomes(t *testing.T) {
	store := newIngestStore(t)
	metrics := &fakeIngestMetrics{}

	// LLM summary -> ok.
	okBackend := &fakeIngestBackend{output: `{"summary":"did the thing","entities":[],"tags":[]}`}
	ing := NewIngester(store, okBackend, "pi", "test-model", 0)
	ing.SetMetrics(metrics)
	if err := ing.Ingest(context.Background(), SegmentExport{SegmentID: "seg-ok", AgentRunID: "r1", Trajectory: testTrajectory("do the thing", "done")}); err != nil {
		t.Fatalf("ingest ok: %v", err)
	}
	// Idempotent repeat: no metric.
	if err := ing.Ingest(context.Background(), SegmentExport{SegmentID: "seg-ok", AgentRunID: "r1"}); err != nil {
		t.Fatalf("ingest repeat: %v", err)
	}

	// Backend failure -> extractive fallback -> fallback.
	badBackend := &fakeIngestBackend{err: errors.New("backend down")}
	ing2 := NewIngester(store, badBackend, "pi", "test-model", 0)
	ing2.SetMetrics(metrics)
	if err := ing2.Ingest(context.Background(), SegmentExport{SegmentID: "seg-fb", AgentRunID: "r2", Trajectory: testTrajectory("hi", "hello")}); err != nil {
		t.Fatalf("ingest fallback: %v", err)
	}

	got := metrics.captured()
	if len(got) != 2 || got[0] != "ok" || got[1] != "fallback" {
		t.Fatalf("metric results = %v, want [ok fallback]", got)
	}
}
