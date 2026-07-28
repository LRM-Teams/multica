// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// --- Integration test against a real Postgres (skipped without DATABASE_URL) ---
//
// This is the only check that migration 244's schema and the savepoint-ownership
// queries actually work. The environment this change was built in had no
// Postgres, so it has never been executed: treat a first green run as new
// information, not a regression check.

func envCheckpointTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return pool
}

// TestEnvCheckpointSavepointQueries_Integration exercises save_mode and
// savepoint ownership inside a transaction that rolls back, so it is hermetic.
// It covers what no test without a database can: that the generated column
// lists and scan targets agree, that a savepoint cannot be stolen from its
// owner, and that deleting a checkpoint releases its savepoints by cascade.
func TestEnvCheckpointSavepointQueries_Integration(t *testing.T) {
	pool := envCheckpointTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	// sandbox_snapshot needs a workspace and a node; env_checkpoint has no FKs.
	var wsID, nodeID string
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ('cp savepoint test', 'cp-savepoint-' || gen_random_uuid()::text)
		RETURNING id::text`).Scan(&wsID))
	require.NoError(t, tx.QueryRow(ctx, `
		INSERT INTO sandbox_node (node_key, name)
		VALUES ('cp-savepoint-' || gen_random_uuid()::text, 'cp savepoint node')
		RETURNING id::text`).Scan(&nodeID))
	workspace := util.MustParseUUID(wsID)

	var projID string
	require.NoError(t, tx.QueryRow(ctx, `SELECT gen_random_uuid()::text`).Scan(&projID))

	newCheckpoint := func(mode EnvCheckpointSaveMode) db.EnvCheckpoint {
		cp, err := q.CreateEnvCheckpoint(ctx, db.CreateEnvCheckpointParams{
			WorkspaceID:    workspace,
			ProjectID:      util.MustParseUUID(projID),
			EventRef:       "evt",
			CheckpointKind: "structural",
			EnvIDMap:       []byte(`{}`),
			SandboxRefs:    []byte(`[]`),
			DbSnapshot:     []byte(`{}`),
			SaveTimeoutMs:  30_000,
			SaveStatus:     "complete",
			SaveMode:       string(mode),
		})
		require.NoError(t, err)
		return cp
	}

	owner := newCheckpoint(SaveModeSnapshot)
	assert.Equal(t, string(SaveModeSnapshot), owner.SaveMode, "save_mode must round-trip through create")

	// CreateSandboxSnapshot now selects checkpoint_id too, so a successful call
	// here is the proof that its column list and scan targets stay aligned.
	snap, err := q.CreateSandboxSnapshot(ctx, db.CreateSandboxSnapshotParams{
		WorkspaceID:    workspace,
		NodeID:         util.MustParseUUID(nodeID),
		CubeSnapshotID: "cube-savepoint-1",
		Name:           "savepoint 1",
		Status:         "ready",
		Metadata:       []byte(`{}`),
	})
	require.NoError(t, err)
	assert.False(t, snap.CheckpointID.Valid, "a fresh snapshot is owned by nobody")

	attach := func(cp db.EnvCheckpoint) (db.SandboxSnapshot, error) {
		return q.AttachSandboxSnapshotToCheckpoint(ctx, db.AttachSandboxSnapshotToCheckpointParams{
			CheckpointID: cp.ID,
			ID:           snap.ID,
			WorkspaceID:  workspace,
		})
	}

	owned, err := attach(owner)
	require.NoError(t, err)
	assert.Equal(t, owner.ID, owned.CheckpointID)

	// Retrying the same owner must not fail, so a retried checkpoint create is
	// safe.
	_, err = attach(owner)
	require.NoError(t, err, "attaching the same owner twice must be idempotent")

	// A second checkpoint must not be able to take the savepoint away: the
	// UPDATE matches no row, which a :one query reports as no rows.
	other := newCheckpoint(SaveModeSnapshot)
	_, err = attach(other)
	require.ErrorIs(t, err, pgx.ErrNoRows, "a savepoint must not be stolen from its owner")

	listed, err := q.ListSandboxSnapshotsForCheckpoint(ctx, db.ListSandboxSnapshotsForCheckpointParams{
		CheckpointID: owner.ID,
		WorkspaceID:  workspace,
	})
	require.NoError(t, err)
	require.Len(t, listed, 1)
	assert.Equal(t, snap.ID, listed[0].ID)

	empty, err := q.ListSandboxSnapshotsForCheckpoint(ctx, db.ListSandboxSnapshotsForCheckpointParams{
		CheckpointID: other.ID,
		WorkspaceID:  workspace,
	})
	require.NoError(t, err)
	assert.Empty(t, empty)

	updated, err := q.UpdateEnvCheckpointSaveMode(ctx, db.UpdateEnvCheckpointSaveModeParams{
		SaveMode:    string(SaveModePauseInPlace),
		ID:          owner.ID,
		WorkspaceID: workspace,
	})
	require.NoError(t, err)
	assert.Equal(t, string(SaveModePauseInPlace), updated.SaveMode)

	// Deleting the checkpoint releases its savepoints. The row carrying
	// cube_snapshot_id goes with it, so whatever releases the Cube template must
	// read that id before deleting the checkpoint.
	_, err = tx.Exec(ctx, `DELETE FROM env_checkpoint WHERE id = $1`, owner.ID)
	require.NoError(t, err)
	var remaining int
	require.NoError(t, tx.QueryRow(ctx,
		`SELECT count(*) FROM sandbox_snapshot WHERE id = $1`, snap.ID).Scan(&remaining))
	assert.Equal(t, 0, remaining, "deleting a checkpoint must release its savepoints")

	// Last, because a constraint violation aborts the surrounding transaction.
	_, err = tx.Exec(ctx, `UPDATE env_checkpoint SET save_mode = 'bogus' WHERE id = $1`, other.ID)
	require.Error(t, err, "save_mode CHECK must reject unknown values")
}
