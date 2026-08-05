package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
		return e.handleBudgetExhaustion(ctx, run, "wall_time", fmt.Sprintf("run exceeded %d seconds", run.Config.MaxRunSeconds))
	}

	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	inspectKeys := make([]string, 0, len(attempts))
	for _, attempt := range attempts {
		if attempt.Status != AttemptStatusDispatching && attempt.Status != AttemptStatusRunning {
			continue
		}
		if attempt.InboxTaskID != "" {
			inspectKeys = append(inspectKeys, attempt.InboxTaskID)
		} else {
			inspectKeys = append(inspectKeys, attempt.DispatchKey)
		}
	}
	states := map[string]InboxTaskState{}
	if len(inspectKeys) > 0 {
		states, err = e.dispatcher.Inspect(ctx, inspectKeys)
		if err != nil {
			return fmt.Errorf("inspect research attempts: %w", err)
		}
	}
	if _, err = e.store.ReconcileAttempts(ctx, sessionID, states); err != nil {
		return err
	}
	if _, err = e.store.ActivateReadyTasks(ctx, sessionID); err != nil {
		return err
	}

	tasks, err := e.store.ListTasks(ctx, sessionID)
	if err != nil {
		return err
	}
	attempts, err = e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return err
	}
	members, err := e.store.ListFleetMembers(ctx, sessionID, run.WorkspaceID)
	if err != nil {
		return err
	}
	dispatched, err := e.dispatchReady(ctx, run, tasks, attempts, members)
	if err != nil {
		return e.handleDispatchFailure(ctx, sessionID, err)
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
		failed, _, _, failErr := e.store.MarkFailed(ctx, sessionID, reason)
		_, cancelErr := e.cancelPendingAttempts(ctx, failed, "research_remediation_failed")
		if cancelErr != nil {
			return errors.Join(terminalErr, failErr, cancelErr)
		}
		return errors.Join(terminalErr, failErr, e.projectPending(ctx, sessionID))
	}
	task, _, err := e.store.CreateControlTask(ctx, control)
	if err != nil {
		if errors.Is(err, ErrBudgetExhausted) {
			return e.handleBudgetExhaustion(ctx, run, "tasks", err.Error())
		}
		if errors.Is(err, ErrControlTargetChanged) {
			next = e.clock.Now().Add(time.Second)
			return e.projectPending(ctx, sessionID)
		}
		reason := fmt.Sprintf("cannot create %s remediation task: %v", control.Kind, err)
		failed, _, _, failErr := e.store.MarkFailed(ctx, sessionID, reason)
		_, cancelErr := e.cancelPendingAttempts(ctx, failed, "research_run_failed")
		if cancelErr != nil {
			return errors.Join(err, failErr, cancelErr)
		}
		return errors.Join(err, failErr, e.projectPending(ctx, sessionID))
	}
	if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		reason := fmt.Sprintf("%s remediation task is terminal: %s", control.Kind, task.Status)
		failed, _, _, failErr := e.store.MarkFailed(ctx, sessionID, reason)
		_, cancelErr := e.cancelPendingAttempts(ctx, failed, "research_run_failed")
		if cancelErr != nil {
			return errors.Join(failErr, cancelErr)
		}
		return errors.Join(failErr, e.projectPending(ctx, sessionID))
	}
	if _, err = e.store.ActivateReadyTasks(ctx, sessionID); err != nil {
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
		return e.handleDispatchFailure(ctx, sessionID, err)
	}
	next = e.clock.Now().Add(10 * time.Second)
	return e.projectPending(ctx, sessionID)
}

func (e *Engine) handleDispatchFailure(ctx context.Context, sessionID string, err error) error {
	reason := ""
	cancelReason := ""
	switch {
	case errors.Is(err, ErrCapabilityUnavailable):
		reason = err.Error()
		cancelReason = "research_capability_unavailable"
	case !dispatchErrorRetryable(err):
		reason = "non-retryable research task dispatch failed: " + err.Error()
		cancelReason = "research_dispatch_failed"
	default:
		return err
	}
	failed, _, _, failErr := e.store.MarkFailed(ctx, sessionID, reason)
	_, cancelErr := e.cancelPendingAttempts(ctx, failed, cancelReason)
	if cancelErr != nil {
		return errors.Join(err, failErr, cancelErr)
	}
	return errors.Join(err, failErr, e.projectPending(ctx, sessionID))
}

func (e *Engine) handleBudgetExhaustion(ctx context.Context, run Run, budgetKind, details string) error {
	if _, err := e.store.RecordBudgetExhausted(ctx, run.SessionID, budgetKind, details); err != nil {
		return err
	}
	gate, err := e.store.EvaluateGate(ctx, run.SessionID)
	if err != nil {
		return err
	}
	if gate.Passed {
		if _, _, err = e.store.SetAwaitingConfirmation(ctx, run.SessionID, gate); err != nil {
			return err
		}
		return e.projectPending(ctx, run.SessionID)
	}
	failed, _, _, failErr := e.store.MarkFailed(ctx, run.SessionID, "research budget exhausted before delivery gates passed: "+details)
	_, cancelErr := e.cancelPendingAttempts(ctx, failed, "research_budget_exhausted")
	if cancelErr != nil {
		return errors.Join(failErr, cancelErr)
	}
	return errors.Join(failErr, e.projectPending(ctx, run.SessionID))
}

