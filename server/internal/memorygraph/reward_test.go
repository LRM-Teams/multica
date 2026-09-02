// SPDX-License-Identifier: Apache-2.0

package memorygraph

import (
	"fmt"
	"slices"
	"testing"
)

// Spec §7, acceptance A7: overall is the minimum of the three dimensions (no
// compensation); reward = overall − w_round × server-counted explore rounds,
// unclamped, with no threshold, pass/fail label, or fixed miss penalty.
// Negative rewards are possible for low-quality/high-cost normal runs.

func TestOverallScoreIsMinDimension(t *testing.T) {
	cases := []struct {
		name  string
		score DiveTrajectoryScore
		want  float64
	}{
		{"relevance binds", DiveTrajectoryScore{Relevance: 0.1, Groundedness: 0.9, Completeness: 0.9}, 0.1},
		{"groundedness binds", DiveTrajectoryScore{Relevance: 0.9, Groundedness: 0.2, Completeness: 0.9}, 0.2},
		{"completeness binds", DiveTrajectoryScore{Relevance: 0.9, Groundedness: 0.9, Completeness: 0.3}, 0.3},
		{"all equal", DiveTrajectoryScore{Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5}, 0.5},
		{"perfect", DiveTrajectoryScore{Relevance: 1, Groundedness: 1, Completeness: 1}, 1},
		{"zero floor", DiveTrajectoryScore{Relevance: 0.8, Groundedness: 0, Completeness: 0.8}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.score.Overall(); got != c.want {
				t.Fatalf("Overall() = %v, want %v (min dimension, no compensation)", got, c.want)
			}
		})
	}
}

func TestExploreRewardFormula(t *testing.T) {
	cases := []struct {
		name    string
		overall float64
		wRound  float64
		rounds  int
		want    float64
	}{
		{"basic", 0.9, 0.1, 3, 0.6},
		{"zero rounds", 0.7, 0.1, 0, 0.7},
		{"zero weight", 0.7, 0, 9, 0.7},
		// A low-quality/high-cost normal run goes negative; the reward is
		// never clamped (A7).
		{"negative allowed", 0.2, 0.1, 5, -0.3},
		{"deeply negative", 0.0, 0.25, 8, -2.0},
		// A miss run is graded by the same formula: no fixed miss penalty.
		{"miss uses same formula", 0.4, 0.1, 2, 0.2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExploreReward(c.overall, c.wRound, c.rounds)
			if got < c.want-1e-9 || got > c.want+1e-9 {
				t.Fatalf("ExploreReward(%v, %v, %d) = %v, want %v", c.overall, c.wRound, c.rounds, got, c.want)
			}
		})
	}
}

// Task 19 (spec §14.2): the explore agent's own budget violation receives a
// deterministic negative reward — never a numeric judge grade and never a
// neutral zero — computed from the server-counted rounds it burned.

func TestDeterministicViolationRewardIsNegativeAndDeterministic(t *testing.T) {
	first := DeterministicViolationReward(0.1, 3)
	again := DeterministicViolationReward(0.1, 3)
	if first != again {
		t.Fatalf("DeterministicViolationReward not deterministic: %v vs %v", first, again)
	}
	if first >= 0 {
		t.Fatalf("DeterministicViolationReward = %v, want negative", first)
	}
	// The violation must rank strictly below a graded trajectory that used
	// the same rounds (zero quality minus the same round cost, minus one).
	if got, want := first, ExploreReward(0, 0.1, 3)-1; got != want {
		t.Fatalf("DeterministicViolationReward(0.1, 3) = %v, want %v", got, want)
	}
	// Zero recorded rounds still burn at least one round: still negative.
	if got := DeterministicViolationReward(0.1, 0); got >= 0 {
		t.Fatalf("DeterministicViolationReward(0.1, 0) = %v, want negative", got)
	}
	// More burned rounds (or a higher round weight) must never improve it.
	if DeterministicViolationReward(0.2, 5) >= DeterministicViolationReward(0.1, 3) {
		t.Fatal("burning more rounds at a higher weight must not improve the violation reward")
	}
}

// Task 19 (spec §14.3): the holdout/safety partition is stable and auditable
// — a query's class depends only on its trace id, never on the batch.

