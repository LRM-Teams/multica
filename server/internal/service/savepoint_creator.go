// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// sandbox_snapshot statuses, as written by the create_template job completion
// path in the sandbox handler.
const (
	savepointStatusCreating = "creating"
	savepointStatusReady    = "ready"
	savepointStatusFailed   = "failed"
	savepointStatusDeleting = "deleting"
)

// savepointDefaultTimeout bounds the wait for a snapshot to reach a terminal
// state. The prerequisite experiment measured ~1.2s for a small sandbox; the
// headroom is for a large SWE checkout, and it matches the 10-minute client
// timeout sandboxd already applies to the same Cube call.
const savepointDefaultTimeout = 10 * time.Minute

// savepointPollInterval bounds how often the snapshot row is re-read while
// waiting. The row is written by a different process (sandboxd completing the
// job), so polling is the only signal available here.
const savepointPollInterval = 250 * time.Millisecond

// savepointQueries is the narrow generated-query surface the savepoint creator
// needs. *db.Queries satisfies it in production; tests substitute a fake.
type savepointQueries interface {
	CreateSandboxSnapshot(ctx context.Context, arg db.CreateSandboxSnapshotParams) (db.SandboxSnapshot, error)
	AttachSandboxSnapshotToCheckpoint(ctx context.Context, arg db.AttachSandboxSnapshotToCheckpointParams) (db.SandboxSnapshot, error)
	GetSandboxSnapshotForWorkspace(ctx context.Context, arg db.GetSandboxSnapshotForWorkspaceParams) (db.SandboxSnapshot, error)
	MarkSandboxSnapshotFailed(ctx context.Context, arg db.MarkSandboxSnapshotFailedParams) (db.SandboxSnapshot, error)
	UpdateSandboxInstanceStatus(ctx context.Context, arg db.UpdateSandboxInstanceStatusParams) (db.SandboxInstance, error)
}

// sandboxJobEnqueuer is the job-dispatch surface the savepoint creator needs.
// EnvSandboxLifecycleDeps satisfies it, so production passes the lifecycle deps
// adapter straight through.
type sandboxJobEnqueuer interface {
	EnqueueSandboxJob(ctx context.Context, workspaceID, actorUserID, nodeID, instanceID, jobType string, payload json.RawMessage) (SandboxLifecycleJobResult, error)
	NotifySandboxJobAvailable(ctx context.Context, nodeID, jobID string) error
}

var (
	_ savepointQueries   = (*db.Queries)(nil)
	_ sandboxJobEnqueuer = (EnvSandboxLifecycleDeps)(nil)
)

type savepointCreator struct {
	q        savepointQueries
	jobs     sandboxJobEnqueuer
	timeout  time.Duration
	interval time.Duration
}

// NewSavepointCreator constructs the production savepoint creator over the
// existing create_template path: it persists a sandbox_snapshot row, binds that
// row to its owning checkpoint, enqueues the job sandboxd already knows how to
// run, and waits for the row to reach a terminal state.
func NewSavepointCreator(q savepointQueries, jobs sandboxJobEnqueuer) SavepointCreator {
	return newSavepointCreator(q, jobs, savepointDefaultTimeout, savepointPollInterval)
}

func newSavepointCreator(q savepointQueries, jobs sandboxJobEnqueuer, timeout, interval time.Duration) SavepointCreator {
	return &savepointCreator{q: q, jobs: jobs, timeout: timeout, interval: interval}
}

// savepointStatusIsTerminal reports whether waiting can stop. `deleting` counts:
// a savepoint someone is already releasing will never become ready, so waiting
// for it only burns the caller's deadline.
func savepointStatusIsTerminal(status string) bool {
	switch status {
	case savepointStatusReady, savepointStatusFailed, savepointStatusDeleting:
		return true
	default:
		return false
	}
}

