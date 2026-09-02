package memorygraph

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// fake full-backtest runner
// ---------------------------------------------------------------------------

// fakeFullBacktestRunner records RunExplore calls and returns a fixed
// (rounds, found) result, or a per-version result when roundsFor is set.
// forbid lists queries that must never trigger a full backtest (the
// coverage/conservative-default assertions).
type fakeFullBacktestRunner struct {
	t *testing.T

	mu        sync.Mutex
	queries   []string
	rounds    int
	found     bool
	roundsFor func(version int) (rounds int, found bool)
	forbid    map[string]bool
}

func (f *fakeFullBacktestRunner) RunExplore(_ context.Context, version int, query string) (int, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.forbid[query] {
		f.t.Errorf("RunExplore must not be called for query %q (neighborhood coverage did not pass)", query)
	}
	f.queries = append(f.queries, query)
	if f.roundsFor != nil {
		rounds, found := f.roundsFor(version)
		return rounds, found, nil
	}
	return f.rounds, f.found, nil
}

func testConsolidateScope() ProviderScope {
	return ProviderScope{
		WorkspaceID: "test-workspace", Purpose: ProviderPurposeConsolidate,
		Provider: "test", Model: "test-consolidate-model", Region: "test-region", PolicyVersion: "test-policy",
	}
}

func mustScopedBacktestRunner(t *testing.T, runner FullBacktestRunner) *ScopedFullBacktestRunner {
	t.Helper()
	scoped, err := newScopedFullBacktestRunner(runner, testConsolidateScope())
	if err != nil {
		t.Fatalf("newScopedFullBacktestRunner: %v", err)
	}
	return scoped
}

func mustScopedBacktestConfirmer(t *testing.T, confirmer BacktestConfirmer) *ScopedBacktestConfirmer {
	t.Helper()
	scoped, err := NewScopedBacktestConfirmer(confirmer, testConsolidateScope())
	if err != nil {
		t.Fatalf("NewScopedBacktestConfirmer: %v", err)
	}
	return scoped
}
func (f *fakeFullBacktestRunner) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.queries)
}

// ---------------------------------------------------------------------------
// BacktestQueries
// ---------------------------------------------------------------------------

