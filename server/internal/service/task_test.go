// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// retryWakeup is a TaskWakeupNotifier that appends a "NotifyTaskEnqueued"
// marker into the shared fakeRLClient.callOrder slice so tests can assert
// whether the daemon announce fired (and in what order relative to any
// session lifecycle calls). The existing fakeRLClient already records
// "StartSession"/"SetReward"; this adapter adds the notify signal to the
// same ordered log without inventing a parallel harness.
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
	parent   db.AgentInboxEvent
	project  db.Project
	issue    db.Issue
	agent    db.Agent
}

type fakeEphemeralSandboxManager struct {
	prepared   *EphemeralRetryResources
	prepareErr error
	reclaims   int
	cleanups   int
}

func (f *fakeEphemeralSandboxManager) PrepareRetry(context.Context, db.AgentInboxEvent) (*EphemeralRetryResources, error) {
	return f.prepared, f.prepareErr
}

func (f *fakeEphemeralSandboxManager) Reclaim(context.Context, *EphemeralRetryResources) error {
	f.reclaims++
	return nil
}

func (f *fakeEphemeralSandboxManager) Cleanup(context.Context, db.AgentInboxEvent) error {
	f.cleanups++
	return nil
}

func ephemeralRetryMarker(instanceID string) json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{"ephemeral_sandbox":{"sandbox_instance_id":%q,"actor_user_id":"cccccccc-0000-0000-0000-000000000001"}}`, instanceID))
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
		WorkspaceID:   ws.ID,
		Name:          "retry-agent",
		DisplayName:   "Retry Agent",
		Description:   "test",
		RuntimeMode:   "cloud",
		RuntimeConfig: []byte("{}"),
		RuntimeID:     rtID,
		Instructions:  "",
		CustomEnv:     []byte("{}"),
		CustomArgs:    []byte("[]"),
		Model:         pgtype.Text{String: "composer-1.5", Valid: true},
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
	// context (a LEGACY shape — under the Task 18 inversion this ordinary
	// source task must NOT trigger any session lifecycle), the given
	// failure_reason, and a remaining attempt budget (so retry is eligible for
	// retryable reasons).
	parent, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agent.ID, RuntimeID: rtID, IssueID: issue.ID, Priority: 0,
	})
	require.NoError(t, err)
	parentCtx := arealProxyContext("sess-parent", "pk-parent")
	_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET status='acked', terminal_outcome='failed', terminal_at=now(), acked_at=now(), completed_at=now(), failure_reason=$1, context=$2, attempt=1, max_attempts=3 WHERE id=$3`,
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
	store := &fakeTaskStore{task: db.AgentInboxEvent{IssueID: issue.ID}}
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

func TestCleanupCancelledTaskUsesEphemeralManager(t *testing.T) {
	env := setupRetryTestDB(t, "runtime_offline")
	manager := &fakeEphemeralSandboxManager{}
	env.svc.EphemeralSandboxManager = manager
	env.parent.Context = ephemeralRetryMarker("sandbox-cancelled")

	env.svc.finalizeCancelledTask(context.Background(), env.parent)

	assert.Equal(t, 1, manager.cleanups)
}

func TestMaybeCleanupEphemeralSandbox_PersistentMarkerSkipsManager(t *testing.T) {
	manager := &fakeEphemeralSandboxManager{}
	svc := &TaskService{EphemeralSandboxManager: manager}
	task := db.AgentInboxEvent{Context: json.RawMessage(`{
		"ephemeral_sandbox": {
			"sandbox_instance_id": "sandbox-env-dispatch",
			"cleanup_on_terminal": false
		}
	}`)}

	svc.maybeCleanupEphemeralSandbox(context.Background(), task)

	assert.Equal(t, 0, manager.cleanups, "explicitly persistent sandbox must await env-dispatch cleanup")
}

