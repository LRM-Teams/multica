package researchrun

import (
	"reflect"
	"testing"
)

func TestDecideBranchLifecycleUsesCanonicalSaturation(t *testing.T) {
	state := BranchLifecycleState{BranchID: "b1", Status: ResearchBranchActive, BudgetLimit: 10, BudgetSpent: 3, LowGainStreak: 2, RequiredLowGainRounds: 2, ContractStillValid: true}
	decision, err := DecideBranchLifecycle(state)
	if err != nil || decision.NextStatus != ResearchBranchSaturated || decision.Reason != "marginal_gain_saturated" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	state.PendingHighValueCandidates = 1
	decision, err = DecideBranchLifecycle(state)
	if err != nil || decision.NextStatus != ResearchBranchActive {
		t.Fatalf("high-value work must prevent saturation: %+v err=%v", decision, err)
	}
}

func TestDecideBranchLifecycleDistinguishesCompletionTerminationAndObsolete(t *testing.T) {
	base := BranchLifecycleState{BranchID: "b1", Status: ResearchBranchActive, BudgetLimit: 10, RequiredLowGainRounds: 2, ContractStillValid: true}
	completed := base
	completed.ExitConditionsSatisfied = true
	decision, _ := DecideBranchLifecycle(completed)
	if decision.NextStatus != ResearchBranchCompleted {
		t.Fatalf("completed=%+v", decision)
	}
	exhausted := base
	exhausted.BudgetSpent = 10
	decision, _ = DecideBranchLifecycle(exhausted)
	if decision.NextStatus != ResearchBranchTerminated || !decision.CancelPendingWork {
		t.Fatalf("terminated=%+v", decision)
	}
	obsolete := base
	obsolete.ContractStillValid = false
	decision, _ = DecideBranchLifecycle(obsolete)
	if decision.NextStatus != ResearchBranchObsolete || !decision.CancelPendingWork {
		t.Fatalf("obsolete=%+v", decision)
	}
}

func TestDecideResearchStopRequiresAllIndependentGates(t *testing.T) {
	facts := RunStopFacts{ContractDeliveryRequirementsMet: true, EvidenceRequirementsMet: true, FreshIntegration: true, EvaluationPassed: true, CitationAuditPassed: true, PreDeliveryDivergencePassed: true, MarginalGainSaturated: true}
	if decision := DecideResearchStop(facts); !decision.MayStop || len(decision.Reasons) != 0 {
		t.Fatalf("decision=%+v", decision)
	}
	facts.BlockingDisputesOpen = 1
	facts.PendingHighValueCandidates = 1
	facts.CancellationPending = true
	want := []string{"blocking_disputes_open", "high_value_work_pending", "cancellation_pending"}
	if decision := DecideResearchStop(facts); decision.MayStop || !reflect.DeepEqual(decision.Reasons, want) {
		t.Fatalf("decision=%+v want=%v", decision, want)
	}
}

func TestDecideResearchStopAllowsBudgetExhaustionOnlyAfterHardGates(t *testing.T) {
	facts := RunStopFacts{ContractDeliveryRequirementsMet: true, EvidenceRequirementsMet: true, FreshIntegration: true, EvaluationPassed: true, CitationAuditPassed: true, PreDeliveryDivergencePassed: true, BudgetExhausted: true}
	if decision := DecideResearchStop(facts); !decision.MayStop {
		t.Fatalf("decision=%+v", decision)
	}
	facts.EvidenceRequirementsMet = false
	if decision := DecideResearchStop(facts); decision.MayStop {
		t.Fatalf("budget bypassed evidence gate: %+v", decision)
	}
}
