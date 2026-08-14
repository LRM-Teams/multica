package researchrun

import (
	"errors"
	"testing"
)

const convergenceTestHash = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func integrationConvergencePolicyFixture() IntegrationConvergencePolicy {
	return IntegrationConvergencePolicy{
		Version: IntegrationConvergencePolicyVersionV1, MaximumRounds: 8,
		MaximumInsightDepth: 6, MaximumCostUnits: 100,
		MinimumMarginalGain: 0.1, LowGainRoundsToConverge: 2,
	}
}

func integrationConvergenceSnapshotFixture() IntegrationConvergenceSnapshot {
	return IntegrationConvergenceSnapshot{
		WorkspaceID: "workspace-1", SessionID: "session-1", GoalVersion: 2, PlanVersion: 3,
		ThroughEventSequence: 50, CanonicalStateHash: convergenceTestHash,
		CompletedRounds: 3, CurrentMaximumInsightDepth: 2, ConsumedCostUnits: 30,
		FrontierStable: true, RecentMarginalGains: []float64{0.2, 0.05, 0.04},
	}
}

func TestDecideIntegrationConvergenceRequiresStableLowGainWindow(t *testing.T) {
	decision, err := DecideIntegrationConvergence(integrationConvergencePolicyFixture(), integrationConvergenceSnapshotFixture())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != IntegrationConvergenceConverged || decision.Reason != IntegrationConvergenceLowMarginalGain || len(decision.Fingerprint) != 71 {
		t.Fatalf("decision=%+v", decision)
	}
	snapshot := integrationConvergenceSnapshotFixture()
	snapshot.RecentMarginalGains = []float64{0.05, 0.1}
	decision, err = DecideIntegrationConvergence(integrationConvergencePolicyFixture(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != IntegrationConvergenceAwaitUpstream || decision.Reason != IntegrationConvergenceObservationWindow {
		t.Fatalf("threshold is inclusive, decision=%+v", decision)
	}
}

func TestDecideIntegrationConvergenceDecisionMatrix(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*IntegrationConvergenceSnapshot)
		action IntegrationConvergenceAction
		reason IntegrationConvergenceReason
	}{
		"blocking dispute":      {func(s *IntegrationConvergenceSnapshot) { s.BlockingDisputeCount = 1 }, IntegrationConvergenceAwaitUpstream, IntegrationConvergenceBlockingDispute},
		"round limit with work": {func(s *IntegrationConvergenceSnapshot) { s.CompletedRounds = 8; s.UnassimilatedResultCount = 1 }, IntegrationConvergenceEscalate, IntegrationConvergenceRoundLimit},
		"depth limit with work": {func(s *IntegrationConvergenceSnapshot) {
			s.CurrentMaximumInsightDepth = 6
			s.PendingDerivationCount = 1
		}, IntegrationConvergenceEscalate, IntegrationConvergenceDepthLimit},
		"cost limit with work": {func(s *IntegrationConvergenceSnapshot) { s.ConsumedCostUnits = 100; s.StaleInsightCount = 1 }, IntegrationConvergenceEscalate, IntegrationConvergenceCostLimit},
		"unassimilated":        {func(s *IntegrationConvergenceSnapshot) { s.UnassimilatedResultCount = 1 }, IntegrationConvergenceContinue, IntegrationConvergenceUnassimilatedResults},
		"pending derivation":   {func(s *IntegrationConvergenceSnapshot) { s.PendingDerivationCount = 1 }, IntegrationConvergenceContinue, IntegrationConvergencePendingDerivations},
		"stale insight":        {func(s *IntegrationConvergenceSnapshot) { s.StaleInsightCount = 1 }, IntegrationConvergenceContinue, IntegrationConvergenceStaleInsights},
		"changing frontier":    {func(s *IntegrationConvergenceSnapshot) { s.FrontierStable = false }, IntegrationConvergenceContinue, IntegrationConvergenceFrontierChanging},
		"required question":    {func(s *IntegrationConvergenceSnapshot) { s.OpenRequiredQuestionCount = 1 }, IntegrationConvergenceAwaitUpstream, IntegrationConvergenceRequiredQuestions},
		"short window":         {func(s *IntegrationConvergenceSnapshot) { s.RecentMarginalGains = []float64{0.01} }, IntegrationConvergenceAwaitUpstream, IntegrationConvergenceObservationWindow},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := integrationConvergenceSnapshotFixture()
			tc.mutate(&snapshot)
			decision, err := DecideIntegrationConvergence(integrationConvergencePolicyFixture(), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.action || decision.Reason != tc.reason || decision.Fingerprint == "" {
				t.Fatalf("decision=%+v want %s/%s", decision, tc.action, tc.reason)
			}
		})
	}
}

