package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestGCExpiredAgentCredentialsHonorsRetention(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()
	var agentID, userID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, m.user_id
		FROM agent AS a
		JOIN member AS m ON m.workspace_id = a.workspace_id
		WHERE a.workspace_id = $1
		  AND a.archived_at IS NULL
		LIMIT 1
	`, testWorkspaceID).Scan(&agentID, &userID); err != nil {
		t.Fatalf("load active credential subject: %v", err)
	}

	insertExpired := func(label, age string) string {
		t.Helper()
		var id string
		query := fmt.Sprintf(`
			INSERT INTO agent_credential (
				token_hash,
				token_prefix,
				agent_id,
				workspace_id,
				user_id,
				created_at,
				updated_at,
				expires_at
			)
			VALUES (
				gen_random_uuid()::text,
				$1,
				$2,
				$3,
				$4,
				now() - interval '%s',
				now() - interval '%s',
				now() - interval '%s'
			)
			RETURNING id
		`, age, age, label)
		if err := testPool.QueryRow(ctx, query, "mat_gc_"+label, agentID, testWorkspaceID, userID).Scan(&id); err != nil {
			t.Fatalf("insert %s expired credential: %v", label, err)
		}
		return id
	}

	oldID := insertExpired("8 days", "10 days")
	recentID := insertExpired("1 day", "2 days")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_credential WHERE id = ANY($1::uuid[])`, []string{oldID, recentID})
	})

	gcExpiredAgentCredentials(ctx, db.New(testPool))

	var oldExists, recentExists bool
	if err := testPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_credential WHERE id = $1)`, oldID).Scan(&oldExists); err != nil {
		t.Fatalf("check old credential: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agent_credential WHERE id = $1)`, recentID).Scan(&recentExists); err != nil {
		t.Fatalf("check recent credential: %v", err)
	}
	if oldExists || !recentExists {
		t.Fatalf("gc retention old/recent exists = %v/%v, want false/true", oldExists, recentExists)
	}
}

// setupSweeperTestFixture creates an issue and a task in the given status with
// timestamps old enough to trigger the sweeper. Returns (issueID, agentID, taskID).
func setupSweeperTestFixture(t *testing.T, taskStatus string) (string, string, string) {
	t.Helper()
	ctx := context.Background()

	// Find the integration test agent
	var agentID, runtimeID string
	err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &runtimeID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Create an issue assigned to the agent
	var issueID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id)
		SELECT $1, 'Sweeper test issue', 'todo', 'none', 'member', m.user_id, 'agent', $2
		FROM member m WHERE m.workspace_id = $1 LIMIT 1
		RETURNING id
	`, testWorkspaceID, agentID).Scan(&issueID)
	if err != nil {
		t.Fatalf("failed to create test issue: %v", err)
	}

	// Create a task in the desired status with old timestamps
	var taskID string
	switch taskStatus {
	case "started":
		err = testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, dispatched_at, claimed_at, started_at)
			VALUES ($1, $2, $3, 'draining', 0, now() - interval '3 hours', now() - interval '3 hours', now() - interval '3 hours')
			RETURNING id
		`, agentID, runtimeID, issueID).Scan(&taskID)
	case "leased":
		err = testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, dispatched_at, claimed_at)
			VALUES ($1, $2, $3, 'draining', 0, now() - interval '10 minutes', now() - interval '10 minutes')
			RETURNING id
		`, agentID, runtimeID, issueID).Scan(&taskID)
	}
	if err != nil {
		t.Fatalf("failed to create test task: %v", err)
	}

	// Set agent status to "working"
	_, err = testPool.Exec(ctx, `UPDATE agent SET status = 'working' WHERE id = $1`, agentID)
	if err != nil {
		t.Fatalf("failed to set agent status: %v", err)
	}

	return issueID, agentID, taskID
}

func cleanupSweeperFixture(t *testing.T, issueID, agentID string) {
	t.Helper()
	ctx := context.Background()
	testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
	testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	testPool.Exec(ctx, `UPDATE agent SET status = 'idle' WHERE id = $1`, agentID)
}

func TestRefreshAgentStatusFromTasks(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()
	issueID, agentID, taskID := setupSweeperTestFixture(t, "leased")
	t.Cleanup(func() { cleanupSweeperFixture(t, issueID, agentID) })

	queries := db.New(testPool)

	if _, err := testPool.Exec(ctx, `UPDATE agent SET status = 'idle' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to seed idle agent status: %v", err)
	}

	agent, err := queries.RefreshAgentStatusFromTasks(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("RefreshAgentStatusFromTasks with dispatched task failed: %v", err)
	}
	if agent.Status != "working" {
		t.Fatalf("expected dispatched task to refresh agent status to working, got %q", agent.Status)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'suppressed', completed_at = now()
		WHERE id = $1
	`, taskID); err != nil {
		t.Fatalf("failed to cancel seeded task: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent SET status = 'working' WHERE id = $1`, agentID); err != nil {
		t.Fatalf("failed to reseed working agent status: %v", err)
	}

	agent, err = queries.RefreshAgentStatusFromTasks(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("RefreshAgentStatusFromTasks with no active tasks failed: %v", err)
	}
	if agent.Status != "idle" {
		t.Fatalf("expected cancelled-only task set to refresh agent status to idle, got %q", agent.Status)
	}
}

