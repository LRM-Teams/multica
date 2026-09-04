// SPDX-License-Identifier: Apache-2.0

package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGraphMemoryToolOperationFailedReservationReleasesKey pins the durable
// half of the 2026-09-04 run7 fix: a reservation completed with an error is
// terminal (a same-key call never sees OPERATION_PENDING again) and releases
// its idempotency key, so the retry that fixes the refusal — carrying a
// possibly different request — is admitted fresh. A successfully completed
// operation still replays, and a failed key reused for another operation
// remains a replay conflict.
func TestGraphMemoryToolOperationFailedReservationReleasesKey(t *testing.T) {
	t.Setenv("MULTICA_MEMORY_TYPE", "graph")
	pool := graphMemoryRunSegmentTestPool(t)
	f := graphMemoryRunFixtures(t, pool)
	ctx := t.Context()
	// One active run per channel: reuse the fixture's running run.
	runID := f.runID
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM graph_memory_agent_tool_operation
			WHERE trajectory_id IN (SELECT id FROM graph_memory_agent_trajectory WHERE run_id=$1::uuid)`, runID)
	})
	store := NewGraphMemoryAgentRunStore(pool)
	graphID := "channel:" + f.channelID

	const key = "release-key"
	res, err := store.ReserveToolOperation(ctx, runID, 1, key, graphID, "submit", []byte(`{"n":1}`))
	require.NoError(t, err)
	require.False(t, res.Replay)
	require.False(t, res.Pending)

	require.NoError(t, store.CompleteToolOperation(ctx, runID, 1, res.OperationID,
		[]byte(`{}`), "citation node \"daily:x\" was not viewed by the submitted trajectory"))

	res2, err := store.ReserveToolOperation(ctx, runID, 1, key, graphID, "submit", []byte(`{"n":2}`))
	require.NoError(t, err)
	require.False(t, res2.Replay, "a failed reservation must not replay")
	require.False(t, res2.Pending, "a failed reservation must not block as pending")

	require.NoError(t, store.CompleteToolOperation(ctx, runID, 1, res2.OperationID, []byte(`{"ok":true}`), ""))

	res3, err := store.ReserveToolOperation(ctx, runID, 1, key, graphID, "submit", []byte(`{"n":2}`))
	require.NoError(t, err)
	require.True(t, res3.Replay, "a successful completion still replays")
	assert.Equal(t, `{"ok":true}`, string(res3.Response))

	const otherKey = "release-other"
	res4, err := store.ReserveToolOperation(ctx, runID, 1, otherKey, graphID, "explore", []byte(`{}`))
	require.NoError(t, err)
	require.NoError(t, store.CompleteToolOperation(ctx, runID, 1, res4.OperationID, []byte(`{}`), "quota exceeded"))
	_, err = store.ReserveToolOperation(ctx, runID, 1, otherKey, graphID, "redirect", []byte(`{}`))
	assert.ErrorIs(t, err, ErrGraphMemoryToolReplayConflict)
}
