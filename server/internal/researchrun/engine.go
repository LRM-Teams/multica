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
	run, err := e.store.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	task, err := e.store.GetTask(ctx, taskID, sessionID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	attempts, err := e.store.ListAttempts(ctx, sessionID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	found := false
	for _, attempt := range attempts {
		if attempt.ID == attemptID && attempt.TaskID == taskID {
			found = true
			if attempt.InboxTaskID == "" || attempt.InboxTaskID != inboxTaskID {
				return AcceptResultOutcome{}, ErrAttemptNotAssigned
			}
			break
		}
	}
	if !found {
		return AcceptResultOutcome{}, ErrRunNotFound
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	members, err := e.store.ListFleetMembers(ctx, sessionID, workspaceID)
	if err != nil {
		return AcceptResultOutcome{}, err
	}
	if missing := missingResultCapabilities(result, members); len(missing) > 0 {
		return AcceptResultOutcome{}, fmt.Errorf(
			"%w: no active fleet member has role(s) %s; the lead must hire, optimize, and activate those specialties before retrying the same result",
			ErrCapabilityUnavailable,
			strings.Join(missing, ", "),
		)
	}
	outcome, err := e.store.AcceptResult(ctx, AcceptResultInput{
		SessionID:   sessionID,
		AttemptID:   attemptID,
		AgentID:     agentID,
		InboxTaskID: inboxTaskID,
		Raw:         raw,
		Result:      result,
		Hash:        hash,
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

	kind, objective, capability := remediationTask(gate)
	if reason, terminal := terminalRemediationFailure(run, tasks, kind, objective); terminal {
		terminalErr := errors.New(reason)
		failed, _, _, failErr := e.store.MarkFailed(ctx, sessionID, reason)
		_, cancelErr := e.cancelPendingAttempts(ctx, failed, "research_remediation_failed")
		if cancelErr != nil {
			return errors.Join(terminalErr, failErr, cancelErr)
		}
		return errors.Join(terminalErr, failErr, e.projectPending(ctx, sessionID))
	}
	task, _, err := e.store.CreateControlTask(ctx, sessionID, kind, objective, capability, 1)
	if err != nil {
		if errors.Is(err, ErrBudgetExhausted) {
			return e.handleBudgetExhaustion(ctx, run, "tasks", err.Error())
		}
		reason := fmt.Sprintf("cannot create %s remediation task: %v", kind, err)
		failed, _, _, failErr := e.store.MarkFailed(ctx, sessionID, reason)
		_, cancelErr := e.cancelPendingAttempts(ctx, failed, "research_run_failed")
		if cancelErr != nil {
			return errors.Join(err, failErr, cancelErr)
		}
		return errors.Join(err, failErr, e.projectPending(ctx, sessionID))
	}
	if task.Status == TaskStatusBlocked || task.Status == TaskStatusFailed || task.Status == TaskStatusCancelled {
		reason := fmt.Sprintf("%s remediation task is terminal: %s", kind, task.Status)
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

func missingResultCapabilities(result ResultEnvelope, members []FleetMember) []string {
	active := map[string]struct{}{}
	for _, member := range members {
		if member.Status == "active" {
			active[strings.ToLower(strings.TrimSpace(member.Role))] = struct{}{}
		}
	}
	missing := map[string]struct{}{}
	check := func(tasks []TaskProposal) {
		for _, task := range tasks {
			capability := strings.ToLower(strings.TrimSpace(task.RequiredCapability))
			if _, ok := active[capability]; !ok {
				missing[capability] = struct{}{}
			}
		}
	}
	if result.Plan != nil {
		check(result.Plan.Tasks)
	}
	check(result.ProposedTasks)
	out := make([]string, 0, len(missing))
	for capability := range missing {
		out = append(out, capability)
	}
	sort.Strings(out)
	return out
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

func remediationTask(gate GateResult) (TaskKind, string, string) {
	codes := map[string]bool{}
	for _, finding := range gate.Findings {
		codes[finding.Code] = true
	}
	if codes["plan_incomplete"] {
		return TaskKindReplan, gateObjective("Repair the research plan and replace blocked work with an executable evidence plan.", gate), "lead"
	}
	evidenceGap := codes["tasks_blocked"] || codes["required_questions_missing"] ||
		codes["required_questions_unanswered"] || codes["independent_sources_insufficient"] ||
		codes["report_claims_unsupported"] || codes["major_claim_sources_insufficient"] || codes["report_claims_stale"] || codes["report_conflicts_unresolved"] ||
		codes["citation_audit_failed"]
	if evidenceGap {
		return TaskKindReplan, gateObjective("Revise the plan to remediate every delivery-gate finding. Preserve valid evidence, add targeted verification and counter-search work, then require a new report revision.", gate), "lead"
	}
	if codes["report_missing"] || codes["report_claims_missing"] || codes["major_claims_unlinked"] ||
		codes["required_answers_unreported"] || codes["report_structure_incomplete"] || codes["report_author_missing"] || codes["quality_evaluation_failed"] {
		return TaskKindSynthesize, gateObjective("Produce a decision-useful report from the normalized claims and verified evidence. Link every report section to claim keys.", gate), "reporter"
	}
	if codes["quality_evaluation_missing"] || codes["quality_evaluation_not_independent"] {
		return TaskKindQualityGate, gateObjective("Independently evaluate the latest report. Check factual grounding, coverage, analytical depth, source quality, contradiction handling, instruction adherence, and readability.", gate), "validator"
	}
	if codes["citation_audit_missing"] || codes["citation_audit_not_independent"] {
		return TaskKindCitationAudit, gateObjective("Audit every latest-report claim against exact observations and source snapshots. Fail unsupported, misquoted, or unresolved contradictory claims.", gate), "validator"
	}
	return TaskKindReplan, gateObjective("Inspect the remaining gate findings and create the smallest evidence-producing remediation graph that resolves all of them.", gate), "lead"
}

func gateObjective(prefix string, gate GateResult) string {
	encoded, _ := json.Marshal(gate.Findings)
	return prefix + "\n\nDeterministic gate findings:\n" + string(encoded)
}

func buildTaskPrompt(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) (string, error) {
	switch run.OrchestratorVersion {
	case OrchestratorVersionV1:
		return buildTaskPromptV1(run, task, attempt, snapshot, members), nil
	case OrchestratorVersionV2:
		return buildTaskPromptV2(run, task, attempt, snapshot, members), nil
	default:
		return "", fmt.Errorf("%w: %q", ErrUnsupportedVersion, run.OrchestratorVersion)
	}
}

// buildTaskPromptV1 is immutable for active research-run-v1 runs. Behavioral
// prompt changes require a new orchestrator version and a retained v1 builder.
func buildTaskPromptV1(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Acceptance criteria: `%s`\n", string(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n")
	b.WriteString("\nCurrent required questions:\n")
	for _, question := range snapshot.Questions {
		if question.GoalVersion == run.GoalVersion && question.PlanVersion == run.PlanVersion && question.Required {
			fmt.Fprintf(&b, "- `%s` [%s, coverage %.2f]: %s\n", question.ClientKey, question.Status, question.Coverage, question.Question)
		}
	}
	fmt.Fprintf(&b, "\nCanonical evidence ledger: %d source snapshots, %d observations, %d claims. Read them with `multica research session get %s --output json`; chat messages are not evidence.\n", len(snapshot.Sources), len(snapshot.Observations), len(snapshot.Claims), run.SessionID)
	b.WriteString("\nExecution contract:\n")
	b.WriteString("1. Inspect current state with `multica research session get ")
	b.WriteString(run.SessionID)
	b.WriteString("` before working.\n")
	b.WriteString("2. Explore actively and use primary/independent sources where available. A source must include the retrieved text snapshot; every quote must occur exactly in that snapshot.\n")
	b.WriteString("3. Return one JSON object with schema_version=1 and a globally unique client_request_id. Use only these top-level fields: schema_version, client_request_id, summary, questions, plan, sources, observations, claims, proposed_tasks, report, evaluation, coverage_delta, confidence, incomplete_reason.\n")
	b.WriteString("4. Question/task/source/observation/claim client keys must be stable strings. Source fields: client_key,url,title,publisher,source_class,independence_key,retrieved_at,snapshot_text,metadata. Observation fields: client_key,source_key,quote,datum,locator,interpretation. Claim fields: client_key,text,significance,confidence,status,resolution,evidence; evidence uses observation_key,relation,strength,rationale.\n")
	b.WriteString("5. Plan results require plan.questions, plan.tasks, inclusion_criteria, exclusion_criteria, source_strategy, uncertainties, planning_risks. Synthesize results require report.content_md, report.structured, report.claims[{claim_key,section_id}]. Quality/citation results require evaluation with passed plus seven 0..1 scores and findings.\n")
	b.WriteString("6. Every required_capability must exactly match an active fleet role. If research needs a missing specialty, the lead must hire, optimize, and activate it before submitting the plan.\n")
	switch task.Kind {
	case TaskKindVerify, TaskKindCounterSearch:
		b.WriteString("7. Include every source, observation, claim, and evidence link being verified in this result. Reuse stable keys and exact content from the ledger when corroborating existing artifacts; deduplication upgrades their verification state transactionally.\n")
	case TaskKindSynthesize:
		b.WriteString("7. Link report sections to current normalized claim keys from the ledger. Existing claims do not need to be copied into the result.\n")
	case TaskKindQualityGate, TaskKindCitationAudit:
		b.WriteString("7. Evaluate the latest report revision and current evidence ledger independently. Return a failing evaluation when any gate dimension is below the rubric; do not add evidence merely to make the audit pass.\n")
	default:
		b.WriteString("7. Keep result artifacts scoped to this assignment and create follow-up tasks only when they resolve an identified frontier gap.\n")
	}
	b.WriteString("8. Submit the JSON exactly once with:\n\n")
	fmt.Fprintf(&b, "```bash\nmultica research task-result %s %s %s --file /absolute/path/research-result.json\n```\n", run.SessionID, task.ID, attempt.ID)
	b.WriteString("\nDo not use graph-append, source-upsert, report-patch, or stage-eval for this task. Do not claim completion in chat before task-result succeeds.\n")
	return b.String()
}

func buildTaskPromptV2(run Run, task Task, attempt Attempt, snapshot RunSnapshot, members []FleetMember) string {
	var b strings.Builder
	b.WriteString("## Durable Research Run task\n\n")
	fmt.Fprintf(&b, "- Session ID: `%s`\n- Task ID: `%s`\n- Attempt ID: `%s`\n- Dispatch key: `%s`\n", run.SessionID, task.ID, attempt.ID, attempt.DispatchKey)
	fmt.Fprintf(&b, "- Goal version: %d\n- Plan version: %d\n- Task kind: `%s`\n- Expected result: `%s`\n", task.GoalVersion, task.PlanVersion, task.Kind, task.ExpectedResult)
	fmt.Fprintf(&b, "- Research goal: %s\n- Objective: %s\n", run.Goal, task.Objective)
	fmt.Fprintf(&b, "- Contract language: %s\n- Contract audience: %s\n- Contract freshness: %s\n", snapshot.Contract.Language, snapshot.Contract.Audience, snapshot.Contract.Freshness)
	fmt.Fprintf(&b, "- Contract scope: `%s`\n- Source policy: `%s`\n", compactJSON(snapshot.Contract.Scope), compactJSON(snapshot.Contract.SourcePolicy))
	if len(task.AcceptanceCriteria) > 0 {
		fmt.Fprintf(&b, "- Acceptance criteria: `%s`\n", string(task.AcceptanceCriteria))
	}
	b.WriteString("- Active fleet roles:")
	for _, member := range members {
		if member.Status == "active" {
			fmt.Fprintf(&b, " `%s`", strings.ToLower(strings.TrimSpace(member.Role)))
		}
	}
	b.WriteString("\n\nCurrent required questions:\n")
	for _, question := range snapshot.Questions {
		if question.GoalVersion == run.GoalVersion && question.PlanVersion == run.PlanVersion && question.Required {
			fmt.Fprintf(&b, "- `%s` [%s, coverage %.2f]: %s\n", question.ClientKey, question.Status, question.Coverage, question.Question)
		}
	}
	fmt.Fprintf(&b, "\nCanonical evidence ledger: %d source snapshots, %d observations, %d claims. Read the complete session with `multica research session get %s --output json`; chat messages are not evidence.\n", len(snapshot.Sources), len(snapshot.Observations), len(snapshot.Claims), run.SessionID)

	b.WriteString("\nExecution contract:\n")
	b.WriteString("1. Inspect current state before working. Return exactly one strict JSON object with schema_version=2 and a globally unique client_request_id. Allowed top-level fields: schema_version, client_request_id, summary, questions, plan, sources, observations, claims, proposed_tasks, report, evaluation, answer_claim_key, coverage_delta, confidence, incomplete_reason.\n")
	b.WriteString("2. Preserve retrieved source text in bounded snapshots. Source fields are client_key,url,title,publisher,source_class,independence_key,retrieved_at,snapshot_text,metadata. Observation fields are client_key,source_key,quote,datum,locator,interpretation. Claim fields are client_key,text,significance,confidence,status,resolution,evidence; evidence uses observation_key,relation,strength,rationale. Every Observation quote must occur exactly in its snapshot. Separate independent source families and record counterevidence.\n")
	b.WriteString("3. Every proposed task uses an active fleet role and this exact expected_result mapping: plan/replan=research_plan_v2; discover/deep_read/verify/counter_search=research_evidence_v2; synthesize=research_report_v2; quality_gate=research_quality_evaluation_v2; citation_audit=research_citation_audit_v2. Delivery roles are fixed: synthesize=reporter; quality_gate=validator; citation_audit=validator. Every plan includes all three delivery tasks, and both audit tasks directly depend on a synthesize task.\n")
	b.WriteString("4. A question-scoped evidence result that increases coverage supplies answer_claim_key pointing to one Claim in that result.\n")
	b.WriteString("5. A report uses the existing reader schema exactly: report={content_md,structured,claims}; structured={schema_version:1,title,outline:[{id,title,level,children}],sections:[{id,title,level,markdown,citation_ids}],citations:[{id,index,source_id,label,quote,locator}],sources:[{source_id,title,url,credibility_weight,source_class}],gaps,conclusion}; claims=[{claim_key,section_id,anchor_quote}]. Every outline item maps to a section; every section markdown and the conclusion occur verbatim in content_md; every citation resolves to a source. Every report Claim link uses an exact anchor_quote from its section, and that section cites verified evidence supporting the Claim.\n")
	policy := reportPolicyForDepth(run.DepthTier)
	fmt.Fprintf(&b, "6. This %s run requires at least %d sections, %d substantive characters per section, and %d in the conclusion. These reject placeholders; evidence coverage and independent review remain the quality gates.\n", run.DepthTier, policy.MinimumSections, policy.MinimumSectionCharacters, policy.MinimumConclusionCharacters)
	b.WriteString("7. A quality or citation evaluation reviews a report written by another Agent. Return evaluation={passed,factual_grounding,coverage,analytical_depth,source_quality,contradiction_handling,instruction_adherence,readability,dimension_findings with one substantive rationale for each named score,reviewed_claim_keys covering every report Claim,reviewed_section_ids covering every report section,findings}. Fail the evaluation when any material defect remains.\n")
	switch task.Kind {
	case TaskKindVerify, TaskKindCounterSearch:
		b.WriteString("8. Include every source, observation, claim, and evidence link being verified. Reuse stable keys and exact ledger content when corroborating existing artifacts; deduplication upgrades verification state transactionally.\n")
	case TaskKindSynthesize:
		b.WriteString("8. Cover every required question's answer Claim and every supported high-significance Claim in the report. Report metadata without explanatory prose and verified citations is rejected.\n")
	case TaskKindQualityGate, TaskKindCitationAudit:
		b.WriteString("8. Evaluate the latest report and current evidence ledger independently. Do not add evidence or manufacture passing scores.\n")
	default:
		b.WriteString("8. Keep result artifacts scoped to this assignment and propose follow-up work only for an identified frontier gap.\n")
	}
	b.WriteString("9. Submit the JSON exactly once with:\n\n")
	fmt.Fprintf(&b, "```bash\nmultica research task-result %s %s %s --file /absolute/path/research-result.json\n```\n", run.SessionID, task.ID, attempt.ID)
	b.WriteString("\nDo not use graph-append, source-upsert, report-patch, or stage-eval for this task. Do not claim completion in chat before task-result succeeds.\n")
	return b.String()
}

func (e *Engine) projectPending(ctx context.Context, sessionID string) error {
	if e.projector == nil {
		return nil
	}
	for i := 0; i < 500; i++ {
		events, err := e.store.ListUnprojectedEvents(ctx, sessionID, 1)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		event := events[0]
		if err = e.projector.Project(ctx, event); err != nil {
			delay := projectionRetryDelay(event.ProjectionAttempts)
			if markErr := e.store.MarkEventProjectionFailed(ctx, event.ID, err.Error(), e.clock.Now().Add(delay)); markErr != nil {
				return errors.Join(err, markErr)
			}
			return err
		}
		if err = e.store.MarkEventProjected(ctx, event.ID); err != nil {
			return err
		}
	}
	return errors.New("research event projection batch limit reached")
}

func projectionRetryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	return time.Duration(1<<min(attempts, 8)) * time.Second
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
		Run: run, Contract: contract, Questions: questions, Tasks: tasks, Attempts: attempts,
		Sources: sources, Observations: observations, Claims: claims, Gate: gate,
	}, nil
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "{}"
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

func (e *Engine) Steer(ctx context.Context, in SteerInput) (Run, error) {
	run, _, _, err := e.store.Steer(ctx, in)
	if err != nil {
		return Run{}, err
	}
	if _, err = e.cancelPendingAttempts(ctx, run, "research_goal_steered"); err != nil {
		return run, err
	}
	if _, _, err = e.store.CreateControlTask(ctx, in.SessionID, TaskKindReplan, "Create a new evidence-oriented plan for the revised user goal. Treat earlier-version artifacts as audit history only.", "lead", 1); err != nil {
		return run, err
	}
	return run, e.ReconcileSession(ctx, in.SessionID)
}

// NodeCommand applies continue|fork from a canvas node, then reconciles so the
// new ready task can dispatch (LRM-1413).
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