func TestBacktestQueriesCollectsWindow(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")

	// Window w1: one judged query at v1 (in window), one unjudged (skipped),
	// one judged at v2 (outside the (prev, fromVersion] window).
	if err := store.AppendQueryLog("w1", &QueryLogEntry{
		TraceID: "t-in", Query: "alpha", Version: 1, Found: true, Rounds: 3,
		JudgeDone: true, JudgeScore: 0.9, RelevantNodes: []string{"n1"},
	}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}
	if err := store.AppendQueryLog("w1", &QueryLogEntry{
		TraceID: "t-unjudged", Query: "alpha", Version: 1,
	}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}
	if _, err := store.CreateVersionFrom(1, "ttt"); err != nil { // v2
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	if err := store.AppendQueryLog("w1", &QueryLogEntry{
		TraceID: "t-next", Query: "alpha", Version: 2, Found: true, Rounds: 1,
		JudgeDone: true, JudgeScore: 0.9, RelevantNodes: []string{"n1"},
	}); err != nil {
		t.Fatalf("AppendQueryLog: %v", err)
	}

	queries, err := BacktestQueries(store, 1)
	if err != nil {
		t.Fatalf("BacktestQueries: %v", err)
	}
	if len(queries) != 1 {
		t.Fatalf("BacktestQueries = %d queries, want 1 (window t-in)", len(queries))
	}
	q := queries[0]
	if q.TraceID != "t-in" || !q.JudgeDone || q.JudgeScore != 0.9 {
		t.Fatalf("window query = %+v, want judged t-in", q)
	}
	// The window baseline is the recorded adopted-path rounds + found flag.
	if q.BaselineRounds != 3 || !q.BaselineFound {
		t.Fatalf("window baseline = rounds %d found %v, want 3/true", q.BaselineRounds, q.BaselineFound)
	}
	count, err := BacktestWindowQueryCount(store, 1)
	if err != nil || count != 2 {
		t.Fatalf("BacktestWindowQueryCount = %d, %v; want 2 including unjudged entry", count, err)
	}
}

// ---------------------------------------------------------------------------
// candidate evaluation
// ---------------------------------------------------------------------------

// Runner-absent conservative default (A2): a covered query passes on the
// neighborhood coverage signal alone; the runner is never invoked.
func TestEvaluateCoveredQueryPassesWithoutRunner(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	seedGraphNode(t, store, 1, "n2", "alpha target")
	if err := store.SaveEdges(1, []*Edge{
		{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "n1", To: "n2", CreatedBy: CreatorIngester, CreatedVersion: 1},
	}, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	cand, err := store.CreateVersionFrom(1, "ttt") // v2
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	// Candidate improves retrieval for the query: n2 now dominates BM25.
	seedGraphNode(t, store, cand, "n2", "alpha alpha alpha target")

	q := &BacktestQuery{
		TraceID: "t1", Query: "alpha", RelevantNodes: []string{"n2"},
		BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9,
	}
	// No runner wired: the conservative default accepts covered queries.
	bt := NewBacktester(store, BacktestConfig{})

	stats := bt.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{q})
	if !stats.Passed {
		t.Fatalf("candidate failed gates: %v", stats.GateFailures)
	}
	if len(stats.Queries) != 1 {
		t.Fatalf("queries = %d, want 1", len(stats.Queries))
	}
	qs := stats.Queries[0]
	if !qs.Covered || !qs.Found || !qs.AcceptedWithoutExplore || qs.RequiresFullBacktest || qs.FullBacktestRan {
		t.Fatalf("query stat = %+v, want covered + accepted without explore", qs)
	}
	if qs.Rounds != 1 {
		t.Fatalf("rounds = %v, want 1 (the baseline-rounds estimate)", qs.Rounds)
	}
	if qs.Regressed {
		t.Fatalf("query stat = %+v, want not regressed", qs)
	}
	// Coverage-only acceptance is not an agent run and must not affect round statistics.
	if stats.MeanRounds != 0 || stats.P95Rounds != 0 {
		t.Fatalf("mean/p95 rounds = %v/%v, want 0/0 without a full backtest", stats.MeanRounds, stats.P95Rounds)
	}
}

// Step-3 failure (A2): ground truth outside the n-hop neighborhood of the
// top-k hits fails the query outright — no agent run, recall miss, and a
// regression because the query passed baseline-side.
func TestEvaluateUncoveredQueryFailsOutright(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	// Candidate rewrites n1 so the query no longer retrieves anything.
	seedGraphNode(t, store, cand, "n1", "zzz qqq")

	q := &BacktestQuery{
		TraceID: "t1", Query: "alpha", RelevantNodes: []string{"n1"},
		BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9,
	}
	runner := &fakeFullBacktestRunner{t: t, rounds: 1, found: true, forbid: map[string]bool{"alpha": true}}
	bt := NewBacktester(store, BacktestConfig{Runner: mustScopedBacktestRunner(t, runner)})

	stats := bt.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{q})
	qs := stats.Queries[0]
	if qs.Covered || qs.Found || qs.FullBacktestRan || qs.AcceptedWithoutExplore {
		t.Fatalf("query stat = %+v, want uncovered outright failure", qs)
	}
	if !qs.Regressed {
		t.Fatalf("query stat = %+v, want regressed (baseline pass -> candidate miss)", qs)
	}
	if runner.callCount() != 0 {
		t.Fatalf("runner called %d times, want 0 (no agent run on uncovered queries)", runner.callCount())
	}
	// Recall gate: candidate recall 0 vs baseline 1.
	if stats.Passed {
		t.Fatalf("candidate passed despite the recall miss")
	}
	if !strings.Contains(strings.Join(stats.GateFailures, ";"), "recall") {
		t.Fatalf("gate failures = %v, want a recall failure", stats.GateFailures)
	}
}

