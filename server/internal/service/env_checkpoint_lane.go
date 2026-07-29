// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
)

// Lane statuses. A lane is the unit of materialization, so its status answers
// one question: is this continuation usable yet, and if not, is it still coming?
const (
	LaneStatusProvisioning = "provisioning"
	LaneStatusReady        = "ready"
	LaneStatusFailed       = "failed"
)

// ErrSavepointGone reports a savepoint whose underlying snapshot no longer
// exists. It is permanent for every lane of that savepoint, so it also marks the
// savepoint failed and lets later resumes fail fast instead of each one
// rediscovering the same missing snapshot.
var ErrSavepointGone = errors.New("savepoint_gone")

// ErrLaneConversationUnavailable reports a lane that has no conversation to
// continue. A lane's channel is either pre-seeded by the caller (branch dispatch,
// design D6) or minted from the source conversation a checkpoint records (design
// D8). With neither, the only way to proceed would be to reuse the source's
// channel, which would collapse independent continuations into one thread; that
// is refused rather than done quietly.
var ErrLaneConversationUnavailable = errors.New("lane_conversation_unavailable")

// EnvCheckpointLane is one materialized continuation of a snapshot-mode
// checkpoint. The per-step ids double as the recovery log: an empty one is a
// step that has not completed, so a lane interrupted partway is resumed from its
// first empty field rather than redone.
type EnvCheckpointLane struct {
	ID           string
	CheckpointID string
	WorkspaceID  string
	LaneKey      string
	Status       string
	InstanceID   string
	ProjectID    string
	RuntimeID    string
	TaskID       string
	EnvID        string
	// The lane's own conversation. Lanes must not share these with the source or
	// each other, or they would not be independent continuations.
	ChannelID       string
	ChatSessionID   string
	SourceMessageID string
	Error           string
}

// LaneStep carries the ids filled by one materialization step. Empty fields
// leave the stored value untouched, so recording a later step can never erase an
// earlier one.
type LaneStep struct {
	InstanceID      string
	ProjectID       string
	RuntimeID       string
	TaskID          string
	EnvID           string
	ChannelID       string
	ChatSessionID   string
	SourceMessageID string
}

// EnvCheckpointLaneRepository is the persistence seam for lane records.
//
// ClaimLane returns won=false when the unique index rejected the insert. That is
// not an error: it means another caller owns the lane, and this caller reads the
// existing row and branches on its status. The database is the arbiter, so no
// application lock is involved.
type EnvCheckpointLaneRepository interface {
	ClaimLane(ctx context.Context, checkpointID, workspaceID, laneKey string) (lane EnvCheckpointLane, won bool, err error)
	GetLane(ctx context.Context, checkpointID, workspaceID, laneKey string) (EnvCheckpointLane, error)
	ListLanes(ctx context.Context, checkpointID, workspaceID string) ([]EnvCheckpointLane, error)
	RecordLaneStep(ctx context.Context, laneID, workspaceID string, step LaneStep) (EnvCheckpointLane, error)
	MarkLaneReady(ctx context.Context, laneID, workspaceID string) (EnvCheckpointLane, error)
	MarkLaneFailed(ctx context.Context, laneID, workspaceID, reason string) (EnvCheckpointLane, error)
	CountProvisioningLanes(ctx context.Context, checkpointID, workspaceID string) (int, error)
}

// LaneMaterializer builds the parts of a lane that are not the agent run itself.
// The task enqueue is deliberately absent: it goes through the continuation
// seam, so branch dispatch and fan-out resume share one enqueue path.
type LaneMaterializer interface {
	CreateLaneInstance(ctx context.Context, in LaneInstanceInput) (SandboxInstanceRef, error)
	CopyLaneProjectSubtree(ctx context.Context, in LaneProjectInput) (projectID, envID string, err error)
	// ProvisionLaneAgent mints the lane's runtime and its own conversation. The
	// enqueue path needs all of channel, chat session and source message, and
	// each must be the lane's own copy, so they are produced together rather
	// than a runtime alone.
	ProvisionLaneAgent(ctx context.Context, in LaneRuntimeInput) (LaneBinding, error)
}

// LaneBinding is one lane's execution identity, mirroring what the branch
// dispatch path already produces per derived agent.
type LaneBinding struct {
	RuntimeID       string
	DaemonID        string
	AgentID         string
	ChannelID       string
	ChatSessionID   string
	SourceMessageID string
}

type LaneInstanceInput struct {
	WorkspaceID string
	ActorUserID string
	LaneKey     string
	Savepoint   Savepoint
}

type LaneProjectInput struct {
	WorkspaceID     string
	ActorUserID     string
	LaneKey         string
	SourceProjectID string
}

// LaneRuntimeInput carries the lane's identity as recorded so far. Project, env
// and channel are already known by the time the runtime step runs: fan-out fills
// them in from the preceding step, and branch dispatch pre-seeds them from the
// rows its reset phase created (design D6). The runtime step must act on those
// rather than create its own, or a branch lane would end up with a second
// conversation while the copied one it is meant to continue sits unused.
type LaneRuntimeInput struct {
	WorkspaceID string
	ActorUserID string
	LaneKey     string
	AgentID     string
	InstanceID  string
	ProjectID   string
	EnvID       string
	ChannelID   string
}

