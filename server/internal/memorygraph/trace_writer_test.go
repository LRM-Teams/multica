package memorygraph

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// completedSessionWithMessages returns a session whose message channel
// carries msgs (buffered and closed before the result resolves, mirroring
// the pkg/agent contract) and whose result is a completed output.
func completedSessionWithMessages(output string, msgs ...agent.Message) *agent.Session {
	ch := make(chan agent.Message, len(msgs))
	for _, m := range msgs {
		ch <- m
	}
	close(ch)
	results := make(chan agent.Result, 1)
	results <- agent.Result{Status: "completed", Output: output}
	close(results)
	return &agent.Session{Messages: ch, Result: results}
}

// traceFakeBackend replays a fixed completed session: output as the final
// response, msgs streamed before it. executeErr fails the Execute call.
type traceFakeBackend struct {
	output     string
	msgs       []agent.Message
	executeErr error
}

func (f *traceFakeBackend) Execute(_ context.Context, _ string, _ agent.ExecOptions) (*agent.Session, error) {
	if f.executeErr != nil {
		return nil, f.executeErr
	}
	return completedSessionWithMessages(f.output, f.msgs...), nil
}

// readTraceRecords parses a trajectory JSONL file into raw records.
func readTraceRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read trace file %s: %v", path, err)
	}
	var records []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("parse %s line %q: %v", path, line, err)
		}
		records = append(records, rec)
	}
	return records
}

func exploreTracePath(graphDir, traceID, runID string) string {
	return filepath.Join(graphDir, "trajectories", "explore", traceID+"__"+runID+".jsonl")
}

func consolidateTracePath(graphDir string, version int, actor string) string {
	return filepath.Join(graphDir, "trajectories", "consolidate", fmt.Sprintf("v%d__%s.jsonl", version, actor))
}

// ---------------------------------------------------------------------------
// TraceRecorder unit tests
// ---------------------------------------------------------------------------

func TestTraceRecorderExploreFileShape(t *testing.T) {
	dir := t.TempDir()
	rec := NewTraceRecorder(dir)

	msgs := make(chan agent.Message, 4)
	msgs <- agent.Message{Type: agent.MessageText, Content: "exploring the graph"}
	msgs <- agent.Message{Type: agent.MessageToolUse, Tool: "shell", CallID: "c1", Input: map[string]any{"cmd": "curl /expand"}, SessionID: "secret-session"}
	msgs <- agent.Message{Type: agent.MessageToolResult, Tool: "shell", CallID: "c1", Output: `{"round":1}`}
	// Diagnostic internals must never reach the trace file.
	msgs <- agent.Message{Type: agent.MessageDiagnostic, Title: "diag", Diagnostic: "provider-internal", Content: "diag content"}
	close(msgs)
	drain := rec.Drain(msgs)

	started := time.Now().UTC().Truncate(time.Second)
	rec.WriteExploreTrace(ExploreTraceMeta{
		TraceID:      "trace-1",
		RunID:        "run-1",
		GraphVersion: 3,
		Seed:         2,
		Model:        "test-model",
		StartedAt:    started,
		PromptChars:  42,
	}, drain, ExploreRun{Found: true, Rounds: 2, NodeIDs: []string{"n1", "n2"}})

	records := readTraceRecords(t, exploreTracePath(dir, "trace-1", "run-1"))
	if len(records) != 6 { // header + 4 messages + footer
		t.Fatalf("records = %d, want 6: %v", len(records), records)
	}

	header := records[0]
	if header["kind"] != "header" || header["trace_id"] != "trace-1" || header["run_id"] != "run-1" {
		t.Fatalf("header = %v", header)
	}
	if header["graph_version"] != 3.0 || header["seed"] != 2.0 || header["model"] != "test-model" || header["prompt_chars"] != 42.0 {
		t.Fatalf("header = %v", header)
	}
	if header["started_at"] == "" {
		t.Fatalf("header missing started_at: %v", header)
	}

	// Messages: arrival order, string-form types, allowlisted keys only.
	wantTypes := []string{"text", "tool-use", "tool-result", "diagnostic"}
	allowedKeys := map[string]bool{"kind": true, "sequence": true, "type": true, "tool": true, "content": true, "input": true, "output": true}
	for i, want := range wantTypes {
		m := records[1+i]
		if m["kind"] != "message" || m["sequence"] != float64(i) || m["type"] != want {
			t.Fatalf("message %d = %v, want type %q", i, m, want)
		}
		for k := range m {
			if !allowedKeys[k] {
				t.Fatalf("message %d carries non-allowlisted key %q: %v", i, k, m)
			}
		}
	}
	if records[1]["content"] != "exploring the graph" || records[1]["input"] != "" {
		t.Fatalf("text message = %v", records[1])
	}
	if records[2]["tool"] != "shell" || records[2]["input"] != `{"cmd":"curl /expand"}` {
		t.Fatalf("tool-use message = %v", records[2])
	}
	if records[3]["output"] != `{"round":1}` {
		t.Fatalf("tool-result message = %v", records[3])
	}

	footer := records[5]
	if footer["kind"] != "footer" || footer["found"] != true || footer["rounds"] != 2.0 || footer["error"] != "" {
		t.Fatalf("footer = %v", footer)
	}
	ids, _ := footer["node_ids"].([]any)
	if len(ids) != 2 || ids[0] != "n1" || ids[1] != "n2" {
		t.Fatalf("footer node_ids = %v", footer)
	}
	if footer["finished_at"] == "" {
		t.Fatalf("footer missing finished_at: %v", footer)
	}
}

