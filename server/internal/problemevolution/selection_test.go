package problemevolution

import (
	"testing"
	"time"
)

func scoreWith(total float64, hardGatePassed bool) *Score {
	return &Score{
		SchemaVersion:  SchemaVersion,
		Total:          total,
		Scale:          ScaleUnitInterval,
		HardGatePassed: hardGatePassed,
		Dimensions: []ScoreDimension{
			{DimensionID: "correctness", Score: total, Weight: 1, Hard: true},
		},
	}
}

func profileWith(entries ...BehaviorEntry) *BehaviorProfile {
	return &BehaviorProfile{
		SchemaVersion: SchemaVersion,
		Kind:          BehaviorKindDimensionVector,
		Entries:       entries,
	}
}

func TestSelectElitePicksHighestScoreAsBest(t *testing.T) {
	result := SelectElite([]SelectionInput{
		{CandidateRef: "c1", Status: "selectable", Score: scoreWith(0.5, true)},
		{CandidateRef: "c2", Status: "selectable", Score: scoreWith(0.9, true)},
	}, 1)
	if result.BestRef != "c2" {
		t.Fatalf("best = %q, want c2", result.BestRef)
	}
	if len(result.EliteRefs) != 1 || result.EliteRefs[0] != "c2" {
		t.Fatalf("elite = %v, want [c2]", result.EliteRefs)
	}
	if len(result.PrunedRefs) != 1 || result.PrunedRefs[0] != "c1" {
		t.Fatalf("pruned = %v, want [c1]", result.PrunedRefs)
	}
}

func TestSelectEliteNeverPromotesHardGateFailure(t *testing.T) {
	// A fluent but wrong answer outscoring a correct one must not seed the next
	// generation, or the run optimises for prose.
	result := SelectElite([]SelectionInput{
		{CandidateRef: "fluent-wrong", Status: "selectable", Score: scoreWith(0.95, false)},
		{CandidateRef: "correct", Status: "selectable", Score: scoreWith(0.6, true)},
	}, 2)
	if result.BestRef != "correct" {
		t.Fatalf("best = %q, want correct", result.BestRef)
	}
	for _, ref := range result.EliteRefs {
		if ref == "fluent-wrong" {
			t.Fatal("a hard-gate failure was promoted to elite")
		}
	}
}

func TestSelectEliteBreaksTiesByComplementarity(t *testing.T) {
	// c2 duplicates c1's behavior; c3 fails differently at the same score, so
	// it is the more useful second elite.
	result := SelectElite([]SelectionInput{
		{
			CandidateRef: "c1", Status: "selectable", Score: scoreWith(0.8, true),
			BehaviorProfile: profileWith(BehaviorEntry{Key: "coverage", Value: 0.9}, BehaviorEntry{Key: "rigor", Value: 0.2}),
		},
		{
			CandidateRef: "c2", Status: "selectable", Score: scoreWith(0.7, true),
			BehaviorProfile: profileWith(BehaviorEntry{Key: "coverage", Value: 0.88}, BehaviorEntry{Key: "rigor", Value: 0.22}),
		},
		{
			CandidateRef: "c3", Status: "selectable", Score: scoreWith(0.7, true),
			BehaviorProfile: profileWith(BehaviorEntry{Key: "coverage", Value: 0.2}, BehaviorEntry{Key: "rigor", Value: 0.9}),
		},
	}, 2)
	if len(result.EliteRefs) != 2 {
		t.Fatalf("elite = %v, want two entries", result.EliteRefs)
	}
	if result.EliteRefs[1] != "c3" {
		t.Fatalf("second elite = %q, want c3 (the complementary candidate)", result.EliteRefs[1])
	}
}

func TestSelectElitePrunesUnscoredAndFailedCandidates(t *testing.T) {
	result := SelectElite([]SelectionInput{
		{CandidateRef: "unscored", Status: "selectable"},
		{CandidateRef: "failed", Status: "failed", Score: scoreWith(0.9, true)},
		{CandidateRef: "ok", Status: "selectable", Score: scoreWith(0.4, true)},
	}, 3)
	if result.BestRef != "ok" {
		t.Fatalf("best = %q, want ok", result.BestRef)
	}
	if len(result.EliteRefs) != 1 {
		t.Fatalf("elite = %v, want only the scored selectable candidate", result.EliteRefs)
	}
}