// SavepointReader reads a checkpoint's savepoints and can mark one failed.
type SavepointReader interface {
	ListSavepoints(ctx context.Context, checkpointID, workspaceID string) ([]Savepoint, error)
	MarkSavepointFailed(ctx context.Context, snapshotID, workspaceID, reason string) error
}

// WithLanes installs the fan-out seams. Snapshot-mode resume requires all three;
// without them it is refused rather than degraded, since a partial fan-out
// implementation would materialize sandboxes it cannot track.
func (s *EnvCheckpointService) WithLanes(repo EnvCheckpointLaneRepository, mat LaneMaterializer, savepoints SavepointReader) *EnvCheckpointService {
	s.lanes = repo
	s.materializer = mat
	s.savepointReader = savepoints
	return s
}

// laneKeyForOrdinal derives one lane's key from the request anchor. The anchor
// must be stable across retries of the same logical request, because this key is
// what the unique index deduplicates on: a changing anchor turns a retry into a
// second sandbox.
//
// The anchor for the branch path is the dispatch's own idempotency key
// (`idempotency_key` on the env-dispatch request body, already required to be a
// UUID when present). Nothing else in the request is retry-stable: the ids the
// server mints are fresh per attempt, and the env_dispatch_request row is
// written only after the dispatch completes, so a retry that arrives mid-flight
// finds no prior row to recover the anchor from.
//
// That key is still optional at the boundary, so branch dispatch is not yet
// retry-safe by this mechanism. Making it mandatory is deliberately held until
// the branch path is actually served by checkpoint resume (Task 3.8 / plan Task
// 13), because rejecting a keyless branch dispatch before then would break
// today's clients — which never send one — for a property nothing yet relies
// on. When it does land, the client must send the key first: a server that
// requires it ahead of the client fails every branch dispatch in between.
func laneKeyForOrdinal(anchor string, ordinal int) string {
	return fmt.Sprintf("%s#%d", anchor, ordinal)
}

