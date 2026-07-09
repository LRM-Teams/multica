// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// retryWakeup is a TaskWakeupNotifier that appends a "NotifyTaskEnqueued"
// marker into the shared fakeRLClient.callOrder slice so tests can assert the
// D9 ordering: the child's StartSession (fresh session open) must happen BEFORE
// NotifyTaskEnqueued (daemon announce). The existing fakeRLClient already
// records "StartSession"/"EndSession"/"SetReward"; this adapter adds the notify
// signal to the same ordered log without inventing a parallel harness.
type retryWakeup struct {
	rl *fakeRLClient
}

func (w *retryWakeup) NotifyTaskAvailable(runtimeID, taskID string) {
	w.rl.callOrder = append(w.rl.callOrder, "NotifyTaskEnqueued")
}

// retryTestEnv bundles the fakes + tx-backed TaskService built by
// setupRetryTestDB for a MaybeRetryFailedTask test.
type retryTestEnv struct {
	svc      *TaskService
	rl       *fakeRLClient
	dagStore *fakeInteractionDAGStore
	parent   db.AgentTaskQueue
	project  db.Project
	issue    db.Issue
	agent    db.Agent
}

// setupRetryTestDB builds a real-Postgres, tx-backed fixture exercising the
// MaybeRetryFailedTask -> openFreshSessionForRetryChild path:
//
//	workspace -> agent_runtime -> agent -> project(env_id) -> issue
//	-> parent task (failed, areal_proxy context, failureReason, attempt budget)
//
// The TaskService uses tx-backed Queries (so CreateRetryTask /
// StripArealProxyFromTaskContext / GetIssue / GetProject run real SQL) with
// FAKE Training deps: a fakeRLClient (RL starter + closer, records call order),
// a fakeDispatchLookup (so the child's agent resolves as the train target), a
// fakeTaskStore returning a task with NO areal_proxy (so the child's
// idempotency guard passes and a fresh session opens), and a fake DAG store
// (so D10 RecordSessionAgentRun is observable). The tx rolls back on cleanup,
// so the test is hermetic. failureReason controls retryability: "timeout" is
// retryable; "iteration_limit" is not.
func setupRetryTestDB(t *testing.T, failureReason string) *retryTestEnv {
	t.Helper()
	pool := interactionDAGTestPool(t)
	t.Cleanup(pool.Close)

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "retry-test", Slug: "retry-test", IssuePrefix: "RT",
	})
	require.NoError(t, err)

	// Raw INSERT (not UpsertAgentRuntime): the upsert's ON CONFLICT target is a
	// partial unique index (profile_id IS NULL) that Postgres cannot infer for
	// this test DB, so the generated query fails. The FK target only needs a row.
	var rtID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-retry", "retry-runtime", "cloud", "daytona", "online", "", []byte("{}"), "private",
	).Scan(&rtID)
	require.NoError(t, err)

	agent, err := q.CreateAgent(ctx, db.CreateAgentParams{
		WorkspaceID:        ws.ID,
		Name:               "retry-agent",
		DisplayName:        "Retry Agent",
		Description:        "test",
		RuntimeMode:        "cloud",
		RuntimeConfig:      []byte("{}"),
		RuntimeID:          rtID,
		Visibility:         "workspace",
		MaxConcurrentTasks: 1,
		Instructions:       "",
		CustomEnv:          []byte("{}"),
		CustomArgs:         []byte("[]"),
	})
	require.NoError(t, err)

	// Project carries an env_id; D9 requires the child's StartSession env_id to
	// come from here (not the areal session id). env_id FKs environment(id), so
	// seed an environment row with the chosen id first.
	envUUID := util.MustParseUUID("eeeeeeee-0000-0000-0000-000000000001")
	_, err = tx.Exec(ctx, `INSERT INTO environment (id, workspace_id, sandbox_ids, mode, domain) VALUES ($1, $2, $3, $4, $5)`,
		envUUID, ws.ID, []string{}, "scratch", "swe_lego")
	require.NoError(t, err)
	proj, err := q.CreateProjectWithEnv(ctx, db.CreateProjectWithEnvParams{
		WorkspaceID: ws.ID, Title: "retry-proj", Status: "in_progress", Priority: "none", EnvID: envUUID,
	})
	require.NoError(t, err)

	issue, err := q.CreateIssue(ctx, db.CreateIssueParams{
		WorkspaceID: ws.ID,
		Title:       "retry-issue",
		Status:      "in_progress",
		Priority:    "medium",
		CreatorType: "member",
		CreatorID:   util.MustParseUUID("cccccccc-0000-0000-0000-000000000001"),
		Number:      1,
		ProjectID:   proj.ID,
	})
	require.NoError(t, err)

	// Parent task: created queued, then flipped to failed with an areal_proxy
	// context (so RouteTerminalTrainingTask closes S_A), the given
	// failure_reason, and a remaining attempt budget (so retry is eligible for
	// retryable reasons).
	parent, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agent.ID, RuntimeID: rtID, IssueID: issue.ID, Priority: 0,
	})
	require.NoError(t, err)
	parentCtx := arealProxyContext("sess-parent", "pk-parent")
	_, err = tx.Exec(ctx, `UPDATE agent_task_queue SET status='failed', failure_reason=$1, context=$2, attempt=1, max_attempts=3 WHERE id=$3`,
		failureReason, parentCtx, parent.ID)
	require.NoError(t, err)
	parent, err = q.GetAgentTask(ctx, parent.ID)
	require.NoError(t, err)

	// --- fake Training deps ---
	rl := &fakeRLClient{creds: arealrl.SessionCreds{SessionID: "sess-child", ProxyKey: "pk-child"}}
	lookup := &fakeDispatchLookup{dispatch: db.TrainingDispatch{
		ProjectID: proj.ID, TrainAgentID: agent.ID, DefaultReward: 1.0,
	}}
	// The store returns a task with NO areal_proxy so the child's idempotency
	// guard passes and a fresh session opens (Task 6 stripped areal_proxy from
	// the child's DB row; the fake mirrors that post-strip state).
	store := &fakeTaskStore{task: db.AgentTaskQueue{IssueID: issue.ID}}
	dagStore := newFakeInteractionDAGStore()
	dag := NewInteractionDAGService(dagStore, &fakeArealSegmentClient{}, true)

	svc := &TaskService{
		Queries: q,
		Bus:     events.New(),
		Training: &TrainingSessionDeps{
			Lookup:   lookup,
			Store:    store,
			RL:       rl,
			Closer:   rl,
			ProxyURL: testProxyURL,
			DAG:      dag,
		},
		Wakeup: &retryWakeup{rl: rl},
	}

	return &retryTestEnv{
		svc: svc, rl: rl, dagStore: dagStore,
		parent: parent, project: proj, issue: issue, agent: agent,
	}
}

