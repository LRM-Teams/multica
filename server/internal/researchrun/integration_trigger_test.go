package researchrun

import (
	"errors"
	"reflect"
	"testing"
)

const integrationTestHash = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func integrationTriggerPolicyFixture() IntegrationTriggerPolicy {
	return IntegrationTriggerPolicy{
		Version: IntegrationTriggerPolicyVersionV1, MinimumDistinctAgents: 2,
		ResultsPerRound: 3, MinimumInformationGain: 0.2,
		MaximumCompletedRounds: 8, MaximumTotalCostUnits: 100,
	}
}

func integrationTriggerSnapshotFixture() IntegrationTriggerSnapshot {
	return IntegrationTriggerSnapshot{
		WorkspaceID: "workspace-1", SessionID: "session-1", GoalVersion: 2, PlanVersion: 3,
		ThroughEventSequence: 14, LastIntegratedThroughEventSequence: 10,
		CompletedRounds: 1, ReservedCostUnits: 20, EstimatedRoundCostUnits: 10,
		InformationGain: 0.25,
		AcceptedResults: []AcceptedIntegrationResultRef{
			{ResultID: "result-12", TaskID: "task-2", AttemptID: "attempt-2", AgentID: "agent-2", ArtifactPassportID: "passport-2", ArtifactVersionID: "version-2", ArtifactContentHash: integrationTestHash, AcceptedEventSequence: 12},
			{ResultID: "result-11", TaskID: "task-1", AttemptID: "attempt-1", AgentID: "agent-1", ArtifactPassportID: "passport-1", ArtifactVersionID: "version-1", ArtifactContentHash: integrationTestHash, AcceptedEventSequence: 11},
		},
	}
}