func TestMaybeRetryOfflineEphemeralTaskUsesFreshResources(t *testing.T) {
	env := setupRetryTestDB(t, "runtime_offline")
	ctx := context.Background()
	replacement, err := env.svc.Queries.PrecreateAgentRuntime(ctx, db.PrecreateAgentRuntimeParams{
		WorkspaceID: env.agent.WorkspaceID,
		DaemonID:    pgtype.Text{String: "daemon-fresh-retry", Valid: true},
		Name:        "fresh retry runtime",
		Provider:    "pi",
	})
	require.NoError(t, err)
	resources := &EphemeralRetryResources{
		RuntimeID: replacement.ID,
		Context:   ephemeralRetryMarker("sandbox-fresh"),
	}
	manager := &fakeEphemeralSandboxManager{prepared: resources}
	env.svc.EphemeralSandboxManager = manager
	env.parent.Context = ephemeralRetryMarker("sandbox-old")

	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	require.NotNil(t, child)
	assert.Equal(t, resources.RuntimeID, child.RuntimeID)
	assert.JSONEq(t, string(resources.Context), string(child.Context))
	assert.Equal(t, 0, manager.reclaims)
}

func TestMaybeRetryOfflineEphemeralTaskReclaimsFreshResourcesWhenChildInsertFails(t *testing.T) {
	env := setupRetryTestDB(t, "runtime_offline")
	ctx := context.Background()
	resources := &EphemeralRetryResources{
		RuntimeID: util.MustParseUUID("eeeeeeee-0000-0000-0000-000000000098"),
		Context:   ephemeralRetryMarker("sandbox-fresh-invalid-runtime"),
	}
	manager := &fakeEphemeralSandboxManager{prepared: resources}
	env.svc.EphemeralSandboxManager = manager
	env.parent.Context = ephemeralRetryMarker("sandbox-old")

	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.Error(t, err)
	assert.Nil(t, child)
	assert.Equal(t, 1, manager.reclaims)
}

// TestMaybeRetryFailedTask_ChildSpawnsWithoutAReaLSession verifies the Task 18
// inversion (spec 14.1): an ORDINARY source task never touches AReaL. A
// retryable failure still spawns the child task (attempt bump + parent link +
// daemon notify), but no session is opened for it, no session run is recorded,
// and the parent's terminal routing issues no SetReward — online training
// exists only behind a manifest-backed training execution.
func TestMaybeRetryFailedTask_ChildSpawnsWithoutAReaLSession(t *testing.T) {
	env := setupRetryTestDB(t, "timeout")
	ctx := context.Background()

	env.svc.RouteTerminalTrainingTask(ctx, env.parent)
	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	require.NotNil(t, child, "retryable failure must spawn a child task")

	// Ordinary source tasks never open, close, or reward an AReaL session.
	assert.Equal(t, -1, callOrderIndex(env.rl, "SetReward"),
		"ordinary parent terminal routing must not reward a session")
	assert.Equal(t, -1, callOrderIndex(env.rl, "StartSession"),
		"ordinary child must not open an AReaL session")

	// The daemon announce still fires for the spawned child.
	notifyIdx := callOrderIndex(env.rl, "NotifyTaskEnqueued")
	require.NotEqual(t, -1, notifyIdx, "NotifyTaskEnqueued must fire for the child")

	// No session open means no {session_id -> agent_run_id} record either.
	env.dagStore.mu.Lock()
	_, ok := env.dagStore.sessionRuns["sess-child"]
	env.dagStore.mu.Unlock()
	assert.False(t, ok, "RecordSessionAgentRun must not fire without a session")

	// The child is a fresh attempt (attempt incremented) carrying the parent link.
	assert.Equal(t, env.parent.Attempt+1, child.Attempt)
	assert.True(t, child.ParentTaskID.Valid, "child must point back at parent")
	assert.Equal(t, env.parent.ID, child.ParentTaskID)
}

