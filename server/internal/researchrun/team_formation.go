package researchrun

import (
	"fmt"
	"sort"
	"strings"
)

type ResearchDirectorIdentity struct {
	AgentID         string
	IdentityVersion int
}

func PinResearchDirector(fleetLeadAgentID string) (ResearchDirectorIdentity, error) {
	if fleetLeadAgentID == "" {
		return ResearchDirectorIdentity{}, fmt.Errorf("%w: Research Fleet lead is required", ErrInvalidContract)
	}
	return ResearchDirectorIdentity{AgentID: fleetLeadAgentID, IdentityVersion: 1}, nil
}

type DirectorReassignment struct {
	ActorUserID        string
	NewDirectorAgentID string
	ExpectedVersion    int
	Reason             string
}

func ReassignResearchDirector(current ResearchDirectorIdentity, reassignment DirectorReassignment, validWorkspaceAgentIDs []string) (ResearchDirectorIdentity, error) {
	if reassignment.ActorUserID == "" || reassignment.NewDirectorAgentID == "" || reassignment.Reason == "" {
		return ResearchDirectorIdentity{}, fmt.Errorf("%w: explicit user actor, target Agent, and reason are required", ErrInvalidContract)
	}
	if reassignment.ExpectedVersion != current.IdentityVersion {
		return ResearchDirectorIdentity{}, fmt.Errorf("%w: Director identity version changed", ErrInvalidTransition)
	}
	if !teamFormationContains(validWorkspaceAgentIDs, reassignment.NewDirectorAgentID) {
		return ResearchDirectorIdentity{}, fmt.Errorf("%w: reassigned Director is not a valid workspace Agent", ErrInvalidContract)
	}
	if reassignment.NewDirectorAgentID == current.AgentID {
		return ResearchDirectorIdentity{}, fmt.Errorf("%w: Director reassignment does not change identity", ErrInvalidContract)
	}
	return ResearchDirectorIdentity{AgentID: reassignment.NewDirectorAgentID, IdentityVersion: current.IdentityVersion + 1}, nil
}

type TeamFormationReason string

const (
	TeamFormationCapabilityGap    TeamFormationReason = "capability_gap"
	TeamFormationParallelCapacity TeamFormationReason = "parallel_capacity"
	TeamFormationIndependence     TeamFormationReason = "independence"
	TeamFormationNovelPerspective TeamFormationReason = "novel_perspective"
	TeamFormationNewBranch        TeamFormationReason = "new_branch"
)

type TeamFormationAuthorization struct {
	AllowAgentCreation  bool
	MaximumAgents       int
	CurrentAgents       int
	RemainingBudget     float64
	AllowedToolKeys     []string
	AllowedSourceAccess []string
}

type TeamFormationProposal struct {
	ActorAgentID                    string
	ActorMembershipActive           bool
	DirectorIdentityVersion         int
	Reason                          TeamFormationReason
	TargetID                        string
	RequiredCapability              string
	RoleDescription                 string
	ToolKeys                        []string
	SourceAccess                    []string
	Budget                          float64
	ExpectedArtifact                string
	StopCondition                   string
	ExistingMembersUnsuitableReason string
}

type TeamFormationDecision struct {
	Authorized     bool
	Status         string
	Reason         string
	IdempotencyKey string
}

func AuthorizeTeamFormation(director ResearchDirectorIdentity, authorization TeamFormationAuthorization, proposal TeamFormationProposal) (TeamFormationDecision, error) {
	decision := TeamFormationDecision{Status: "blocked"}
	if proposal.ActorAgentID != director.AgentID || proposal.DirectorIdentityVersion != director.IdentityVersion || !proposal.ActorMembershipActive {
		return decision, fmt.Errorf("%w: only the pinned active Research Director may propose team formation", ErrInvalidContract)
	}
	if !validTeamFormationReason(proposal.Reason) || proposal.TargetID == "" || proposal.RequiredCapability == "" || proposal.RoleDescription == "" || proposal.ExpectedArtifact == "" || proposal.StopCondition == "" || proposal.ExistingMembersUnsuitableReason == "" || proposal.Budget < 0 {
		return decision, fmt.Errorf("%w: Team Formation proposal is incomplete", ErrInvalidContract)
	}
	decision.IdempotencyKey = string(proposal.Reason) + ":" + proposal.TargetID + ":" + proposal.RequiredCapability + ":" + joinFormationValues(proposal.ToolKeys) + ":" + joinFormationValues(proposal.SourceAccess)
	if !authorization.AllowAgentCreation {
		decision.Reason = "contract_disallows_agent_creation"
		return decision, nil
	}
	if authorization.MaximumAgents <= authorization.CurrentAgents {
		decision.Reason = "agent_limit_reached"
		return decision, nil
	}
	if proposal.Budget > authorization.RemainingBudget {
		decision.Reason = "insufficient_budget"
		return decision, nil
	}
	if !containsAllFormationValues(authorization.AllowedToolKeys, proposal.ToolKeys) {
		decision.Reason = "tool_permission_not_authorized"
		return decision, nil
	}
	if !containsAllFormationValues(authorization.AllowedSourceAccess, proposal.SourceAccess) {
		decision.Reason = "source_access_not_authorized"
		return decision, nil
	}
	decision.Authorized, decision.Status, decision.Reason = true, "authorized", "contract_permissions_and_budget_satisfied"
	return decision, nil
}

func validTeamFormationReason(reason TeamFormationReason) bool {
	switch reason {
	case TeamFormationCapabilityGap, TeamFormationParallelCapacity, TeamFormationIndependence, TeamFormationNovelPerspective, TeamFormationNewBranch:
		return true
	}
	return false
}
func teamFormationContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
func containsAllFormationValues(have, required []string) bool {
	for _, value := range required {
		if !teamFormationContains(have, value) {
			return false
		}
	}
	return true
}
func joinFormationValues(values []string) string {
	canonical := append([]string(nil), values...)
	sort.Strings(canonical)
	return strings.Join(canonical, ",")
}