func TestDecideIntegrationTriggerFreezesStableIdempotentInputs(t *testing.T) {
	policy, snapshot := integrationTriggerPolicyFixture(), integrationTriggerSnapshotFixture()
	first, err := DecideIntegrationTrigger(policy, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !first.ShouldTrigger || first.Reason != IntegrationTriggerInformationGain || first.RoundKey == "" || len(first.InputHash) != 71 {
		t.Fatalf("decision=%+v", first)
	}
	shuffled := snapshot
	shuffled.AcceptedResults = []AcceptedIntegrationResultRef{snapshot.AcceptedResults[1], snapshot.AcceptedResults[0]}
	second, err := DecideIntegrationTrigger(policy, shuffled)
	if err != nil {
		t.Fatal(err)
	}
	if first.RoundKey != second.RoundKey || first.InputHash != second.InputHash || !reflect.DeepEqual(first.Inputs, second.Inputs) {
		t.Fatalf("unstable decisions\nfirst=%+v\nsecond=%+v", first, second)
	}
	withHistory := snapshot
	withHistory.AcceptedResults = append(append([]AcceptedIntegrationResultRef(nil), snapshot.AcceptedResults...), AcceptedIntegrationResultRef{
		ResultID: "result-9", TaskID: "task-old", AttemptID: "attempt-old", AgentID: "agent-old",
		ArtifactPassportID: "passport-old", ArtifactVersionID: "version-old",
		ArtifactContentHash: integrationTestHash, AcceptedEventSequence: 9,
	})
	historical, err := DecideIntegrationTrigger(policy, withHistory)
	if err != nil {
		t.Fatal(err)
	}
	if historical.RoundKey != first.RoundKey || !reflect.DeepEqual(historical.Inputs, first.Inputs) {
		t.Fatalf("already integrated history changed frozen inputs: %+v", historical)
	}
	changed := snapshot
	changed.AcceptedResults = append([]AcceptedIntegrationResultRef(nil), snapshot.AcceptedResults...)
	changed.AcceptedResults[0].ArtifactVersionID = "version-2-revised"
	third, err := DecideIntegrationTrigger(policy, changed)
	if err != nil {
		t.Fatal(err)
	}
	if third.RoundKey == first.RoundKey {
		t.Fatal("changed frozen input reused Integration Round identity")
	}
	changedPolicy := policy
	changedPolicy.MaximumTotalCostUnits++
	fourth, err := DecideIntegrationTrigger(changedPolicy, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if fourth.RoundKey == first.RoundKey {
		t.Fatal("changed trigger policy reused Integration Round identity")
	}
}

func TestDecideIntegrationTriggerReasonPriority(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate      func(*IntegrationTriggerSnapshot)
		wantTrigger bool
		wantReason  IntegrationTriggerReason
	}{
		"blocking contradiction": {func(s *IntegrationTriggerSnapshot) { s.BlockingContradiction = true }, true, IntegrationTriggerContradiction},
		"pending delivery":       {func(s *IntegrationTriggerSnapshot) { s.PendingDelivery = true; s.InformationGain = 0 }, true, IntegrationTriggerDelivery},
		"result threshold": {func(s *IntegrationTriggerSnapshot) {
			s.InformationGain = 0
			s.AcceptedResults = append(s.AcceptedResults, AcceptedIntegrationResultRef{ResultID: "result-13", TaskID: "task-3", AttemptID: "attempt-3", AgentID: "agent-1", ArtifactPassportID: "passport-3", ArtifactVersionID: "version-3", ArtifactContentHash: integrationTestHash, AcceptedEventSequence: 13})
		}, true, IntegrationTriggerResultThreshold},
		"active round":  {func(s *IntegrationTriggerSnapshot) { s.ActiveRound = true }, false, IntegrationTriggerRoundActive},
		"budget rounds": {func(s *IntegrationTriggerSnapshot) { s.CompletedRounds = 8 }, false, IntegrationTriggerBudgetExhausted},
		"budget cost":   {func(s *IntegrationTriggerSnapshot) { s.ReservedCostUnits = 95 }, false, IntegrationTriggerBudgetExhausted},
		"insufficient agents": {func(s *IntegrationTriggerSnapshot) {
			s.AcceptedResults[1].AgentID = "agent-2"
			s.AcceptedResults[0].AgentID = "agent-2"
		}, false, IntegrationTriggerInsufficientAgents},
		"below threshold": {func(s *IntegrationTriggerSnapshot) { s.InformationGain = 0.1 }, false, IntegrationTriggerThresholdNotMet},
		"no new results":  {func(s *IntegrationTriggerSnapshot) { s.LastIntegratedThroughEventSequence = 14 }, false, IntegrationTriggerNoNewResults},
	} {
		t.Run(name, func(t *testing.T) {
			snapshot := integrationTriggerSnapshotFixture()
			tc.mutate(&snapshot)
			decision, err := DecideIntegrationTrigger(integrationTriggerPolicyFixture(), snapshot)
			if err != nil {
				t.Fatal(err)
			}
			if decision.ShouldTrigger != tc.wantTrigger || decision.Reason != tc.wantReason {
				t.Fatalf("decision=%+v want trigger=%t reason=%s", decision, tc.wantTrigger, tc.wantReason)
			}
			if !tc.wantTrigger && (decision.RoundKey != "" || decision.InputHash != "") {
				t.Fatalf("non-trigger decision reserved identity: %+v", decision)
			}
		})
	}
}

func TestDecideIntegrationTriggerRejectsInvalidSnapshots(t *testing.T) {
	for name, tc := range map[string]struct {
		mutatePolicy   func(*IntegrationTriggerPolicy)
		mutateSnapshot func(*IntegrationTriggerSnapshot)
	}{
		"unknown policy":       {func(p *IntegrationTriggerPolicy) { p.Version = "future" }, nil},
		"single agent policy":  {func(p *IntegrationTriggerPolicy) { p.MinimumDistinctAgents = 1 }, nil},
		"invalid gain":         {nil, func(s *IntegrationTriggerSnapshot) { s.InformationGain = 2 }},
		"watermark regression": {nil, func(s *IntegrationTriggerSnapshot) { s.LastIntegratedThroughEventSequence = 15 }},
		"future acceptance":    {nil, func(s *IntegrationTriggerSnapshot) { s.AcceptedResults[0].AcceptedEventSequence = 15 }},
		"duplicate result":     {nil, func(s *IntegrationTriggerSnapshot) { s.AcceptedResults[1].ResultID = s.AcceptedResults[0].ResultID }},
		"duplicate sequence": {nil, func(s *IntegrationTriggerSnapshot) {
			s.AcceptedResults[1].AcceptedEventSequence = s.AcceptedResults[0].AcceptedEventSequence
		}},
		"invalid hash": {nil, func(s *IntegrationTriggerSnapshot) { s.AcceptedResults[0].ArtifactContentHash = "sha256:nope" }},
	} {
		t.Run(name, func(t *testing.T) {
			policy, snapshot := integrationTriggerPolicyFixture(), integrationTriggerSnapshotFixture()
			if tc.mutatePolicy != nil {
				tc.mutatePolicy(&policy)
			}
			if tc.mutateSnapshot != nil {
				tc.mutateSnapshot(&snapshot)
			}
			if _, err := DecideIntegrationTrigger(policy, snapshot); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}