// TestMaybeRetryFailedTask_NonRetryableIsTerminal verifies that a non-retryable
// failure reason is terminal: no retry child is created, nothing is announced,
// and (per the Task 18 inversion) no AReaL session lifecycle fires at all for
// an ordinary source task.
func TestMaybeRetryFailedTask_NonRetryableIsTerminal(t *testing.T) {
	// "iteration_limit" is resume-unsafe and NOT in retryableReasons.
	env := setupRetryTestDB(t, "iteration_limit")
	ctx := context.Background()

	env.svc.RouteTerminalTrainingTask(ctx, env.parent)
	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	assert.Nil(t, child, "non-retryable failure must not spawn a child")

	assert.Equal(t, -1, callOrderIndex(env.rl, "SetReward"),
		"ordinary parent terminal routing must not reward a session")
	assert.Equal(t, -1, callOrderIndex(env.rl, "StartSession"),
		"no child StartSession for a non-retryable failure")
	assert.Equal(t, -1, callOrderIndex(env.rl, "NotifyTaskEnqueued"),
		"no NotifyTaskEnqueued when no child is created")
}

func TestMaybeRetryFailedTaskLeavesResearchRecoveryToWorkScheduler(t *testing.T) {
	env := setupRetryTestDB(t, "runtime_recovery")
	env.parent.Context = json.RawMessage(`{
		"type":"research_run_work_item",
		"research_dispatch_key":"v6-dispatch:attempt-1"
	}`)

	child, err := env.svc.MaybeRetryFailedTask(context.Background(), env.parent)
	require.NoError(t, err)
	require.Nil(t, child)
}