func (e *Engine) cancelPendingAttempts(ctx context.Context, run Run, reason string) (bool, error) {
	if run.SessionID == "" {
		return false, nil
	}
	pending, err := e.store.ListPendingCancellations(ctx, run.SessionID)
	if err != nil || len(pending) == 0 {
		return false, err
	}
	lookupKeys := make([]string, 0, len(pending))
	for _, attempt := range pending {
		if attempt.InboxTaskID == "" {
			lookupKeys = append(lookupKeys, attempt.DispatchKey)
		}
	}
	states := map[string]InboxTaskState{}
	if len(lookupKeys) > 0 {
		states, err = e.dispatcher.Inspect(ctx, lookupKeys)
		if err != nil {
			return true, fmt.Errorf("inspect pending research cancellations: %w", err)
		}
	}
	inboxIDs := make([]string, 0, len(pending))
	completedAttemptIDs := make([]string, 0, len(pending))
	staleAfter := time.Duration(run.Config.StaleAfterSeconds) * time.Second
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	for _, attempt := range pending {
		inboxID := attempt.InboxTaskID
		if inboxID == "" {
			if state, ok := states[attempt.DispatchKey]; ok {
				inboxID = state.ID
			}
		}
		if inboxID != "" {
			inboxIDs = append(inboxIDs, inboxID)
			completedAttemptIDs = append(completedAttemptIDs, attempt.AttemptID)
			continue
		}
		if !e.clock.Now().Before(attempt.DispatchedAt.Add(staleAfter)) {
			completedAttemptIDs = append(completedAttemptIDs, attempt.AttemptID)
		}
	}
	if len(inboxIDs) > 0 {
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		err = e.dispatcher.Cancel(cancelCtx, inboxIDs, reason)
		cancel()
		if err != nil {
			return true, fmt.Errorf("cancel research inbox tasks: %w", err)
		}
	}
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	err = e.store.MarkCancellationsCompleted(markCtx, run.SessionID, completedAttemptIDs)
	cancel()
	if err != nil {
		return true, err
	}
	return len(completedAttemptIDs) < len(pending), nil
}

func (e *Engine) dispatchReady(ctx context.Context, run Run, tasks []Task, attempts []Attempt, members []FleetMember) (int, error) {
	if err := ensureSupportedOrchestratorVersion(run.OrchestratorVersion); err != nil {
		return 0, err
	}
	activeByAgent := map[string]int{}
	activeAttempts := 0
	for _, attempt := range attempts {
		if attempt.Status == AttemptStatusDispatching || attempt.Status == AttemptStatusRunning {
			activeByAgent[attempt.AssignedAgentID]++
			activeAttempts++
		}
	}
	available := run.Config.MaxParallelTasks - activeAttempts
	if available <= 0 {
		return 0, nil
	}
	ready := make([]Task, 0, len(tasks))
	for _, task := range tasks {
		if task.Status == TaskStatusReady && task.GoalVersion == run.GoalVersion && task.PlanVersion == run.PlanVersion {
			if task.ReadyAt != nil && task.ReadyAt.After(e.clock.Now()) {
				continue
			}
			ready = append(ready, task)
		}
	}
	sort.SliceStable(ready, func(i, j int) bool {
		if ready[i].Priority == ready[j].Priority {
			if ready[i].ReadyAt != nil && ready[j].ReadyAt != nil && !ready[i].ReadyAt.Equal(*ready[j].ReadyAt) {
				return ready[i].ReadyAt.Before(*ready[j].ReadyAt)
			}
			if ready[i].ReadyAt != nil && ready[j].ReadyAt == nil {
				return true
			}
			return ready[i].ID < ready[j].ID
		}
		return ready[i].Priority > ready[j].Priority
	})
	dispatched := 0
	for _, task := range ready {
		if dispatched >= available {
			break
		}
		agentID := selectAgent(task, members, activeByAgent)
		if agentID == "" {
			if hasActiveCapability(task, members) {
				continue
			}
			return dispatched, fmt.Errorf("%w: no active fleet member has capability %q for task %s", ErrCapabilityUnavailable, roleForTask(task), task.ID)
		}
		attempt, _, err := e.store.CreateAttempt(ctx, run.SessionID, task.ID, agentID)
		if errors.Is(err, ErrInvalidTransition) {
			continue
		}
		if err != nil {
			return dispatched, err
		}
		snapshot, err := e.store.TaskContext(ctx, task.ID, run.WorkspaceID)
		if err != nil {
			_, _ = e.store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "context_load_failed", Diagnostics: err.Error(), Retryable: true})
			continue
		}
		prompt, err := buildTaskPrompt(run, task, attempt, snapshot, members)
		if err != nil {
			_, _ = e.store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "unsupported_orchestrator_version", Diagnostics: err.Error(), Retryable: false})
			return dispatched, err
		}
		request := DispatchRequest{
			Run:       run,
			Task:      task,
			AttemptID: attempt.ID,
			AgentID:   agentID,
			Key:       attempt.DispatchKey,
			Prompt:    prompt,
		}
		dispatch, err := e.dispatcher.Dispatch(ctx, request)
		if err != nil {
			retryable := dispatchErrorRetryable(err)
			if _, failErr := e.store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "dispatch_failed", Diagnostics: err.Error(), Retryable: retryable}); failErr != nil {
				return dispatched, errors.Join(err, failErr)
			}
			if !retryable {
				return dispatched, fmt.Errorf("dispatch research task %s: %w", task.ID, err)
			}
			continue
		}
		if _, _, err = e.store.AttachInboxTask(ctx, attempt.ID, dispatch.InboxTaskID); err != nil {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			cancelErr := e.dispatcher.Cancel(cancelCtx, []string{dispatch.InboxTaskID}, "research_attempt_no_longer_dispatchable")
			cancel()
			return dispatched, errors.Join(err, cancelErr)
		}
		activeByAgent[agentID]++
		dispatched++
	}
	return dispatched, nil
}

