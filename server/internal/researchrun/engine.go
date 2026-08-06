package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Engine struct {
	store      Store
	dispatcher Dispatcher
	projector  Projector
	clock      Clock
}

func NewEngine(store Store, dispatcher Dispatcher, projector Projector) *Engine {
	return &Engine{store: store, dispatcher: dispatcher, projector: projector, clock: systemClock{}}
}

func (e *Engine) Create(ctx context.Context, in StartInput) (Run, error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, _, err := e.store.CreateRun(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if err = e.ReconcileSession(ctx, run.SessionID); err != nil {
		return run, err
	}
	return e.store.GetRun(ctx, run.SessionID, in.WorkspaceID)
}

func (e *Engine) Start(ctx context.Context, in StartInput) (Run, error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return Run{}, errors.New("research run engine is unavailable")
	}
	run, _, err := e.store.InitializeRun(ctx, in, DefaultRunConfig(in.DepthTier))
	if err != nil {
		return Run{}, err
	}
	if err = e.ReconcileSession(ctx, in.SessionID); err != nil {
		return run, err
	}
	return e.store.GetRun(ctx, in.SessionID, in.WorkspaceID)
}

func (e *Engine) SubmitResult(ctx context.Context, sessionID, workspaceID, taskID, attemptID, agentID, inboxTaskID string, raw json.RawMessage) (AcceptResultOutcome, error) {
	outcome, err := (resultAcceptanceModule{store: e.store}).Accept(ctx, resultSubmission{
		SessionID: sessionID, WorkspaceID: workspaceID, TaskID: taskID,
		AttemptID: attemptID, AgentID: agentID, InboxTaskID: inboxTaskID, Raw: raw,
	})
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	if err = e.ReconcileSession(ctx, sessionID); err != nil {
		return outcome, fmt.Errorf("result accepted but run advancement failed: %w", err)
	}
	return outcome, nil
}

func (e *Engine) ReconcileDue(ctx context.Context, limit int) (int, error) {
	ids, err := e.store.ListDueRunIDs(ctx, limit)
	if err != nil {
		return 0, err
	}
	processed := 0
	errs := []error{}
	for _, sessionID := range ids {
		if err = e.ReconcileSession(ctx, sessionID); err != nil {
			errs = append(errs, fmt.Errorf("research run %s: %w", sessionID, err))
			continue
		}
		processed++
	}
	return processed, errors.Join(errs...)
}

func (e *Engine) ReconcileSession(ctx context.Context, sessionID string) (retErr error) {
	if e == nil || e.store == nil || e.dispatcher == nil {
		return errors.New("research run engine is unavailable")
	}
	token := uuid.NewString()
	run, claimed, err := e.store.ClaimRun(ctx, sessionID, token, 45*time.Second)
	if err != nil {
		return err
	}
	if !claimed {
		return nil
	}
	next := e.clock.Now().Add(15 * time.Second)
	defer func() {
		if err := e.store.ReleaseRun(context.WithoutCancel(ctx), sessionID, token, next); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("release research run lease: %w", err))
		}
	}()
	pendingCancellations, cancelErr := e.cancelPendingAttempts(ctx, run, "research_run_"+string(run.Status))
	if cancelErr != nil {
		next = e.clock.Now().Add(10 * time.Second)
		return cancelErr
	}
	if pendingCancellations {
		next = e.clock.Now().Add(10 * time.Second)
		return e.projectPending(ctx, sessionID)
	}
	if run.Status != RunStatusRunning {
		next = e.clock.Now().Add(time.Hour)
		return e.projectPending(ctx, sessionID)
	}
	if run.InitializedAt != nil && run.Config.MaxRunSeconds > 0 && e.clock.Now().After(run.InitializedAt.Add(time.Duration(run.Config.MaxRunSeconds)*time.Second)) {
		return e.failureModule().HandleBudgetExhaustion(ctx, run, "wall_time", fmt.Sprintf("run exceeded %d seconds", run.Config.MaxRunSeconds))
	}

	if err = e.executionModule().SyncAttempts(ctx, sessionID); err != nil {
		return err
	}

	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	members, err := e.store.ListFleetMembers(ctx, sessionID, run.WorkspaceID)
	if err != nil {
		return err
	}
	dispatched, err := e.dispatchReady(ctx, run, tasks, attempts, members)
	if err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}
	if dispatched > 0 || hasActiveCurrentWork(run, tasks) {
		next = e.clock.Now().Add(10 * time.Second)
		return e.projectPending(ctx, sessionID)
	}

	gate, err := e.store.EvaluateGate(ctx, sessionID)
	if err != nil {
		return err
	}
	if gate.Passed {
		if _, _, err = e.store.SetAwaitingConfirmation(ctx, sessionID, gate); err != nil {
			return err
		}
		next = e.clock.Now().Add(time.Hour)
		return e.projectPending(ctx, sessionID)
	}

	control := remediationTask(gate)
	control.SessionID = sessionID
	if reason, terminal := terminalRemediationFailure(run, tasks, control.Kind, control.Objective); terminal {
		terminalErr := errors.New(reason)
		return e.failureModule().FailRun(ctx, sessionID, reason, "research_remediation_failed", terminalErr)
	}
	task, _, err := e.store.CreateControlTask(ctx, control)
	if err != nil {
		if errors.Is(err, ErrBudgetExhausted) {
			return e.failureModule().HandleBudgetExhaustion(ctx, run, "tasks", err.Error())
		}
		if errors.Is(err, ErrControlTargetChanged) {
			next = e.clock.Now().Add(time.Second)
			return e.projectPending(ctx, sessionID)
		}
		reason := fmt.Sprintf("cannot create %s remediation task: %v", control.Kind, err)
		return e.failureModule().FailRun(ctx, sessionID, reason, "research_run_failed", err)
	}
	if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		reason := fmt.Sprintf("%s remediation task is terminal: %s", control.Kind, task.Status)
		return e.failureModule().FailRun(ctx, sessionID, reason, "research_run_failed", nil)
	}
	if err = e.executionModule().ActivateReadyTasks(ctx, sessionID); err != nil {
		return err
	}
	tasks, err = e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err = e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	if _, err = e.dispatchReady(ctx, run, tasks, attempts, members); err != nil {
		return e.failureModule().HandleDispatchFailure(ctx, sessionID, err)
	}
	next = e.clock.Now().Add(10 * time.Second)
	return e.projectPending(ctx, sessionID)
}

