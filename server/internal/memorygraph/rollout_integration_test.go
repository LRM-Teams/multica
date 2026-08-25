package memorygraph

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestGraphMemoryRolloutWipeColdStartAndShadowBacktest exercises the isolated
// workspace rollout sequence: generation-1 data is wiped, cold-start gates
// degrade then recover, and a no-cutover shadow backtest uses one budget union
// for every candidate while recording unmeasured queries honestly.
func TestGraphMemoryRolloutWipeColdStartAndShadowBacktest(t *testing.T) {
	ctx := context.Background()
	workspaceID := "11111111-1111-1111-1111-111111111111"
	projectID := "22222222-2222-2222-2222-222222222222"
	dir, err := EnsureScopedDir(t.TempDir(), workspaceID, GraphDirKindProject, projectID)
	if err != nil {
		t.Fatalf("EnsureScopedDir: %v", err)
	}
	store := NewStore(dir)
	if err := store.Init(); err != nil {
		t.Fatalf("initial Init: %v", err)
	}

	// 1. Seed a generation-1 store, then ensure the protocol migration starts
	// from an empty generation-2 graph without changing the scoped identity.
	seedGraphNode(t, store, 1, "legacy", "legacy graph data")
	if err := store.AppendQueryLog("legacy", &QueryLogEntry{TraceID: "legacy", Query: "legacy query", Version: 1}); err != nil {
		t.Fatalf("AppendQueryLog legacy: %v", err)
	}
	legacyRegressionSet := filepath.Join(dir, "regression_set.jsonl")
	if err := os.WriteFile(legacyRegressionSet, []byte("{\"query\":\"legacy\"}\n"), 0o644); err != nil {
		t.Fatalf("write legacy regression set: %v", err)
	}
	if err := os.WriteFile(store.protocolFile(), []byte("1"), 0o644); err != nil {
		t.Fatalf("write generation-1 protocol marker: %v", err)
	}
	if err := store.Init(); err != nil {
		t.Fatalf("Init generation-1 store: %v", err)
	}
	if err := VerifyGraphIdentity(dir, GraphIdentity{WorkspaceID: workspaceID, Kind: string(GraphDirKindProject), OwnerID: projectID}); err != nil {
		t.Fatalf("identity did not survive protocol wipe: %v", err)
	}
	marker, err := os.ReadFile(store.protocolFile())
	if err != nil || string(marker) != strconv.Itoa(GraphProtocolGeneration) {
		t.Fatalf("protocol marker = %q, %v; want generation %d", marker, err, GraphProtocolGeneration)
	}
	g, err := LoadGraph(store, 1)
	if err != nil || len(g.Nodes()) != 0 {
		t.Fatalf("graph after generation wipe = %d nodes, %v; want empty", len(g.Nodes()), err)
	}
	if windows, err := store.ListQueryLogWindows(); err != nil || len(windows) != 0 {
		t.Fatalf("query-log windows after generation wipe = %v, %v; want none", windows, err)
	}
	if _, err := os.Stat(legacyRegressionSet); !os.IsNotExist(err) {
		t.Fatalf("legacy regression set survived generation wipe: %v", err)
	}

	// 2. In cold start, structural gates still run: ttt-0 is valid and covers
	// staging while ttt-1 fails staging coverage. The same ttt-0 candidate has
	// a recall drop and a rounds overflow, both of which are statistical gates
	// suppressed below the threshold.
	seedGraphNode(t, store, 1, "n-alpha", "alpha routing rules")
	seedGraphNode(t, store, 1, "n-gone", "omega cache eviction")
	if err := store.WriteStagingSegment("seg-cold", []byte("cold-start staging")); err != nil {
		t.Fatalf("WriteStagingSegment: %v", err)
	}
	appendLog := func(traceID, query, node string, version int, judged bool) {
		t.Helper()
		entry := &QueryLogEntry{
			TraceID: traceID, Query: query, Timestamp: time.Now().UTC(), Version: version,
			Found: true, Rounds: 1, JudgeDone: judged, RelevantNodes: []string{node},
		}
		if judged {
			entry.JudgeScore = 0.9
		}
		if err := store.AppendQueryLog("w1", entry); err != nil {
			t.Fatalf("AppendQueryLog %s: %v", traceID, err)
		}
	}
	appendLog("cold-alpha", "alpha routing", "n-alpha", 1, true)
	appendLog("cold-omega", "omega cache eviction", "n-gone", 1, true)

	coldBackend := &fakeConsolidateBackend{respond: func(prompt string, _ int) string {
		if strings.Contains(prompt, "trajectory 0 of 2") {
			return consolidateOpsJSON(
				ConsolidateOp{Op: OpDeleteNode, NodeID: "n-gone"},
				ConsolidateOp{Op: OpAddNode, Node: &Node{NodeID: "n-cold", Body: "cold-start staging", SegmentRefs: []string{"seg-cold"}}},
			)
		}
		return consolidateOpsJSON(ConsolidateOp{Op: OpDeleteNode, NodeID: "n-gone"})
	}}
	coldRunner := &fakeFullBacktestRunner{t: t, rounds: 5, found: true}
	coldCfg := DefaultConsolidateConfig()
	coldCfg.TTVTrajectories = 2
	cold := NewConsolidator(store, coldBackend, coldCfg, "test", nil, nil)
	cold.SetRunner(coldRunner)
	coldResult, err := cold.Consolidate(ctx)
	if err != nil {
		t.Fatalf("cold-start Consolidate: %v", err)
	}
	coldByActor := make(map[string]CandidateStats, len(coldResult.Candidates))
	for _, candidate := range coldResult.Candidates {
		coldByActor[candidate.Actor] = candidate
	}
	coldCandidate := coldByActor["ttt-0"]
	if !coldResult.Switched || !coldCandidate.Passed || coldCandidate.Recall != 0.5 || coldCandidate.BaselineRecall != 1.0 {
		t.Fatalf("cold candidate = %+v; want a switched structural-gate survivor with recall 0.5/1.0", coldCandidate)
	}
	if !strings.Contains(strings.Join(coldByActor["ttt-1"].GateFailures, ";"), "seg-cold") {
		t.Fatalf("cold candidate without staging reference did not fail gate 2: %+v", coldByActor["ttt-1"])
	}
	coldAlpha := queryStatByText(t, coldCandidate.Queries, "alpha routing")
	if coldAlpha.Regressed || coldAlpha.Rounds != 5 || !coldAlpha.FullBacktestRan {
		t.Fatalf("cold alpha stat = %+v; want rounds overflow measured but suppressed", coldAlpha)
	}

	// Add 20 entries in the newly adopted version: two judged entries reproduce
	// the recall loss and 18 unjudged entries bring the window to the threshold.
	for _, q := range []struct{ traceID, query, node string }{
		{"warm-alpha", "alpha routing", "n-alpha"},
		{"warm-omega", "omega cache eviction", "n-gone"},
	} {
		appendLog(q.traceID, q.query, q.node, coldResult.WinnerVersion, true)
	}
	for i := 0; i < 18; i++ {
		appendLog(fmt.Sprintf("warm-pending-%d", i), "pending query", "", coldResult.WinnerVersion, false)
	}
	warmBackend := &fakeConsolidateBackend{respond: func(string, int) string {
		return consolidateOpsJSON(ConsolidateOp{Op: OpSubmit})
	}}
	warm := NewConsolidator(store, warmBackend, coldCfg, "test", nil, nil)
	warm.SetRunner(&fakeFullBacktestRunner{t: t, rounds: 5, found: true})
	warmResult, err := warm.Consolidate(ctx)
	if err != nil {
		t.Fatalf("threshold-recovery Consolidate: %v", err)
	}
	if warmResult.Switched {
		t.Fatalf("warm consolidation switched despite recall regression: %+v", warmResult.Candidates)
	}
	for _, candidate := range warmResult.Candidates {
		if candidate.Passed || !strings.Contains(strings.Join(candidate.GateFailures, ";"), "recall") {
			t.Fatalf("warm candidate = %+v; want restored recall-gate failure", candidate)
		}
		warmAlpha := queryStatByText(t, candidate.Queries, "alpha routing")
		if !warmAlpha.Regressed {
			t.Fatalf("warm alpha stat = %+v; want restored rounds-overflow regression", warmAlpha)
		}
	}

	// 3. Build a baseline and two isolated shadow candidates. Their top-1 D_q
	// queries differ, so the measured set is their union; the current pointer
	// remains on the baseline throughout this direct shadow evaluation.
	baseline, err := store.CurrentVersion()
	if err != nil {
		t.Fatalf("CurrentVersion before shadow: %v", err)
	}
	seedGraphNode(t, store, baseline, "n-beta", "beta scheduling queue")
	seedGraphNode(t, store, baseline, "n-gamma", "gamma retention policy")
	alphaCandidate, err := store.CreateVersionFrom(baseline, "shadow-alpha")
	if err != nil {
		t.Fatalf("CreateVersionFrom alpha candidate: %v", err)
	}
	betaCandidate, err := store.CreateVersionFrom(baseline, "shadow-beta")
	if err != nil {
		t.Fatalf("CreateVersionFrom beta candidate: %v", err)
	}
	seedGraphNode(t, store, alphaCandidate, "n-alpha-shadow", "alpha shadow data")
	if err := store.SaveEdges(alphaCandidate, []*Edge{{EdgeID: "h-alpha-shadow", Type: EdgeTypeSummarizes, From: "n-alpha-shadow", To: "n-alpha"}}, nil); err != nil {
		t.Fatalf("SaveEdges alpha candidate: %v", err)
	}
	seedGraphNode(t, store, betaCandidate, "n-beta-shadow", "beta shadow datax")
	if err := store.SaveEdges(betaCandidate, []*Edge{{EdgeID: "h-beta-shadow", Type: EdgeTypeSummarizes, From: "n-beta-shadow", To: "n-beta"}}, nil); err != nil {
		t.Fatalf("SaveEdges beta candidate: %v", err)
	}
	shadowQueries := []*BacktestQuery{
		{TraceID: "shadow-alpha", Query: "alpha routing rules", RelevantNodes: []string{"n-alpha"}, BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9},
		{TraceID: "shadow-beta", Query: "beta scheduling queue", RelevantNodes: []string{"n-beta"}, BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9},
		{TraceID: "shadow-gamma", Query: "gamma retention policy", RelevantNodes: []string{"n-gamma"}, BaselineRounds: 1, BaselineFound: true, JudgeDone: true, JudgeScore: 0.9},
	}
	budget := BacktestBudget{B: 1, Epsilon: 0.2, ColdStartThreshold: 20}
	plan, err := PlanBudget(ctx, store, baseline, []int{alphaCandidate, betaCandidate}, shadowQueries, budget, DefaultRetrievalConfig(), nil, DefaultExploreConfig().MaxRounds, DefaultExploreConfig().MaxExpandPerRound)
	if err != nil {
		t.Fatalf("PlanBudget: %v", err)
	}
	if plan.Full || len(plan.PerCandidate[alphaCandidate].Selected) != 1 || len(plan.PerCandidate[betaCandidate].Selected) != 1 || plan.PerCandidate[alphaCandidate].Selected[0].Query != "alpha routing rules" || plan.PerCandidate[betaCandidate].Selected[0].Query != "beta scheduling queue" {
		t.Fatalf("per-candidate budget plan = %+v; want independent alpha/beta top-1 selections", plan)
	}
	if len(plan.Union) != 2 || plan.Union[0].Query == "gamma retention policy" || plan.Union[1].Query == "gamma retention policy" {
		t.Fatalf("budget union = %+v; want alpha and beta only", plan.Union)
	}

	shadowRunner := &fakeFullBacktestRunner{t: t, roundsFor: func(version int) (int, bool) {
		if version == alphaCandidate {
			return 2, true
		}
		return 4, true
	}}
	backtester := NewBacktester(store, BacktestConfig{Runner: shadowRunner})
	alphaStats := backtester.EvaluateCandidate(ctx, alphaCandidate, baseline, plan.Union)
	betaStats := backtester.EvaluateCandidate(ctx, betaCandidate, baseline, plan.Union)
	if !alphaStats.Passed || !betaStats.Passed || shadowRunner.callCount() != 4 {
		t.Fatalf("shadow measurement = alpha %+v beta %+v calls %d; want both candidates measured on the same two-query union", alphaStats, betaStats, shadowRunner.callCount())
	}
	if !sameQueryStats(alphaStats.Queries, betaStats.Queries) {
		t.Fatalf("shadow candidates measured different query sets: alpha=%+v beta=%+v", alphaStats.Queries, betaStats.Queries)
	}
	if current, err := store.CurrentVersion(); err != nil || current != baseline {
		t.Fatalf("shadow backtest changed current version to %d, %v; want baseline v%d", current, err, baseline)
	}

	// SelectWinner still uses the established weighted Cost formula and
	// min-max normalization. These candidates have identical normalized graph
	// components, leaving only their measured-union mean/P95 rounds (2 vs 4).
	if alphaStats.EmbedBytes != betaStats.EmbedBytes || alphaStats.ChangedNodes != betaStats.ChangedNodes || alphaStats.EdgeChurn != betaStats.EdgeChurn {
		t.Fatalf("shadow cost components differ unexpectedly: alpha=%+v beta=%+v", alphaStats, betaStats)
	}
	weights := DefaultConsolidateConfig().CostWeights
	candidates := []CandidateStats{alphaStats, betaStats}
	winner := SelectWinner(candidates, weights)
	alphaExpected := weights.Round*alphaStats.MeanRounds + weights.Tail*alphaStats.P95Rounds
	betaExpected := weights.Round*betaStats.MeanRounds + weights.Tail*betaStats.P95Rounds
	if winner != 0 || candidates[0].Cost != alphaExpected || candidates[1].Cost != betaExpected {
		t.Fatalf("SelectWinner = %d with costs %v/%v, want alpha and unchanged formula %v/%v", winner, candidates[0].Cost, candidates[1].Cost, alphaExpected, betaExpected)
	}

	// 4. Fold the plan audit in exactly as consolidation does. Gamma was outside
	// every candidate's top-B union, so it is a recorded zero-round skip and
	// cannot affect the already computed measured-only recall or latency stats.
	identities := budgetQueryIdentities(shadowQueries)
	alphaStats.Queries = appendBudgetAudit(alphaStats.Queries, plan.PerCandidate[alphaCandidate], plan.Union, shadowQueries)
	betaStats.Queries = appendBudgetAudit(betaStats.Queries, plan.PerCandidate[betaCandidate], plan.Union, shadowQueries)
	for _, stats := range []CandidateStats{alphaStats, betaStats} {
		skipped := queryStatByText(t, stats.Queries, "gamma retention policy")
		if !skipped.Skipped || skipped.Rounds != 0 || skipped.Found || skipped.FullBacktestRan || skipped.SkipReason == "" || skipped.Dq != plan.PerCandidate[stats.Version].Dq[identities[shadowQueries[2]]] {
			t.Fatalf("honest skip for candidate v%d = %+v", stats.Version, skipped)
		}
	}
	if alphaStats.Recall != 1 || alphaStats.BaselineRecall != 1 || alphaStats.MeanRounds != 2 || alphaStats.P95Rounds != 2 || betaStats.Recall != 1 || betaStats.BaselineRecall != 1 || betaStats.MeanRounds != 4 || betaStats.P95Rounds != 4 {
		t.Fatalf("skipped gamma leaked into measured-only aggregates: alpha=%+v beta=%+v", alphaStats, betaStats)
	}
}

func queryStatByText(t *testing.T, stats []QueryBacktestStat, query string) QueryBacktestStat {
	t.Helper()
	for _, stat := range stats {
		if stat.Query == query {
			return stat
		}
	}
	t.Fatalf("query %q not found in stats: %+v", query, stats)
	return QueryBacktestStat{}
}

func sameQueryStats(a, b []QueryBacktestStat) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[string]bool, len(a))
	for _, stat := range a {
		seen[stat.Query] = true
	}
	for _, stat := range b {
		if !seen[stat.Query] {
			return false
		}
	}
	return true
}
