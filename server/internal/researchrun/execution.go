package researchrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// executionStore is the canonical persistence input required by Execution
// Module. It excludes Gate, Result acceptance, Projection, and Run lifecycle
// mutations.
type executionStore interface {
	ListAttempts(context.Context, string) ([]Attempt, error)
	ReconcileAttempts(context.Context, string, map[string]InboxTaskState) ([]RunEvent, error)
	ActivateReadyTasks(context.Context, string) (int, error)
	ListPendingCancellations(context.Context, string) ([]PendingCancellation, error)
	MarkCancellationsCompleted(context.Context, string, []string) error
	CreateAttempt(context.Context, string, string, string) (Attempt, RunEvent, error)
	TaskContext(context.Context, string, string) (RunSnapshot, error)
	FailAttempt(context.Context, AttemptFailure) (RunEvent, error)
	AttachInboxTask(context.Context, string, string) (Attempt, RunEvent, error)
}

type executionModule struct {
	store      executionStore
	dispatcher Dispatcher
	clock      Clock
	prompts    taskPromptModule
}

// SyncAttempts reconciles external runtime state before dependency activation.
// An Inbox Task ID is authoritative once attached; DispatchKey is used only
// while the attach acknowledgement is still missing.
func (module executionModule) SyncAttempts(ctx context.Context, sessionID string) error {
	attempts, err := module.store.ListAttempts(ctx, sessionID)
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
		states, err = module.dispatcher.Inspect(ctx, inspectKeys)
		if err != nil {
			return fmt.Errorf("inspect research attempts: %w", err)
		}
	}
	if _, err = module.store.ReconcileAttempts(ctx, sessionID, states); err != nil {
		return err
	}
	return module.ActivateReadyTasks(ctx, sessionID)
}

func (module executionModule) ActivateReadyTasks(ctx context.Context, sessionID string) error {
	_, err := module.store.ActivateReadyTasks(ctx, sessionID)
	return err
}

func (module executionModule) CancelPendingAttempts(ctx context.Context, run Run, reason string) (bool, error) {
	if run.SessionID == "" {
		return false, nil
	}
	pending, err := module.store.ListPendingCancellations(ctx, run.SessionID)
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
		states, err = module.dispatcher.Inspect(ctx, lookupKeys)
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
		if !module.clock.Now().Before(attempt.DispatchedAt.Add(staleAfter)) {
			completedAttemptIDs = append(completedAttemptIDs, attempt.AttemptID)
		}
	}
	if len(inboxIDs) > 0 {
		cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		err = module.dispatcher.Cancel(cancelCtx, inboxIDs, reason)
		cancel()
		if err != nil {
			return true, fmt.Errorf("cancel research inbox tasks: %w", err)
		}
	}
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	err = module.store.MarkCancellationsCompleted(markCtx, run.SessionID, completedAttemptIDs)
	cancel()
	if err != nil {
		return true, err
	}
	return len(completedAttemptIDs) < len(pending), nil
}

func (module executionModule) DispatchReady(ctx context.Context, run Run, tasks []Task, attempts []Attempt, members []FleetMember) (int, error) {
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
			if task.ReadyAt != nil && task.ReadyAt.After(module.clock.Now()) {
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
		attempt, _, err := module.store.CreateAttempt(ctx, run.SessionID, task.ID, agentID)
		if errors.Is(err, ErrInvalidTransition) {
			continue
		}
		if err != nil {
			return dispatched, err
		}
		snapshot, err := module.store.TaskContext(ctx, task.ID, run.WorkspaceID)
		if err != nil {
			_, _ = module.store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "context_load_failed", Diagnostics: err.Error(), Retryable: true})
			continue
		}
		prompt, err := module.prompts.Build(run, task, attempt, snapshot, members)
		if err != nil {
			_, _ = module.store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "unsupported_orchestrator_version", Diagnostics: err.Error(), Retryable: false})
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
		dispatch, err := module.dispatcher.Dispatch(ctx, request)
		if err != nil {
			retryable := dispatchErrorRetryable(err)
			if _, failErr := module.store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "dispatch_failed", Diagnostics: err.Error(), Retryable: retryable}); failErr != nil {
				return dispatched, errors.Join(err, failErr)
			}
			if !retryable {
				return dispatched, fmt.Errorf("dispatch research task %s: %w", task.ID, err)
			}
			continue
		}
		if _, _, err = module.store.AttachInboxTask(ctx, attempt.ID, dispatch.InboxTaskID); err != nil {
			cancelCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			cancelErr := module.dispatcher.Cancel(cancelCtx, []string{dispatch.InboxTaskID}, "research_attempt_no_longer_dispatchable")
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

func (e *Engine) executionModule() executionModule {
	return executionModule{store: e.store, dispatcher: e.dispatcher, clock: e.clock, prompts: taskPromptModule{}}
}

func (e *Engine) cancelPendingAttempts(ctx context.Context, run Run, reason string) (bool, error) {
	return e.executionModule().CancelPendingAttempts(ctx, run, reason)
}

func (e *Engine) dispatchReady(ctx context.Context, run Run, tasks []Task, attempts []Attempt, members []FleetMember) (int, error) {
	return e.executionModule().DispatchReady(ctx, run, tasks, attempts, members)
}
