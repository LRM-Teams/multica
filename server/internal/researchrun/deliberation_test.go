package researchrun

import "testing"

func activeDeliberation() DeliberationState {
	return DeliberationState{
		DisputeID: "d1", DirectorAgentID: "director", ParticipantAgentIDs: []string{"a1", "a2"},
		Status: DeliberationActive, Watermark: DeliberationWatermark{PositionHashes: []string{"p1", "p2"}},
	}
}

func deliberationLimits() DeliberationLimits {
	return DeliberationLimits{MaximumRounds: 5, MaximumNoProgressRounds: 2, MaximumElapsedSeconds: 3600, MaximumTokens: 1000, MaximumToolCalls: 10}
}

func TestAdvanceDeliberationUsesCanonicalDeltaNotClaimedProgress(t *testing.T) {
	state := activeDeliberation()
	state.NoProgressRounds = 1
	transition, err := AdvanceDeliberation(state, DeliberationTurnInput{
		ActorAgentID: "a1", ClaimedProgress: "position_changed", NextWatermark: state.Watermark,
	}, deliberationLimits())
	if err != nil {
		t.Fatal(err)
	}
	if transition.CanonicalProgress || transition.State.Status != DeliberationDeadlocked || transition.LeadAdjudicationTask == nil {
		t.Fatalf("transition=%+v", transition)
	}
	if transition.LeadAdjudicationTask.AssignedAgentID != "director" || transition.LeadAdjudicationTask.Reason != "no_canonical_progress" {
		t.Fatalf("task=%+v", transition.LeadAdjudicationTask)
	}
}

func TestAdvanceDeliberationCanonicalDeltaResetsStagnation(t *testing.T) {
	state := activeDeliberation()
	state.NoProgressRounds = 1
	nextWatermark := state.Watermark
	nextWatermark.EvidenceIDs = []string{"e1"}
	transition, err := AdvanceDeliberation(state, DeliberationTurnInput{ActorAgentID: "a2", ClaimedProgress: "no_change", NextWatermark: nextWatermark}, deliberationLimits())
	if err != nil || !transition.CanonicalProgress || transition.State.NoProgressRounds != 0 || transition.State.Status != DeliberationActive {
		t.Fatalf("transition=%+v err=%v", transition, err)
	}
}

func TestAdvanceDeliberationSeparatesConsensusAndEvidenceWaitFromDeadlock(t *testing.T) {
	state := activeDeliberation()
	consensus, err := AdvanceDeliberation(state, DeliberationTurnInput{
		ActorAgentID: "a1", NextWatermark: state.Watermark,
		ResolutionProposalByAgent: map[string]string{"a1": "proposal-hash", "a2": "proposal-hash"},
	}, deliberationLimits())
	if err != nil || consensus.State.Status != DeliberationConsensus || consensus.LeadAdjudicationTask != nil {
		t.Fatalf("consensus=%+v err=%v", consensus, err)
	}
	waiting, err := AdvanceDeliberation(state, DeliberationTurnInput{ActorAgentID: "a1", NextWatermark: state.Watermark, NeedsExternalEvidence: true}, deliberationLimits())
	if err != nil || waiting.State.Status != DeliberationAwaitingEvidence || waiting.LeadAdjudicationTask != nil {
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
}

func TestAdvanceDeliberationBudgetOrUnavailableParticipantEscalates(t *testing.T) {
	tests := []DeliberationTurnInput{
		{ActorAgentID: "a1", NextWatermark: activeDeliberation().Watermark, TokenCost: 1000},
		{ActorAgentID: "a1", NextWatermark: activeDeliberation().Watermark, UnavailableParticipantIDs: []string{"a2"}},
	}
	for _, turn := range tests {
		transition, err := AdvanceDeliberation(activeDeliberation(), turn, deliberationLimits())
		if err != nil || transition.State.Status != DeliberationDeadlocked || transition.LeadAdjudicationTask == nil {
			t.Fatalf("transition=%+v err=%v", transition, err)
		}
	}
}
