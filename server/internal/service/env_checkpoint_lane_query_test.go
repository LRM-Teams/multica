// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- Integration test against a real Postgres (skipped without DATABASE_URL) ---
//
// Lane idempotency is a database constraint, so an in-memory fake cannot
// demonstrate it: this is the only test that can. It has never been executed,
// because the environment this change was built in had no Postgres.

func insertLaneTestCheckpoint(t *testing.T, ctx context.Context, tx pgx.Tx) db.EnvCheckpoint {
	t.Helper()
	var wsID, projID string
	require.NoError(t, tx.QueryRow(ctx, `SELECT gen_random_uuid()::text, gen_random_uuid()::text`).Scan(&wsID, &projID))
	cp, err := db.New(tx).CreateEnvCheckpoint(ctx, db.CreateEnvCheckpointParams{
		WorkspaceID:    util.MustParseUUID(wsID),
		ProjectID:      util.MustParseUUID(projID),
		EventRef:       "evt",
		CheckpointKind: "structural",
		EnvIDMap:       []byte(`{}`),
		SandboxRefs:    []byte(`[]`),
		DbSnapshot:     []byte(`{}`),
		SaveTimeoutMs:  30_000,
		SaveStatus:     "complete",
		SaveMode:       string(SaveModeSnapshot),
	})
	require.NoError(t, err)
	return cp
}

// TestEnvCheckpointLaneUniqueIndex_Integration proves that two claims of one
// lane key create exactly one lane, that an interrupted lane is continued on the
// same row rather than duplicated, and that deleting a checkpoint releases its
// lanes. It runs inside a transaction that rolls back, so it is hermetic.
func TestEnvCheckpointLaneUniqueIndex_Integration(t *testing.T) {
	pool := envCheckpointTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	cp := insertLaneTestCheckpoint(t, ctx, tx)

	claim := func(laneKey string) (db.EnvCheckpointLane, error) {
		return q.ClaimEnvCheckpointLane(ctx, db.ClaimEnvCheckpointLaneParams{
			CheckpointID: cp.ID,
			LaneKey:      laneKey,
		})
	}

	first, err := claim("lane-0")
	require.NoError(t, err, "first claim must win")
	assert.Equal(t, "provisioning", first.Status)
	assert.Equal(t, cp.WorkspaceID, first.WorkspaceID,
		"the lane must inherit its checkpoint's workspace")

	// The losing claim is reported as no rows, which is how a concurrent caller
	// learns to read the existing lane instead of building a second one.
	_, err = claim("lane-0")
	require.ErrorIs(t, err, pgx.ErrNoRows, "a second claim of the same lane key must lose")

	lanes, err := q.ListEnvCheckpointLanes(ctx, db.ListEnvCheckpointLanesParams{
		CheckpointID: cp.ID,
		WorkspaceID:  cp.WorkspaceID,
	})
	require.NoError(t, err)
	require.Len(t, lanes, 1, "one lane key must yield exactly one lane")

	// An interrupted lane recorded its instance but not its task. Claiming again
	// must not create a second row, and the recorded instance must survive.
	var instanceID string
	require.NoError(t, tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&instanceID))
	stepped, err := q.UpdateEnvCheckpointLaneStep(ctx, db.UpdateEnvCheckpointLaneStepParams{
		InstanceID:  util.MustParseUUID(instanceID),
		ID:          first.ID,
		WorkspaceID: cp.WorkspaceID,
	})
	require.NoError(t, err)
	assert.Equal(t, util.MustParseUUID(instanceID), stepped.InstanceID)
	assert.False(t, stepped.TaskID.Valid, "an unfilled step stays NULL")

	_, err = claim("lane-0")
	require.ErrorIs(t, err, pgx.ErrNoRows, "claiming an interrupted lane must not create a second row")

	// Filling a later step must not regress the earlier one: that is what the
	// COALESCE is for.
	var taskID string
	require.NoError(t, tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&taskID))
	continued, err := q.UpdateEnvCheckpointLaneStep(ctx, db.UpdateEnvCheckpointLaneStepParams{
		TaskID:      util.MustParseUUID(taskID),
		ID:          first.ID,
		WorkspaceID: cp.WorkspaceID,
	})
	require.NoError(t, err)
	assert.Equal(t, util.MustParseUUID(instanceID), continued.InstanceID,
		"continuing a lane must keep the steps it already completed")
	assert.Equal(t, util.MustParseUUID(taskID), continued.TaskID)

	// A different lane key expands the frontier on the same checkpoint.
	second, err := claim("lane-1")
	require.NoError(t, err)
	require.NotEqual(t, first.ID, second.ID)

	countProvisioning := func() int64 {
		n, err := q.CountProvisioningEnvCheckpointLanes(ctx, db.CountProvisioningEnvCheckpointLanesParams{
			CheckpointID: cp.ID,
			WorkspaceID:  cp.WorkspaceID,
		})
		require.NoError(t, err)
		return n
	}
	require.Equal(t, int64(2), countProvisioning())

	ready, err := q.MarkEnvCheckpointLaneReady(ctx, db.MarkEnvCheckpointLaneReadyParams{
		ID:          first.ID,
		WorkspaceID: cp.WorkspaceID,
	})
	require.NoError(t, err)
	assert.Equal(t, "ready", ready.Status)
	require.Equal(t, int64(1), countProvisioning(),
		"only provisioning lanes may block checkpoint deletion")

	failed, err := q.MarkEnvCheckpointLaneFailed(ctx, db.MarkEnvCheckpointLaneFailedParams{
		Error:       ptrText("provision timed out"),
		ID:          second.ID,
		WorkspaceID: cp.WorkspaceID,
	})
	require.NoError(t, err)
	assert.Equal(t, "failed", failed.Status)
	require.Equal(t, int64(0), countProvisioning())

	// A lane must be invisible from another workspace.
	var otherWorkspace string
	require.NoError(t, tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&otherWorkspace))
	_, err = q.GetEnvCheckpointLane(ctx, db.GetEnvCheckpointLaneParams{
		CheckpointID: cp.ID,
		LaneKey:      "lane-0",
		WorkspaceID:  util.MustParseUUID(otherWorkspace),
	})
	require.ErrorIs(t, err, pgx.ErrNoRows, "lane reads must be workspace scoped")

	// Deleting the checkpoint releases its lanes.
	_, err = tx.Exec(ctx, `DELETE FROM env_checkpoint WHERE id = $1`, cp.ID)
	require.NoError(t, err)
	var remaining int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM env_checkpoint_lane WHERE checkpoint_id = $1`, cp.ID).Scan(&remaining))
	assert.Equal(t, 0, remaining, "checkpoint deletion cascades its lanes")

	// Last, because a constraint violation aborts the surrounding transaction.
	other := insertLaneTestCheckpoint(t, ctx, tx)
	lane, err := q.ClaimEnvCheckpointLane(ctx, db.ClaimEnvCheckpointLaneParams{
		CheckpointID: other.ID,
		LaneKey:      "lane-0",
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE env_checkpoint_lane SET status = 'bogus' WHERE id = $1`, lane.ID)
	require.Error(t, err, "the lane status CHECK must reject unknown values")
}
