package researchrun

import "fmt"

type ResearchBranchStatus string

const (
	ResearchBranchProposed   ResearchBranchStatus = "proposed"
	ResearchBranchActive     ResearchBranchStatus = "active"
	ResearchBranchSaturated  ResearchBranchStatus = "saturated"
	ResearchBranchCompleted  ResearchBranchStatus = "completed"
	ResearchBranchTerminated ResearchBranchStatus = "terminated"
	ResearchBranchObsolete   ResearchBranchStatus = "obsolete"
)

type BranchLifecycleState struct {
	BranchID                   string
	Status                     ResearchBranchStatus
	BudgetSpent                float64
	BudgetLimit                float64
	LowGainStreak              int
	RequiredLowGainRounds      int
	OpenRequiredQuestions      int
	OpenBlockingDisputes       int
	ActiveTasks                int
	PendingHighValueCandidates int
	EntryConditionsSatisfied   bool
	ExitConditionsSatisfied    bool
	ContractStillValid         bool
}

type BranchLifecycleDecision struct {
	PreviousStatus    ResearchBranchStatus
	NextStatus        ResearchBranchStatus
	Reason            string
	CancelPendingWork bool
}

// DecideBranchLifecycle derives branch state from canonical facts. Agent prose
// is intentionally absent and cannot directly complete or terminate a branch.
func DecideBranchLifecycle(state BranchLifecycleState) (BranchLifecycleDecision, error) {
	if state.BranchID == "" || state.BudgetLimit < 0 || state.BudgetSpent < 0 || state.RequiredLowGainRounds <= 0 || state.LowGainStreak < 0 || state.OpenRequiredQuestions < 0 || state.OpenBlockingDisputes < 0 || state.ActiveTasks < 0 || state.PendingHighValueCandidates < 0 {
		return BranchLifecycleDecision{}, fmt.Errorf("%w: branch lifecycle state is invalid", ErrInvalidContract)
	}
	if terminalResearchBranchStatus(state.Status) {
		return BranchLifecycleDecision{PreviousStatus: state.Status, NextStatus: state.Status, Reason: "terminal_history_preserved"}, nil
	}
	decision := BranchLifecycleDecision{PreviousStatus: state.Status, NextStatus: state.Status, Reason: "no_transition"}
	if !state.ContractStillValid {
		decision.NextStatus, decision.Reason, decision.CancelPendingWork = ResearchBranchObsolete, "contract_or_scope_invalidated", true
		return decision, nil
	}
	if state.Status == ResearchBranchProposed {
		if state.EntryConditionsSatisfied {
			decision.NextStatus, decision.Reason = ResearchBranchActive, "entry_conditions_satisfied"
		}
		return decision, nil
	}
	if state.BudgetSpent >= state.BudgetLimit {
		decision.NextStatus, decision.Reason, decision.CancelPendingWork = ResearchBranchTerminated, "branch_budget_exhausted", true
		return decision, nil
	}
	if state.ExitConditionsSatisfied && state.OpenRequiredQuestions == 0 && state.OpenBlockingDisputes == 0 && state.ActiveTasks == 0 {
		decision.NextStatus, decision.Reason = ResearchBranchCompleted, "exit_conditions_satisfied"
		return decision, nil
	}
	if state.LowGainStreak >= state.RequiredLowGainRounds && state.PendingHighValueCandidates == 0 && state.ActiveTasks == 0 {
		decision.NextStatus, decision.Reason = ResearchBranchSaturated, "marginal_gain_saturated"
		return decision, nil
	}
	return decision, nil
}

type RunStopFacts struct {
	ContractDeliveryRequirementsMet bool
	EvidenceRequirementsMet         bool
	RequiredQuestionsOpen           int
	BlockingDisputesOpen            int
	FreshIntegration                bool
	EvaluationPassed                bool
	CitationAuditPassed             bool
	PreDeliveryDivergencePassed     bool
	PendingHighValueCandidates      int
	ActiveTasks                     int
	CancellationPending             bool
	MarginalGainSaturated           bool
	BudgetExhausted                 bool
}

type RunStopDecision struct {
	MayStop bool
	Reasons []string
}

func DecideResearchStop(facts RunStopFacts) RunStopDecision {
	reasons := make([]string, 0)
	if !facts.ContractDeliveryRequirementsMet {
		reasons = append(reasons, "contract_delivery_unmet")
	}
	if !facts.EvidenceRequirementsMet {
		reasons = append(reasons, "evidence_requirements_unmet")
	}
	if facts.RequiredQuestionsOpen > 0 {
		reasons = append(reasons, "required_questions_open")
	}
	if facts.BlockingDisputesOpen > 0 {
		reasons = append(reasons, "blocking_disputes_open")
	}
	if !facts.FreshIntegration {
		reasons = append(reasons, "integration_stale_or_incomplete")
	}
	if !facts.EvaluationPassed {
		reasons = append(reasons, "evaluation_not_passed")
	}
	if !facts.CitationAuditPassed {
		reasons = append(reasons, "citation_audit_not_passed")
	}
	if !facts.PreDeliveryDivergencePassed {
		reasons = append(reasons, "pre_delivery_divergence_missing")
	}
	if facts.PendingHighValueCandidates > 0 {
		reasons = append(reasons, "high_value_work_pending")
	}
	if facts.ActiveTasks > 0 {
		reasons = append(reasons, "tasks_active")
	}
	if facts.CancellationPending {
		reasons = append(reasons, "cancellation_pending")
	}
	if !facts.MarginalGainSaturated && !facts.BudgetExhausted {
		reasons = append(reasons, "stopping_rule_not_met")
	}
	return RunStopDecision{MayStop: len(reasons) == 0, Reasons: reasons}
}

func terminalResearchBranchStatus(status ResearchBranchStatus) bool {
	return status == ResearchBranchCompleted || status == ResearchBranchTerminated || status == ResearchBranchObsolete
}