// Cold start suppresses only statistical gates: malformed graphs and
// unreferenced staging material must still fail gates 1 and 2.
func TestEvaluateColdStartKeepsStructuralGates(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	if err := store.WriteStagingSegment("seg-unreferenced", []byte("staging evidence")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	if err := store.SaveEdges(cand, []*Edge{
		{EdgeID: "bad", Type: EdgeTypeSummarizes, From: "n1", To: "missing", CreatedBy: "ttt", CreatedVersion: cand},
	}, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}

	stats := NewBacktester(store, BacktestConfig{ColdStart: true}).EvaluateCandidate(context.Background(), cand, 1, nil)
	if stats.Passed {
		t.Fatal("cold-start candidate passed despite graph and staging structural failures")
	}
	failures := strings.Join(stats.GateFailures, ";")
	if !strings.Contains(failures, "validate") || !strings.Contains(failures, "seg-unreferenced") {
		t.Fatalf("cold-start gate failures = %v, want validate and staging coverage failures", stats.GateFailures)
	}
}

// The n of the coverage check is the number of rounds the original query
// needed (A2 step 2): the same hit set covers a two-hop ground truth only
// when n reaches 2.
func TestEvaluateNeighborhoodHonorsBaselineRounds(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha seed")
	seedGraphNode(t, store, 1, "n2", "middle")
	seedGraphNode(t, store, 1, "n3", "deep target")
	if err := store.SaveEdges(1, []*Edge{
		{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "n1", To: "n2", CreatedBy: CreatorIngester, CreatedVersion: 1},
		{EdgeID: "h2", Type: EdgeTypeSummarizes, From: "n2", To: "n3", CreatedBy: CreatorIngester, CreatedVersion: 1},
	}, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	cand, err := store.CreateVersionFrom(1, "ttt") // unchanged copy
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}

	// Retrieval hits only n1; n3 is two hops away over the hierarchy.
	mk := func(rounds int) *BacktestQuery {
		return &BacktestQuery{
			Query: "alpha", RelevantNodes: []string{"n3"},
			BaselineRounds: rounds, BaselineFound: true,
		}
	}
	// No runner wired: isolate the coverage check.
	bt := NewBacktester(store, BacktestConfig{})

	stats := bt.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{mk(1), mk(2)})
	one, two := stats.Queries[0], stats.Queries[1]
	if one.Covered || one.Found {
		t.Fatalf("n=1 stat = %+v, want uncovered miss (n3 two hops from hit)", one)
	}
	if !two.Covered || !two.Found || two.Rounds != 2 {
		t.Fatalf("n=2 stat = %+v, want covered pass with rounds estimate 2", two)
	}
}

// A covered query runs the full explore backtest when a runner is wired
// (A2 step 4); rounds within baseline+tolerance are not a regression.
func TestEvaluateCoveredQueryRunsFullBacktest(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}

	q := &BacktestQuery{
		TraceID: "t1", Query: "alpha", RelevantNodes: []string{"n1"},
		BaselineRounds: 3, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9,
	}
	runner := &fakeFullBacktestRunner{t: t, rounds: 4, found: true} // baseline 3 + tolerance 1
	bt := NewBacktester(store, BacktestConfig{Runner: mustScopedBacktestRunner(t, runner)})

	stats := bt.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{q})
	if !stats.Passed {
		t.Fatalf("candidate failed gates: %v", stats.GateFailures)
	}
	qs := stats.Queries[0]
	if !qs.Covered || !qs.RequiresFullBacktest || !qs.FullBacktestRan || qs.AcceptedWithoutExplore {
		t.Fatalf("query stat = %+v, want full backtest executed", qs)
	}
	if !qs.Found || qs.Rounds != 4 {
		t.Fatalf("query stat = %+v, want found with 4 rounds", qs)
	}
	if qs.Regressed {
		t.Fatalf("query stat = %+v, want not regressed (rounds within tolerance)", qs)
	}
	if runner.callCount() != 1 {
		t.Fatalf("runner calls = %d, want 1", runner.callCount())
	}
	if stats.MeanRounds != 4 || stats.P95Rounds != 4 {
		t.Fatalf("mean/p95 = %v/%v, want 4/4", stats.MeanRounds, stats.P95Rounds)
	}
	// Cost diff vs parent: the candidate is an unchanged copy.
	if stats.ChangedNodes != 0 || stats.EdgeChurn != 0 || stats.EmbedBytes != 0 {
		t.Fatalf("cost diff = nodes %d churn %d embed %d, want 0/0/0",
			stats.ChangedNodes, stats.EdgeChurn, stats.EmbedBytes)
	}
}

// Rounds overflow (A2): full-backtest rounds beyond baseline + tolerance
// mark the query regressed for audit, but a window query never fails a gate
// on rounds alone — recall and mean/p95 are the only rounds-sensitive
// signals.
func TestEvaluateRoundsOverflowRegresses(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}

	window := &BacktestQuery{
		TraceID: "t1", Query: "alpha", RelevantNodes: []string{"n1"},
		BaselineRounds: 2, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9,
	}
	// 5 rounds > baseline 2 + tolerance 1.
	runner := &fakeFullBacktestRunner{t: t, rounds: 5, found: true}
	bt := NewBacktester(store, BacktestConfig{Runner: mustScopedBacktestRunner(t, runner)})

	stats := bt.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{window})
	if !stats.Passed {
		t.Fatalf("candidate failed gates on a window-query rounds overflow: %v", stats.GateFailures)
	}
	qs := stats.Queries[0]
	if !qs.Regressed {
		t.Fatalf("query stat = %+v, want regressed (rounds overflow)", qs)
	}
	if !qs.Found || qs.Rounds != 5 {
		t.Fatalf("query stat = %+v, want found with 5 rounds (recall intact)", qs)
	}
	// The recall gate is unaffected by rounds overflow: the query still
	// passes candidate-side.
	if stats.Recall != 1.0 || stats.BaselineRecall != 1.0 {
		t.Fatalf("recall = %v baseline = %v, want 1/1", stats.Recall, stats.BaselineRecall)
	}
}