func TestDecideIntegrationConvergenceNeverCallsLimitsConvergence(t *testing.T) {
	snapshot := integrationConvergenceSnapshotFixture()
	snapshot.CompletedRounds = 8
	snapshot.ConsumedCostUnits = 100
	snapshot.CurrentMaximumInsightDepth = 6
	decision, err := DecideIntegrationConvergence(integrationConvergencePolicyFixture(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != IntegrationConvergenceConverged {
		t.Fatalf("stable exhausted snapshot should converge from evidence, decision=%+v", decision)
	}
	snapshot.UnassimilatedResultCount = 1
	decision, err = DecideIntegrationConvergence(integrationConvergencePolicyFixture(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != IntegrationConvergenceEscalate || decision.Reason != IntegrationConvergenceRoundLimit {
		t.Fatalf("unfinished exhausted snapshot must escalate, decision=%+v", decision)
	}
}

func TestDecideIntegrationConvergenceFinishesAvailableWorkBeforeAwaitingUpstream(t *testing.T) {
	snapshot := integrationConvergenceSnapshotFixture()
	snapshot.BlockingDisputeCount = 1
	snapshot.OpenRequiredQuestionCount = 1
	snapshot.UnassimilatedResultCount = 1
	snapshot.CurrentMaximumInsightDepth = 6
	decision, err := DecideIntegrationConvergence(integrationConvergencePolicyFixture(), snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Action != IntegrationConvergenceContinue || decision.Reason != IntegrationConvergenceUnassimilatedResults {
		t.Fatalf("available same-level assimilation was abandoned: %+v", decision)
	}
}

func TestDecideIntegrationConvergenceRejectsInvalidFacts(t *testing.T) {
	for name, tc := range map[string]struct {
		mutatePolicy   func(*IntegrationConvergencePolicy)
		mutateSnapshot func(*IntegrationConvergenceSnapshot)
	}{
		"unknown policy":         {func(p *IntegrationConvergencePolicy) { p.Version = "future" }, nil},
		"zero gain window":       {func(p *IntegrationConvergencePolicy) { p.LowGainRoundsToConverge = 0 }, nil},
		"invalid canonical hash": {nil, func(s *IntegrationConvergenceSnapshot) { s.CanonicalStateHash = "sha256:nope" }},
		"negative count":         {nil, func(s *IntegrationConvergenceSnapshot) { s.StaleInsightCount = -1 }},
		"gain over one":          {nil, func(s *IntegrationConvergenceSnapshot) { s.RecentMarginalGains[0] = 1.1 }},
		"more gains than rounds": {nil, func(s *IntegrationConvergenceSnapshot) { s.CompletedRounds = 1 }},
	} {
		t.Run(name, func(t *testing.T) {
			policy, snapshot := integrationConvergencePolicyFixture(), integrationConvergenceSnapshotFixture()
			if tc.mutatePolicy != nil {
				tc.mutatePolicy(&policy)
			}
			if tc.mutateSnapshot != nil {
				tc.mutateSnapshot(&snapshot)
			}
			if _, err := DecideIntegrationConvergence(policy, snapshot); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}

func TestDecideIntegrationConvergenceFingerprintBindsCanonicalState(t *testing.T) {
	policy, snapshot := integrationConvergencePolicyFixture(), integrationConvergenceSnapshotFixture()
	first, err := DecideIntegrationConvergence(policy, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.CanonicalStateHash = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	second, err := DecideIntegrationConvergence(policy, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == second.Fingerprint {
		t.Fatal("canonical state change reused convergence fingerprint")
	}
}