func TestSelectEliteHandlesNoUsableCandidates(t *testing.T) {
	result := SelectElite([]SelectionInput{
		{CandidateRef: "c1", Status: "failed"},
	}, 2)
	if result.BestRef != "" || len(result.EliteRefs) != 0 {
		t.Fatalf("expected an empty selection, got %+v", result)
	}
}

func TestLaneForRelationMatchesTaxonomy(t *testing.T) {
	cases := map[string]string{
		RelationRepairOf:    LaneRepair,
		RelationChallengeOf: LaneChallenge,
		RelationCrossoverOf: LaneCrossover,
		RelationSynthesisOf: LaneCrossover,
		RelationDerivedFrom: LaneDiverse,
		"unknown":           LaneBaseline,
	}
	for relation, lane := range cases {
		if got := LaneForRelation(relation); got != lane {
			t.Fatalf("lane for %q = %q, want %q", relation, got, lane)
		}
	}
	if IsKnownRelation("teleported_from") {
		t.Fatal("unexpected relation accepted")
	}
}

func TestShouldStopOnEachBoundary(t *testing.T) {
	config := DefaultStopConfig()
	config.MaxCostUSD = 5
	cases := []struct {
		name     string
		progress RunProgress
		reason   string
	}{
		{"cost", RunProgress{CostUSD: config.MaxCostUSD}, StopReasonCostCeiling},
		{"target", RunProgress{BestScore: config.TargetScore}, StopReasonTargetReached},
		{"candidates", RunProgress{CandidateCount: config.MaxCandidates}, StopReasonBudgetExhausted},
		{"model_calls", RunProgress{ModelCalls: config.MaxModelCalls}, StopReasonBudgetExhausted},
		{"generations", RunProgress{Generation: config.MaxGenerations}, StopReasonBudgetExhausted},
		{"plateau", RunProgress{RoundsWithoutGain: config.NoImprovementRounds}, StopReasonNoImprovement},
	}
	for _, testCase := range cases {
		stop, reason := ShouldStop(config, testCase.progress)
		if !stop || reason != testCase.reason {
			t.Fatalf("%s: stop = %v reason = %q, want true %q", testCase.name, stop, reason, testCase.reason)
		}
	}
	if stop, _ := ShouldStop(config, RunProgress{Generation: 1, CandidateCount: 4, BestScore: 0.5}); stop {
		t.Fatal("expected a mid-run progress snapshot to continue")
	}
}

func TestDefaultStopConfigAllowsUnlimitedCostAndCapsModelCalls(t *testing.T) {
	config := DefaultStopConfig()
	if config.MaxModelCalls != 100 {
		t.Fatalf("default model call limit = %d, want 100", config.MaxModelCalls)
	}
	if config.MaxCostUSD != 0 {
		t.Fatalf("default cost limit = %v, want 0 (unlimited)", config.MaxCostUSD)
	}
	if stop, reason := ShouldStop(config, RunProgress{CostUSD: 1_000_000}); stop {
		t.Fatalf("unlimited cost unexpectedly stopped run: %s", reason)
	}
	if stop, reason := ShouldStop(config, RunProgress{ModelCalls: 100}); !stop || reason != StopReasonBudgetExhausted {
		t.Fatalf("model call ceiling: stop=%v reason=%q", stop, reason)
	}
}

func TestCancellationOverdueUsesFixedDeadline(t *testing.T) {
	now := time.Now()
	if CancellationOverdue(now.Add(-RunCancellationDeadline+time.Second), now) {
		t.Fatal("expected a run inside the deadline to be left alone")
	}
	if !CancellationOverdue(now.Add(-RunCancellationDeadline), now) {
		t.Fatal("expected a run at the deadline to be cancellable")
	}
	if CancellationOverdue(time.Time{}, now) {
		t.Fatal("expected a run without a stop request to be left alone")
	}
}

func TestClaimAbandonedUsesHeartbeatWindow(t *testing.T) {
	now := time.Now()
	if ClaimAbandoned(now.Add(-time.Second), now) {
		t.Fatal("expected a fresh heartbeat to keep the claim")
	}
	if !ClaimAbandoned(now.Add(-HeartbeatStaleAfter), now) {
		t.Fatal("expected a stale heartbeat to release the claim")
	}
}