// Cold start (spec §7): a window thinner than ColdStartThreshold makes the
// recorded baselines untrustworthy, so the recall gate is skipped and rounds
// overflow no longer marks a regression. Miss regressions stay recorded (an
// honest observation), and the structural gates still run.
func TestEvaluateColdStartDegradesStatisticalGates(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	seedGraphNode(t, store, 1, "n2", "gamma delta")
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	// Retrieval regression on the alpha query: n1 no longer matches.
	seedGraphNode(t, store, cand, "n1", "zzz qqq")

	qAlpha := &BacktestQuery{
		TraceID: "t1", Query: "alpha", RelevantNodes: []string{"n1"},
		BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9,
	}
	qGamma := &BacktestQuery{
		TraceID: "t2", Query: "gamma", RelevantNodes: []string{"n2"},
		BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9,
	}
	// 5 rounds > baseline 1 + tolerance 1 on the covered gamma query.
	runner := &fakeFullBacktestRunner{t: t, rounds: 5, found: true}

	// Without cold start the same setup fails the recall gate (0.5 < 1.0 -
	// 0.02) and marks the gamma rounds overflow.
	warm := NewBacktester(store, BacktestConfig{Runner: mustScopedBacktestRunner(t, runner)})
	stats := warm.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{qAlpha, qGamma})
	if stats.Passed {
		t.Fatalf("warm candidate passed despite recall 0.5: %v", stats.GateFailures)
	}
	gammaWarm := stats.Queries[1]
	if !gammaWarm.Regressed {
		t.Fatalf("warm gamma stat = %+v, want rounds-overflow regression", gammaWarm)
	}

	// Cold start: the recall gate is skipped and the rounds overflow is not
	// held against the candidate; the alpha miss stays marked regressed.
	cold := NewBacktester(store, BacktestConfig{Runner: mustScopedBacktestRunner(t, runner), ColdStart: true})
	stats = cold.EvaluateCandidate(context.Background(), cand, 1, []*BacktestQuery{qAlpha, qGamma})
	if !stats.Passed {
		t.Fatalf("cold-start candidate failed statistical gates: %v", stats.GateFailures)
	}
	qAlphaStat, qGammaStat := stats.Queries[0], stats.Queries[1]
	if !qAlphaStat.Regressed {
		t.Fatalf("cold alpha stat = %+v, want the miss still marked regressed", qAlphaStat)
	}
	if qGammaStat.Regressed {
		t.Fatalf("cold gamma stat = %+v, want rounds overflow suppressed under cold start", qGammaStat)
	}
	if stats.Recall != 0.5 || stats.BaselineRecall != 1.0 {
		t.Fatalf("cold recall = %v baseline = %v, want the honest 0.5/1.0 (reported, not gated)", stats.Recall, stats.BaselineRecall)
	}
}

func TestEvaluateRecallToleranceBoundary(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	// Candidate is an unchanged copy: "alpha" retrieves n1 (covered), the
	// "nomatch" queries retrieve nothing and miss outright.

	// Recall values are exact in binary: 4 queries, baseline recall 1.0,
	// tolerance 0.25 -> the gate is recall >= 0.75.
	mkQueries := func(hits, misses int) []*BacktestQuery {
		var qs []*BacktestQuery
		for i := 0; i < hits; i++ {
			qs = append(qs, &BacktestQuery{Query: "alpha", RelevantNodes: []string{"n1"}, BaselineRounds: 1, BaselineFound: true})
		}
		for i := 0; i < misses; i++ {
			qs = append(qs, &BacktestQuery{Query: "nomatch", RelevantNodes: []string{"n1"}, BaselineRounds: 1, BaselineFound: true})
		}
		return qs
	}

	bt := NewBacktester(store, BacktestConfig{RecallTolerance: 0.25})

	// Exactly at the boundary: 3/4 = 0.75 >= 1.0 - 0.25 -> passes.
	stats := bt.EvaluateCandidate(context.Background(), cand, 1, mkQueries(3, 1))
	if stats.Recall != 0.75 || stats.BaselineRecall != 1.0 {
		t.Fatalf("recall = %v baseline = %v, want 0.75/1.0", stats.Recall, stats.BaselineRecall)
	}
	if !stats.Passed {
		t.Fatalf("boundary recall 0.75 with tolerance 0.25 must pass: %v", stats.GateFailures)
	}

	// One more miss drops below the boundary: 0.5 < 0.75 -> rejected.
	stats = bt.EvaluateCandidate(context.Background(), cand, 1, mkQueries(2, 2))
	if stats.Passed {
		t.Fatalf("recall 0.5 with tolerance 0.25 must fail")
	}
	if !strings.Contains(strings.Join(stats.GateFailures, ";"), "recall") {
		t.Fatalf("gate failures = %v, want a recall failure", stats.GateFailures)
	}
}