func TestQueryPartitionStableAndAuditable(t *testing.T) {
	for _, trace := range []string{"t-1", "trace-alpha", "query-42"} {
		first := QueryPartition(trace)
		for i := 0; i < 5; i++ {
			if got := QueryPartition(trace); got != first {
				t.Fatalf("QueryPartition(%q) unstable: %v vs %v", trace, got, first)
			}
		}
		if !slices.Contains([]string{"evaluation", "holdout", "safety"}, first) {
			t.Fatalf("QueryPartition(%q) = %q, want a documented class", trace, first)
		}
	}
	// A spread of trace ids populates all three classes (60/20/20 bootstrap).
	classes := map[string]int{}
	for i := 0; i < 300; i++ {
		classes[QueryPartition(fmt.Sprintf("trace-%03d", i))]++
	}
	for _, class := range []string{"evaluation", "holdout", "safety"} {
		if classes[class] == 0 {
			t.Fatalf("partition class %q never selected in 300 traces: %v", class, classes)
		}
	}
	if classes["holdout"] > classes["evaluation"] {
		t.Fatalf("holdout (%d) larger than evaluation (%d); bootstrap split is 60/20/20",
			classes["holdout"], classes["evaluation"])
	}
}

// Task 19 (spec §14.3): the consolidation reward is a baseline delta over
// ABSOLUTE costs — the same candidate stats produce the same reward whether
// evaluated alone or beside other candidates (never batch-relative min-max).

func TestConsolidationRewardBaselineDeltaAndAbsoluteCosts(t *testing.T) {
	w := DefaultConsolidationRewardWeights()
	base := ConsolidationRewardInput{
		Passed: true, Recall: 0.8, BaselineRecall: 0.7,
		Coverage: 0.75, BaselineCoverage: 0.75,
		Queries: 10, Regressions: 1,
		CandidateRounds: 3, BaselineRounds: 4,
		EmbedBytes: 2048, ChangedNodes: 5, EdgeChurn: 2,
	}
	value, components := ConsolidationReward(base, w)
	// quality_delta = recall_delta + coverage_delta − regression_penalty.
	wantRecallDelta := 0.8 - 0.7
	wantCoverageDelta := 0.75 - 0.75
	wantRegression := 1.0 / 10.0
	if components.RecallDelta < wantRecallDelta-1e-9 || components.RecallDelta > wantRecallDelta+1e-9 {
		t.Fatalf("recall delta = %v, want %v", components.RecallDelta, wantRecallDelta)
	}
	if components.CoverageDelta != wantCoverageDelta {
		t.Fatalf("coverage delta = %v, want %v", components.CoverageDelta, wantCoverageDelta)
	}
	if components.RegressionPenalty < wantRegression-1e-9 || components.RegressionPenalty > wantRegression+1e-9 {
		t.Fatalf("regression penalty = %v, want %v", components.RegressionPenalty, wantRegression)
	}
	// Efficiency subtracts ABSOLUTE costs: candidate rounds saved minus the
	// raw per-candidate embedding/node/edge cost (documented KiB units).
	wantEfficiency := (4 - 3) - float64(2048)/1024 - 5 - 2
	if components.EfficiencyDelta < wantEfficiency-1e-9 || components.EfficiencyDelta > wantEfficiency+1e-9 {
		t.Fatalf("efficiency delta = %v, want %v (absolute costs)", components.EfficiencyDelta, wantEfficiency)
	}
	wantQuality := wantRecallDelta + wantCoverageDelta - wantRegression
	wantValue := w.Quality*wantQuality + w.Efficiency*wantEfficiency
	if value < wantValue-1e-9 || value > wantValue+1e-9 {
		t.Fatalf("reward = %v, want %v", value, wantValue)
	}
	// Batch independence: identical stats evaluated twice with a different
	// batch context produce the identical reward (no min-max normalization).
	alone, _ := ConsolidationReward(base, w)
	costlier := base
	costlier.EmbedBytes, costlier.ChangedNodes, costlier.EdgeChurn = 100000, 900, 800
	_, _ = ConsolidationReward(costlier, w) // evaluated "beside" base
	if beside, _ := ConsolidationReward(base, w); beside != alone {
		t.Fatalf("batch-relative leak: base reward changed from %v to %v", alone, beside)
	}
	// Higher absolute cost with equal quality strictly lowers the reward.
	pricier := base
	pricier.ChangedNodes = 50
	if pricierValue, _ := ConsolidationReward(pricier, w); pricierValue >= value {
		t.Fatalf("pricier candidate reward %v not below %v", pricierValue, value)
	}
}