func (c *savepointCreator) CreateSavepoint(ctx context.Context, ref SandboxInstanceRef, checkpointID, actorUserID string) (Savepoint, error) {
	wsUUID, err := util.ParseUUID(ref.WorkspaceID)
	if err != nil {
		return Savepoint{}, fmt.Errorf("parse workspace_id: %w", err)
	}
	instUUID, err := util.ParseUUID(ref.InstanceID)
	if err != nil {
		return Savepoint{}, fmt.Errorf("parse instance_id: %w", err)
	}
	nodeUUID, err := util.ParseUUID(ref.NodeID)
	if err != nil {
		return Savepoint{}, fmt.Errorf("parse node_id: %w", err)
	}
	cpUUID, err := util.ParseUUID(checkpointID)
	if err != nil {
		return Savepoint{}, fmt.Errorf("parse checkpoint_id: %w", err)
	}
	// The actor is optional: a savepoint taken by a scheduled checkpoint has no
	// interactive user, and creator_user_id is nullable.
	actorUUID, _ := util.ParseUUID(actorUserID)

	name := fmt.Sprintf("savepoint-%s", checkpointID)
	snap, err := c.q.CreateSandboxSnapshot(ctx, db.CreateSandboxSnapshotParams{
		WorkspaceID:    wsUUID,
		NodeID:         nodeUUID,
		InstanceID:     instUUID,
		CreatorUserID:  actorUUID,
		CubeSnapshotID: "",
		Name:           name,
		Description:    "env checkpoint savepoint",
		Status:         savepointStatusCreating,
		Metadata:       []byte(`{}`),
	})
	if err != nil {
		return Savepoint{}, fmt.Errorf("create savepoint row: %w", err)
	}
	snapshotID := uuidText(snap.ID)
	sp := Savepoint{SnapshotID: snapshotID, InstanceID: ref.InstanceID, Status: snap.Status}

	// Bind ownership before the job runs. The query refuses to steal a snapshot
	// another checkpoint owns, and ON DELETE CASCADE from the checkpoint is what
	// later lets deletion find the Cube template to release; an unattached
	// savepoint would leak it.
	if _, err := c.q.AttachSandboxSnapshotToCheckpoint(ctx, db.AttachSandboxSnapshotToCheckpointParams{
		CheckpointID: cpUUID,
		ID:           snap.ID,
		WorkspaceID:  wsUUID,
	}); err != nil {
		c.failSavepointRow(ctx, snap.ID, wsUUID, "failed to bind savepoint to its checkpoint")
		return sp, fmt.Errorf("attach savepoint %s to checkpoint %s: %w", snapshotID, checkpointID, err)
	}

	// Mirrors the existing snapshot endpoint: the source is marked snapshotting
	// for the duration and put back to running by the job completion. The source
	// is never stopped -- that is the whole difference from a save.
	_, _ = c.q.UpdateSandboxInstanceStatus(ctx, db.UpdateSandboxInstanceStatusParams{
		ID:     instUUID,
		Status: "snapshotting",
	})

	payload, err := json.Marshal(map[string]any{
		"instance_id": ref.InstanceID,
		"local_ref":   ref.LocalRef,
		"snapshot_id": snapshotID,
		"name":        name,
		"description": "env checkpoint savepoint",
	})
	if err != nil {
		c.failSavepointRow(ctx, snap.ID, wsUUID, "failed to encode create_template payload")
		return sp, fmt.Errorf("encode create_template payload: %w", err)
	}
	job, err := c.jobs.EnqueueSandboxJob(ctx, ref.WorkspaceID, actorUserID, ref.NodeID, ref.InstanceID, "create_template", payload)
	if err != nil {
		// A row with no job behind it would sit in `creating` until the sweeper
		// noticed, so fail it now while the cause is known.
		c.failSavepointRow(ctx, snap.ID, wsUUID, "failed to enqueue create_template job")
		return sp, fmt.Errorf("enqueue create_template job: %w", err)
	}
	if job.JobID != "" {
		_ = c.jobs.NotifySandboxJobAvailable(ctx, ref.NodeID, job.JobID)
	}
	return c.waitForSavepoint(ctx, snap.ID, wsUUID, sp)
}

// waitForSavepoint blocks until the row sandboxd is writing reaches a terminal
// state. A non-ready terminal state is returned without an error, because
// EnvCheckpointService.Create is what turns it into ErrSavepointFailed with the
// snapshot id attached; only never reaching a terminal state is an error here.
func (c *savepointCreator) waitForSavepoint(ctx context.Context, snapID, wsUUID pgtype.UUID, sp Savepoint) (Savepoint, error) {
	deadlineCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	for {
		row, err := c.q.GetSandboxSnapshotForWorkspace(deadlineCtx, db.GetSandboxSnapshotForWorkspaceParams{
			ID:          snapID,
			WorkspaceID: wsUUID,
		})
		if err == nil {
			sp.Status = row.Status
			sp.CubeSnapshotID = row.CubeSnapshotID
			if uuidText(row.ID) != "" {
				sp.SnapshotID = uuidText(row.ID)
			}
			if uuidText(row.InstanceID) != "" {
				sp.InstanceID = uuidText(row.InstanceID)
			}
			if savepointStatusIsTerminal(row.Status) {
				return sp, nil
			}
		}
		select {
		case <-deadlineCtx.Done():
			c.failSavepointRow(context.WithoutCancel(ctx), snapID, wsUUID, "savepoint timeout: create_template never reached a terminal state")
			sp.Status = savepointStatusFailed
			return sp, fmt.Errorf("%w: savepoint %s timed out", ErrSavepointFailed, sp.SnapshotID)
		case <-time.After(c.interval):
		}
	}
}

func (c *savepointCreator) failSavepointRow(ctx context.Context, snapID, wsUUID pgtype.UUID, reason string) {
	_, _ = c.q.MarkSandboxSnapshotFailed(ctx, db.MarkSandboxSnapshotFailedParams{
		ID:          snapID,
		WorkspaceID: wsUUID,
		Error:       pgtype.Text{String: reason, Valid: true},
	})
}