func TestTraceRecorderConsolidateFileShape(t *testing.T) {
	dir := t.TempDir()
	rec := NewTraceRecorder(dir)

	drain := rec.Drain(nil) // session without a message stream
	rec.WriteConsolidateTrace(ConsolidateTraceMeta{
		GraphVersion: 5,
		Actor:        "ttt-1",
		Model:        "test-model",
		StartedAt:    time.Now().UTC(),
		PromptChars:  7,
	}, drain, 3, 1, nil)

	records := readTraceRecords(t, consolidateTracePath(dir, 5, "ttt-1"))
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (header+footer): %v", len(records), records)
	}
	if records[0]["kind"] != "header" || records[0]["run_id"] != "ttt-1" || records[0]["graph_version"] != 5.0 || records[0]["prompt_chars"] != 7.0 {
		t.Fatalf("header = %v", records[0])
	}
	footer := records[1]
	if footer["kind"] != "footer" || footer["applied"] != 3.0 || footer["rejected"] != 1.0 || footer["error"] != "" {
		t.Fatalf("footer = %v", footer)
	}

	// Error footer on a failed trajectory.
	rec.WriteConsolidateTrace(ConsolidateTraceMeta{GraphVersion: 6, Actor: "ttt-2", StartedAt: time.Now().UTC()},
		nil, 0, 0, fmt.Errorf("agent exploded"))
	errRecords := readTraceRecords(t, consolidateTracePath(dir, 6, "ttt-2"))
	if got := errRecords[1]["error"]; got != "agent exploded" {
		t.Fatalf("error footer = %v", errRecords[1])
	}
}

func TestTraceRecorderNilNoOp(t *testing.T) {
	var rec *TraceRecorder

	// Writes and reward appends are no-ops and never panic.
	rec.WriteExploreTrace(ExploreTraceMeta{TraceID: "t", RunID: "r"}, nil, ExploreRun{Found: true})
	rec.WriteConsolidateTrace(ConsolidateTraceMeta{GraphVersion: 1, Actor: "consolidator"}, nil, 0, 0, nil)
	if err := rec.AppendRewardTrace(RewardTraceRecord{TraceID: "t"}); err != nil {
		t.Fatalf("nil AppendRewardTrace: %v", err)
	}

	// The nil recorder still drains the message channel: a long trajectory
	// must not stall on the 256-cap buffer when recording is disabled.
	msgs := make(chan agent.Message, 300)
	for i := 0; i < 300; i++ {
		msgs <- agent.Message{Type: agent.MessageLog, Content: "line"}
	}
	close(msgs)
	if got := len(rec.Drain(msgs).Messages()); got != 300 {
		t.Fatalf("nil recorder drained %d messages, want 300", got)
	}
}

