// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// branchCheckpointStore is the checkpoint half of the branch savepoint provider.
// *EnvCheckpointService satisfies it.
type branchCheckpointStore interface {
	Create(ctx context.Context, in EnvCheckpointCreateInput) (EnvCheckpoint, error)
	List(ctx context.Context, workspaceID, projectID string) ([]EnvCheckpoint, error)
}

// branchInstanceRefResolver turns an instance id into the full ref a savepoint
// needs (node and local ref). *EnvSandboxLifecycleService satisfies it.
type branchInstanceRefResolver interface {
	GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error)
}

type branchSourceQueries interface {
	ListReadyEnvDispatchChannelInstances(ctx context.Context, arg db.ListReadyEnvDispatchChannelInstancesParams) ([]string, error)
	GetReadySavepointForInstance(ctx context.Context, arg db.GetReadySavepointForInstanceParams) (db.SandboxSnapshot, error)
}

// ErrNoSavepointForInstance means no captured state exists for a source instance.
// A caller holding a binding that names a source sandbox must refuse rather than
// provision an empty one: the binding says there is state to continue, so booting
// fresh would silently discard it.
var ErrNoSavepointForInstance = errors.New("no ready savepoint for source instance")

// branchSavepointCheckpointKind marks the checkpoints this path creates, so a
// checkpoint taken to serve a branch dispatch stays distinguishable from one a
// user asked for.
const branchSavepointCheckpointKind = "branch_dispatch"

// branchSavepointSaveTimeout bounds capture of the whole source channel. It is
// generous because snapshotting a real repository is not fast, and the dispatch is
// the only place that can afford to wait: the mention path that provisions peers
// later runs under a five second deadline.
const branchSavepointSaveTimeout = 15 * time.Minute

type branchSavepointProvider struct {
	checkpoints branchCheckpointStore
	savepoints  SavepointReader
	lanes       EnvCheckpointLaneRepository
	refs        branchInstanceRefResolver
	q           branchSourceQueries
}

// NewBranchSavepointProvider builds the production BranchSavepointProvider.
func NewBranchSavepointProvider(
	checkpoints branchCheckpointStore,
	savepoints SavepointReader,
	lanes EnvCheckpointLaneRepository,
	refs branchInstanceRefResolver,
	q branchSourceQueries,
) *branchSavepointProvider {
	return &branchSavepointProvider{
		checkpoints: checkpoints, savepoints: savepoints, lanes: lanes, refs: refs, q: q,
	}
}

// branchSavepointEventRef keys a capture on its source env, so re-expanding the
// same state finds this checkpoint again instead of snapshotting the source a
// second time (design D2).
func branchSavepointEventRef(sourceEnvID string) string {
	return "branch_dispatch:env:" + sourceEnvID
}

