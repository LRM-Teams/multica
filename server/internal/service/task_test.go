// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"sync"
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

func TestCreateRetryTaskOverridesRuntimeAndContext(t *testing.T) {
	env := setupRetryTestDB(t, "runtime_offline")
	ctx := context.Background()

	replacement, err := env.svc.Queries.PrecreateAgentRuntime(ctx, db.PrecreateAgentRuntimeParams{
		WorkspaceID: env.agent.WorkspaceID,
		DaemonID:    pgtype.Text{String: "daemon-retry-replacement", Valid: true},
		Name:        "replacement retry runtime",
		Provider:    "pi",
	})
	require.NoError(t, err)
	replacementContext := json.RawMessage(`{"ephemeral_sandbox":{"sandbox_instance_id":"sandbox-replacement","actor_user_id":"cccccccc-0000-0000-0000-000000000001"}}`)

	child, err := env.svc.Queries.CreateRetryTask(ctx, db.CreateRetryTaskParams{
		ID:        env.parent.ID,
		RuntimeID: replacement.ID,
		Context:   replacementContext,
	})
	require.NoError(t, err)
	assert.Equal(t, replacement.ID, child.RuntimeID)
	assert.JSONEq(t, string(replacementContext), string(child.Context))
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

// TestExtractEphemeralSandbox verifies the Phase 5 context-marker extraction
// helper for every edge case: valid marker, nil/empty context, missing key,
// null value, and an empty sandbox_instance_id.
func TestExtractEphemeralSandbox(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantOK  bool
		wantSID string
	}{
		{name: "nil context", input: nil, wantOK: false},
		{name: "empty json", input: []byte("{}"), wantOK: false},
		{name: "no ephemeral_sandbox key", input: []byte(`{"other":1}`), wantOK: false},
		{name: "null ephemeral_sandbox", input: []byte(`{"ephemeral_sandbox":null}`), wantOK: false},
		{name: "empty string instance_id", input: []byte(`{"ephemeral_sandbox":{"sandbox_instance_id":""}}`), wantOK: false},
		{name: "valid marker", input: []byte(`{"ephemeral_sandbox":{"sandbox_instance_id":"inst-42"}}`), wantOK: true, wantSID: "inst-42"},
		{name: "valid marker alongside squad_id", input: []byte(`{"squad_id":"sq","ephemeral_sandbox":{"sandbox_instance_id":"inst-1"}}`), wantOK: true, wantSID: "inst-1"},
		{name: "malformed json", input: []byte(`{notjson`), wantOK: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := extractEphemeralSandbox(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if tt.wantOK {
				if got == nil || got.SandboxInstanceID != tt.wantSID {
					t.Fatalf("SandboxInstanceID = %q, want %q", got.SandboxInstanceID, tt.wantSID)
				}
			}
		})
	}
}

// fakeEphemeralSandboxCleaner records calls to DeleteSandboxInstance.
type fakeEphemeralSandboxCleaner struct {
	mu    sync.Mutex
	calls []cleanerCall
}

type cleanerCall struct {
	WorkspaceID       string
	SandboxInstanceID string
}

func (f *fakeEphemeralSandboxCleaner) DeleteSandboxInstance(_ context.Context, workspaceID, instanceID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cleanerCall{workspaceID, instanceID})
	return nil
}

