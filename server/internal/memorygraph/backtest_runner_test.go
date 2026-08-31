package memorygraph

import (
	"context"
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
	scope := testConsolidateScope()
	c := NewConsolidator(store, consBackend, cfg, scope, nil, nil)
	runner, err := NewExploreBacktestRunner(store, nil, explBackend, DefaultRetrievalConfig(), testExploreConfig(), scope)
	if err != nil {
		t.Fatalf("NewExploreBacktestRunner: %v", err)
	}
	if err := c.SetRunner(runner); err != nil {
		t.Fatalf("SetRunner: %v", err)
	}

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

func TestNewExploreBacktestRunnerRejectsEmbedderWorkspaceMismatch(t *testing.T) {
	store := newTestStore(t)
	scope := testConsolidateScope()
	embedScope := scope
	embedScope.Purpose = ProviderPurposeEmbed
	embedScope.WorkspaceID = "other-workspace"
	emb, err := NewCachedEmbedder(NewHashEmbedder(), store, embedScope)
	if err != nil {
		t.Fatalf("NewCachedEmbedder: %v", err)
	}
	backend := &fakeExploreBackend{t: t}
	if _, err := NewExploreBacktestRunner(store, emb, backend, DefaultRetrievalConfig(), testExploreConfig(), scope); err == nil {
		t.Fatal("NewExploreBacktestRunner accepted an embedder from another Workspace")
	}
	if got := exploreBackendCalls(backend); got != 0 {
		t.Fatalf("backend calls = %d, want 0", got)
	}
}