// callOrderIndex returns the index of the first occurrence of name in the RL
// fake's ordered call log, or -1 if absent.
func callOrderIndex(rl *fakeRLClient, name string) int {
	for i, e := range rl.callOrder {
		if e == name {
			return i
		}
	}
	return -1
}

// TestMaybeRetryFailedTask_ChildOpensFreshSessionBeforeNotify verifies the D9
// ordering: a trained parent fails with a retryable reason; RouteTerminalTrainingTask
// closes the parent's session (S_A EndSession) BEFORE MaybeRetryFailedTask opens
// the child's FRESH session (S_B StartSession), and the child's session open +
// RecordSessionAgentRun fires BEFORE NotifyTaskEnqueued (open->broadcast->notify).
func TestMaybeRetryFailedTask_ChildOpensFreshSessionBeforeNotify(t *testing.T) {
	env := setupRetryTestDB(t, "timeout")
	ctx := context.Background()

	// Parent terminal routing closes S_A (mirrors FailTask:1685).
	env.svc.RouteTerminalTrainingTask(ctx, env.parent)
	// Auto-retry spawns the child + opens its fresh session (mirrors FailTask:1690).
	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	require.NotNil(t, child, "retryable failure must spawn a child task")

	// Parent EndSession (S_A closed) BEFORE child StartSession (S_B opened).
	endIdx := callOrderIndex(env.rl, "EndSession")
	startIdx := callOrderIndex(env.rl, "StartSession")
	require.NotEqual(t, -1, endIdx, "parent EndSession must fire")
	require.NotEqual(t, -1, startIdx, "child StartSession must fire")
	assert.Less(t, endIdx, startIdx, "parent EndSession must precede child StartSession")

	// Child StartSession BEFORE NotifyTaskEnqueued (open->broadcast->notify).
	notifyIdx := callOrderIndex(env.rl, "NotifyTaskEnqueued")
	require.NotEqual(t, -1, notifyIdx, "NotifyTaskEnqueued must fire for the child")
	assert.Less(t, startIdx, notifyIdx, "child StartSession must precede NotifyTaskEnqueued")

	// D10: the child's session open records {session_id -> agent_run_id=child.ID}.
	env.dagStore.mu.Lock()
	run, ok := env.dagStore.sessionRuns["sess-child"]
	env.dagStore.mu.Unlock()
	require.True(t, ok, "RecordSessionAgentRun must fire for the child session")
	assert.Equal(t, util.UUIDToString(child.ID), run.AgentRunID,
		"agent_run_id must be the child task.ID (D8), not the agent id")
	assert.Equal(t, util.UUIDToString(env.project.ID), run.ProjectID)

	// The child is a fresh attempt (attempt incremented) carrying the parent link.
	assert.Equal(t, env.parent.Attempt+1, child.Attempt)
	assert.True(t, child.ParentTaskID.Valid, "child must point back at parent")
	assert.Equal(t, env.parent.ID, child.ParentTaskID)
}

