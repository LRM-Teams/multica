package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ErrTriggerTaskNotResumable means the trigger's task is terminal, missing, or
// bound to a different runtime - resume must not double-run it. ResumeFromCheckpoint
// surfaces this as TriggerFailed (partial resume: sandboxes resumed, agent runtime
// not re-engaged) rather than silently returning a handle AReaL cannot use.
var ErrTriggerTaskNotResumable = errors.New("trigger_task_not_resumable")

// InFlightTaskResetter is the sqlc seam the primitive resets a task through.
// Implemented by *db.Queries (ResetInFlightTaskForResume); faked in tests so the
// primitive is unit-testable without a database.
type InFlightTaskResetter interface {
	ResetInFlightTaskForResume(ctx context.Context, arg db.ResetInFlightTaskForResumeParams) (db.AgentInboxEvent, error)
}

// sameRuntimeContinuation implements ResumeAgentRunner for pause_in_place by
// resetting the existing in-flight task row to `pending` and waking the resumed
// daemon so its claim loop re-claims it. It does NOT create a new task row
// (continuity preserved: same id, context, runtime_id, issue/chat binding) and
// does NOT send a chat message - the resumed runtime simply picks up the
// original task. This is the "trigger" that re-engages the agent runtime after
// a checkpoint resume.
type sameRuntimeContinuation struct {
	resetter InFlightTaskResetter
	waker    TaskWakeupNotifier
}

// NewSameRuntimeContinuation builds the pause_in_place continuation strategy
// from a sqlc resetter and a wakeup notifier. The notifier wakes the resumed
// daemon to re-claim the task; a nil notifier disables the fast-path wake (the
// daemon's poll loop still re-claims, just less promptly).
func NewSameRuntimeContinuation(resetter InFlightTaskResetter, waker TaskWakeupNotifier) ResumeAgentRunner {
	return &sameRuntimeContinuation{resetter: resetter, waker: waker}
}

func (r *sameRuntimeContinuation) Mode() EnvCheckpointSaveMode { return SaveModePauseInPlace }

func (r *sameRuntimeContinuation) ResumeAgentRun(ctx context.Context, req ContinuationRequest) (ContinuationOutcome, error) {
	taskID, err := util.ParseUUID(req.Trigger.TaskID)
	if err != nil {
		return ContinuationOutcome{Status: TriggerFailed}, fmt.Errorf("invalid task_id: %w", err)
	}
	runtimeID, err := util.ParseUUID(req.Trigger.RuntimeID)
	if err != nil {
		return ContinuationOutcome{Status: TriggerFailed}, fmt.Errorf("invalid runtime_id: %w", err)
	}
	task, err := r.resetter.ResetInFlightTaskForResume(ctx, db.ResetInFlightTaskForResumeParams{
		TaskID:    taskID,
		RuntimeID: runtimeID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Terminal, missing, or bound to a different runtime - do not wake.
		return ContinuationOutcome{Status: TriggerFailed}, ErrTriggerTaskNotResumable
	}
	if err != nil {
		return ContinuationOutcome{Status: TriggerFailed}, fmt.Errorf("reset in-flight task: %w", err)
	}
	if r.waker != nil {
		r.waker.NotifyTaskAvailable(util.UUIDToString(task.RuntimeID), util.UUIDToString(task.ID))
	}
	return ContinuationOutcome{Status: TriggerExecuted, TaskID: util.UUIDToString(task.ID)}, nil
}
