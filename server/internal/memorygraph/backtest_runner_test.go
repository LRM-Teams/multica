package memorygraph

import (
	"context"
	"strings"
	"testing"
	"time"
)

// exploreBackendCalls returns the fake explore backend's call count.
func exploreBackendCalls(f *fakeExploreBackend) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// TestConsolidateTTTWithRealBacktestRunner (review P0-3/R2): with the
// production ExploreBacktestRunner wired, a covered backtest query runs a
// REAL explore against each candidate version (pinned to the candidate, not
// the current pointer) instead of taking the runner-absent conservative
// pass. The explore trajectories play against a live loopback tool server
// over the temp store.
func TestConsolidateTTTWithRealBacktestRunner(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta routing notes")

	// One judged window query against v1; adopted path found n1 in 1 round.
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

	// Both consolidation trajectories submit without changes: candidates are
	// exact copies of v1, so the query stays covered and the runner runs.
	consBackend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(ConsolidateOp{Op: OpSubmit})
	}}
	explBackend := &fakeExploreBackend{t: t}

	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 2
	c := NewConsolidator(store, consBackend, cfg, "test", nil, nil)
	c.SetRunner(NewExploreBacktestRunner(store, nil, explBackend, DefaultRetrievalConfig(), testExploreConfig(), "test"))

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if !res.Switched {
		t.Fatalf("Switched = false, want a winning candidate (candidates: %+v)", res.Candidates)
	}
	if got := exploreBackendCalls(explBackend); got != 2 {
		t.Fatalf("explore backend calls = %d, want 2 (one full backtest per candidate)", got)
	}
	for _, cs := range res.Candidates {
		if !cs.Passed {
			t.Fatalf("candidate v%d failed gates: %v", cs.Version, cs.GateFailures)
		}
		if len(cs.Queries) != 1 {
			t.Fatalf("candidate v%d queries = %d, want 1", cs.Version, len(cs.Queries))
		}
		qs := cs.Queries[0]
		if !qs.Covered || !qs.RequiresFullBacktest || !qs.FullBacktestRan {
			t.Fatalf("candidate v%d query stat = %+v, want covered query with a real full backtest", cs.Version, qs)
		}
		if qs.AcceptedWithoutExplore {
			t.Fatalf("candidate v%d took the runner-absent conservative pass despite a wired runner", cs.Version)
		}
		if !qs.Found || qs.Rounds != 1 {
			t.Fatalf("candidate v%d full backtest = found %v rounds %.0f, want true/1", cs.Version, qs.Found, qs.Rounds)
		}
	}
}