func TestEvaluateValidateGateRejectsCorruptCandidate(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha beta")
	cand, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	// Corrupt the candidate on disk (bypassing the safe applier): a
	// hierarchy edge pointing at a nonexistent node.
	if err := store.SaveEdges(cand, []*Edge{
		{EdgeID: "hx", Type: EdgeTypeSummarizes, From: "n1", To: "ghost", CreatedBy: "ttt-0", CreatedVersion: cand},
	}, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}

	bt := NewBacktester(store, BacktestConfig{})
	stats := bt.EvaluateCandidate(context.Background(), cand, 1, nil)
	if stats.Passed {
		t.Fatalf("corrupt candidate passed the validate gate")
	}
	if !strings.Contains(strings.Join(stats.GateFailures, ";"), "validate") {
		t.Fatalf("gate failures = %v, want a validate failure", stats.GateFailures)
	}
}

// ---------------------------------------------------------------------------
// selection & statistics helpers
// ---------------------------------------------------------------------------

func TestSelectWinnerMinCost(t *testing.T) {
	w := DefaultConsolidateConfig().CostWeights

	// Two survivors: the lower-rounds candidate wins.
	cands := []CandidateStats{
		{Version: 2, Passed: true, MeanRounds: 1, P95Rounds: 2, EmbedBytes: 100, ChangedNodes: 1},
		{Version: 3, Passed: true, MeanRounds: 5, P95Rounds: 9, EmbedBytes: 500, ChangedNodes: 3, EdgeChurn: 2},
	}
	if got := SelectWinner(cands, w); got != 0 {
		t.Fatalf("SelectWinner = %d, want 0", got)
	}
	// The loser carries every norm at 1; the winner's norms are all 0.
	wantLoser := w.Round*5 + w.Tail*9 + w.Embed + w.Node + w.Graph
	if cands[1].Cost != wantLoser {
		t.Fatalf("loser cost = %v, want %v", cands[1].Cost, wantLoser)
	}
	if cands[0].Cost != w.Round*1+w.Tail*2 {
		t.Fatalf("winner cost = %v, want raw rounds terms only", cands[0].Cost)
	}

	// Single survivor: zero norm vector, cost is the raw rounds terms.
	single := []CandidateStats{
		{Version: 2, Passed: false},
		{Version: 3, Passed: true, MeanRounds: 4, P95Rounds: 6, EmbedBytes: 900, ChangedNodes: 7, EdgeChurn: 5},
	}
	if got := SelectWinner(single, w); got != 1 {
		t.Fatalf("SelectWinner single = %d, want 1", got)
	}
	if single[1].Cost != w.Round*4+w.Tail*6 {
		t.Fatalf("single survivor cost = %v, want %v", single[1].Cost, w.Round*4+w.Tail*6)
	}

	// No survivors.
	if got := SelectWinner([]CandidateStats{{Version: 2}}, w); got != -1 {
		t.Fatalf("SelectWinner none = %d, want -1", got)
	}

	// Ties break toward the lowest version.
	tied := []CandidateStats{
		{Version: 5, Passed: true, MeanRounds: 1},
		{Version: 3, Passed: true, MeanRounds: 1},
	}
	if got := SelectWinner(tied, w); got != 1 {
		t.Fatalf("SelectWinner tie = %d, want index 1 (version 3)", got)
	}
}

func TestPercentileLinearInterpolation(t *testing.T) {
	// Linear interpolation between closest ranks (numpy "linear" method):
	// rank = p/100*(n-1); result = xs[floor] + frac*(xs[ceil]-xs[floor]).
	xs := []float64{1, 2, 3, 4}
	cases := []struct {
		p    float64
		want float64
	}{
		{0, 1},
		{50, 2.5},
		{95, 3.85}, // rank 2.85 -> 3 + 0.85*(4-3); interpolation is not exact in binary
		{100, 4},
	}
	for _, tc := range cases {
		if got := percentile(xs, tc.p); math.Abs(got-tc.want) > 1e-12 {
			t.Fatalf("percentile(%v, %v) = %v, want %v", xs, tc.p, got, tc.want)
		}
	}
	if got := percentile([]float64{7}, 95); got != 7 {
		t.Fatalf("single value percentile = %v, want 7", got)
	}
	if got := percentile(nil, 95); got != 0 {
		t.Fatalf("empty percentile = %v, want 0", got)
	}
	// Unsorted input is handled and not mutated.
	unsorted := []float64{4, 1, 3, 2}
	if got := percentile(unsorted, 50); got != 2.5 {
		t.Fatalf("unsorted percentile = %v, want 2.5", got)
	}
	if unsorted[0] != 4 || unsorted[1] != 1 {
		t.Fatalf("percentile mutated its input: %v", unsorted)
	}
}

