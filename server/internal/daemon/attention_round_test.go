package daemon

import "testing"

func TestResolveAttentionRoundSingleAnswerGrants(t *testing.T) {
	res := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Decision: "SILENT", Usable: true},
		{AgentID: "b", Decision: "ANSWER", Usable: true, Summary: "I can help"},
		{AgentID: "c", Decision: "SILENT", Usable: true},
	})
	if res.Outcome != AttentionRoundOutcomeGranted {
		t.Fatalf("outcome = %q, want granted", res.Outcome)
	}
	if res.GrantAgentID != "b" {
		t.Fatalf("grant = %q, want b", res.GrantAgentID)
	}
}

func TestResolveAttentionRoundNoAnswerSilent(t *testing.T) {
	res := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Decision: "SILENT", Usable: true},
		{AgentID: "b", Decision: "SILENT", Usable: true},
	})
	if res.Outcome != AttentionRoundOutcomeSilent {
		t.Fatalf("outcome = %q, want silent", res.Outcome)
	}
	// CONTRIBUTE only still means silent (contributions are internal offers).
	res2 := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Decision: "CONTRIBUTE", Usable: true},
		{AgentID: "b", Decision: "SILENT", Usable: true},
	})
	if res2.Outcome != AttentionRoundOutcomeSilent || res2.ContributeCount != 1 {
		t.Fatalf("contribute-only outcome = %q contribute=%d", res2.Outcome, res2.ContributeCount)
	}
}

func TestResolveAttentionRoundMultipleAnswersConverge(t *testing.T) {
	res := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Decision: "ANSWER", Usable: true},
		{AgentID: "b", Decision: "ANSWER", Usable: true},
		{AgentID: "c", Decision: "SILENT", Usable: true},
	})
	if res.Outcome != AttentionRoundOutcomeConverge {
		t.Fatalf("outcome = %q, want converge", res.Outcome)
	}
	if len(res.ConvergeAgentIDs) != 2 {
		t.Fatalf("converge agents = %v, want 2", res.ConvergeAgentIDs)
	}
}

func TestResolveAttentionRoundCoordinateEscalates(t *testing.T) {
	res := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Decision: "ANSWER", Usable: true},
		{AgentID: "b", Decision: "COORDINATE", Usable: true},
	})
	if res.Outcome != AttentionRoundOutcomeManager {
		t.Fatalf("outcome = %q, want manager", res.Outcome)
	}
}

func TestResolveAttentionRoundFailedNeverDecides(t *testing.T) {
	// a is the only usable ANSWER; b failed. b's failure must not silence a.
	res := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Decision: "ANSWER", Usable: true},
		{AgentID: "b", Decision: "ANSWER", Usable: false, Failed: true},
	})
	if res.Outcome != AttentionRoundOutcomeGranted || res.GrantAgentID != "a" {
		t.Fatalf("failed participant affected outcome: %+v", res)
	}
	// All failed → silent (no usable signals), but must not crash.
	res2 := ResolveAttentionRound([]AttentionParticipant{
		{AgentID: "a", Failed: true}, {AgentID: "b", Failed: true},
	})
	if res2.Outcome != AttentionRoundOutcomeSilent {
		t.Fatalf("all-failed outcome = %q, want silent", res2.Outcome)
	}
}

func TestResolveConvergenceUniqueKeepGrants(t *testing.T) {
	res := ResolveConvergence([]ConvergenceVote{
		{AgentID: "a", Vote: ConvergenceVoteKeep},
		{AgentID: "b", Vote: ConvergenceVoteYield},
	})
	if res.Outcome != AttentionRoundOutcomeGranted || res.GrantAgentID != "a" {
		t.Fatalf("unique keep resolution = %+v", res)
	}
}

func TestResolveConvergenceMergeGrantsTarget(t *testing.T) {
	res := ResolveConvergence([]ConvergenceVote{
		{AgentID: "a", Vote: ConvergenceVoteMerge, TargetAgentID: "b"},
		{AgentID: "b", Vote: ConvergenceVoteMerge, TargetAgentID: "b"},
	})
	if res.Outcome != AttentionRoundOutcomeGranted || res.GrantAgentID != "b" {
		t.Fatalf("merge resolution = %+v", res)
	}
}

func TestResolveConvergenceRejectsOutsiderMergeTarget(t *testing.T) {
	res := ResolveConvergence([]ConvergenceVote{
		{AgentID: "a", Vote: ConvergenceVoteMerge, TargetAgentID: "outsider"},
		{AgentID: "b", Vote: ConvergenceVoteMerge, TargetAgentID: "outsider"},
	})
	if res.Outcome != AttentionRoundOutcomeManager {
		t.Fatalf("outsider merge outcome = %q, want manager", res.Outcome)
	}
	if res.GrantAgentID != "" {
		t.Fatalf("outsider unexpectedly granted: %+v", res)
	}
}

func TestResolveConvergenceConflictsEscalate(t *testing.T) {
	// Two distinct KEEP → manager.
	multiKeep := ResolveConvergence([]ConvergenceVote{
		{AgentID: "a", Vote: ConvergenceVoteKeep},
		{AgentID: "b", Vote: ConvergenceVoteKeep},
	})
	if multiKeep.Outcome != AttentionRoundOutcomeManager {
		t.Fatalf("multi-keep = %q, want manager", multiKeep.Outcome)
	}
	// REQUEST_MANAGER → manager.
	reqMan := ResolveConvergence([]ConvergenceVote{
		{AgentID: "a", Vote: ConvergenceVoteRequestManager},
	})
	if reqMan.Outcome != AttentionRoundOutcomeManager {
		t.Fatalf("request-manager = %q, want manager", reqMan.Outcome)
	}
	// All YIELD → manager (claimed but no owner).
	allYield := ResolveConvergence([]ConvergenceVote{
		{AgentID: "a", Vote: ConvergenceVoteYield},
		{AgentID: "b", Vote: ConvergenceVoteYield},
	})
	if allYield.Outcome != AttentionRoundOutcomeManager {
		t.Fatalf("all-yield = %q, want manager", allYield.Outcome)
	}
}

func TestValidConvergenceVote(t *testing.T) {
	for _, v := range AttentionVoteValues {
		if !ValidConvergenceVote(v) {
			t.Fatalf("ValidConvergenceVote(%q) = false", v)
		}
	}
	if ValidConvergenceVote("NOPE") {
		t.Fatal("unknown vote accepted")
	}
}
