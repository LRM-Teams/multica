package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestCountActiveTrainingTasks_Integration pins the idle-sweep gate against the
// real schema. Both halves of this query were wrong in a way only Postgres can
// catch: the status vocabulary (agent_inbox_event is
// pending/draining/acked/failed/suppressed — a task never reaches "completed")
// and the project scoping (rollout tasks run under per-project derived agents,
// so matching training_dispatch.train_agent_id selected nothing and, with no
// project predicate on the task, the count was global).
func TestCountActiveTrainingTasks_Integration(t *testing.T) {
	pool := interactionDAGTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "sweep-test", Slug: "sweep-test", IssuePrefix: "SW",
	})
	require.NoError(t, err)

	var rtID pgtype.UUID
	require.NoError(t, tx.QueryRow(ctx,
		`INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-sweep", "sweep-runtime", "cloud", "daytona", "online", "", []byte("{}"), "private",
	).Scan(&rtID))

	agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID: ws.ID, Name: "sweep-agent", DisplayName: "Sweep Agent",
		Description: "test", RuntimeMode: "cloud", RuntimeConfig: []byte("{}"),
		RuntimeID: rtID, MaxConcurrentTasks: 1,
		Instructions: "", CustomEnv: []byte("{}"), CustomArgs: []byte("[]"),
			Model:              pgtype.Text{String: "composer-1.5", Valid: true},
})
	require.NoError(t, err)

	// newTask mirrors a rollout task: the session_run row is what binds it to a
	// project, exactly as the training session-open hook records it.
	newTask := func(projectID pgtype.UUID, sessionID, status string) {
		t.Helper()
		var taskID pgtype.UUID
		require.NoError(t, tx.QueryRow(ctx,
			`INSERT INTO agent_inbox_event (workspace_id, agent_id, reason, status)
			 VALUES ($1, $2, 'dm', $3) RETURNING id`,
			ws.ID, agent.ID, status,
		).Scan(&taskID))
		require.NoError(t, q.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
			SessionID:  sessionID,
			ProjectID:  util.UUIDToString(projectID),
			AgentRunID: util.UUIDToString(taskID),
		}))
	}

	rollout := util.MustParseUUID("dddddddd-0000-0000-0000-000000000001")
	other := util.MustParseUUID("dddddddd-0000-0000-0000-000000000002")

	// A rollout whose tasks all reached a terminal status is idle. "suppressed"
	// (cancelled) is terminal alongside "acked"; neither is "completed", which
	// the status CHECK constraint would reject outright.
	newTask(rollout, "sweep-sess-0", "acked")
	newTask(rollout, "sweep-sess-1", "suppressed")

	count, err := q.CountActiveTrainingTasks(ctx, rollout)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "all-terminal rollout must be idle")

	// A second rollout still working must not keep the first one from sweeping.
	newTask(other, "other-sess-0", "pending")

	count, err = q.CountActiveTrainingTasks(ctx, rollout)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count, "another project's active task must not leak in")

	// Non-terminal statuses on this rollout do hold the sweep back. "failed" is
	// retryable, so it counts as active.
	for _, status := range []string{"pending", "draining", "failed"} {
		newTask(rollout, "sweep-active-"+status, status)
		count, err = q.CountActiveTrainingTasks(ctx, rollout)
		require.NoError(t, err)
		assert.Equal(t, int64(1), count, "status %q must count as active", status)

		_, err = tx.Exec(ctx,
			`UPDATE agent_inbox_event SET status = 'acked'
			 WHERE id = (SELECT agent_run_id::uuid FROM interaction_dag_session_run WHERE session_id = $1)`,
			"sweep-active-"+status)
		require.NoError(t, err)
	}
}
