package handler

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/daemonws"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// A transition that races a terminal freeze must be acknowledged and dropped:
// the freeze settled the counters authoritatively, so rejecting the frame
// would strand a poison entry in the daemon outbox that replays on every
// reconnect, while applying it would corrupt frozen quiescence bookkeeping.
func TestRecordMixedRunActivityTransition_AcknowledgesAndDropsForTerminalRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedMixedRunDeliveryFixture(t)
	identity := daemonws.ClientIdentity{DaemonID: fixture.daemonID, WorkspaceID: testWorkspaceID}

	// A live transition applies normally while the run is running.
	live := protocol.MixedRunActivityTransitionPayload{
		AgentID: fixture.agentID, RuntimeID: fixture.runtimeID,
		RunID: fixture.runID.String(), RunAgentID: fixture.runAgentID.String(),
		TransitionID: uuid.NewString(),
		Dimension:    protocol.MixedRunActivityActiveTurn, Delta: 1,
	}
	require.NoError(t, testHandler.recordMixedRunActivityTransition(fixture.ctx, identity, live))

	// Freeze the run into a terminal status.
	snapshotID := "sha256:activity-late-" + fixture.runID.String()
	_, err := testPool.Exec(fixture.ctx, `
		UPDATE env_dispatch_run SET status = 'freezing' WHERE run_id = $1 AND status = 'running'`, fixture.runID)
	require.NoError(t, err)
	_, err = testHandler.Queries.CreateMixedRLFrozenSnapshot(fixture.ctx, db.CreateMixedRLFrozenSnapshotParams{
		SnapshotID: snapshotID, RunID: fixture.runID, RunStatus: "completed",
		SchemaVersion: "1", NormalizationVersion: "1",
		CanonicalManifest: []byte(`{"calls":[],"segments":[],"edges":[]}`), SnapshotHash: snapshotID,
	})
	require.NoError(t, err)
	_, err = testHandler.Queries.CompleteMixedRLRunWithSnapshot(fixture.ctx, db.CompleteMixedRLRunWithSnapshotParams{
		TerminalStatus: "completed", RunID: fixture.runID, SnapshotID: snapshotID, SnapshotHash: snapshotID,
	})
	require.NoError(t, err)

	// The racing settle transition is acknowledged (nil error -> hub acks) but
	// must neither mutate counters nor persist a new transition row.
	late := protocol.MixedRunActivityTransitionPayload{
		AgentID: fixture.agentID, RuntimeID: fixture.runtimeID,
		RunID: fixture.runID.String(), RunAgentID: fixture.runAgentID.String(),
		TransitionID: uuid.NewString(),
		Dimension:    protocol.MixedRunActivityActiveTurn, Delta: -1,
	}
	require.NoError(t, testHandler.recordMixedRunActivityTransition(fixture.ctx, identity, late))

	var activeTurns int64
	require.NoError(t, testPool.QueryRow(fixture.ctx,
		`SELECT active_turn_count FROM env_dispatch_run WHERE run_id = $1`, fixture.runID).Scan(&activeTurns))
	assert.Equal(t, int64(1), activeTurns)

	var transitionRows int64
	require.NoError(t, testPool.QueryRow(fixture.ctx,
		`SELECT count(*) FROM env_dispatch_activity_transition WHERE run_id = $1 AND delta = -1`, fixture.runID).Scan(&transitionRows))
	assert.Equal(t, int64(0), transitionRows)
}

// A non-terminal run keeps the strict guard: counter underflow is an error so
// the daemon retains the entry for a later, possibly valid, retry.
func TestRecordMixedRunActivityTransition_RejectsUnderflowForActiveRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := seedMixedRunDeliveryFixture(t)
	identity := daemonws.ClientIdentity{DaemonID: fixture.daemonID, WorkspaceID: testWorkspaceID}

	underflow := protocol.MixedRunActivityTransitionPayload{
		AgentID: fixture.agentID, RuntimeID: fixture.runtimeID,
		RunID: fixture.runID.String(), RunAgentID: fixture.runAgentID.String(),
		TransitionID: uuid.NewString(),
		Dimension:    protocol.MixedRunActivityInflightTool, Delta: -1,
	}
	err := testHandler.recordMixedRunActivityTransition(fixture.ctx, identity, underflow)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "would make counter negative")
}