func TestConsolidationRewardHardGateFailureIsDeterministicNegative(t *testing.T) {
	w := DefaultConsolidationRewardWeights()
	input := ConsolidationRewardInput{
		Passed: false, GateFailures: []string{"recall_below_baseline"},
		Recall: 0.9, BaselineRecall: 0.1, // tempting stats must be ignored
		Coverage: 1, BaselineCoverage: 0,
		CandidateRounds: 1, BaselineRounds: 9,
	}
	value, components := ConsolidationReward(input, w)
	if value != HardGateFailureReward {
		t.Fatalf("gate-failed candidate reward = %v, want fixed %v", value, HardGateFailureReward)
	}
	if HardGateFailureReward >= 0 {
		t.Fatalf("HardGateFailureReward = %v, want negative", HardGateFailureReward)
	}
	if components.Source != "hard_gate_failure" {
		t.Fatalf("components source = %q, want hard_gate_failure", components.Source)
	}
	again, _ := ConsolidationReward(input, w)
	if again != value {
		t.Fatalf("gate failure reward not deterministic: %v vs %v", again, value)
	}
}

func TestConsolidationRewardWinnerIdentityIsNotReward(t *testing.T) {
	w := DefaultConsolidationRewardWeights()
	// Winner candidate: cheapest (would win SelectWinner) but weaker quality.
	winner := ConsolidationRewardInput{
		Passed: true, Recall: 0.70, BaselineRecall: 0.70,
		Coverage: 0.7, BaselineCoverage: 0.7,
		Queries: 10, Regressions: 0,
		CandidateRounds: 2, BaselineRounds: 2,
		EmbedBytes: 1024, ChangedNodes: 1, EdgeChurn: 0,
	}
	// Loser candidate: more expensive (loses the cost comparison) but a
	// real quality gain over its baseline.
	loser := ConsolidationRewardInput{
		Passed: true, Recall: 0.95, BaselineRecall: 0.70,
		Coverage: 0.95, BaselineCoverage: 0.70,
		Queries: 10, Regressions: 0,
		CandidateRounds: 4, BaselineRounds: 2,
		EmbedBytes: 4096, ChangedNodes: 30, EdgeChurn: 10,
	}
	winnerValue, _ := ConsolidationReward(winner, w)
	loserValue, _ := ConsolidationReward(loser, w)
	if loserValue <= winnerValue {
		t.Fatalf("winner identity must not dominate reward: winner=%v loser=%v", winnerValue, loserValue)
	}
}