// ---------------------------------------------------------------------------
// ComputeBaselineCoverage (R10: judge-time baseline on the current version)
// ---------------------------------------------------------------------------

// baselineFixtureStore builds v1 with the hierarchy a -> b -> c, so "alpha"
// retrieves a and c sits exactly 2 hops from the hit set.
func baselineFixtureStore(t *testing.T) (*Store, *Graph, *HybridRetriever) {
	t.Helper()
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "a", "alpha beta routing")
	seedGraphNode(t, store, 1, "b", "gamma delta summary")
	seedGraphNode(t, store, 1, "c", "epsilon zeta leaf")
	if err := store.SaveEdges(1, []*Edge{
		{EdgeID: "h1", Type: EdgeTypeSummarizes, From: "a", To: "b", CreatedBy: CreatorConsolidator, CreatedVersion: 1},
		{EdgeID: "h2", Type: EdgeTypeSummarizes, From: "b", To: "c", CreatedBy: CreatorConsolidator, CreatedVersion: 1},
	}, nil); err != nil {
		t.Fatalf("SaveEdges: %v", err)
	}
	g, err := LoadGraph(store, 1)
	if err != nil {
		t.Fatalf("LoadGraph: %v", err)
	}
	retr := NewHybridRetriever(store, nil, DefaultRetrievalConfig())
	if err := retr.Rebuild(context.Background()); err != nil {
		t.Fatalf("Rebuild: %v", err)
	}
	return store, g, retr
}

// TestComputeBaselineCoverage: the R10 baseline is the A2 machinery applied
// on the current version at judge time — hybrid top-k hits plus the n-hop
// coverage check with n = the adopted path's rounds.
func TestComputeBaselineCoverage(t *testing.T) {
	_, g, retr := baselineFixtureStore(t)
	ctx := context.Background()

	// n=2: c is inside the 2-hop neighborhood of the hit a -> covered.
	sig := ComputeBaselineCoverage(ctx, retr, g, "alpha", []string{"c"}, 2)
	if !sig.Covered {
		t.Fatalf("Covered = false, want true (c within 2 hops of a)")
	}
	if len(sig.TopK) == 0 || sig.TopK[0] != "a" {
		t.Fatalf("TopK = %v, want [a ...]", sig.TopK)
	}

	// n=1: c is 2 hops out -> not covered.
	sig = ComputeBaselineCoverage(ctx, retr, g, "alpha", []string{"c"}, 1)
	if sig.Covered {
		t.Fatalf("Covered = true, want false (c outside the 1-hop neighborhood)")
	}

	// The hit node itself is always inside its own neighborhood.
	sig = ComputeBaselineCoverage(ctx, retr, g, "alpha", []string{"a"}, 1)
	if !sig.Covered {
		t.Fatalf("Covered = false, want true (hit node covers itself)")
	}

	// No ground truth -> not covered; zero rounds normalize to the default.
	sig = ComputeBaselineCoverage(ctx, retr, g, "alpha", nil, 0)
	if sig.Covered {
		t.Fatalf("Covered = true, want false for an empty ground truth set")
	}
}

// ---------------------------------------------------------------------------
// item-semantics backtests (spec §8/§9)
// ---------------------------------------------------------------------------

type fakeBacktestConfirmer struct {
	confirm func(statement string, node *Node) (bool, error)
}

func (f fakeBacktestConfirmer) ConfirmNode(_ context.Context, statement string, node *Node) (bool, error) {
	return f.confirm(statement, node)
}

type queryBacktestRunner struct {
	results map[string]struct {
		rounds int
		found  bool
	}
}

func (r queryBacktestRunner) RunExplore(_ context.Context, _ int, query string) (int, bool, error) {
	result := r.results[query]
	return result.rounds, result.found, nil
}

func itemBacktestStore(t *testing.T, nodes ...struct{ id, body string }) (*Store, int) {
	t.Helper()
	store := newTestStore(t)
	for _, node := range nodes {
		seedGraphNode(t, store, 1, node.id, node.body)
	}
	candidate, err := store.CreateVersionFrom(1, "ttt")
	if err != nil {
		t.Fatalf("CreateVersionFrom: %v", err)
	}
	return store, candidate
}