func TestTraceRecorderWriteFailureBestEffort(t *testing.T) {
	// A regular file as the graph dir makes every MkdirAll fail; writes must
	// be swallowed, not panic.
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := NewTraceRecorder(blocker)
	rec.WriteExploreTrace(ExploreTraceMeta{TraceID: "t", RunID: "r"}, nil, ExploreRun{Found: true})
	rec.WriteConsolidateTrace(ConsolidateTraceMeta{GraphVersion: 1, Actor: "a"}, nil, 0, 0, nil)
	if err := rec.AppendRewardTrace(RewardTraceRecord{TraceID: "t"}); err != nil {
		t.Fatalf("AppendRewardTrace with no matching files: %v", err)
	}
}

func TestTraceRecorderSanitizesFileComponents(t *testing.T) {
	dir := t.TempDir()
	rec := NewTraceRecorder(dir)
	rec.WriteExploreTrace(ExploreTraceMeta{TraceID: "../evil/trace", RunID: "run"}, nil, ExploreRun{})

	matches, err := filepath.Glob(filepath.Join(dir, "trajectories", "explore", "*.jsonl"))
	if err != nil || len(matches) != 1 {
		t.Fatalf("glob = %v, %v; want exactly one file inside the explore dir", matches, err)
	}
	// The file landed directly inside the explore dir (the glob proves no
	// path traversal: "../" became a flat sanitized name).
	name := filepath.Base(matches[0])
	if strings.Contains(name, "/") || name == ".." || name == "." {
		t.Fatalf("unsanitized file name %q", name)
	}
}