// TestSweepStaleTasksBroadcastsWithWorkspaceID verifies that when the task sweeper
// fails a stale running task, the task:failed event is broadcast with the correct
// WorkspaceID so it reaches frontend WebSocket clients (events without WorkspaceID
// are silently dropped by the WS listener — that was the original bug).
func TestSweepStaleTasksBroadcastsWithWorkspaceID(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	issueID, agentID, taskID := setupSweeperTestFixture(t, "started")
	t.Cleanup(func() { cleanupSweeperFixture(t, issueID, agentID) })

	queries := db.New(testPool)
	bus := events.New()

	// Capture task:failed events to verify WorkspaceID is set
	var taskEvents []events.Event
	var mu sync.Mutex
	bus.Subscribe("task:failed", func(e events.Event) {
		mu.Lock()
		taskEvents = append(taskEvents, e)
		mu.Unlock()
	})

	// Use very short timeouts to trigger the sweep on our test task
	failedTasks, err := queries.FailStaleTasks(context.Background(), db.FailStaleTasksParams{
		DispatchTimeoutSecs: 300.0,
		RunningTimeoutSecs:  1.0, // 1 second — our task is 3 hours old
	})
	if err != nil {
		t.Fatalf("FailStaleTasks query failed: %v", err)
	}
	if len(failedTasks) == 0 {
		t.Fatal("expected at least 1 stale task to be failed")
	}

	// Verify our task was included
	found := false
	for _, ft := range failedTasks {
		if ft.ID.Bytes == parseUUIDBytes(taskID) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected task %s to be in failed tasks list", taskID)
	}

	// Call broadcastFailedTasks — this is what we're testing
	broadcastFailedTasks(context.Background(), queries, nil, bus, failedTasks)

	// Verify the event was published with WorkspaceID (the core of the bug fix)
	mu.Lock()
	defer mu.Unlock()
	var foundEvent bool
	for _, e := range taskEvents {
		payload, _ := e.Payload.(map[string]any)
		if payload["task_id"] == taskID {
			if e.WorkspaceID == "" {
				t.Fatal("task:failed event is missing WorkspaceID — this was the original bug")
			}
			if e.WorkspaceID != testWorkspaceID {
				t.Fatalf("expected WorkspaceID %s, got %s", testWorkspaceID, e.WorkspaceID)
			}
			foundEvent = true
			break
		}
	}
	if !foundEvent {
		t.Fatalf("expected task:failed event for task %s", taskID)
	}

	// Verify DB: task should be failed
	var status string
	err = testPool.QueryRow(context.Background(), `SELECT status FROM agent_inbox_event WHERE id = $1`, taskID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to query task status: %v", err)
	}
	if status != "acked" {
		t.Fatalf("expected task status 'acked', got '%s'", status)
	}
}

// TestSweepStaleTasksReconcileAgentStatus verifies that after the sweeper fails
// stale tasks, the agent status is reconciled from "working" back to "idle".
func TestSweepStaleTasksReconcileAgentStatus(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	issueID, agentID, _ := setupSweeperTestFixture(t, "started")
	t.Cleanup(func() { cleanupSweeperFixture(t, issueID, agentID) })

	queries := db.New(testPool)
	bus := events.New()

	// Capture agent:status events
	var agentStatusEvents []events.Event
	var mu sync.Mutex
	bus.Subscribe("agent:status", func(e events.Event) {
		mu.Lock()
		agentStatusEvents = append(agentStatusEvents, e)
		mu.Unlock()
	})

	// Fail stale tasks with short timeout
	failedTasks, err := queries.FailStaleTasks(context.Background(), db.FailStaleTasksParams{
		DispatchTimeoutSecs: 300.0,
		RunningTimeoutSecs:  1.0,
	})
	if err != nil {
		t.Fatalf("FailStaleTasks failed: %v", err)
	}
	if len(failedTasks) == 0 {
		t.Fatal("expected at least 1 stale task")
	}

	broadcastFailedTasks(context.Background(), queries, nil, bus, failedTasks)

	// Verify agent status is now "idle" in DB
	var agentStatus string
	err = testPool.QueryRow(context.Background(), `SELECT status FROM agent WHERE id = $1`, agentID).Scan(&agentStatus)
	if err != nil {
		t.Fatalf("failed to query agent status: %v", err)
	}
	if agentStatus != "idle" {
		t.Fatalf("expected agent status 'idle', got '%s'", agentStatus)
	}

	// Verify agent:status event was published with correct WorkspaceID
	mu.Lock()
	defer mu.Unlock()
	if len(agentStatusEvents) == 0 {
		t.Fatal("expected agent:status event to be published")
	}
	lastEvent := agentStatusEvents[len(agentStatusEvents)-1]
	if lastEvent.WorkspaceID == "" {
		t.Fatal("agent:status event should have WorkspaceID set")
	}
	if lastEvent.WorkspaceID != testWorkspaceID {
		t.Fatalf("expected WorkspaceID %s, got %s", testWorkspaceID, lastEvent.WorkspaceID)
	}
}