// TestMaybeRetryFailedTask_NonRetryableIsTerminal verifies that a non-retryable
// failure reason is terminal: the parent's session is closed, no retry child is
// created, and no fresh session is opened (no StartSession).
func TestMaybeRetryFailedTask_NonRetryableIsTerminal(t *testing.T) {
	// "iteration_limit" is resume-unsafe and NOT in retryableReasons.
	env := setupRetryTestDB(t, "iteration_limit")
	ctx := context.Background()

	env.svc.RouteTerminalTrainingTask(ctx, env.parent)
	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	assert.Nil(t, child, "non-retryable failure must not spawn a child")

	// Session was closed (terminal), but no fresh session opened for a child.
	assert.NotEqual(t, -1, callOrderIndex(env.rl, "EndSession"),
		"parent session must be closed on terminal routing")
	assert.Equal(t, -1, callOrderIndex(env.rl, "StartSession"),
		"no child StartSession for a non-retryable failure")
	assert.Equal(t, -1, callOrderIndex(env.rl, "NotifyTaskEnqueued"),
		"no NotifyTaskEnqueued when no child is created")
}

// TestMaybeRetryFailedTask_EnvIDFromProjectEnvID verifies the child's StartSession
// receives the project's env_id (resolved via issue -> project), NOT the areal
// session id. This is the D9 resolution path the helper implements.
func TestMaybeRetryFailedTask_EnvIDFromProjectEnvID(t *testing.T) {
	env := setupRetryTestDB(t, "timeout")
	ctx := context.Background()

	env.svc.RouteTerminalTrainingTask(ctx, env.parent)
	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	require.NotNil(t, child)

	require.NotEqual(t, -1, callOrderIndex(env.rl, "StartSession"), "child must open a session")
	// StartSession env_id must equal project.env_id (the canonical UUID string),
	// not the areal session id ("sess-child" / "sess-parent").
	assert.Equal(t, util.UUIDToString(env.project.EnvID), env.rl.lastEnv,
		"child StartSession env_id must come from project.env_id")
	assert.NotEqual(t, "sess-child", env.rl.lastEnv)
	// task id passed to StartSession is the child's, confirming a fresh session.
	assert.Equal(t, util.UUIDToString(child.ID), env.rl.lastTask)
}