// materializeLane claims one lane and drives it to ready, continuing rather than
// restarting if a previous attempt was interrupted partway.
func (s *EnvCheckpointService) materializeLane(
	ctx context.Context,
	cp EnvCheckpoint,
	in ResumeFromCheckpointInput,
	laneKey string,
	savepoint Savepoint,
	trigger ResumeTrigger,
	hasTrigger bool,
	strategy ResumeAgentRunner,
	ordinal int,
) (ResumeLane, error) {
	lane, won, err := s.lanes.ClaimLane(ctx, cp.ID, in.WorkspaceID, laneKey)
	if err != nil {
		return ResumeLane{LaneKey: laneKey, Status: LaneStatusFailed, Error: err.Error()},
			fmt.Errorf("claim lane %q: %w", laneKey, err)
	}
	if !won {
		// Somebody else owns this lane. Read what they got to, and either hand
		// back their result or carry their unfinished work forward.
		lane, err = s.lanes.GetLane(ctx, cp.ID, in.WorkspaceID, laneKey)
		if err != nil {
			return ResumeLane{LaneKey: laneKey, Status: LaneStatusFailed, Error: err.Error()},
				fmt.Errorf("read existing lane %q: %w", laneKey, err)
		}
		switch lane.Status {
		case LaneStatusReady:
			return laneResult(lane, TriggerExecuted), nil
		case LaneStatusFailed:
			return laneResult(lane, TriggerFailed), nil
		}
	}

	// Each step is skipped when the lane row already records its id, which is
	// what makes an interrupted lane continue instead of duplicating work.
	if lane.InstanceID == "" {
		ref, err := s.materializer.CreateLaneInstance(ctx, LaneInstanceInput{
			WorkspaceID: in.WorkspaceID,
			ActorUserID: in.ActorUserID,
			LaneKey:     laneKey,
			Savepoint:   savepoint,
		})
		if err != nil {
			return s.failLane(ctx, lane, savepoint, in.WorkspaceID, err)
		}
		if lane, err = s.recordStep(ctx, lane, in.WorkspaceID, LaneStep{InstanceID: ref.InstanceID}); err != nil {
			return laneResult(lane, TriggerFailed), err
		}
	}
	if lane.ProjectID == "" {
		projectID, envID, err := s.materializer.CopyLaneProjectSubtree(ctx, LaneProjectInput{
			WorkspaceID:     in.WorkspaceID,
			ActorUserID:     in.ActorUserID,
			LaneKey:         laneKey,
			SourceProjectID: cp.ProjectID,
		})
		if err != nil {
			return s.failLane(ctx, lane, savepoint, in.WorkspaceID, err)
		}
		if lane, err = s.recordStep(ctx, lane, in.WorkspaceID, LaneStep{ProjectID: projectID, EnvID: envID}); err != nil {
			return laneResult(lane, TriggerFailed), err
		}
	}
	if lane.RuntimeID == "" {
		binding, err := s.materializer.ProvisionLaneAgent(ctx, LaneRuntimeInput{
			WorkspaceID: in.WorkspaceID,
			ActorUserID: in.ActorUserID,
			LaneKey:     laneKey,
			AgentID:     trigger.AgentID,
			InstanceID:  lane.InstanceID,
			ProjectID:   lane.ProjectID,
			EnvID:       lane.EnvID,
			ChannelID:   lane.ChannelID,
		})
		if err != nil {
			return s.failLane(ctx, lane, savepoint, in.WorkspaceID, err)
		}
		if lane, err = s.recordStep(ctx, lane, in.WorkspaceID, LaneStep{
			RuntimeID:       binding.RuntimeID,
			ChannelID:       binding.ChannelID,
			ChatSessionID:   binding.ChatSessionID,
			SourceMessageID: binding.SourceMessageID,
		}); err != nil {
			return laneResult(lane, TriggerFailed), err
		}
	}

	// No continuation descriptor: the sandbox side of the lane is done, and
	// there is no agent run to re-engage.
	if !hasTrigger || strategy == nil {
		ready, err := s.lanes.MarkLaneReady(ctx, lane.ID, in.WorkspaceID)
		if err != nil {
			return laneResult(lane, TriggerSkippedLegacy), fmt.Errorf("mark lane %q ready: %w", laneKey, err)
		}
		return laneResult(ready, TriggerSkippedLegacy), nil
	}

	if lane.TaskID == "" {
		outcome, err := strategy.ResumeAgentRun(ctx, ContinuationRequest{
			Trigger:     trigger,
			WorkspaceID: in.WorkspaceID,
			ActorUserID: in.ActorUserID,
			Index:       ordinal,
			Lane: LaneRef{
				LaneKey:    laneKey,
				LaneEnvID:  lane.EnvID,
				InstanceID: lane.InstanceID,
				ProjectID:  lane.ProjectID,
				RuntimeID:  lane.RuntimeID,
				AgentID:    trigger.AgentID,
				// The lane's own conversation, not the source's.
				ChannelID:       lane.ChannelID,
				ChatSessionID:   lane.ChatSessionID,
				SourceMessageID: lane.SourceMessageID,
			},
		})
		if err != nil {
			// The sandbox, subtree and runtime all exist; only the agent run
			// failed to start. That is a partial lane worth reporting, not a
			// reason to tear the lane down.
			return laneResult(lane, TriggerFailed), nil
		}
		if lane, err = s.recordStep(ctx, lane, in.WorkspaceID, LaneStep{TaskID: outcome.TaskID}); err != nil {
			return laneResult(lane, outcome.Status), err
		}
		if outcome.Status != TriggerExecuted {
			return laneResult(lane, outcome.Status), nil
		}
	}

	ready, err := s.lanes.MarkLaneReady(ctx, lane.ID, in.WorkspaceID)
	if err != nil {
		return laneResult(lane, TriggerExecuted), fmt.Errorf("mark lane %q ready: %w", laneKey, err)
	}
	return laneResult(ready, TriggerExecuted), nil
}

func (s *EnvCheckpointService) recordStep(ctx context.Context, lane EnvCheckpointLane, workspaceID string, step LaneStep) (EnvCheckpointLane, error) {
	updated, err := s.lanes.RecordLaneStep(ctx, lane.ID, workspaceID, step)
	if err != nil {
		return lane, fmt.Errorf("record lane %q step: %w", lane.LaneKey, err)
	}
	return updated, nil
}

// failLane records a lane failure and, when the cause is a vanished savepoint,
// marks the savepoint failed too so sibling and later lanes fail fast instead of
// each one rediscovering the same missing snapshot.
func (s *EnvCheckpointService) failLane(ctx context.Context, lane EnvCheckpointLane, savepoint Savepoint, workspaceID string, cause error) (ResumeLane, error) {
	if errors.Is(cause, ErrSavepointGone) {
		if err := s.savepointReader.MarkSavepointFailed(ctx, savepoint.SnapshotID, workspaceID, cause.Error()); err != nil {
			cause = fmt.Errorf("%w (and marking the savepoint failed also failed: %v)", cause, err)
		}
	}
	failed, err := s.lanes.MarkLaneFailed(ctx, lane.ID, workspaceID, cause.Error())
	if err != nil {
		lane.Status = LaneStatusFailed
		lane.Error = cause.Error()
		return laneResult(lane, TriggerFailed), fmt.Errorf("%w (and recording the lane failure also failed: %v)", cause, err)
	}
	return laneResult(failed, TriggerFailed), cause
}

func laneResult(lane EnvCheckpointLane, trigger TriggerStatus) ResumeLane {
	return ResumeLane{
		LaneKey:       lane.LaneKey,
		Status:        lane.Status,
		InstanceID:    lane.InstanceID,
		ProjectID:     lane.ProjectID,
		RuntimeID:     lane.RuntimeID,
		TaskID:        lane.TaskID,
		EnvID:         lane.EnvID,
		ChatSessionID: lane.ChatSessionID,
		TriggerStatus: trigger,
		Error:         lane.Error,
	}
}
