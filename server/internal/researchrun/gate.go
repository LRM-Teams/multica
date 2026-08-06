package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type deliveryGateStore interface {
	EvaluateGate(context.Context, string) (GateResult, error)
	SetAwaitingConfirmation(context.Context, string, GateResult) (Run, RunEvent, error)
	CreateControlTask(context.Context, ControlTaskInput) (Task, RunEvent, error)
	Complete(context.Context, string, string, string) (Run, RunEvent, error)
}

type gateFailureHandler interface {
	FailRun(context.Context, string, string, string, error) error
	HandleBudgetExhaustion(context.Context, Run, string, string) error
}

type gateAdvanceOutcome struct {
	RemediationCreated bool
	NextReconcileAfter time.Duration
}

type deliveryGateModule struct {
	store      deliveryGateStore
	failures   gateFailureHandler
	projection pendingEventProjector
}

func (module deliveryGateModule) Evaluate(ctx context.Context, sessionID string) (GateResult, error) {
	return module.store.EvaluateGate(ctx, sessionID)
}

// Advance evaluates the current canonical graph and either transitions the Run
// to confirmation or creates exactly one minimal remediation task. Execution of
// a newly created task remains the responsibility of Execution Module.
func (module deliveryGateModule) Advance(ctx context.Context, run Run, tasks []Task) (gateAdvanceOutcome, error) {
	gate, err := module.store.EvaluateGate(ctx, run.SessionID)
	if err != nil {
		return gateAdvanceOutcome{}, err
	}
	if gate.Passed {
		if _, _, err = module.store.SetAwaitingConfirmation(ctx, run.SessionID, gate); err != nil {
			return gateAdvanceOutcome{}, err
		}
		if err = module.projection.ProjectPending(ctx, run.SessionID); err != nil {
			return gateAdvanceOutcome{}, err
		}
		return gateAdvanceOutcome{NextReconcileAfter: time.Hour}, nil
	}

	control := remediationTask(gate)
	control.SessionID = run.SessionID
	if reason, terminal := terminalRemediationFailure(run, tasks, control.Kind, control.Objective); terminal {
		terminalErr := errors.New(reason)
		return gateAdvanceOutcome{}, module.failures.FailRun(ctx, run.SessionID, reason, "research_remediation_failed", terminalErr)
	}
	task, _, err := module.store.CreateControlTask(ctx, control)
	if err != nil {
		switch {
		case errors.Is(err, ErrBudgetExhausted):
			return gateAdvanceOutcome{}, module.failures.HandleBudgetExhaustion(ctx, run, "tasks", err.Error())
		case errors.Is(err, ErrControlTargetChanged):
			if projectErr := module.projection.ProjectPending(ctx, run.SessionID); projectErr != nil {
				return gateAdvanceOutcome{}, projectErr
			}
			return gateAdvanceOutcome{NextReconcileAfter: time.Second}, nil
		default:
			reason := fmt.Sprintf("cannot create %s remediation task: %v", control.Kind, err)
			return gateAdvanceOutcome{}, module.failures.FailRun(ctx, run.SessionID, reason, "research_run_failed", err)
		}
	}
	if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		reason := fmt.Sprintf("%s remediation task is terminal: %s", control.Kind, task.Status)
		return gateAdvanceOutcome{}, module.failures.FailRun(ctx, run.SessionID, reason, "research_run_failed", nil)
	}
	return gateAdvanceOutcome{RemediationCreated: true}, nil
}

