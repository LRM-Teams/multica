package researchrun

import "testing"

func membershipFixture(status TeamFormationStatus) TeamMembershipState {
	return TeamMembershipState{FormationDecisionID: "formation-1", Status: status, AgentID: "agent-1", Role: "verifier", AuthorizationScope: []string{"claim-1"}, Retention: MembershipRunScoped, BoundTasksRemaining: 1}
}

func TestAdvanceTeamMembershipActivatesOnlyAvailableProvisionedAgent(t *testing.T) {
	state := membershipFixture(FormationProvisioning)
	pending, err := AdvanceTeamMembership(state, false, "")
	if err != nil || pending.MembershipActive || pending.MayDispatch || pending.State.Status != FormationProvisioning {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	active, err := AdvanceTeamMembership(state, true, "")
	if err != nil || !active.MembershipActive || !active.MayDispatch || active.State.Status != FormationActive {
		t.Fatalf("active=%+v err=%v", active, err)
	}
}

func TestAdvanceTeamMembershipProvisioningFailureDoesNotCreateMembership(t *testing.T) {
	state := membershipFixture(FormationProvisioning)
	failed, err := AdvanceTeamMembership(state, false, "provider_rejected")
	if err != nil || failed.State.Status != FormationFailed || failed.State.AgentID != "" || failed.MembershipActive || failed.Event.FailureClass != "provider_rejected" {
		t.Fatalf("failed=%+v err=%v", failed, err)
	}
}

func TestAdvanceTeamMembershipWaitsForContributionsDeliberationsAndAttempts(t *testing.T) {
	state := membershipFixture(FormationActive)
	state.BoundTasksRemaining = 0
	state.PendingContributions, state.PendingDeliberations, state.ActiveAttempts = 1, 1, 1
	waiting, err := AdvanceTeamMembership(state, true, "")
	if err != nil || waiting.State.Status != FormationActive || waiting.MayDispatch || waiting.Event.Kind != "retirement_waiting_for_active_work" {
		t.Fatalf("waiting=%+v err=%v", waiting, err)
	}
	state.PendingContributions, state.PendingDeliberations, state.ActiveAttempts = 0, 0, 0
	retiring, err := AdvanceTeamMembership(state, true, "")
	if err != nil || retiring.State.Status != FormationRetiring || retiring.State.ExitReason != "bound_tasks_completed" {
		t.Fatalf("retiring=%+v err=%v", retiring, err)
	}
	retired, err := AdvanceTeamMembership(retiring.State, false, "")
	if err != nil || retired.State.Status != FormationRetired || retired.State.AgentID != "agent-1" {
		t.Fatalf("retired=%+v err=%v", retired, err)
	}
}

func TestAdvanceTeamMembershipPreservesHistoricalIdentityAfterRetirement(t *testing.T) {
	state := membershipFixture(FormationRetired)
	state.ExitReason = "branch_terminated"
	transition, err := AdvanceTeamMembership(state, false, "")
	if err != nil || transition.State.AgentID != "agent-1" || transition.State.ExitReason != "branch_terminated" {
		t.Fatalf("transition=%+v err=%v", transition, err)
	}
}
