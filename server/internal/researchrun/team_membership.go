package researchrun

import "fmt"

type TeamFormationStatus string

const (
	FormationAuthorized   TeamFormationStatus = "authorized"
	FormationProvisioning TeamFormationStatus = "provisioning"
	FormationActive       TeamFormationStatus = "active"
	FormationBlocked      TeamFormationStatus = "blocked"
	FormationFailed       TeamFormationStatus = "failed"
	FormationRetiring     TeamFormationStatus = "retiring"
	FormationRetired      TeamFormationStatus = "retired"
)

type MembershipRetention string

const (
	MembershipRunScoped       MembershipRetention = "run_scoped"
	MembershipRetainForReview MembershipRetention = "retain_for_user_review"
)

type TeamMembershipState struct {
	FormationDecisionID      string
	Status                   TeamFormationStatus
	AgentID                  string
	Role                     string
	AuthorizationScope       []string
	Retention                MembershipRetention
	ProvisioningFailureClass string
	PendingContributions     int
	PendingDeliberations     int
	ActiveAttempts           int
	BoundTasksRemaining      int
	BranchTerminated         bool
	RunCancelled             bool
	BudgetWithdrawn          bool
	ExitReason               string
}

type TeamMembershipEvent struct {
	Kind         string
	AgentID      string
	FailureClass string
	ExitReason   string
}

type TeamMembershipTransition struct {
	State            TeamMembershipState
	MembershipActive bool
	MayDispatch      bool
	Event            TeamMembershipEvent
}

func AdvanceTeamMembership(state TeamMembershipState, agentAvailable bool, provisioningFailureClass string) (TeamMembershipTransition, error) {
	if state.FormationDecisionID == "" || !validMembershipRetention(state.Retention) || state.PendingContributions < 0 || state.PendingDeliberations < 0 || state.ActiveAttempts < 0 || state.BoundTasksRemaining < 0 {
		return TeamMembershipTransition{}, fmt.Errorf("%w: Team Membership state is invalid", ErrInvalidContract)
	}
	next := state
	switch state.Status {
	case FormationAuthorized:
		next.Status = FormationProvisioning
		return TeamMembershipTransition{State: next, Event: TeamMembershipEvent{Kind: "provisioning_started"}}, nil
	case FormationProvisioning:
		if provisioningFailureClass != "" {
			next.Status, next.ProvisioningFailureClass = FormationFailed, provisioningFailureClass
			next.AgentID = ""
			return TeamMembershipTransition{State: next, Event: TeamMembershipEvent{Kind: "provisioning_failed", FailureClass: provisioningFailureClass}}, nil
		}
		if !agentAvailable {
			return TeamMembershipTransition{State: next, Event: TeamMembershipEvent{Kind: "provisioning_pending"}}, nil
		}
		if state.AgentID == "" || state.Role == "" || len(state.AuthorizationScope) == 0 {
			return TeamMembershipTransition{}, fmt.Errorf("%w: available Agent requires identity, role, and authorization scope", ErrInvalidContract)
		}
		next.Status = FormationActive
		return TeamMembershipTransition{State: next, MembershipActive: true, MayDispatch: true, Event: TeamMembershipEvent{Kind: "membership_activated", AgentID: state.AgentID}}, nil
	case FormationActive:
		shouldRetire := state.BoundTasksRemaining == 0 || state.BranchTerminated || state.RunCancelled || state.BudgetWithdrawn
		if !shouldRetire {
			return TeamMembershipTransition{State: next, MembershipActive: true, MayDispatch: true, Event: TeamMembershipEvent{Kind: "membership_unchanged"}}, nil
		}
		if state.PendingContributions > 0 || state.PendingDeliberations > 0 || state.ActiveAttempts > 0 {
			return TeamMembershipTransition{State: next, MembershipActive: true, MayDispatch: false, Event: TeamMembershipEvent{Kind: "retirement_waiting_for_active_work"}}, nil
		}
		next.Status = FormationRetiring
		if next.ExitReason == "" {
			next.ExitReason = membershipExitReason(state)
		}
		return TeamMembershipTransition{State: next, Event: TeamMembershipEvent{Kind: "membership_retiring", AgentID: state.AgentID, ExitReason: next.ExitReason}}, nil
	case FormationRetiring:
		if state.PendingContributions > 0 || state.PendingDeliberations > 0 || state.ActiveAttempts > 0 {
			return TeamMembershipTransition{}, fmt.Errorf("%w: retiring Membership regained active work", ErrInvalidTransition)
		}
		next.Status = FormationRetired
		return TeamMembershipTransition{State: next, Event: TeamMembershipEvent{Kind: "membership_retired", AgentID: state.AgentID, ExitReason: state.ExitReason}}, nil
	case FormationFailed, FormationBlocked, FormationRetired:
		return TeamMembershipTransition{State: next, Event: TeamMembershipEvent{Kind: "terminal_history_preserved", AgentID: state.AgentID}}, nil
	default:
		return TeamMembershipTransition{}, fmt.Errorf("%w: unsupported Team Formation status %q", ErrInvalidContract, state.Status)
	}
}

func validMembershipRetention(retention MembershipRetention) bool {
	return retention == MembershipRunScoped || retention == MembershipRetainForReview
}
func membershipExitReason(state TeamMembershipState) string {
	if state.RunCancelled {
		return "run_cancelled"
	}
	if state.BudgetWithdrawn {
		return "budget_withdrawn"
	}
	if state.BranchTerminated {
		return "branch_terminated"
	}
	return "bound_tasks_completed"
}