// TestSweepDispatchedStaleTask verifies the sweeper handles dispatched tasks
// stuck beyond the dispatch timeout.
func TestSweepDispatchedStaleTask(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	issueID, agentID, taskID := setupSweeperTestFixture(t, "leased")
	t.Cleanup(func() { cleanupSweeperFixture(t, issueID, agentID) })

	queries := db.New(testPool)
	bus := events.New()

	// Capture task:failed events
	var taskEvents []events.Event
	var mu sync.Mutex
	bus.Subscribe("task:failed", func(e events.Event) {
		mu.Lock()
		taskEvents = append(taskEvents, e)
		mu.Unlock()
	})

	// Fail stale tasks — dispatch timeout of 1 second (our task is 10 minutes old)
	failedTasks, err := queries.FailStaleTasks(context.Background(), db.FailStaleTasksParams{
		DispatchTimeoutSecs: 1.0,
		RunningTimeoutSecs:  9000.0,
	})
	if err != nil {
		t.Fatalf("FailStaleTasks failed: %v", err)
	}
	if len(failedTasks) == 0 {
		t.Fatal("expected at least 1 stale dispatched task")
	}

	broadcastFailedTasks(context.Background(), queries, nil, bus, failedTasks)

	// Verify DB: task should be failed
	var status string
	err = testPool.QueryRow(context.Background(), `SELECT status FROM agent_inbox_event WHERE id = $1`, taskID).Scan(&status)
	if err != nil {
		t.Fatalf("failed to query task: %v", err)
	}
	if status != "acked" {
		t.Fatalf("expected task status 'acked', got '%s'", status)
	}

	// Verify task:failed event was published WITH WorkspaceID
	mu.Lock()
	defer mu.Unlock()
	found := false
	for _, e := range taskEvents {
		payload, _ := e.Payload.(map[string]any)
		if payload["task_id"] == taskID {
			if e.WorkspaceID == "" {
				t.Fatal("task:failed event is missing WorkspaceID — this was the bug")
			}
			if e.WorkspaceID != testWorkspaceID {
				t.Fatalf("expected WorkspaceID %s, got %s", testWorkspaceID, e.WorkspaceID)
			}
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected task:failed event for task %s", taskID)
	}

	// Verify agent status reconciled to idle
	var agentStatus string
	err = testPool.QueryRow(context.Background(), `SELECT status FROM agent WHERE id = $1`, agentID).Scan(&agentStatus)
	if err != nil {
		t.Fatalf("failed to query agent: %v", err)
	}
	if agentStatus != "idle" {
		t.Fatalf("expected agent status 'idle' after sweep, got '%s'", agentStatus)
	}
}

// TestSweepResetsInProgressIssueToTodo verifies the core fix: when the sweeper
// force-fails a stale task whose issue is still in_progress (because the daemon
// crashed mid-run), the issue is reset back to todo so the daemon can re-queue it.
//
// Without this fix the issue stays in_progress permanently — the agent never runs
// to update the status because it was never dispatched.
func TestSweepResetsInProgressIssueToTodo(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()

	// Use the same agent/runtime as the other sweeper tests.
	var agentID, runtimeID string
	err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &runtimeID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Create an issue already in in_progress (simulates a daemon crash mid-run).
	var issueID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id)
		SELECT $1, 'Stuck in_progress issue', 'in_progress', 'none', 'member', m.user_id, 'agent', $2
		FROM member m WHERE m.workspace_id = $1 LIMIT 1
		RETURNING id
	`, testWorkspaceID, agentID).Scan(&issueID)
	if err != nil {
		t.Fatalf("failed to create test issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// Create a stale running task for the issue (3 hours old — beyond any timeout).
	var taskID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, dispatched_at, started_at)
		VALUES ($1, $2, $3, 'draining', 0, now() - interval '3 hours', now() - interval '3 hours')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID)
	if err != nil {
		t.Fatalf("failed to create stale task: %v", err)
	}

	queries := db.New(testPool)
	bus := events.New()

	// Fail the stale task (running timeout of 1 second — our task is 3 hours old).
	failedTasks, err := queries.FailStaleTasks(ctx, db.FailStaleTasksParams{
		DispatchTimeoutSecs: 300.0,
		RunningTimeoutSecs:  1.0,
	})
	if err != nil {
		t.Fatalf("FailStaleTasks failed: %v", err)
	}

	// Confirm our task was swept.
	found := false
	for _, ft := range failedTasks {
		if ft.ID.Bytes == parseUUIDBytes(taskID) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected task %s to be in failed tasks, got %v", taskID, failedTasks)
	}

	// This is what we're testing: issue must be reset from in_progress → todo.
	broadcastFailedTasks(ctx, queries, nil, bus, failedTasks)

	var issueStatus string
	err = testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&issueStatus)
	if err != nil {
		t.Fatalf("failed to query issue status: %v", err)
	}
	if issueStatus != "todo" {
		t.Fatalf("expected issue status 'todo' after sweep, got '%s' — issue is stuck", issueStatus)
	}
}

// TestSweepDoesNotResetIssueAlreadyInReview verifies that the sweeper only resets
// issues that are truly stuck in in_progress — it must not clobber issues whose
// agents already moved them forward (e.g. to in_review) before the task timed out.
func TestSweepDoesNotResetIssueAlreadyInReview(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &runtimeID)
	if err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Issue already advanced to in_review by the agent before the task timed out.
	var issueID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id)
		SELECT $1, 'Already in_review issue', 'in_review', 'none', 'member', m.user_id, 'agent', $2
		FROM member m WHERE m.workspace_id = $1 LIMIT 1
		RETURNING id
	`, testWorkspaceID, agentID).Scan(&issueID)
	if err != nil {
		t.Fatalf("failed to create test issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	var taskID string
	err = testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, dispatched_at, started_at)
		VALUES ($1, $2, $3, 'draining', 0, now() - interval '3 hours', now() - interval '3 hours')
		RETURNING id
	`, agentID, runtimeID, issueID).Scan(&taskID)
	if err != nil {
		t.Fatalf("failed to create stale task: %v", err)
	}

	queries := db.New(testPool)
	bus := events.New()

	failedTasks, err := queries.FailStaleTasks(ctx, db.FailStaleTasksParams{
		DispatchTimeoutSecs: 300.0,
		RunningTimeoutSecs:  1.0,
	})
	if err != nil {
		t.Fatalf("FailStaleTasks failed: %v", err)
	}

	broadcastFailedTasks(ctx, queries, nil, bus, failedTasks)

	// Issue should remain in_review — the sweeper must not clobber agent progress.
	var issueStatus string
	err = testPool.QueryRow(ctx, `SELECT status FROM issue WHERE id = $1`, issueID).Scan(&issueStatus)
	if err != nil {
		t.Fatalf("failed to query issue status: %v", err)
	}
	if issueStatus != "in_review" {
		t.Fatalf("expected issue status 'in_review' to be preserved, got '%s'", issueStatus)
	}
}

// TestExpireStaleQueuedTasks verifies the MUL-1899 queued-TTL sweeper:
// tasks that have been sitting in 'pending' beyond the TTL are transitioned
// to 'failed' with failure_reason='queued_expired', while fresh queued tasks
// are left alone and the per-tick batch limit is respected.
func TestExpireStaleQueuedTasks(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()

	// Find the integration test agent
	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}
	// Task #50: the TTL backstop only applies to a runtime with a fresh
	// heartbeat (see TestExpireStaleQueuedTasksSkipsOfflineRuntimes) — pin
	// the shared fixture runtime's last_seen_at so this test exercises that
	// "healthy runtime, task genuinely stuck" case regardless of whatever
	// heartbeat state other tests left it in.
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET last_seen_at = now() WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("failed to refresh test runtime heartbeat: %v", err)
	}

	// One ancient queued task (should expire) and one fresh queued task (should not).
	// Constraint: idx_one_pending_task_per_issue_agent → use distinct issues.
	mkIssue := func(label string) string {
		var issueID string
		if err := testPool.QueryRow(ctx, `
			WITH bumped AS (
				UPDATE workspace SET issue_counter = issue_counter + 1
				WHERE id = $1 RETURNING issue_counter
			)
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, number)
			SELECT $1, $3, 'todo', 'none', 'member', m.user_id, 'agent', $2, (SELECT issue_counter FROM bumped)
			FROM member m WHERE m.workspace_id = $1 LIMIT 1
			RETURNING id
		`, testWorkspaceID, agentID, label).Scan(&issueID); err != nil {
			t.Fatalf("failed to create %s issue: %v", label, err)
		}
		return issueID
	}
	oldIssueID := mkIssue("Queued TTL test (old)")
	freshIssueID := mkIssue("Queued TTL test (fresh)")
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE issue_id IN ($1, $2)`, oldIssueID, freshIssueID)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id IN ($1, $2)`, oldIssueID, freshIssueID)
	})

	var oldTaskID, freshTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '5 hours')
		RETURNING id
	`, agentID, runtimeID, oldIssueID).Scan(&oldTaskID); err != nil {
		t.Fatalf("failed to insert old queued task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now())
		RETURNING id
	`, agentID, runtimeID, freshIssueID).Scan(&freshTaskID); err != nil {
		t.Fatalf("failed to insert fresh queued task: %v", err)
	}

	queries := db.New(testPool)
	failed, err := queries.ExpireStaleQueuedTasks(ctx, db.ExpireStaleQueuedTasksParams{
		TtlSecs:            3600.0, // 1h TTL — old task is 5h, fresh task is 0s
		MaxPerTick:         100,
		StaleThresholdSecs: 150.0,
	})
	if err != nil {
		t.Fatalf("ExpireStaleQueuedTasks failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("expected exactly 1 expired task, got %d", len(failed))
	}
	if failed[0].ID.Bytes != parseUUIDBytes(oldTaskID) {
		t.Fatalf("expired the wrong task: got %x", failed[0].ID.Bytes)
	}

	// DB assertions: old → terminal failure/queued_expired, fresh → still pending.
	var oldStatus, oldReason, oldErr string
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(failure_reason, ''), COALESCE(error, '')
		FROM agent_inbox_event WHERE id = $1
	`, oldTaskID).Scan(&oldStatus, &oldReason, &oldErr); err != nil {
		t.Fatalf("failed to read old task: %v", err)
	}
	if oldStatus != "acked" {
		t.Fatalf("old task: expected status=acked, got %q", oldStatus)
	}
	if oldReason != "queued_expired" {
		t.Fatalf("old task: expected failure_reason=queued_expired, got %q", oldReason)
	}
	if !strings.Contains(oldErr, "expired in queue") {
		t.Fatalf("old task: expected error to mention expiry, got %q", oldErr)
	}

	var freshStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM agent_inbox_event WHERE id = $1
	`, freshTaskID).Scan(&freshStatus); err != nil {
		t.Fatalf("failed to read fresh task: %v", err)
	}
	if freshStatus != "pending" {
		t.Fatalf("fresh task: expected status=queued, got %q", freshStatus)
	}
}

// TestExpireStaleQueuedTasksSkipsOfflineRuntimes verifies task #50: a task
// queued behind a runtime with a stale heartbeat must not be reaped by the
// blind-clock TTL — it stays 'pending' and waits for the runtime to come
// back, however long that takes. This is what let ~20 real messages to an
// offline daemon silently fail with "task expired in queue" over a 22h
// window (see #50's incident writeup) instead of just waiting.
//
// The exclusion is checked via last_seen_at directly, NOT via the
// agent_runtime.status column (review catch from Barry/Parker): status is
// only flipped to 'offline' by a separate sweeper (sweepStaleRuntimes)
// earlier in the same tick, an unenforced ordering coincidence this test
// must not rely on. The "stale heartbeat, status still 'online'" case below
// is the regression test for exactly that gap — it fails if the exclusion
// predicate is ever changed back to reading r.status.
//
// An old task on a runtime with a FRESH heartbeat must still expire — this
// sweeper's original backstop for a genuinely-stuck task behind a healthy
// runtime (the MUL-1899 historical-backlog case) is preserved unchanged.
func TestExpireStaleQueuedTasksSkipsOfflineRuntimes(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()
	const staleThresholdSecs = 150.0

	var agentID, ownerID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, m.user_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &ownerID); err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// staleOnlineRT: status still says 'online' (sweepStaleRuntimes hasn't
	// run yet this tick) but the heartbeat is stale — the exact gap the
	// fix must catch independent of status.
	var staleOnlineRT, offlineRT, freshOnlineRT string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, owner_id, last_seen_at)
		VALUES ($1, 'stale-online-daemon-clock-test', 'clock test stale-online', 'local', 'pi', 'online', $2, now() - interval '10 minutes')
		RETURNING id
	`, testWorkspaceID, ownerID).Scan(&staleOnlineRT); err != nil {
		t.Fatalf("failed to insert stale-online runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, owner_id, last_seen_at)
		VALUES ($1, 'offline-daemon-clock-test', 'clock test offline', 'local', 'pi', 'offline', $2, now() - interval '10 minutes')
		RETURNING id
	`, testWorkspaceID, ownerID).Scan(&offlineRT); err != nil {
		t.Fatalf("failed to insert offline runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, owner_id, last_seen_at)
		VALUES ($1, 'online-daemon-clock-test', 'clock test online', 'local', 'pi', 'online', $2, now())
		RETURNING id
	`, testWorkspaceID, ownerID).Scan(&freshOnlineRT); err != nil {
		t.Fatalf("failed to insert fresh-online runtime: %v", err)
	}

	mkIssue := func(label string) string {
		var issueID string
		if err := testPool.QueryRow(ctx, `
			WITH bumped AS (
				UPDATE workspace SET issue_counter = issue_counter + 1
				WHERE id = $1 RETURNING issue_counter
			)
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, number)
			SELECT $1, $3, 'todo', 'none', 'member', $2, 'agent', $4, (SELECT issue_counter FROM bumped)
			FROM member m WHERE m.workspace_id = $1 LIMIT 1
			RETURNING id
		`, testWorkspaceID, ownerID, label, agentID).Scan(&issueID); err != nil {
			t.Fatalf("failed to create %s issue: %v", label, err)
		}
		return issueID
	}
	staleOnlineIssue := mkIssue("Clock-skip test (old, stale heartbeat, status still online)")
	oldOfflineIssue := mkIssue("Clock-skip test (old, offline runtime)")
	oldFreshIssue := mkIssue("Clock-skip test (old, fresh heartbeat)")
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE runtime_id IN ($1, $2, $3)`, staleOnlineRT, offlineRT, freshOnlineRT)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id IN ($1, $2, $3)`, staleOnlineIssue, oldOfflineIssue, oldFreshIssue)
		testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id IN ($1, $2, $3)`, staleOnlineRT, offlineRT, freshOnlineRT)
	})

	var staleOnlineTask, oldOfflineTask, oldFreshTask string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '5 hours')
		RETURNING id
	`, agentID, staleOnlineRT, staleOnlineIssue).Scan(&staleOnlineTask); err != nil {
		t.Fatalf("failed to insert task on stale-online runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '5 hours')
		RETURNING id
	`, agentID, offlineRT, oldOfflineIssue).Scan(&oldOfflineTask); err != nil {
		t.Fatalf("failed to insert old task on offline runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '5 hours')
		RETURNING id
	`, agentID, freshOnlineRT, oldFreshIssue).Scan(&oldFreshTask); err != nil {
		t.Fatalf("failed to insert old task on fresh-online runtime: %v", err)
	}

	queries := db.New(testPool)
	failed, err := queries.ExpireStaleQueuedTasks(ctx, db.ExpireStaleQueuedTasksParams{
		TtlSecs:            3600.0, // 1h TTL — all tasks are 5h old
		MaxPerTick:         100,
		StaleThresholdSecs: staleThresholdSecs,
	})
	if err != nil {
		t.Fatalf("ExpireStaleQueuedTasks failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("expected exactly 1 expired task (the fresh-heartbeat one), got %d", len(failed))
	}
	if failed[0].ID.Bytes != parseUUIDBytes(oldFreshTask) {
		t.Fatalf("expired the wrong task: expected the fresh-heartbeat task, got %x", failed[0].ID.Bytes)
	}

	var staleOnlineTaskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, staleOnlineTask).Scan(&staleOnlineTaskStatus); err != nil {
		t.Fatalf("failed to read stale-online task: %v", err)
	}
	if staleOnlineTaskStatus != "pending" {
		t.Fatalf("task queued behind a stale-heartbeat runtime (status still 'online'): expected status=pending (must key off heartbeat, not the lagging status column), got %q", staleOnlineTaskStatus)
	}

	var offlineTaskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, oldOfflineTask).Scan(&offlineTaskStatus); err != nil {
		t.Fatalf("failed to read old-offline-runtime task: %v", err)
	}
	if offlineTaskStatus != "pending" {
		t.Fatalf("task queued behind an offline runtime: expected status=pending (must keep waiting, however old), got %q", offlineTaskStatus)
	}

	var freshTaskStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, oldFreshTask).Scan(&freshTaskStatus); err != nil {
		t.Fatalf("failed to read old-fresh-heartbeat task: %v", err)
	}
	if freshTaskStatus != "acked" {
		t.Fatalf("task queued behind a fresh-heartbeat runtime: expected status=acked (blind-clock backstop preserved for the healthy-runtime case), got %q", freshTaskStatus)
	}
}