func hasActiveCurrentWork(run Run, tasks []Task) bool {
	for _, task := range tasks {
		if task.GoalVersion != run.GoalVersion || task.PlanVersion != run.PlanVersion {
			continue
		}
		switch task.Status {
		case TaskStatusPending, TaskStatusReady, TaskStatusDispatching, TaskStatusRunning:
			return true
		}
	}
	return false
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

func (e *Engine) Pause(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, _, _, err := e.store.Pause(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_run_paused"); err != nil {
		return run, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Resume(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	run, _, err := e.store.Resume(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, e.ReconcileSession(ctx, sessionID)
}

func (e *Engine) Cancel(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, error) {
	run, _, _, err := e.store.Cancel(ctx, sessionID, workspaceID, userID, reason)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_run_cancelled"); err != nil {
		return run, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Archive(ctx context.Context, sessionID, workspaceID, userID, reason string) (Run, error) {
	run, _, _, err := e.store.Archive(ctx, sessionID, workspaceID, userID, reason)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_run_archived"); err != nil {
		return run, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Confirm(ctx context.Context, sessionID, workspaceID, userID string) (Run, error) {
	gate, err := e.store.EvaluateGate(ctx, sessionID)
	if err != nil {
		return Run{}, err
	}
	if !gate.Passed {
		return Run{}, fmt.Errorf("%w: delivery gate failed: %s", ErrInvalidTransition, gateObjective("", gate))
	}
	run, _, err := e.store.Complete(ctx, sessionID, workspaceID, userID)
	if err != nil {
		return Run{}, err
	}
	return run, e.projectPending(ctx, sessionID)
}

func (e *Engine) Snapshot(ctx context.Context, sessionID, workspaceID string) (RunSnapshot, error) {
	run, err := e.store.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	contract, err := e.store.GetCurrentContract(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	method, err := e.store.GetCurrentMethod(ctx, sessionID, workspaceID)
	if err != nil {
		return RunSnapshot{}, err
	}
	questions, err := e.store.ListQuestions(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	sources, err := e.store.ListSourceSnapshots(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	observations, err := e.store.ListObservations(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	claims, err := e.store.ListClaims(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	gate, err := e.store.EvaluateGate(ctx, sessionID)
	if err != nil {
		return RunSnapshot{}, err
	}
	return RunSnapshot{
		Run: run, Contract: contract, Method: method, Questions: questions, Tasks: tasks, Attempts: attempts,
		Sources: sources, Observations: observations, Claims: claims, Gate: gate,
	}, nil
}

// ListFleetMembers returns the session-bound research fleet roster used for
// presence/dispatch (LRM-1377 follow-up).
func (e *Engine) ListFleetMembers(ctx context.Context, sessionID, workspaceID string) ([]FleetMember, error) {
	return e.store.ListFleetMembers(ctx, sessionID, workspaceID)
}

func (e *Engine) Steer(ctx context.Context, in SteerInput) (Run, error) {
	run, _, _, err := e.store.Steer(ctx, in)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_goal_steered"); err != nil {
		return run, err
	}
	if _, _, err = e.store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: in.SessionID, Kind: TaskKindReplan,
		Objective:  "Create a new evidence-oriented plan for the revised user goal. Treat earlier-version artifacts as audit history only.",
		Capability: "lead", Priority: 1, Rationale: "The user changed the durable research goal.",
	}); err != nil {
		return run, err
	}
	return run, e.ReconcileSession(ctx, in.SessionID)
}

// NodeCommand applies continue|fork|retry|reassign from a canvas node, then
// reconciles so ready tasks can dispatch (LRM-1413 / LRM-1408).
func (e *Engine) NodeCommand(ctx context.Context, in NodeCommandInput) (NodeCommandOutcome, error) {
	outcome, err := e.store.NodeCommand(ctx, in)
	if err != nil {
		return NodeCommandOutcome{}, err
	}
	if !outcome.Replayed {
		if recErr := e.ReconcileSession(ctx, in.SessionID); recErr != nil {
			// Command already committed; surface reconcile failure without rolling back.
			return outcome, recErr
		}
	}
	if outcome.Task != nil {
		if latest, getErr := e.store.GetTask(ctx, outcome.Task.ID, in.SessionID); getErr == nil {
			outcome.Task = &latest
			if aid := strings.TrimSpace(latest.AssignedAgentID); aid != "" {
				outcome.Assigned = &aid
			}
			outcome.Queued = latest.Status == TaskStatusReady || latest.Status == TaskStatusPending ||
				latest.Status == TaskStatusDispatching || latest.Status == TaskStatusRunning
		}
	}
	return outcome, nil
}