func TestTraceRecorderAppendRewardTrace(t *testing.T) {
	dir := t.TempDir()
	rec := NewTraceRecorder(dir)

	// Two runs of trace-1, one run of another trace.
	rec.WriteExploreTrace(ExploreTraceMeta{TraceID: "trace-1", RunID: "r1", StartedAt: time.Now().UTC()}, nil, ExploreRun{Found: true, Rounds: 1})
	rec.WriteExploreTrace(ExploreTraceMeta{TraceID: "trace-1", RunID: "r2", StartedAt: time.Now().UTC()}, nil, ExploreRun{Found: true, Rounds: 2})
	rec.WriteExploreTrace(ExploreTraceMeta{TraceID: "trace-2", RunID: "r9", StartedAt: time.Now().UTC()}, nil, ExploreRun{Found: true, Rounds: 1})

	if err := rec.AppendRewardTrace(RewardTraceRecord{TraceID: "trace-1", JudgeScore: 0.9, Reward: 0.8, Rounds: 1}); err != nil {
		t.Fatalf("AppendRewardTrace: %v", err)
	}

	for _, runID := range []string{"r1", "r2"} {
		records := readTraceRecords(t, exploreTracePath(dir, "trace-1", runID))
		last := records[len(records)-1]
		if last["kind"] != "reward" || last["trace_id"] != "trace-1" || last["judge_score"] != 0.9 || last["reward"] != 0.8 || last["rounds"] != 1.0 || last["miss"] != false {
			t.Fatalf("run %s reward record = %v", runID, last)
		}
	}
	// The other trace's file is untouched (header+footer only).
	if records := readTraceRecords(t, exploreTracePath(dir, "trace-2", "r9")); len(records) != 2 {
		t.Fatalf("trace-2 file records = %d, want 2", len(records))
	}
	// An unknown trace id is not an error.
	if err := rec.AppendRewardTrace(RewardTraceRecord{TraceID: "no-such-trace"}); err != nil {
		t.Fatalf("AppendRewardTrace unknown trace: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Explorer wiring
// ---------------------------------------------------------------------------

func TestExplorePersistsTrajectoryTrace(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &traceFakeBackend{
		output: `{"found":true,"summary":"dispatch retries","node_ids":["n-target"],"rounds":1}`,
		msgs: []agent.Message{
			{Type: agent.MessageText, Content: "let me look at the seed node"},
			{Type: agent.MessageToolUse, Tool: "shell", Input: map[string]any{"cmd": "curl /view"}},
			{Type: agent.MessageToolResult, Tool: "shell", Output: "node body"},
		},
	}
	rec := NewTraceRecorder(store.Root)
	cfg := testExploreConfig()
	cfg.Model = "explore-model"
	ex := NewExplorer(store, retr, backend, cfg, "pi", rec)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if !res.Found || len(res.AgentRuns) != 1 {
		t.Fatalf("recall = %+v, want one found run", res)
	}

	path := exploreTracePath(store.Root, res.TraceID, res.AgentRuns[0].RunID)
	records := readTraceRecords(t, path)
	if len(records) != 5 { // header + 3 messages + footer
		t.Fatalf("records = %d, want 5: %v", len(records), records)
	}
	header := records[0]
	if header["trace_id"] != res.TraceID || header["run_id"] != res.AgentRuns[0].RunID {
		t.Fatalf("header = %v", header)
	}
	if header["graph_version"] != float64(res.Version) || header["model"] != "explore-model" {
		t.Fatalf("header = %v, want version %d", header, res.Version)
	}
	if header["prompt_chars"].(float64) <= 0 {
		t.Fatalf("header prompt_chars = %v, want > 0", header["prompt_chars"])
	}
	for i, want := range []string{"text", "tool-use", "tool-result"} {
		if records[1+i]["type"] != want || records[1+i]["sequence"] != float64(i) {
			t.Fatalf("message %d = %v, want type %q", i, records[1+i], want)
		}
	}
	footer := records[4]
	// The replayed session claims rounds:1 in its final JSON but never
	// actually called /explore, so the server-authoritative round count is 0
	// (spec §4.2: rounds bill nodes served, never the agent's claim).
	if footer["found"] != true || footer["rounds"] != 0.0 || footer["error"] != "" {
		t.Fatalf("footer = %v, want found with server-counted rounds=0", footer)
	}
	ids, _ := footer["node_ids"].([]any)
	if len(ids) != 1 || ids[0] != "n-target" {
		t.Fatalf("footer node_ids = %v", footer)
	}
}

// TestExplorePersistsBudgetBlownFooter: the footer must carry the final
// outcome AFTER the server-side budget override forces Found=false.
func TestExplorePersistsBudgetBlownFooter(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &fakeExploreBackend{
		t:              t,
		expandsPerCall: func(int) int { return 3 }, // blows MaxRounds=1
	}
	rec := NewTraceRecorder(store.Root)
	cfg := testExploreConfig()
	cfg.MaxRounds = 1
	ex := NewExplorer(store, retr, backend, cfg, "pi", rec)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil {
		t.Fatalf("Explore: %v", err)
	}
	if len(backend.errs) > 0 {
		t.Fatalf("fake backend tool errors: %v", backend.errs)
	}
	if len(res.AgentRuns) != 1 || res.AgentRuns[0].Found || res.Found {
		t.Fatalf("runs = %+v, want one budget-blown not-found run", res.AgentRuns)
	}

	path := exploreTracePath(store.Root, res.TraceID, res.AgentRuns[0].RunID)
	records := readTraceRecords(t, path)
	footer := records[len(records)-1]
	if footer["kind"] != "footer" || footer["found"] != false || footer["error"] != "" {
		t.Fatalf("footer = %v, want found=false (budget blown) without error", footer)
	}
}

// TestExploreNilRecorderWritesNothing: without a recorder the run works and
// no trajectories directory appears.
func TestExploreNilRecorderWritesNothing(t *testing.T) {
	store := newExploreStore(t)
	retr := newExploreRetriever(t, store)
	backend := &traceFakeBackend{
		output: `{"found":true,"summary":"s","node_ids":["n-target"],"rounds":1}`,
		msgs:   []agent.Message{{Type: agent.MessageText, Content: "hi"}},
	}
	ex := NewExplorer(store, retr, backend, testExploreConfig(), "pi", nil)

	res, err := ex.Explore(context.Background(), "dispatch router retries")
	if err != nil || !res.Found {
		t.Fatalf("Explore = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(store.Root, "trajectories")); !os.IsNotExist(err) {
		t.Fatalf("trajectories dir exists with nil recorder: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Consolidator wiring
// ---------------------------------------------------------------------------

func TestConsolidateInPlacePersistsTrajectoryTrace(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha routing notes")
	if err := store.WriteStagingSegment("seg-1", []byte("gamma delta segment summary")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	backend := &fakeConsolidateBackend{
		respond: func(string, int) string {
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n2", Body: "gamma delta consolidated", SegmentRefs: []string{"seg-1"}}},
				ConsolidateOp{Op: OpDeleteNode, NodeID: "ghost"}, // rejected
			)
		},
		msgs: []agent.Message{
			{Type: agent.MessageText, Content: "folding segments"},
			{Type: agent.MessageToolUse, Tool: "shell", Input: map[string]any{"cmd": "curl /op"}},
		},
	}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	rec := NewTraceRecorder(store.Root)
	c := NewConsolidator(store, backend, cfg, "test", nil, rec)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if res.OpsApplied != 1 || len(res.Rejected) != 1 {
		t.Fatalf("result = %+v, want 1 applied / 1 rejected", res)
	}

	records := readTraceRecords(t, consolidateTracePath(store.Root, 1, CreatorConsolidator))
	if len(records) != 4 { // header + 2 messages + footer
		t.Fatalf("records = %d, want 4: %v", len(records), records)
	}
	if records[0]["run_id"] != CreatorConsolidator || records[0]["graph_version"] != 1.0 {
		t.Fatalf("header = %v", records[0])
	}
	footer := records[3]
	if footer["applied"] != 1.0 || footer["rejected"] != 1.0 || footer["error"] != "" {
		t.Fatalf("footer = %v, want applied=1 rejected=1", footer)
	}
}

func TestConsolidateTTTPersistsPerCandidateTraces(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta routing")
	if err := store.WriteStagingSegment("seg-1", []byte("gamma delta")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	if err := store.AppendQueryLog("w1", &QueryLogEntry{
		TraceID:       "t1",
		Query:         "alpha routing",
		Timestamp:     time.Now().UTC(),
		Version:       1,
		Found:         true,
		Rounds:        1,
		JudgeDone:     true,
		JudgeScore:    0.9,
		RelevantNodes: []string{"n1"},
	}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}

	backend := &fakeConsolidateBackend{respond: func(prompt string, _ int) string {
		switch {
		case strings.Contains(prompt, "trajectory 0 of 2"):
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-t0", Body: "gamma delta consolidated", SegmentRefs: []string{"seg-1"}}},
			)
		default: // trajectory 1 of 2
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-t1", Body: "gamma delta again", SegmentRefs: []string{"seg-1"}}},
			)
		}
	}}
	runner := &fakeFullBacktestRunner{roundsFor: func(int) (int, bool) { return 1, true }}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 2
	rec := NewTraceRecorder(store.Root)
	c := NewConsolidator(store, backend, cfg, "test", nil, rec)
	c.SetRunner(runner)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("Candidates = %d, want 2", len(res.Candidates))
	}

	// Candidates were created as v2 (ttt-0) and v3 (ttt-1); the loser's
	// version dir is removed by the TTT GC but its trace file must survive
	// under trajectories/.
	for version, actor := range map[int]string{2: "ttt-0", 3: "ttt-1"} {
		records := readTraceRecords(t, consolidateTracePath(store.Root, version, actor))
		if len(records) != 2 {
			t.Fatalf("%s records = %d, want 2 (header+footer): %v", actor, len(records), records)
		}
		header := records[0]
		if header["run_id"] != actor || header["graph_version"] != float64(version) {
			t.Fatalf("%s header = %v", actor, header)
		}
		footer := records[1]
		if footer["applied"] != 1.0 || footer["rejected"] != 0.0 || footer["error"] != "" {
			t.Fatalf("%s footer = %v, want applied=1", actor, footer)
		}
	}
}

// TestConsolidatePersistsFailedTrajectory: an execute-level failure still
// leaves a trace file with the error footer.
func TestConsolidatePersistsFailedTrajectory(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha routing notes")
	backend := &traceFakeBackend{executeErr: fmt.Errorf("backend down")}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 1
	rec := NewTraceRecorder(store.Root)
	c := NewConsolidator(store, backend, cfg, "test", nil, rec)

	if _, err := c.Consolidate(context.Background()); err == nil {
		t.Fatal("Consolidate: expected error")
	}
	records := readTraceRecords(t, consolidateTracePath(store.Root, 1, CreatorConsolidator))
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2 (header+footer): %v", len(records), records)
	}
	footer := records[1]
	if footer["applied"] != 0.0 || !strings.Contains(footer["error"].(string), "backend down") {
		t.Fatalf("footer = %v, want applied=0 with the execute error", footer)
	}
}