// TestMaybeRetryFailedTask_OrdinaryChildNeverResolvesEnv verifies that the
// ordinary retry child consumes no env identity at all: with no AReaL session
// (Task 18 inversion), no env_id is resolved and no task id is handed to the
// bridge. Manifest-backed env resolution for training executions is covered
// in training_test.go (PassesEnvID).
func TestMaybeRetryFailedTask_OrdinaryChildNeverResolvesEnv(t *testing.T) {
	env := setupRetryTestDB(t, "timeout")
	ctx := context.Background()

	env.svc.RouteTerminalTrainingTask(ctx, env.parent)
	child, err := env.svc.MaybeRetryFailedTask(ctx, env.parent)
	require.NoError(t, err)
	require.NotNil(t, child)

	require.Equal(t, -1, callOrderIndex(env.rl, "StartSession"),
		"ordinary child must not open a session")
	assert.Equal(t, "", env.rl.lastEnv, "no env_id may be resolved without a session")
	assert.Equal(t, "", env.rl.lastTask, "no task id may reach the bridge without a session")
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

func createEphemeralSandboxTestIssue(
	t *testing.T,
	ctx context.Context,
	tx pgx.Tx,
	workspaceID, agentID, projectID pgtype.UUID,
	title string,
) pgtype.UUID {
	t.Helper()
	suffix := uuid.NewString()
	var creatorID pgtype.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, "Ephemeral sandbox test user "+suffix, "ephemeral-sandbox-"+suffix+"@multica.test").Scan(&creatorID)
	require.NoError(t, err)

	var issueID pgtype.UUID
	err = tx.QueryRow(ctx, `
		WITH bumped AS (
			UPDATE workspace
			SET issue_counter = issue_counter + 1
			WHERE id = $1
			RETURNING issue_counter
		)
		INSERT INTO issue (
			workspace_id, title, status, priority, creator_type, creator_id,
			assignee_type, assignee_id, project_id, number
		)
		SELECT $1, $2, 'in_progress', 'none', 'member', $3,
		       'agent', $4, $5, (SELECT issue_counter FROM bumped)
		RETURNING id
	`, workspaceID, title, creatorID, agentID, projectID).Scan(&issueID)
	require.NoError(t, err)
	return issueID
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
	err = tx.QueryRow(ctx, `INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks
		, model) VALUES ($1, $2, $3, $4, $5, $6, $7, 'composer-1.5') RETURNING id`,
		ws.ID, "eph-agent", "Eph Agent", "local", []byte("{}"), rtID, 1,
	).Scan(&agentID)
	require.NoError(t, err)

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "eph-project",
	).Scan(&projectID)
	require.NoError(t, err)

	issueID := createEphemeralSandboxTestIssue(t, ctx, tx, ws.ID, agentID, projectID, "Eph Issue")

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
	_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now() WHERE id = $1`, task.ID)
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
	err = tx.QueryRow(ctx, `INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks
		, model) VALUES ($1, $2, $3, $4, $5, $6, $7, 'composer-1.5') RETURNING id`,
		ws.ID, "nm-agent", "NM", "local", []byte("{}"), rtID, 1,
	).Scan(&agentID)
	require.NoError(t, err)

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "nm-project",
	).Scan(&projectID)
	require.NoError(t, err)

	issueID := createEphemeralSandboxTestIssue(t, ctx, tx, ws.ID, agentID, projectID, "NM Issue")

	task, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentID, RuntimeID: rtID, IssueID: issueID, Priority: 0,
		Context: []byte(`{"squad_id":"sq"}`),
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now() WHERE id = $1`, task.ID)
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
	err = tx.QueryRow(ctx, `INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks
		, model) VALUES ($1, $2, $3, $4, $5, $6, $7, 'composer-1.5') RETURNING id`,
		ws.ID, "sib-agent", "SA", "local", []byte("{}"), rtID, 1,
	).Scan(&agentID)
	require.NoError(t, err)

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "sib-project",
	).Scan(&projectID)
	require.NoError(t, err)

	issue1ID := createEphemeralSandboxTestIssue(t, ctx, tx, ws.ID, agentID, projectID, "Sib Issue 1")
	issue2ID := createEphemeralSandboxTestIssue(t, ctx, tx, ws.ID, agentID, projectID, "Sib Issue 2")

	marker, _ := json.Marshal(map[string]any{
		"ephemeral_sandbox": map[string]string{"sandbox_instance_id": "inst-1"},
	})
	parent, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentID, RuntimeID: rtID, IssueID: issue1ID, Priority: 0, Context: marker,
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now() WHERE id = $1`, parent.ID)
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

// TestMaybeCleanupEphemeralSandbox_SharedRuntimeLastTerminalWins pins the
// shared_sandbox lifecycle contract (FR-006/007, research D4): when every
// agent of a sample shares one runtime R' and one sandbox instance, the
// terminal hook must NOT reclaim while any task of the sample is still active
// on R', and MUST reclaim exactly once after the sample's last terminal task.
// The existing HasOtherActiveTaskForRuntime guard is keyed per-runtime, so it
// already yields last-terminal-task-wins semantics for shared runtimes — this
// test documents and locks that behavior.
func TestMaybeCleanupEphemeralSandbox_SharedRuntimeLastTerminalWins(t *testing.T) {
	pool := interactionDAGTestPool(t)
	t.Cleanup(pool.Close)
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { _ = tx.Rollback(ctx) })
	q := db.New(tx)

	ws, err := q.CreateWorkspace(ctx, db.CreateWorkspaceParams{
		Name: "eph-shared", Slug: "eph-shared", IssuePrefix: "EH",
	})
	require.NoError(t, err)

	// One shared runtime R' for the whole sample.
	var rtID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
		ws.ID, "daemon-shared", "shared-rt", "local", "pi", "online", "", []byte("{}"), "private",
	).Scan(&rtID)
	require.NoError(t, err)

	// Two sample agents, both bound to the shared R'.
	var agentAID, agentBID pgtype.UUID
	for i, name := range []string{"shared-agent-a", "shared-agent-b"} {
		var id pgtype.UUID
		err = tx.QueryRow(ctx, `INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks
		, model) VALUES ($1, $2, $3, $4, $5, $6, $7, 'composer-1.5') RETURNING id`,
			ws.ID, name, name, "local", []byte("{}"), rtID, 1,
		).Scan(&id)
		require.NoError(t, err)
		if i == 0 {
			agentAID = id
		} else {
			agentBID = id
		}
	}

	var projectID pgtype.UUID
	err = tx.QueryRow(ctx, `INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		ws.ID, "shared-project",
	).Scan(&projectID)
	require.NoError(t, err)

	issueAID := createEphemeralSandboxTestIssue(t, ctx, tx, ws.ID, agentAID, projectID, "Shared Issue A")
	issueBID := createEphemeralSandboxTestIssue(t, ctx, tx, ws.ID, agentBID, projectID, "Shared Issue B")

	// Both tasks carry the SAME shared sandbox marker and run on the SAME R'.
	marker, _ := json.Marshal(map[string]any{
		"ephemeral_sandbox": map[string]string{"sandbox_instance_id": "inst-shared-1"},
	})
	markTerminal := func(taskID pgtype.UUID) {
		t.Helper()
		_, err = tx.Exec(ctx, `UPDATE agent_inbox_event SET status = 'acked', terminal_outcome = 'completed', terminal_at = now(), acked_at = now(), completed_at = now() WHERE id = $1`, taskID)
		require.NoError(t, err)
	}
	taskA, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentAID, RuntimeID: rtID, IssueID: issueAID, Priority: 0, Context: marker,
	})
	require.NoError(t, err)
	taskB, err := q.CreateAgentTask(ctx, db.CreateAgentTaskParams{
		AgentID: agentBID, RuntimeID: rtID, IssueID: issueBID, Priority: 0, Context: marker,
	})
	require.NoError(t, err)

	cleaner := &fakeEphemeralSandboxCleaner{}
	svc := &TaskService{
		Queries: q, Bus: &events.Bus{}, EphemeralSandboxCleaner: cleaner,
	}

	// Agent A finishes while agent B's task is still active on the shared R':
	// the sample's shared sandbox must survive.
	markTerminal(taskA.ID)
	taskA, err = q.GetAgentTask(ctx, taskA.ID)
	require.NoError(t, err)
	svc.maybeCleanupEphemeralSandbox(ctx, taskA)

	cleaner.mu.Lock()
	assert.Len(t, cleaner.calls, 0, "shared sandbox must not be reclaimed while another sample task is active on R'")
	cleaner.mu.Unlock()

	var rtStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM agent_runtime WHERE id = $1`, rtID).Scan(&rtStatus)
	require.NoError(t, err)
	assert.Equal(t, "online", rtStatus, "shared runtime must stay online while the sample has active tasks")

	// Agent B (the sample's last task) finishes: reclaim exactly once, against
	// the shared instance, and set the shared R' offline.
	markTerminal(taskB.ID)
	taskB, err = q.GetAgentTask(ctx, taskB.ID)
	require.NoError(t, err)
	svc.maybeCleanupEphemeralSandbox(ctx, taskB)

	cleaner.mu.Lock()
	require.Len(t, cleaner.calls, 1, "last terminal task of the sample must reclaim the shared sandbox exactly once")
	assert.Equal(t, "inst-shared-1", cleaner.calls[0].SandboxInstanceID)
	assert.Equal(t, util.UUIDToString(ws.ID), cleaner.calls[0].WorkspaceID)
	cleaner.mu.Unlock()

	err = tx.QueryRow(ctx, `SELECT status FROM agent_runtime WHERE id = $1`, rtID).Scan(&rtStatus)
	require.NoError(t, err)
	assert.Equal(t, "offline", rtStatus)
}

func TestShouldCloseBusinessDAGSegmentExcludesDiagnosisTask(t *testing.T) {
	business := db.AgentInboxEvent{Context: []byte(`{"model":"composer"}`)}
	diagnosis := db.AgentInboxEvent{Context: []byte(`{"diagnosis_run_id":"diag-1"}`)}
	if !shouldCloseBusinessDAGSegment(business) {
		t.Fatal("ordinary business task must close a DAG segment")
	}
	if shouldCloseBusinessDAGSegment(diagnosis) {
		t.Fatal("internal diagnosis task must be excluded from business DAG")
	}
}