// TestConsolidationRewardInputFromStatsReadsEvaluationPartitionOnly: the
// reward input aggregates over the evaluation partition only — holdout and
// safety queries never move a reward — and skipped rows contribute no
// outcome signal (spec §14.3/§14.4, Task 19 wiring).
func TestConsolidationRewardInputFromStatsReadsEvaluationPartitionOnly(t *testing.T) {
	stats := CandidateStats{
		Passed: true,
		Queries: []QueryBacktestStat{
			// Evaluation: found, covered, one regression, 4 vs 6 rounds.
			{TraceID: "e1", Partition: "evaluation", Found: true, Covered: true, BaselineFound: true, Regressed: true, Rounds: 4, BaselineRounds: 6},
			{TraceID: "e2", Partition: "evaluation", Found: false, Covered: false, BaselineFound: true, Rounds: 8, BaselineRounds: 5},
			// Skipped evaluation row: budget-audit only, no signal.
			{TraceID: "e3", Partition: "evaluation", Skipped: true, Found: true, Covered: true, Rounds: 100, BaselineRounds: 100},
			// Holdout/safety: audited, never reward inputs.
			{TraceID: "h1", Partition: "holdout", Found: true, Covered: true, Regressed: true, Rounds: 1, BaselineRounds: 9},
			{TraceID: "s1", Partition: "safety", Found: true, Covered: true, Regressed: true, Rounds: 1, BaselineRounds: 9},
		},
		ChangedNodes: 3, EmbedBytes: 1024, EdgeChurn: 1,
	}
	in := ConsolidationRewardInputFromStats(&stats)
	if in.EvaluationQueries != 3 || in.HoldoutQueries != 1 || in.SafetyQueries != 1 {
		t.Fatalf("partition counts = %d/%d/%d, want 3/1/1", in.EvaluationQueries, in.HoldoutQueries, in.SafetyQueries)
	}
	// Found 1 of 2 signal rows; baseline found 2 of 2; coverage 1 of 2.
	if in.Recall < 0.5-1e-9 || in.Recall > 0.5+1e-9 {
		t.Fatalf("evaluation recall = %v, want 0.5", in.Recall)
	}
	if in.BaselineRecall < 1-1e-9 || in.BaselineRecall > 1+1e-9 {
		t.Fatalf("baseline recall = %v, want 1", in.BaselineRecall)
	}
	if in.Coverage < 0.5-1e-9 || in.Coverage > 0.5+1e-9 {
		t.Fatalf("coverage = %v, want 0.5", in.Coverage)
	}
	if in.Regressions != 1 {
		t.Fatalf("regressions = %d, want 1", in.Regressions)
	}
	// Rounds average the two signal rows only: candidate (4+8)/2, baseline (6+5)/2.
	if in.CandidateRounds < 6-1e-9 || in.CandidateRounds > 6+1e-9 {
		t.Fatalf("candidate rounds = %v, want 6", in.CandidateRounds)
	}
	if in.BaselineRounds < 5.5-1e-9 || in.BaselineRounds > 5.5+1e-9 {
		t.Fatalf("baseline rounds = %v, want 5.5", in.BaselineRounds)
	}
	// Absolute costs ride along unchanged.
	if in.EmbedBytes != 1024 || in.ChangedNodes != 3 || in.EdgeChurn != 1 {
		t.Fatalf("absolute costs = %d/%d/%d, want 1024/3/1", in.EmbedBytes, in.ChangedNodes, in.EdgeChurn)
	}
	// Partition counts reach the audited components (spec §14.4).
	_, components := ConsolidationReward(in, DefaultConsolidationRewardWeights())
	if components.EvaluationQueries != 3 || components.HoldoutQueries != 1 || components.SafetyQueries != 1 {
		t.Fatalf("component partition counts = %d/%d/%d, want 3/1/1",
			components.EvaluationQueries, components.HoldoutQueries, components.SafetyQueries)
	}
	// A holdout-only record has no reward signal: all deltas zero.
	holdoutOnly := CandidateStats{Queries: []QueryBacktestStat{
		{TraceID: "h1", Partition: "holdout", Found: true, Regressed: true, Rounds: 9, BaselineRounds: 1},
	}}
	in2 := ConsolidationRewardInputFromStats(&holdoutOnly)
	if in2.EvaluationQueries != 0 || in2.HoldoutQueries != 1 || in2.Recall != 0 || in2.Regressions != 0 {
		t.Fatalf("holdout-only input = %+v, want zero evaluation signal", in2)
	}
}

// TestConsolidationRewardInputFromStatsColdWindowKeepsAbsoluteCosts: with no
// evaluation queries (cold window, no runner) the deltas are zero and the
// reward reduces to the absolute efficiency brake; a gate-failed candidate
// short-circuits to the deterministic negative regardless of stats.
func TestConsolidationRewardInputFromStatsColdWindowKeepsAbsoluteCosts(t *testing.T) {
	w := DefaultConsolidationRewardWeights()
	// A cold window runs no backtest: no queries, no measured rounds.
	cold := CandidateStats{Passed: true, ChangedNodes: 1, EmbedBytes: 512, EdgeChurn: 1}
	value, components := ConsolidationReward(ConsolidationRewardInputFromStats(&cold), w)
	wantEfficiency := -float64(512)/1024 - 1 - 1 // rounds delta zero on a cold window
	if components.EfficiencyDelta < wantEfficiency-1e-9 || components.EfficiencyDelta > wantEfficiency+1e-9 {
		t.Fatalf("cold efficiency delta = %v, want %v", components.EfficiencyDelta, wantEfficiency)
	}
	if value >= 0 {
		t.Fatalf("cold-window reward = %v, want strictly negative absolute-cost brake", value)
	}
	failed := CandidateStats{Passed: false, GateFailures: []string{"recall_regression"}, Recall: 0.9}
	failedValue, failedComponents := ConsolidationReward(ConsolidationRewardInputFromStats(&failed), w)
	if failedValue != HardGateFailureReward || failedComponents.Source != "hard_gate_failure" {
		t.Fatalf("gate-failed reward = %v (%q), want %v (hard_gate_failure)",
			failedValue, failedComponents.Source, HardGateFailureReward)
	}
}
