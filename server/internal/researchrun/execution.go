package researchrun

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// executionStore is the canonical persistence input required by Execution
// Module. It excludes Gate, Result acceptance, Projection, and Run lifecycle
// mutations.
type executionStore interface {
	ListAttempts(context.Context, string) ([]Attempt, error)
	ReconcileAttempts(context.Context, string, map[string]InboxTaskState) ([]RunEvent, error)
	ActivateReadyTasks(context.Context, string) (int, error)
	ListPendingCancellations(context.Context, string) ([]PendingCancellation, error)
	MarkCancellationsRequested(context.Context, string, []CancellationRequest) error
	CompleteCancellations(context.Context, string, []string) ([]RunEvent, error)
	CreateDispatchIntent(context.Context, CreateDispatchIntentInput) (Attempt, RunEvent, error)
	ClaimDispatchIntents(context.Context, string, string, time.Duration, int) ([]DispatchIntent, error)
	RescheduleDispatchIntent(context.Context, string, string, string, time.Time) (bool, error)
	FailDispatchIntent(context.Context, string, string, AttemptFailure) (bool, RunEvent, error)
	AcknowledgeDispatchIntent(context.Context, string, string, string) (bool, Attempt, RunEvent, error)
	TaskContext(context.Context, string, string) (RunSnapshot, error)
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
	if _, err := module.DeliverPending(ctx, sessionID, 32); err != nil {
		return err
	}
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

const dispatchLeaseDuration = 45 * time.Second

// DeliverPending resumes frozen external mutations. Dispatcher.Dispatch is
// idempotent by request Key, so a crash after the external commit but before
// acknowledgement is repaired by replaying the exact same request.
func (module executionModule) DeliverPending(ctx context.Context, sessionID string, limit int) (int, error) {
	token := uuid.NewString()
	intents, err := module.store.ClaimDispatchIntents(ctx, sessionID, token, dispatchLeaseDuration, limit)
	if err != nil {
		return 0, err
	}
	delivered := 0
	for _, intent := range intents {
		result, dispatchErr := module.dispatcher.Dispatch(ctx, intent.Request)
		if dispatchErr != nil {
			policy := DispatchFailurePolicy(dispatchErr)
			retryable := policy.Retryable
			maxDeliveries := policy.MaxAttempts
			if maxDeliveries < 1 {
				maxDeliveries = 1
			}
			mutationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			if retryable && intent.DeliveryAttempts < maxDeliveries {
				next := module.clock.Now().Add(dispatchDeliveryBackoff(intent.DeliveryAttempts))
				_, err = module.store.RescheduleDispatchIntent(mutationCtx, intent.ID, token, dispatchErr.Error(), next)
				cancel()
				if err != nil {
					return delivered, errors.Join(dispatchErr, err)
				}
				continue
			}
			_, _, err = module.store.FailDispatchIntent(mutationCtx, intent.ID, token, AttemptFailure{
				AttemptID: intent.AttemptID, FailureClass: string(policy.Class),
				Diagnostics: dispatchErr.Error(), Retryable: retryable,
			})
			cancel()
			if err != nil {
				return delivered, errors.Join(dispatchErr, err)
			}
			if !retryable {
				return delivered, fmt.Errorf("dispatch research task %s: %w", intent.Request.Task.ID, dispatchErr)
			}
			continue
		}
		mutationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		accepted, _, _, acknowledgeErr := module.store.AcknowledgeDispatchIntent(mutationCtx, intent.ID, token, result.InboxTaskID)
		cancel()
		if acknowledgeErr != nil {
			// Do not cancel here. The outbox lease will expire and the idempotent
			// external request will return the same Inbox Task on replay.
			return delivered, acknowledgeErr
		}
		if !accepted {
			cancelCtx, cancelRuntime := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			cancelErr := module.dispatcher.Cancel(cancelCtx, []string{result.InboxTaskID}, "research_attempt_no_longer_dispatchable")
			cancelRuntime()
			if cancelErr != nil {
				return delivered, cancelErr
			}
			continue
		}
		delivered++
	}
	return delivered, nil
}

func dispatchDeliveryBackoff(deliveryAttempt int) time.Duration {
	if deliveryAttempt < 1 {
		deliveryAttempt = 1
	}
	return time.Duration(5*(1<<min(deliveryAttempt-1, 6))) * time.Second
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
		if attempt.InboxTaskID != "" {
			lookupKeys = append(lookupKeys, attempt.InboxTaskID)
		} else {
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
	requests := make([]CancellationRequest, 0, len(pending))
	completedAttemptIDs := make([]string, 0, len(pending))
	staleAfter := time.Duration(run.Config.StaleAfterSeconds) * time.Second
	if staleAfter <= 0 {
		staleAfter = 15 * time.Minute
	}
	for _, attempt := range pending {
		lookupKey := attempt.InboxTaskID
		if lookupKey == "" {
			lookupKey = attempt.DispatchKey
		}
		state, found := states[lookupKey]
		if !found {
			state, found = states[attempt.DispatchKey]
		}
		inboxID := attempt.InboxTaskID
		if inboxID == "" && found {
			inboxID = state.ID
		}
		if found && !state.HasActiveLease && terminalInboxTaskState(state.Status) {
			completedAttemptIDs = append(completedAttemptIDs, attempt.AttemptID)
			continue
		}
		if inboxID != "" && attempt.CancellationRequestedAt == nil {
			inboxIDs = append(inboxIDs, inboxID)
			requests = append(requests, CancellationRequest{AttemptID: attempt.AttemptID, InboxTaskID: inboxID})
			continue
		}
		staleBase := attempt.DispatchedAt
		if attempt.CancellationRequestedAt != nil {
			staleBase = *attempt.CancellationRequestedAt
		}
		if !found && !module.clock.Now().Before(staleBase.Add(staleAfter)) {
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
		markCtx, markCancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
		err = module.store.MarkCancellationsRequested(markCtx, run.SessionID, requests)
		markCancel()
		if err != nil {
			return true, err
		}
	}
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	_, err = module.store.CompleteCancellations(markCtx, run.SessionID, completedAttemptIDs)
	cancel()
	if err != nil {
		return true, err
	}
	return len(completedAttemptIDs) < len(pending), nil
}

func terminalInboxTaskState(status string) bool {
	switch status {
	case "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func (module executionModule) DispatchReady(ctx context.Context, run Run, tasks []Task, attempts []Attempt, members []FleetMember) (int, error) {
	if err := ensureSupportedOrchestratorVersion(run.OrchestratorVersion); err != nil {
		return 0, err
	}
	activeByAgent := map[string]int{}
	activeAttempts := 0
	for _, attempt := range attempts {
		if attempt.Status == AttemptStatusDispatching || attempt.Status == AttemptStatusRunning || attempt.Status == AttemptStatusCancelling {
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
		snapshot, err := module.store.TaskContext(ctx, task.ID, run.WorkspaceID)
		if err != nil {
			return dispatched, err
		}
		var currentTask Task
		for _, candidate := range snapshot.Tasks {
			if candidate.ID == task.ID {
				currentTask = candidate
				break
			}
		}
		if currentTask.ID == "" || currentTask.Status != TaskStatusReady {
			continue
		}
		attemptID := uuid.NewString()
		dispatchKey := fmt.Sprintf("research:%s:task:%s:attempt:%s", snapshot.Run.SessionID, currentTask.ID, attemptID)
		target := selectedExecutionTarget(agentID, members)
		attempt := Attempt{ID: attemptID, SessionID: snapshot.Run.SessionID, WorkspaceID: snapshot.Run.WorkspaceID,
			TaskID: currentTask.ID, AssignedAgentID: agentID, ExecutionTarget: target,
			DispatchKey: dispatchKey, Status: AttemptStatusDispatching}
		prompt, err := module.prompts.Build(snapshot.Run, currentTask, attempt, snapshot, members)
		if err != nil {
			return dispatched, err
		}
		request := DispatchRequest{
			Run:       snapshot.Run,
			Task:      currentTask,
			AttemptID: attempt.ID,
			AgentID:   agentID,
			Target:    target,
			Key:       attempt.DispatchKey,
			Prompt:    prompt,
		}
		request.RequestHash, err = HashDispatchRequest(request)
		if err != nil {
			return dispatched, err
		}
		_, _, err = module.store.CreateDispatchIntent(ctx, CreateDispatchIntentInput{
			AttemptID: attemptID, SessionID: snapshot.Run.SessionID, TaskID: currentTask.ID,
			AgentID: agentID, Target: target, ExpectedStateVersion: snapshot.Run.StateVersion, Request: request,
		})
		if errors.Is(err, ErrInvalidTransition) {
			continue
		}
		if err != nil {
			return dispatched, err
		}
		activeByAgent[agentID]++
		dispatched++
	}
	if dispatched > 0 {
		if _, err := module.DeliverPending(ctx, run.SessionID, dispatched); err != nil {
			return dispatched, err
		}
	}
	return dispatched, nil
}

func selectedExecutionTarget(agentID string, members []FleetMember) ExecutionTarget {
	for _, member := range members {
		if member.AgentID != agentID {
			continue
		}
		target := member.ExecutionTarget
		if strings.TrimSpace(target.Adapter) == "" {
			target.Adapter = "agent_inbox"
		}
		target.AgentID = agentID
		return target
	}
	return ExecutionTarget{Adapter: "agent_inbox", AgentID: agentID}
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
