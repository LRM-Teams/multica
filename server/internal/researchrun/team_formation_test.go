package researchrun

import "testing"

func TestResearchDirectorIsPinnedAndOnlyExplicitUserReassignmentChangesIt(t *testing.T) {
	director, err := PinResearchDirector("lead-at-start")
	if err != nil {
		t.Fatal(err)
	}
	// A later Fleet lead is deliberately not an input to authorization.
	proposal := validFormationProposal()
	proposal.ActorAgentID = "later-fleet-lead"
	if _, err := AuthorizeTeamFormation(director, validFormationAuthorization(), proposal); err == nil {
		t.Fatal("later Fleet lead silently gained Director authority")
	}
	next, err := ReassignResearchDirector(director, DirectorReassignment{ActorUserID: "user", NewDirectorAgentID: "later-fleet-lead", ExpectedVersion: 1, Reason: "explicit replacement"}, []string{"later-fleet-lead"})
	if err != nil || next.AgentID != "later-fleet-lead" || next.IdentityVersion != 2 {
		t.Fatalf("next=%+v err=%v", next, err)
	}
}

func validFormationAuthorization() TeamFormationAuthorization {
	return TeamFormationAuthorization{AllowAgentCreation: true, MaximumAgents: 5, CurrentAgents: 2, RemainingBudget: 10, AllowedToolKeys: []string{"search"}, AllowedSourceAccess: []string{"public"}}
}
func validFormationProposal() TeamFormationProposal {
	return TeamFormationProposal{ActorAgentID: "director", ActorMembershipActive: true, DirectorIdentityVersion: 1, Reason: TeamFormationIndependence, TargetID: "claim-1", RequiredCapability: "validator", RoleDescription: "independent verifier", ToolKeys: []string{"search"}, SourceAccess: []string{"public"}, Budget: 2, ExpectedArtifact: "verification", StopCondition: "claim adjudicated", ExistingMembersUnsuitableReason: "all current members share provider"}
}

func TestAuthorizeTeamFormationChecksPrincipalMembershipContractAndBudget(t *testing.T) {
	director := ResearchDirectorIdentity{AgentID: "director", IdentityVersion: 1}
	decision, err := AuthorizeTeamFormation(director, validFormationAuthorization(), validFormationProposal())
	if err != nil || !decision.Authorized || decision.IdempotencyKey == "" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	authorization := validFormationAuthorization()
	authorization.AllowAgentCreation = false
	decision, err = AuthorizeTeamFormation(director, authorization, validFormationProposal())
	if err != nil || decision.Authorized || decision.Reason != "contract_disallows_agent_creation" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	proposal := validFormationProposal()
	proposal.Budget = 20
	decision, err = AuthorizeTeamFormation(director, validFormationAuthorization(), proposal)
	if err != nil || decision.Reason != "insufficient_budget" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
}

func TestAuthorizeTeamFormationRejectsInactiveOrWrongDirector(t *testing.T) {
	director := ResearchDirectorIdentity{AgentID: "director", IdentityVersion: 1}
	proposal := validFormationProposal()
	proposal.ActorMembershipActive = false
	if _, err := AuthorizeTeamFormation(director, validFormationAuthorization(), proposal); err == nil {
		t.Fatal("expected inactive membership rejection")
	}
}
