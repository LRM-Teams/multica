package service

import (
	"context"
	"fmt"
)

// ForkedRuntimeEnqueuer is the seam the forked-runtime strategy enqueues
// through. In production this is EnvDispatchService's deps adapter, which owns
// the channel-run insert; the strategy holds no SQL of its own.
type ForkedRuntimeEnqueuer interface {
	EnqueueEnvDispatchChannelRun(ctx context.Context, workspaceID, userID string, in ChannelRunInput, idx int) (runID string, err error)
}

// forkedRuntimeContinuation enqueues one task per materialized lane, bound to
// that lane's own runtime, sandbox instance, and copied project subtree. Lanes
// never share a task row or a runtime identity with each other or the source.
type forkedRuntimeContinuation struct {
	enqueuer ForkedRuntimeEnqueuer
}

func NewForkedRuntimeContinuation(enqueuer ForkedRuntimeEnqueuer) ResumeAgentRunner {
	return &forkedRuntimeContinuation{enqueuer: enqueuer}
}

func (f *forkedRuntimeContinuation) Mode() EnvCheckpointSaveMode { return SaveModeSnapshot }

func (f *forkedRuntimeContinuation) ResumeAgentRun(ctx context.Context, req ContinuationRequest) (ContinuationOutcome, error) {
	failed := ContinuationOutcome{Status: TriggerFailed, LaneKey: req.Lane.LaneKey}
	if f.enqueuer == nil {
		return failed, fmt.Errorf("validation_failed: forked continuation has no enqueuer")
	}
	if req.Lane.RuntimeID == "" || req.Lane.InstanceID == "" || req.Lane.AgentID == "" {
		return failed, fmt.Errorf("validation_failed: lane %q missing runtime/instance/agent binding", req.Lane.LaneKey)
	}
	// The env id is separate from the lane key, because a fan-out lane key is an
	// anchor plus an ordinal rather than an env id. Enqueueing with an empty env
	// id would start a run detached from the environment it is supposed to act on.
	if req.Lane.LaneEnvID == "" {
		return failed, fmt.Errorf("validation_failed: lane %q missing env id", req.Lane.LaneKey)
	}
	// EnvID is the lane key here because branch dispatch's env_id *is* the lane
	// key today. Task 13 replaces it with the lane's own env_id once branch
	// dispatch is served by checkpoint resume.
	runID, err := f.enqueuer.EnqueueEnvDispatchChannelRun(ctx, req.WorkspaceID, req.ActorUserID, ChannelRunInput{
		AgentID:            req.Lane.AgentID,
		ChannelID:          req.Lane.ChannelID,
		ProjectID:          req.Lane.ProjectID,
		EnvID:              req.Lane.LaneEnvID,
		ChatSessionID:      req.Lane.ChatSessionID,
		SandboxInstanceID:  req.Lane.InstanceID,
		RuntimeID:          req.Lane.RuntimeID,
		SourceMessageID:    req.Lane.SourceMessageID,
		SharedWorkdirEnvID: req.Lane.SharedWorkdirEnvID,
	}, req.Index)
	if err != nil {
		return failed, fmt.Errorf("enqueue lane %q: %w", req.Lane.LaneKey, err)
	}
	return ContinuationOutcome{Status: TriggerExecuted, TaskID: runID, LaneKey: req.Lane.LaneKey}, nil
}
