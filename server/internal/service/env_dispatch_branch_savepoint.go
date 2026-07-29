// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"fmt"
)

// BranchSavepoint is the captured source state a branch rollout boots from,
// together with the checkpoint that owns it. The checkpoint is what keeps the
// snapshot alive: reclamation releases a savepoint only when no lane still needs
// it.
type BranchSavepoint struct {
	CheckpointID string
	SnapshotID   string
	// Template is the Cube snapshot new sandboxes are created from.
	Template string
}

// BranchSavepointInput identifies the source a branch dispatch continues from.
// Capture is keyed on the source env so re-expanding the same state reuses one
// snapshot instead of taking another (design D2).
//
// SourceChannelID matters because the trigger is not the only agent that inherits
// source state. Copying the branch channel copies every roster member's binding
// along with the source sandbox it was bound to, so a peer mentioned later in the
// branch inherits state too. That provisioning runs on the mention path under a
// five second deadline, far less than a snapshot of a real repository takes, so
// every ready source sandbox in the channel is captured here -- while the dispatch
// can still afford to wait -- and the mention path only looks one up.
type BranchSavepointInput struct {
	WorkspaceID      string
	ActorUserID      string
	SourceEnvID      string
	SourceProjectID  string
	SourceChannelID  string
	SourceInstanceID string
}

// BranchLaneInput claims one rollout's lane on the capturing checkpoint. The env,
// project and channel are the rows the reset phase already created: on the branch
// path the rollout owns them and the lane records them (design D6), rather than
// materialization creating a second set and orphaning the copied conversation.
type BranchLaneInput struct {
	WorkspaceID  string
	CheckpointID string
	LaneKey      string
	EnvID        string
	ProjectID    string
	ChannelID    string
}

// BranchLane is the claimed lane row.
type BranchLane struct {
	LaneID  string
	LaneKey string
}

// BranchLaneSettleInput records a lane's terminal state. A lane left in
// provisioning blocks its savepoint from ever being reclaimed, so both outcomes
// are recorded -- including the failure, which is the case that would otherwise
// pin a snapshot forever.
type BranchLaneSettleInput struct {
	WorkspaceID   string
	LaneID        string
	Status        string
	InstanceID    string
	RuntimeID     string
	DaemonID      string
	AgentID       string
	ChatSessionID string
	Error         string
}

// BranchSavepointProvider captures the branch source once per dispatch and tracks
// the lanes booted from that capture. EnsureBranchSavepoint captures every ready
// sandbox in the source channel and returns the trigger's savepoint; the rest are
// found by the mention path through the same checkpoint.
type BranchSavepointProvider interface {
	EnsureBranchSavepoint(ctx context.Context, in BranchSavepointInput) (BranchSavepoint, error)
	ClaimBranchLane(ctx context.Context, in BranchLaneInput) (BranchLane, error)
	SettleBranchLane(ctx context.Context, in BranchLaneSettleInput) error
}

// BranchSavepointResolver adds the lookup used away from the dispatch, where a
// copied binding names a source sandbox but there is no dispatch to inherit a
// template from.
type BranchSavepointResolver interface {
	BranchSavepointProvider
	LookupSavepointTemplate(ctx context.Context, workspaceID, sourceInstanceID string) (string, error)
}

// branchMessageSourceInstance reports the sandbox instance a branch+message
// dispatch continues from, or "" when there is nothing to capture (a trigger with
// no sandbox instance provisions from scratch, unchanged by this path).
func branchMessageSourceInstance(in EnvDispatchInput) string {
	if in.Mode != EnvModeBranch || in.DispatchType != EnvDispatchMessage {
		return ""
	}
	if in.BranchMessageSource == nil {
		return ""
	}
	return in.BranchMessageSource.TriggerSourceSandboxInstanceID
}

// captureBranchSource snapshots the source sandbox once for the whole group,
// before the reset fan-out. Two reasons it belongs here rather than per rollout:
// rollouts are reset and dispatched concurrently, so per-rollout capture would
// snapshot the same source once per rollout and race on creating the checkpoint
// that owns them; and capture is the one step that fails for the entire group at
// once, so failing before reset avoids rolling back N envs, projects and copied
// channels for a dispatch that could never have proceeded.
func (s *EnvDispatchService) captureBranchSource(ctx context.Context, in EnvDispatchInput) (*BranchSavepoint, error) {
	sourceInstance := branchMessageSourceInstance(in)
	if sourceInstance == "" {
		return nil, nil
	}
	// Fail closed. The live filesystem clone this replaces is being removed, so
	// a server without the provider installed has no way to continue the source
	// state and must say so instead of silently provisioning an empty sandbox.
	if s.branchSavepoints == nil {
		return nil, fmt.Errorf(
			"reset_failed: branch dispatch requires a savepoint provider to continue source sandbox %s",
			sourceInstance)
	}
	savepoint, err := s.branchSavepoints.EnsureBranchSavepoint(ctx, BranchSavepointInput{
		WorkspaceID:      in.WorkspaceID,
		ActorUserID:      in.UserID,
		SourceEnvID:      in.EnvID,
		SourceProjectID:  in.SourceProjectID,
		SourceChannelID:  in.BranchMessageSource.SourceChannelID,
		SourceInstanceID: sourceInstance,
	})
	if err != nil {
		return nil, fmt.Errorf("reset_failed: capture branch source %s: %w", sourceInstance, err)
	}
	if savepoint.Template == "" {
		return nil, fmt.Errorf(
			"reset_failed: capture of branch source %s produced no template", sourceInstance)
	}
	return &savepoint, nil
}

// branchLaneKey derives the lane key for one rollout. The dispatch's idempotency
// key is the anchor when the caller supplied one, so a retried dispatch claims the
// same lanes rather than duplicating them. Without an anchor there is nothing
// stable to key on -- an anchorless retry builds fresh envs anyway -- so the
// rollout's own env id stands in.
func branchLaneKey(in EnvDispatchInput, r *EnvRollout, idx int) string {
	if in.IdempotencyKey != "" {
		return laneKeyForOrdinal(in.IdempotencyKey, idx)
	}
	return r.EnvID
}

// claimBranchLane records the rollout as a lane of the capturing checkpoint. The
// lane is what savepoint reclamation reads to know the snapshot is still in use,
// so it is claimed before the sandbox is built from it.
func (s *EnvDispatchService) claimBranchLane(ctx context.Context, in EnvDispatchInput, savepoint *BranchSavepoint, r *EnvRollout, idx int) (BranchLane, error) {
	if savepoint == nil || s.branchSavepoints == nil {
		return BranchLane{}, nil
	}
	return s.branchSavepoints.ClaimBranchLane(ctx, BranchLaneInput{
		WorkspaceID:  in.WorkspaceID,
		CheckpointID: savepoint.CheckpointID,
		LaneKey:      branchLaneKey(in, r, idx),
		EnvID:        r.EnvID,
		ProjectID:    r.ProjectID,
		ChannelID:    r.ChannelID,
	})
}

// settleBranchLane records the lane's outcome. The error is deliberately dropped:
// the run is already enqueued by the time a lane settles ready, so failing the
// rollout here would report failure for work that is running. A lane left in
// provisioning is picked up by the lane sweeper instead.
func (s *EnvDispatchService) settleBranchLane(ctx context.Context, workspaceID string, lane BranchLane, in BranchLaneSettleInput) {
	if lane.LaneID == "" || s.branchSavepoints == nil {
		return
	}
	in.WorkspaceID = workspaceID
	in.LaneID = lane.LaneID
	_ = s.branchSavepoints.SettleBranchLane(ctx, in)
}