func TestEvaluateCandidateItemsRequireAllItems(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"one", "alpha"})
	q := &BacktestQuery{Query: "alpha", BaselineRounds: 1, BaselineFound: true, Items: []BacktestItem{
		{ID: "item-one", Statement: "one", NodeIDs: []string{"one"}},
		{ID: "item-two", Statement: "two", NodeIDs: []string{"two"}},
	}}
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	got := stats.Queries[0]
	if got.Covered || got.Found || got.ItemsTotal != 2 || got.ItemsSatisfied != 1 || len(got.ItemMisses) != 1 || got.ItemMisses[0] != "item-two" {
		t.Fatalf("query stat = %+v, want one of two required items satisfied", got)
	}
	if stats.Recall != 0 || stats.Passed {
		t.Fatalf("candidate = %+v, want recall miss", stats)
	}
}

func TestEvaluateCandidateItemEquivalenceIsOR(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"replacement", "alpha"})
	q := &BacktestQuery{Query: "alpha", BaselineRounds: 1, BaselineFound: true, Items: []BacktestItem{{
		ID: "item", Statement: "fact", NodeIDs: []string{"old-a", "replacement", "old-b"},
	}}}
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	got := stats.Queries[0]
	if !got.Covered || !got.Found || got.ItemsTotal != 1 || got.ItemsSatisfied != 1 {
		t.Fatalf("query stat = %+v, want OR-equivalent item covered", got)
	}
}

func TestEvaluateCandidateLegacyRelevantNodesRemainAND(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"a", "alpha"})
	q := &BacktestQuery{Query: "alpha", RelevantNodes: []string{"a", "b"}, BaselineRounds: 1, BaselineFound: true}
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	got := stats.Queries[0]
	if got.Covered || got.ItemsTotal != 2 || got.ItemsSatisfied != 1 {
		t.Fatalf("query stat = %+v, want legacy nodes as AND single-node items", got)
	}
}

func TestEvaluateCandidateSourceOverlapDoesNotSatisfyItem(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"source-match", "alpha"})
	q := &BacktestQuery{Query: "alpha", BaselineRounds: 1, BaselineFound: true, Items: []BacktestItem{{
		ID: "item", Statement: "fact", NodeIDs: []string{"historical"}, SourceRefs: []string{"source-match"},
	}}}
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	if got := stats.Queries[0]; got.Covered || got.ItemsSatisfied != 0 {
		t.Fatalf("query stat = %+v, want source overlap to remain insufficient", got)
	}
}

func TestEvaluateCandidateSemanticConfirmationRecoversReplacement(t *testing.T) {
	store, candidate := itemBacktestStore(t,
		struct{ id, body string }{"error", "alpha bad"},
		struct{ id, body string }{"replacement", "alpha replacement fact"},
	)
	q := &BacktestQuery{Query: "alpha", BaselineRounds: 1, BaselineFound: true, Items: []BacktestItem{{ID: "item", Statement: "replacement fact", NodeIDs: []string{"old"}}}}
	confirmer := fakeBacktestConfirmer{confirm: func(_ string, node *Node) (bool, error) {
		if node.NodeID == "error" {
			return false, fmt.Errorf("transient")
		}
		return node.NodeID == "replacement", nil
	}}
	stats := NewBacktester(store, BacktestConfig{
		Scope: testConsolidateScope(), Confirmer: mustScopedBacktestConfirmer(t, confirmer),
	}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	got := stats.Queries[0]
	if !got.Covered || got.ItemsSatisfied != 1 || got.ConfirmedNodeIDs["item"] != "replacement" {
		t.Fatalf("query stat = %+v, want semantic replacement confirmation", got)
	}
}

func TestEvaluateCandidateNilConfirmerFailsClosed(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"replacement", "alpha replacement fact"})
	q := &BacktestQuery{Query: "alpha", BaselineRounds: 1, BaselineFound: true, Items: []BacktestItem{{ID: "item", Statement: "replacement fact", NodeIDs: []string{"old"}}}}
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	if got := stats.Queries[0]; got.Covered || got.ItemsSatisfied != 0 {
		t.Fatalf("query stat = %+v, want missing confirmer fail-closed", got)
	}
}

func TestEvaluateCandidateEmptyCohortFailsGroundTruthGate(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"n1", "alpha"})
	stats := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, nil)
	if stats.Passed || stats.Recall == 1 || !strings.Contains(strings.Join(stats.GateFailures, ";"), "no_eligible_backtest_ground_truth") {
		t.Fatalf("candidate = %+v, want unavailable-ground-truth gate", stats)
	}
}