// TestMaybeCleanupEphemeralSandbox exercises the terminal cleanup hook with a
// real DB (tx-backed, hermetic). It verifies:
//   - Caller fires when the task carries a valid ephemeral_sandbox marker
//     and no other active task is on R'.
//   - R' is set offline.
func TestMaybeCleanupEphemeralSandbox(t *testing.T) {
	pool := interactionDAGTestPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "eph-cleanup", Slug: "eph-cleanup", IssuePrefix: "EC",
	})
	require.NoError(t, err)

	var rtID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-eph1", "eph-runtime", "local", "pi", "online", "", []byte("{}"), "private",
	).Scan(&rtID)
	require.NoError(t, err)

	var agentID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		ws.ID, "eph-agent", "Eph Agent", "local", []byte("{}"), rtID, "workspace", 1,
	).Scan(&agentID)
	require.NoError(t, err)

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "eph-project",
	).Scan(&projectID)
	require.NoError(t, err)

	withBump := `WITH bumped AS (UPDATE workspace SET issue_counter = issue_counter + 1 WHERE id = $1 RETURNING issue_counter)
	INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, project_id, number)
	SELECT $1, $2, 'in_progress', 'none', 'member', u.id, 'agent', $3, $4, (SELECT issue_counter FROM bumped)
	FROM "user" u LIMIT 1 RETURNING id`
	var issueID pgtype.UUID
	err = tx.QueryRow(ctx, withBump, ws.ID, "Eph Issue", agentID, projectID).Scan(&issueID)
	require.NoError(t, err)

	marker, _ := json.Marshal(map[string]any{
		"ephemeral_sandbox": map[string]string{"sandbox_instance_id": "inst-99"},
	})
	task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID:   agentID,
		RuntimeID: rtID,
		IssueID:   issueID,
		Priority:  0,
		Context:   marker,
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, task.ID)
	require.NoError(t, err)
	task, err = q.GetAgentTask(ctx, task.ID)
	require.NoError(t, err)

	cleaner := &fakeEphemeralSandboxCleaner{}
	svc := &TaskService{
		Queries:                 q,
		Bus:                     &events.Bus{},
		EphemeralSandboxCleaner: cleaner,
	}

	svc.maybeCleanupEphemeralSandbox(ctx, task)

	cleaner.mu.Lock()
	assert.Len(t, cleaner.calls, 1, "cleaner should have been called once")
	assert.Equal(t, "inst-99", cleaner.calls[0].SandboxInstanceID)
	assert.Equal(t, util.UUIDToString(ws.ID), cleaner.calls[0].WorkspaceID)
	cleaner.mu.Unlock()

	var rtStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM agent_runtime WHERE id = $1`, rtID).Scan(&rtStatus)
	require.NoError(t, err)
	assert.Equal(t, "offline", rtStatus)
}

// TestMaybeCleanupEphemeralSandbox_NoOpWithoutMarker verifies the cleanup is a
// no-op when the task has no ephemeral_sandbox marker in its context.
func TestMaybeCleanupEphemeralSandbox_NoOpWithoutMarker(t *testing.T) {
	pool := interactionDAGTestPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "eph-nomarker", Slug: "eph-nomarker", IssuePrefix: "EN",
	})
	require.NoError(t, err)

	var rtID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-nomarker", "nomarker-rt", "local", "pi", "online", "", []byte("{}"), "private",
	).Scan(&rtID)
	require.NoError(t, err)

	var agentID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		ws.ID, "nm-agent", "NM", "local", []byte("{}"), rtID, "workspace", 1,
	).Scan(&agentID)
	require.NoError(t, err)

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "nm-project",
	).Scan(&projectID)
	require.NoError(t, err)

	withBump := `WITH bumped AS (UPDATE workspace SET issue_counter = issue_counter + 1 WHERE id = $1 RETURNING issue_counter)
	INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, project_id, number)
	SELECT $1, $2, 'in_progress', 'none', 'member', u.id, 'agent', $3, $4, (SELECT issue_counter FROM bumped)
	FROM "user" u LIMIT 1 RETURNING id`
	var issueID pgtype.UUID
	err = tx.QueryRow(ctx, withBump, ws.ID, "NM Issue", agentID, projectID).Scan(&issueID)
	require.NoError(t, err)

	task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentID, RuntimeID: rtID, IssueID: issueID, Priority: 0,
		Context: []byte(`{"squad_id":"sq"}`),
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, task.ID)
	require.NoError(t, err)
	task, err = q.GetAgentTask(ctx, task.ID)
	require.NoError(t, err)

	cleaner := &fakeEphemeralSandboxCleaner{}
	svc := &TaskService{
		Queries: q, Bus: &events.Bus{}, EphemeralSandboxCleaner: cleaner,
	}

	svc.maybeCleanupEphemeralSandbox(ctx, task)

	assert.Len(t, cleaner.calls, 0, "cleaner must not fire without the ephemeral marker")
}

// TestMaybeCleanupEphemeralSandbox_SkipsWhenSiblingActive verifies the retry-
// safety guard: when another active task is still queued on the same R', the
// cleanup must skip so it does not reclaim the sandbox out from under the sibling.
func TestMaybeCleanupEphemeralSandbox_SkipsWhenSiblingActive(t *testing.T) {
	pool := interactionDAGTestPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "eph-sibling", Slug: "eph-sibling", IssuePrefix: "ES",
	})
	require.NoError(t, err)

	var rtID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-sib", "sib-rt", "local", "pi", "online", "", []byte("{}"), "private",
	).Scan(&rtID)
	require.NoError(t, err)

	var agentID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks) VALUES ($1, $2, $3, $4, $5, $6, $7, $8) RETURNING id`,
		ws.ID, "sib-agent", "SA", "local", []byte("{}"), rtID, "workspace", 1,
	).Scan(&agentID)
	require.NoError(t, err)

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "sib-project",
	).Scan(&projectID)
	require.NoError(t, err)

	withBump := `WITH bumped AS (UPDATE workspace SET issue_counter = issue_counter + 1 WHERE id = $1 RETURNING issue_counter)
	INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, project_id, number)
	SELECT $1, $2, 'in_progress', 'none', 'member', u.id, 'agent', $3, $4, (SELECT issue_counter FROM bumped)
	FROM "user" u LIMIT 1 RETURNING id`
	var issue1ID pgtype.UUID
	err = tx.QueryRow(ctx, withBump, ws.ID, "Sib Issue 1", agentID, projectID).Scan(&issue1ID)
	require.NoError(t, err)
	var issue2ID pgtype.UUID
	err = tx.QueryRow(ctx, withBump, ws.ID, "Sib Issue 2", agentID, projectID).Scan(&issue2ID)
	require.NoError(t, err)

	marker, _ := json.Marshal(map[string]any{
		"ephemeral_sandbox": map[string]string{"sandbox_instance_id": "inst-1"},
	})
	parent, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentID, RuntimeID: rtID, IssueID: issue1ID, Priority: 0, Context: marker,
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_task_queue SET status = 'completed', completed_at = now() WHERE id = $1`, parent.ID)
	require.NoError(t, err)
	parent, err = q.GetAgentTask(ctx, parent.ID)
	require.NoError(t, err)

	// Sibling: still queued on the same R'.
	_, err = q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentID, RuntimeID: rtID, IssueID: issue2ID, Priority: 0, Context: marker,
	})
	require.NoError(t, err)

	cleaner := &fakeEphemeralSandboxCleaner{}
	svc := &TaskService{
		Queries: q, Bus: &events.Bus{}, EphemeralSandboxCleaner: cleaner,
	}

	svc.maybeCleanupEphemeralSandbox(ctx, parent)

	cleaner.mu.Lock()
	assert.Len(t, cleaner.calls, 0, "cleanup must skip when a sibling task is still active on R'")
	cleaner.mu.Unlock()
}