func (p *branchSavepointProvider) EnsureBranchSavepoint(ctx context.Context, in BranchSavepointInput) (BranchSavepoint, error) {
	if in.WorkspaceID == "" || in.SourceEnvID == "" || in.SourceInstanceID == "" {
		return BranchSavepoint{}, fmt.Errorf(
			"validation_failed: branch savepoint needs workspace, source env and source instance")
	}
	if in.SourceProjectID == "" {
		return BranchSavepoint{}, fmt.Errorf("validation_failed: branch savepoint needs the source project")
	}

	if reused, ok, err := p.reuseCapture(ctx, in); err != nil {
		return BranchSavepoint{}, err
	} else if ok {
		return reused, nil
	}

	refs, err := p.sourceRefs(ctx, in)
	if err != nil {
		return BranchSavepoint{}, err
	}
	cp, err := p.checkpoints.Create(ctx, EnvCheckpointCreateInput{
		WorkspaceID: in.WorkspaceID,
		ProjectID:   in.SourceProjectID,
		EventRef:    branchSavepointEventRef(in.SourceEnvID),
		Kind:        branchSavepointCheckpointKind,
		SaveMode:    SaveModeSnapshot,
		SandboxRefs: refs,
		ActorUserID: in.ActorUserID,
		SaveTimeout: branchSavepointSaveTimeout,
	})
	if err != nil {
		return BranchSavepoint{}, fmt.Errorf("create branch checkpoint: %w", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		return BranchSavepoint{}, fmt.Errorf(
			"capture of branch source did not complete: checkpoint %s is %s", cp.ID, cp.SaveStatus)
	}
	return p.triggerSavepoint(ctx, cp.ID, in)
}

// reuseCapture finds a completed capture of the same source env. It only counts as
// reusable if the trigger's own savepoint is ready in it: a checkpoint that
// captured a different set of sandboxes cannot serve this trigger.
func (p *branchSavepointProvider) reuseCapture(ctx context.Context, in BranchSavepointInput) (BranchSavepoint, bool, error) {
	existing, err := p.checkpoints.List(ctx, in.WorkspaceID, in.SourceProjectID)
	if err != nil {
		return BranchSavepoint{}, false, fmt.Errorf("list branch checkpoints: %w", err)
	}
	wanted := branchSavepointEventRef(in.SourceEnvID)
	for _, cp := range existing {
		if cp.EventRef != wanted ||
			cp.SaveMode != SaveModeSnapshot ||
			cp.SaveStatus != EnvCheckpointSaveComplete {
			continue
		}
		savepoint, err := p.triggerSavepoint(ctx, cp.ID, in)
		if err != nil {
			// This checkpoint captured a different set. Keep looking rather than
			// failing: an older capture of the same env is still a valid reuse.
			continue
		}
		return savepoint, true, nil
	}
	return BranchSavepoint{}, false, nil
}

// triggerSavepoint picks the trigger's savepoint out of a checkpoint's set.
func (p *branchSavepointProvider) triggerSavepoint(ctx context.Context, checkpointID string, in BranchSavepointInput) (BranchSavepoint, error) {
	savepoints, err := p.savepoints.ListSavepoints(ctx, checkpointID, in.WorkspaceID)
	if err != nil {
		return BranchSavepoint{}, fmt.Errorf("list savepoints of checkpoint %s: %w", checkpointID, err)
	}
	for _, sp := range savepoints {
		if sp.InstanceID != in.SourceInstanceID {
			continue
		}
		if sp.Status != savepointStatusReady || sp.CubeSnapshotID == "" {
			return BranchSavepoint{}, fmt.Errorf(
				"savepoint %s for source instance %s is %s with template %q",
				sp.SnapshotID, sp.InstanceID, sp.Status, sp.CubeSnapshotID)
		}
		return BranchSavepoint{
			CheckpointID: checkpointID,
			SnapshotID:   sp.SnapshotID,
			Template:     sp.CubeSnapshotID,
		}, nil
	}
	return BranchSavepoint{}, fmt.Errorf(
		"checkpoint %s has no savepoint for source instance %s", checkpointID, in.SourceInstanceID)
}

// sourceRefs collects every sandbox the branch may inherit: the trigger's, plus
// every ready binding in the source channel. The peers are captured here because
// their own provisioning happens later on the mention path, which cannot wait for
// a snapshot.
func (p *branchSavepointProvider) sourceRefs(ctx context.Context, in BranchSavepointInput) ([]SandboxInstanceRef, error) {
	ids := []string{in.SourceInstanceID}
	if in.SourceChannelID != "" {
		channelUUID, err := util.ParseUUID(in.SourceChannelID)
		if err != nil {
			return nil, fmt.Errorf("parse source channel id: %w", err)
		}
		workspaceUUID, err := util.ParseUUID(in.WorkspaceID)
		if err != nil {
			return nil, fmt.Errorf("parse workspace id: %w", err)
		}
		peers, err := p.q.ListReadyEnvDispatchChannelInstances(ctx, db.ListReadyEnvDispatchChannelInstancesParams{
			ChannelID:   channelUUID,
			WorkspaceID: workspaceUUID,
		})
		if err != nil {
			return nil, fmt.Errorf("list source channel sandboxes: %w", err)
		}
		ids = append(ids, peers...)
	}

	seen := map[string]bool{}
	refs := make([]SandboxInstanceRef, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		ref, err := p.refs.GetSandboxInstanceRef(ctx, in.WorkspaceID, id)
		if err != nil {
			// The trigger's sandbox is the one the dispatch cannot proceed
			// without. A peer's is best-effort: it may have been deleted between
			// the binding read and here, and that peer then boots fresh.
			if id == in.SourceInstanceID {
				return nil, fmt.Errorf("resolve branch source instance %s: %w", id, err)
			}
			continue
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// LookupSavepointTemplate finds the template a sandbox derived from the given
// source instance must be created from. Used by the mention path, which has a
// source instance recorded on a copied binding but no dispatch context.
func (p *branchSavepointProvider) LookupSavepointTemplate(ctx context.Context, workspaceID, sourceInstanceID string) (string, error) {
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		return "", fmt.Errorf("parse workspace id: %w", err)
	}
	instanceUUID, err := util.ParseUUID(sourceInstanceID)
	if err != nil {
		return "", fmt.Errorf("parse source instance id: %w", err)
	}
	row, err := p.q.GetReadySavepointForInstance(ctx, db.GetReadySavepointForInstanceParams{
		WorkspaceID: workspaceUUID,
		InstanceID:  instanceUUID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNoSavepointForInstance
		}
		return "", fmt.Errorf("look up savepoint for instance %s: %w", sourceInstanceID, err)
	}
	if row.CubeSnapshotID == "" {
		return "", ErrNoSavepointForInstance
	}
	return row.CubeSnapshotID, nil
}

func (p *branchSavepointProvider) ClaimBranchLane(ctx context.Context, in BranchLaneInput) (BranchLane, error) {
	if in.CheckpointID == "" || in.LaneKey == "" {
		return BranchLane{}, fmt.Errorf("validation_failed: lane claim needs a checkpoint and lane key")
	}
	lane, won, err := p.lanes.ClaimLane(ctx, in.CheckpointID, in.WorkspaceID, in.LaneKey)
	if err != nil {
		return BranchLane{}, err
	}
	if !won {
		// A retry of the same dispatch. The lane already exists, so adopt it
		// instead of treating the lost race as a failure.
		lane, err = p.lanes.GetLane(ctx, in.CheckpointID, in.WorkspaceID, in.LaneKey)
		if err != nil {
			return BranchLane{}, fmt.Errorf("load already-claimed lane %q: %w", in.LaneKey, err)
		}
	}
	// Record the rows the reset phase created. The lane follows them; it does not
	// create its own (design D6).
	if _, err := p.lanes.RecordLaneStep(ctx, lane.ID, in.WorkspaceID, LaneStep{
		EnvID:     in.EnvID,
		ProjectID: in.ProjectID,
		ChannelID: in.ChannelID,
	}); err != nil {
		return BranchLane{}, fmt.Errorf("record lane %q rollout rows: %w", in.LaneKey, err)
	}
	return BranchLane{LaneID: lane.ID, LaneKey: lane.LaneKey}, nil
}

func (p *branchSavepointProvider) SettleBranchLane(ctx context.Context, in BranchLaneSettleInput) error {
	if in.LaneID == "" {
		return fmt.Errorf("validation_failed: lane settle needs a lane id")
	}
	if in.Status == LaneStatusFailed {
		_, err := p.lanes.MarkLaneFailed(ctx, in.LaneID, in.WorkspaceID, in.Error)
		return err
	}
	if _, err := p.lanes.RecordLaneStep(ctx, in.LaneID, in.WorkspaceID, LaneStep{
		InstanceID:    in.InstanceID,
		RuntimeID:     in.RuntimeID,
		ChatSessionID: in.ChatSessionID,
	}); err != nil {
		return fmt.Errorf("record lane %s provisioned ids: %w", in.LaneID, err)
	}
	_, err := p.lanes.MarkLaneReady(ctx, in.LaneID, in.WorkspaceID)
	return err
}