func (module deliveryGateModule) Confirm(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	gate, err := module.store.EvaluateGate(ctx, sessionID)
	if err != nil {
		return Run{}, err
	}
	if !gate.Passed {
		return Run{}, fmt.Errorf("%w: delivery gate failed: %s", ErrInvalidTransition, gateObjective("", gate))
	}
	run, _, err := module.store.Complete(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, nil
}

func remediationTask(gate GateResult) ControlTaskInput {
	codes := map[string]bool{}
	for _, finding := range gate.Findings {
		codes[finding.Code] = true
	}
	control := func(kind TaskKind, capability, rationale, objective string, targetCodes ...string) ControlTaskInput {
		target := filterGateFindings(gate, targetCodes...)
		return ControlTaskInput{
			Kind: kind, Capability: capability, Priority: 1,
			Objective: gateObjective(objective, target), Findings: target.Findings,
			ObservedFindings: gate.Findings, Rationale: rationale,
		}
	}
	controlOne := func(kind TaskKind, capability, rationale, objective string, targetCodes ...string) ControlTaskInput {
		out := control(kind, capability, rationale, objective, targetCodes...)
		target := firstGateFinding(out.Findings)
		out.Findings = target.Findings
		out.Objective = gateObjective(objective, target)
		return out
	}
	if codes["plan_incomplete"] || codes["research_method_missing"] || codes["required_questions_missing"] ||
		codes["tasks_blocked"] {
		return control(TaskKindReplan, "lead", "The accepted method or executable task graph is structurally incomplete.",
			"Repair the research method and task graph. Preserve still-valid scope and evidence, replace only invalid or blocked work, and produce an executable plan.",
			"plan_incomplete", "research_method_missing", "required_questions_missing", "tasks_blocked")
	}
	if codes["claim_counterevidence_search_missing"] || codes["report_conflicts_unresolved"] {
		return controlOne(TaskKindCounterSearch, "validator", "The evidence graph requires a targeted adversarial test or conflict resolution.",
			"Target the identified Claim and search for evidence that could falsify, qualify, or reconcile it. Update the Claim resolution from verified observations; do not rewrite the research plan.",
			"claim_counterevidence_search_missing", "report_conflicts_unresolved")
	}
	if codes["required_questions_unanswered"] {
		questionID := findingMetadataString(gate.Findings, "required_questions_unanswered", "question_id")
		if findingMetadataString(gate.Findings, "required_questions_unanswered", "answer_claim_id") != "" &&
			!findingMetadataBool(gate.Findings, "required_questions_unanswered", "has_verified_support") {
			out := control(TaskKindVerify, "validator", "The highest-value required Question has an answer Claim but lacks verified support.",
				"Verify the bound Question's existing answer Claim against the accepted Evidence Standard. Reuse its stable Claim key, add exact verified support, and update coverage only from the verified result.",
				"required_questions_unanswered")
			out.QuestionID = questionID
			return out
		}
		out := control(TaskKindDiscover, "scout", "The highest-priority required question still lacks an accepted evidence-backed answer.",
			"Answer the bound required question with new, directly relevant evidence. Return an answer Claim and a measured coverage increase; do not broaden the plan.",
			"required_questions_unanswered")
		out.QuestionID = questionID
		return out
	}
	if codes["independent_sources_insufficient"] || codes["claim_evidence_standard_missing"] ||
		codes["claim_evidence_standard_unmet"] || codes["report_claims_unsupported"] || codes["major_claim_sources_insufficient"] {
		return controlOne(TaskKindVerify, "validator", "A Claim or required answer lacks evidence that satisfies its accepted method.",
			"Verify the identified Claim against its accepted evidence standard. Add only evidence that repairs the stated independence, source-trait, strength, directness, or method-fit deficit; do not rewrite the plan.",
			"independent_sources_insufficient", "claim_evidence_standard_missing", "claim_evidence_standard_unmet", "report_claims_unsupported", "major_claim_sources_insufficient")
	}
	if codes["report_missing"] || codes["report_claims_missing"] || codes["major_claims_unlinked"] ||
		codes["required_answers_unreported"] || codes["report_structure_incomplete"] || codes["report_author_missing"] ||
		codes["report_claims_stale"] || codes["report_stale_after_evidence"] || codes["quality_evaluation_failed"] || codes["citation_audit_failed"] {
		return control(TaskKindSynthesize, "reporter", "The evidence ledger is usable but the current report does not represent it correctly.",
			"Revise the report from the current normalized Claims and verified evidence. Repair every stated structure, coverage, quality, citation, or version defect without changing the research plan.",
			"report_missing", "report_claims_missing", "major_claims_unlinked", "required_answers_unreported", "report_structure_incomplete", "report_author_missing", "report_claims_stale", "report_stale_after_evidence", "quality_evaluation_failed", "citation_audit_failed")
	}
	if codes["quality_evaluation_missing"] || codes["quality_evaluation_not_independent"] {
		return control(TaskKindQualityGate, "validator", "The current report lacks a valid independent quality evaluation.",
			"Independently evaluate the latest report for factual grounding, coverage, analytical depth, source quality, contradiction handling, instruction adherence, and readability.",
			"quality_evaluation_missing", "quality_evaluation_not_independent")
	}
	if codes["citation_audit_missing"] || codes["citation_audit_not_independent"] {
		return control(TaskKindCitationAudit, "validator", "The current report lacks a valid independent citation audit.",
			"Audit every latest-report Claim against exact observations and source snapshots. Fail unsupported, misquoted, stale, or unresolved contradictory Claims.",
			"citation_audit_missing", "citation_audit_not_independent")
	}
	if codes["marginal_gain_not_saturated"] {
		return control(TaskKindDiscover, "scout", "The configured stopping rule requires another measured exploration batch.",
			"Explore the highest-impact unresolved frontier and return one bounded evidence batch. Maximize information gain, record negative findings, and do not rewrite the accepted plan.",
			"marginal_gain_not_saturated")
	}
	return control(TaskKindReplan, "lead", "No narrower remediation action is defined for the observed gate findings.",
		"Inspect the remaining gate findings and create the smallest evidence-producing task graph that resolves them while preserving valid artifacts.")
}

func filterGateFindings(gate GateResult, codes ...string) GateResult {
	if len(codes) == 0 {
		return gate
	}
	wanted := make(map[string]struct{}, len(codes))
	for _, code := range codes {
		wanted[code] = struct{}{}
	}
	findings := make([]GateFinding, 0, len(gate.Findings))
	for _, finding := range gate.Findings {
		if _, ok := wanted[finding.Code]; ok {
			findings = append(findings, finding)
		}
	}
	return GateResult{Passed: len(findings) == 0, Findings: findings}
}

func firstGateFinding(findings []GateFinding) GateResult {
	if len(findings) == 0 {
		return GateResult{Passed: true}
	}
	return GateResult{Findings: []GateFinding{findings[0]}}
}

func findingMetadataString(findings []GateFinding, code, key string) string {
	for _, finding := range findings {
		if finding.Code != code || finding.Metadata == nil {
			continue
		}
		if value, ok := finding.Metadata[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func findingMetadataBool(findings []GateFinding, code, key string) bool {
	for _, finding := range findings {
		if finding.Code != code || finding.Metadata == nil {
			continue
		}
		if value, ok := finding.Metadata[key].(bool); ok {
			return value
		}
	}
	return false
}

func gateObjective(prefix string, gate GateResult) string {
	encoded, _ := json.Marshal(gate.Findings)
	return prefix + "\n\nDeterministic gate findings:\n" + string(encoded)
}

func (e *Engine) gateModule() deliveryGateModule {
	return deliveryGateModule{
		store:      e.store,
		failures:   e.failureModule(),
		projection: projectionModule{store: e.store, output: e.projector, clock: e.clock},
	}
}