// TestConsolidateTTTRecordsRegressionsOnSwitch (review P0-4/Q26): when a
// candidate WINS but a non-gate window query degraded vs baseline, that
// query joins the permanent regression set so every future transition
// re-checks it.
func TestConsolidateTTTRecordsRegressionsOnSwitch(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n-target", "alpha beta routing notes")
	seedGraphNode(t, store, 1, "n-gone", "omega cache eviction policy")

	appendJudged := func(traceID, query string, nodes []string) {
		t.Helper()
		if err := store.AppendQueryLog("w1", &QueryLogEntry{
			TraceID:       traceID,
			Query:         query,
			Timestamp:     time.Now().UTC(),
			Version:       1,
			Found:         true,
			Rounds:        1,
			JudgeDone:     true,
			JudgeScore:    0.9,
			RelevantNodes: nodes,
		}); err != nil {
			t.Fatalf("AppendQueryLog %s: %v", traceID, err)
		}
	}
	appendJudged("t-ok", "alpha routing", []string{"n-target"})
	appendJudged("t-bad", "omega cache eviction", []string{"n-gone"})

	// Both trajectories delete n-gone: the q-bad ground truth leaves the
	// graph, so q-bad is uncovered (regressed) on every candidate while
	// q-ok stays covered. With RecallTolerance 0.5 the recall drop (1.0 ->
	// 0.5) does not fail gate 3, and gate 4 is empty — a candidate wins and
	// the switch happens with q-bad degraded.
	backend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(
			ConsolidateOp{Op: OpDeleteNode, NodeID: "n-gone"},
			ConsolidateOp{Op: OpSubmit},
		)
	}}
	cfg := DefaultConsolidateConfig()
	cfg.TTVTrajectories = 2
	cfg.RecallTolerance = 0.5
	c := NewConsolidator(store, backend, cfg, "test", nil, nil)

	res, err := c.Consolidate(context.Background())
	if err != nil {
		t.Fatalf("Consolidate: %v", err)
	}
	if !res.Switched {
		t.Fatalf("Switched = false, want a winning switch (candidates: %+v)", res.Candidates)
	}

	entries, err := store.ReadRegression()
	if err != nil {
		t.Fatalf("ReadRegression: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("regression entries = %d, want exactly 1: %+v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Query != "omega cache eviction" {
		t.Fatalf("regression query = %q, want %q", entry.Query, "omega cache eviction")
	}
	if len(entry.RelevantNodes) != 1 || entry.RelevantNodes[0] != "n-gone" {
		t.Fatalf("regression ground truth = %v, want [n-gone]", entry.RelevantNodes)
	}
	if entry.AddedVersion != res.WinnerVersion {
		t.Fatalf("AddedVersion = %d, want winner %d", entry.AddedVersion, res.WinnerVersion)
	}
	if entry.BaselineRounds != 1 {
		t.Fatalf("BaselineRounds = %d, want 1", entry.BaselineRounds)
	}
	if !strings.Contains(entry.Reason, "regressed on switch") {
		t.Fatalf("Reason = %q, want a regression explanation", entry.Reason)
	}
}

// TestRecordRegressionsDeduplicates (review P0-4): a query already in the
// regression set is never appended twice, and regression-set entries
// re-checked as backtest input never re-append themselves.
func TestRecordRegressionsDeduplicates(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")

	c := NewConsolidator(store, nil, DefaultConsolidateConfig(), "test", nil, nil)

	queries := []*BacktestQuery{
		{Query: "omega cache", RelevantNodes: []string{"n-gone"}, BaselineRounds: 2, BaselineFound: true},
		// The regression-set copy of an already-known query (Regression=true)
		// must not re-append itself even when it regresses again.
		{Query: "known regression", RelevantNodes: []string{"n1"}, BaselineRounds: 2, BaselineFound: true, Regression: true},
	}
	winner := CandidateStats{Version: 2, Queries: []QueryBacktestStat{
		{Query: "omega cache", BaselineRounds: 2, Regressed: true},
		{Query: "known regression", BaselineRounds: 2, Regressed: true},
	}}

	c.recordRegressions(2, winner, queries)
	entries, err := store.ReadRegression()
	if err != nil {
		t.Fatalf("ReadRegression: %v", err)
	}
	if len(entries) != 1 || entries[0].Query != "omega cache" {
		t.Fatalf("regression entries = %+v, want exactly [omega cache]", entries)
	}

	// Repeat: the same degraded query (and a new one) — only the new query
	// is appended; the repeat is deduped by query text.
	queries = append(queries, &BacktestQuery{Query: "pi scheduler", RelevantNodes: []string{"n1"}, BaselineRounds: 1, BaselineFound: true})
	winner.Queries = append(winner.Queries, QueryBacktestStat{Query: "pi scheduler", BaselineRounds: 1, Regressed: true})
	c.recordRegressions(3, winner, queries)
	c.recordRegressions(4, winner, queries) // a third time for good measure

	entries, err = store.ReadRegression()
	if err != nil {
		t.Fatalf("ReadRegression: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("regression entries = %d, want 2 (no duplicates): %+v", len(entries), entries)
	}
	got := map[string]int{}
	for _, e := range entries {
		got[e.Query]++
	}
	if got["omega cache"] != 1 || got["pi scheduler"] != 1 {
		t.Fatalf("regression entries by query = %v, want one each", got)
	}
}