// TestExpireQueuedTasksOnOfflineRuntimes verifies the Phase 2b env-dispatch
// liveness backstop: a 'pending' task whose runtime is still 'offline' past the
// TTL and carrying an ephemeral sandbox marker is failed with
// failure_reason='runtime_offline', while an ordinary old queued task, a fresh
// ephemeral task, and an old task on an ONLINE runtime are left alone.
func TestExpireQueuedTasksOnOfflineRuntimes(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()

	// Find the integration test agent + workspace + owner (for runtime owner_id).
	var agentID, ownerID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, m.user_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &ownerID); err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}

	// Insert two fresh runtimes: one offline (sweep target), one online (control).
	// Distinct daemon_ids satisfy UNIQUE(workspace_id, daemon_id, provider).
	var offlineRT, onlineRT string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, owner_id)
		VALUES ($1, 'offline-daemon-sweep-test', 'sweep test offline', 'local', 'pi', 'offline', $2)
		RETURNING id
	`, testWorkspaceID, ownerID).Scan(&offlineRT); err != nil {
		t.Fatalf("failed to insert offline runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, owner_id)
		VALUES ($1, 'online-daemon-sweep-test', 'sweep test online', 'local', 'pi', 'online', $2)
		RETURNING id
	`, testWorkspaceID, ownerID).Scan(&onlineRT); err != nil {
		t.Fatalf("failed to insert online runtime: %v", err)
	}

	// Three distinct issues (idx_one_pending_task_per_issue_agent).
	mkIssue := func(label string) string {
		var issueID string
		if err := testPool.QueryRow(ctx, `
			WITH bumped AS (
				UPDATE workspace SET issue_counter = issue_counter + 1
				WHERE id = $1 RETURNING issue_counter
			)
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, number)
			SELECT $1, $3, 'todo', 'none', 'member', $2, 'agent', $4, (SELECT issue_counter FROM bumped)
			FROM member m WHERE m.workspace_id = $1 LIMIT 1
			RETURNING id
		`, testWorkspaceID, ownerID, label, agentID).Scan(&issueID); err != nil {
			t.Fatalf("failed to create %s issue: %v", label, err)
		}
		return issueID
	}
	oldOfflineIssue := mkIssue("Offline-runtime sweep test (old)")
	ordinaryOfflineIssue := mkIssue("Offline-runtime sweep test (ordinary)")
	freshOfflineIssue := mkIssue("Offline-runtime sweep test (fresh)")
	oldOnlineIssue := mkIssue("Offline-runtime sweep test (online control)")
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE runtime_id IN ($1, $2)`, offlineRT, onlineRT)
		testPool.Exec(ctx, `DELETE FROM issue WHERE id IN ($1, $2, $3, $4)`, oldOfflineIssue, ordinaryOfflineIssue, freshOfflineIssue, oldOnlineIssue)
		testPool.Exec(ctx, `DELETE FROM agent_runtime WHERE id IN ($1, $2)`, offlineRT, onlineRT)
	})

	// old-offline: 10 min old, offline runtime -> should fail.
	// fresh-offline: 0s old, offline runtime -> should stay (under TTL).
	// old-online: 10 min old, ONLINE runtime -> should stay (runtime not offline).
	var oldOfflineTask, ordinaryOfflineTask, freshOfflineTask, oldOnlineTask string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at, context)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '10 minutes',
		        '{"ephemeral_sandbox":{"sandbox_instance_id":"sandbox-old"}}'::jsonb)
		RETURNING id
	`, agentID, offlineRT, oldOfflineIssue).Scan(&oldOfflineTask); err != nil {
		t.Fatalf("failed to insert old offline queued task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '10 minutes')
		RETURNING id
	`, agentID, offlineRT, ordinaryOfflineIssue).Scan(&ordinaryOfflineTask); err != nil {
		t.Fatalf("failed to insert ordinary old offline queued task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now())
		RETURNING id
	`, agentID, offlineRT, freshOfflineIssue).Scan(&freshOfflineTask); err != nil {
		t.Fatalf("failed to insert fresh offline queued task: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
		VALUES ($1, $2, $3, 'pending', 0, now() - interval '10 minutes')
		RETURNING id
	`, agentID, onlineRT, oldOnlineIssue).Scan(&oldOnlineTask); err != nil {
		t.Fatalf("failed to insert old online queued task: %v", err)
	}

	queries := db.New(testPool)
	failed, err := queries.ExpireQueuedTasksOnOfflineRuntimes(ctx, db.ExpireQueuedTasksOnOfflineRuntimesParams{
		TtlSecs:    300.0, // 5 min TTL
		MaxPerTick: 100,
	})
	if err != nil {
		t.Fatalf("ExpireQueuedTasksOnOfflineRuntimes failed: %v", err)
	}
	if len(failed) != 1 {
		t.Fatalf("expected exactly 1 expired task, got %d", len(failed))
	}
	if failed[0].ID.Bytes != parseUUIDBytes(oldOfflineTask) {
		t.Fatalf("expired the wrong task: got %x", failed[0].ID.Bytes)
	}

	// old-offline -> terminal failure/runtime_offline.
	var status, reason, errMsg string
	if err := testPool.QueryRow(ctx, `
		SELECT status, COALESCE(failure_reason, ''), COALESCE(error, '')
		FROM agent_inbox_event WHERE id = $1
	`, oldOfflineTask).Scan(&status, &reason, &errMsg); err != nil {
		t.Fatalf("failed to read old-offline task: %v", err)
	}
	if status != "acked" {
		t.Fatalf("old-offline task: expected status=acked, got %q", status)
	}
	if reason != "runtime_offline" {
		t.Fatalf("old-offline task: expected failure_reason=runtime_offline, got %q", reason)
	}
	if !strings.Contains(errMsg, "did not register") {
		t.Fatalf("old-offline task: expected error to mention daemon not registering, got %q", errMsg)
	}

	// Ordinary tasks on offline runtimes are outside the sandbox liveness sweep.
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, ordinaryOfflineTask).Scan(&status); err != nil {
		t.Fatalf("failed to read ordinary offline task: %v", err)
	}
	if status != "pending" {
		t.Fatalf("ordinary offline task: expected status=queued, got %q", status)
	}

	// fresh-offline -> still queued (under TTL).
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, freshOfflineTask).Scan(&status); err != nil {
		t.Fatalf("failed to read fresh-offline task: %v", err)
	}
	if status != "pending" {
		t.Fatalf("fresh-offline task: expected status=queued, got %q", status)
	}

	// old-online -> still queued (runtime is online, not a sweep target).
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, oldOnlineTask).Scan(&status); err != nil {
		t.Fatalf("failed to read old-online task: %v", err)
	}
	if status != "pending" {
		t.Fatalf("old-online task: expected status=queued, got %q", status)
	}
}

// TestExpireStaleQueuedTasksRespectsBatchLimit verifies the per-tick cap so
// that a large historical backlog cannot monopolise a single sweep.
func TestExpireStaleQueuedTasksRespectsBatchLimit(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	ctx := context.Background()

	var agentID, runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT a.id, a.runtime_id FROM agent a
		JOIN member m ON m.workspace_id = a.workspace_id
		JOIN "user" u ON u.id = m.user_id
		WHERE u.email = $1
		LIMIT 1
	`, integrationTestEmail).Scan(&agentID, &runtimeID); err != nil {
		t.Fatalf("failed to find test agent: %v", err)
	}
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET last_seen_at = now() WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("failed to refresh test runtime heartbeat: %v", err)
	}

	// Create 5 issues, each with one stale queued task — necessary because of the
	// idx_one_pending_task_per_issue_agent unique constraint.
	var issueIDs []string
	t.Cleanup(func() {
		for _, id := range issueIDs {
			testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE issue_id = $1`, id)
			testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, id)
		}
	})
	for i := 0; i < 5; i++ {
		var issueID string
		if err := testPool.QueryRow(ctx, `
			WITH bumped AS (
				UPDATE workspace SET issue_counter = issue_counter + 1
				WHERE id = $1 RETURNING issue_counter
			)
			INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, assignee_type, assignee_id, number)
			SELECT $1, 'Queued TTL batch test', 'todo', 'none', 'member', m.user_id, 'agent', $2, (SELECT issue_counter FROM bumped)
			FROM member m WHERE m.workspace_id = $1 LIMIT 1
			RETURNING id
		`, testWorkspaceID, agentID).Scan(&issueID); err != nil {
			t.Fatalf("failed to create issue %d: %v", i, err)
		}
		issueIDs = append(issueIDs, issueID)
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_inbox_event (agent_id, runtime_id, issue_id, status, priority, created_at)
			VALUES ($1, $2, $3, 'pending', 0, now() - interval '5 hours')
		`, agentID, runtimeID, issueID); err != nil {
			t.Fatalf("failed to insert backlog task %d: %v", i, err)
		}
	}

	queries := db.New(testPool)
	failed, err := queries.ExpireStaleQueuedTasks(ctx, db.ExpireStaleQueuedTasksParams{
		TtlSecs:            3600.0,
		MaxPerTick:         2, // cap below the backlog
		StaleThresholdSecs: 150.0,
	})
	if err != nil {
		t.Fatalf("ExpireStaleQueuedTasks failed: %v", err)
	}
	if len(failed) != 2 {
		t.Fatalf("expected batch cap of 2, got %d", len(failed))
	}

	var remaining int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM agent_inbox_event
		WHERE issue_id = ANY($1::uuid[]) AND status = 'pending'
	`, issueIDs).Scan(&remaining); err != nil {
		t.Fatalf("failed to count remaining queued: %v", err)
	}
	if remaining != 3 {
		t.Fatalf("expected 3 queued tasks remaining after batched sweep, got %d", remaining)
	}
}

// parseUUIDBytes converts a UUID string to the 16-byte array used by pgtype.UUID.
func parseUUIDBytes(s string) [16]byte {
	s = strings.ReplaceAll(s, "-", "")
	var b [16]byte
	for i := 0; i < 16; i++ {
		hi := unhex(s[i*2])
		lo := unhex(s[i*2+1])
		b[i] = hi<<4 | lo
	}
	return b
}

func unhex(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	return 0
}