func TestEvaluateCandidateRequireFullBacktest(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"n1", "alpha"})
	q := &BacktestQuery{Query: "alpha", RelevantNodes: []string{"n1"}, BaselineRounds: 1, BaselineFound: true}
	required := NewBacktester(store, BacktestConfig{RequireFullBacktest: true}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	if required.Passed || !strings.Contains(strings.Join(required.GateFailures, ";"), "full_backtest_runner_required") {
		t.Fatalf("required runner candidate = %+v", required)
	}
	optional := NewBacktester(store, BacktestConfig{}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	if !optional.Passed || !optional.Queries[0].AcceptedWithoutExplore {
		t.Fatalf("optional runner candidate = %+v", optional)
	}
}

func TestEvaluateCandidateRoundsIncludeEverySuccessfulRun(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"n1", "alpha one two three"})
	queries := []*BacktestQuery{
		{Query: "one", RelevantNodes: []string{"n1"}, BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.1},
		{Query: "two", RelevantNodes: []string{"n1"}, BaselineRounds: 1, BaselineFound: false},
		{Query: "three", RelevantNodes: []string{"n1"}, BaselineRounds: 10, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9},
	}
	runner := queryBacktestRunner{results: map[string]struct {
		rounds int
		found  bool
	}{
		"one": {rounds: 3, found: true}, "two": {rounds: 5, found: false}, "three": {rounds: 9, found: true},
	}}
	stats := NewBacktester(store, BacktestConfig{Runner: mustScopedBacktestRunner(t, runner)}).EvaluateCandidate(context.Background(), candidate, 1, queries)
	if stats.MeanRounds != float64(17)/3 || math.Abs(stats.P95Rounds-8.6) > 1e-9 {
		t.Fatalf("mean/p95 = %v/%v, want 17/3 and 8.6", stats.MeanRounds, stats.P95Rounds)
	}
}

func TestBacktestQueriesDeduplicatesWindowTraceID(t *testing.T) {
	store := newTestStore(t)
	seedGraphNode(t, store, 1, "n1", "alpha")
	for _, window := range []string{"first", "second"} {
		if err := store.AppendQueryLog(window, &QueryLogEntry{TraceID: "trace", Query: window, Version: 1, Found: true, Rounds: 1, JudgeDone: true, RelevantNodes: []string{"n1"}}); err != nil {
			t.Fatal(err)
		}
	}
	queries, err := BacktestQueries(store, 1)
	if err != nil || len(queries) != 1 || queries[0].Query != "first" {
		t.Fatalf("BacktestQueries = %+v, %v; want first trace occurrence once", queries, err)
	}
}

type recordingBacktestConfirmer struct {
	calls int
}

func (f *recordingBacktestConfirmer) ConfirmNode(_ context.Context, _ string, _ *Node) (bool, error) {
	f.calls++
	return true, nil
}

func TestEvaluateCandidateRejectsMismatchedConfirmerScopeBeforeUse(t *testing.T) {
	store, candidate := itemBacktestStore(t, struct{ id, body string }{"replacement", "alpha replacement fact"})
	q := &BacktestQuery{Query: "alpha", BaselineRounds: 1, BaselineFound: true, Items: []BacktestItem{{
		ID: "item", Statement: "replacement fact", NodeIDs: []string{"old"},
	}}}
	runner := &fakeFullBacktestRunner{rounds: 1, found: true}
	confirmer := &recordingBacktestConfirmer{}
	mismatched := testConsolidateScope()
	mismatched.WorkspaceID = "other-workspace"
	scopedConfirmer, err := NewScopedBacktestConfirmer(confirmer, mismatched)
	if err != nil {
		t.Fatalf("NewScopedBacktestConfirmer: %v", err)
	}
	stats := NewBacktester(store, BacktestConfig{
		Runner:    mustScopedBacktestRunner(t, runner),
		Confirmer: scopedConfirmer,
	}).EvaluateCandidate(context.Background(), candidate, 1, []*BacktestQuery{q})
	if stats.Passed || !strings.Contains(strings.Join(stats.GateFailures, ";"), "scope identity") {
		t.Fatalf("candidate = %+v, want confirmer scope identity rejection", stats)
	}
	if confirmer.calls != 0 {
		t.Fatalf("confirmer calls = %d, want 0", confirmer.calls)
	}
	if got := runner.callCount(); got != 0 {
		t.Fatalf("runner calls = %d, want 0", got)
	}
}