func hasActiveCapability(task Task, members []FleetMember) bool {
	role := roleForTask(task)
	for _, member := range members {
		if member.Status == "active" && strings.EqualFold(strings.TrimSpace(member.Role), role) {
			return true
		}
	}
	return false
}

func selectAgent(task Task, members []FleetMember, active map[string]int) string {
	role := roleForTask(task)
	if pref := strings.TrimSpace(task.AssignedAgentID); pref != "" {
		for _, member := range members {
			if member.AgentID != pref || member.Status != "active" {
				continue
			}
			if active[pref] > 0 {
				break
			}
			// Prefer sticky/reassigned agent when idle; role match preferred but not required
			// for explicit reassign overrides already validated upstream.
			return pref
		}
	}
	candidates := make([]FleetMember, 0, len(members))
	for _, member := range members {
		if member.Status != "active" || !strings.EqualFold(strings.TrimSpace(member.Role), role) {
			continue
		}
		candidates = append(candidates, member)
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		left, right := active[candidates[i].AgentID], active[candidates[j].AgentID]
		if left == right {
			if candidates[i].IsLead != candidates[j].IsLead {
				return candidates[i].IsLead
			}
			return candidates[i].AgentID < candidates[j].AgentID
		}
		return left < right
	})
	for _, candidate := range candidates {
		if active[candidate.AgentID] == 0 {
			return candidate.AgentID
		}
	}
	return ""
}

func roleForTask(task Task) string {
	if validCapability(task.RequiredCapability) {
		return strings.ToLower(strings.TrimSpace(task.RequiredCapability))
	}
	switch task.Kind {
	case TaskKindPlan, TaskKindReplan:
		return "lead"
	case TaskKindDiscover:
		return "scout"
	case TaskKindDeepRead:
		return "reader"
	case TaskKindVerify, TaskKindCounterSearch, TaskKindQualityGate, TaskKindCitationAudit:
		return "validator"
	case TaskKindSynthesize:
		return "reporter"
	default:
		return "lead"
	}
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

func terminalRemediationFailure(run Run, tasks []Task, kind TaskKind, objective string) (string, bool) {
	planningSucceeded := false
	var terminalInitialPlan *Task
	var terminalSameControl *Task
	for i := range tasks {
		task := &tasks[i]
		if task.GoalVersion != run.GoalVersion || task.PlanVersion != run.PlanVersion {
			continue
		}
		if (task.Kind == TaskKindPlan || task.Kind == TaskKindReplan) && task.Status == TaskStatusSucceeded {
			planningSucceeded = true
		}
		terminal := task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed
		if terminal && task.Kind == TaskKindPlan {
			terminalInitialPlan = task
		}
		if terminal && task.Kind == kind && task.Objective == objective && strings.HasPrefix(task.ClientKey, "control:") {
			terminalSameControl = task
		}
	}
	if kind == TaskKindReplan && !planningSucceeded && terminalInitialPlan != nil {
		return terminalResearchTaskReason("initial research plan", *terminalInitialPlan), true
	}
	if terminalSameControl != nil {
		return terminalResearchTaskReason("research remediation", *terminalSameControl), true
	}
	return "", false
}

func terminalResearchTaskReason(label string, task Task) string {
	reason := strings.TrimSpace(task.TerminalReason)
	if reason == "" {
		reason = string(task.Status)
	}
	return fmt.Sprintf("%s task %s exhausted its attempts: %s", label, task.ID, reason)
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
