package main

import (
	"context"
	"testing"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// TestFailTasksForOfflineRuntimesClearsRunningTaskWithinOneSweep is the
// task #106 verification test: once a runtime is marked offline, does its
// 'draining' (frontend-visible "running") task actually get reset within
// the same sweep pass, or does it linger until the much slower
// runningTimeoutSeconds (2.5h) wall-clock backstop in FailStaleTasks?
//
// sweepStaleRuntimes already calls MarkRuntimesOfflineByIDs immediately
// followed by FailTasksForOfflineRuntimes in the same function — this test
// exercises exactly that pair (not the full sweeper, which needs a live
// Redis/liveness store) to confirm the task-level fix is unnecessary: the
// mechanism already exists and already runs on every ~30s sweep tick, not
// on a 2.5-hour clock.
func TestFailTasksForOfflineRuntimesClearsRunningTaskWithinOneSweep(t *testing.T) {
	if testPool == nil {
		t.Skip("no database connection")
	}

	issueID, agentID, taskID := setupSweeperTestFixture(t, "started")
	t.Cleanup(func() { cleanupSweeperFixture(t, issueID, agentID) })
	ctx := context.Background()

	var runtimeID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1`, agentID).Scan(&runtimeID); err != nil {
		t.Fatalf("load fixture runtime id: %v", err)
	}

	// Snapshot the shared fixture runtime's real status/last_seen_at and
	// restore it after the test — other tests reuse this same runtime row.
	var origStatus string
	var origLastSeen any
	if err := testPool.QueryRow(ctx, `SELECT status, last_seen_at FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&origStatus, &origLastSeen); err != nil {
		t.Fatalf("snapshot original runtime state: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `UPDATE agent_runtime SET status = $2, last_seen_at = $3 WHERE id = $1`, runtimeID, origStatus, origLastSeen); err != nil {
			t.Logf("restore runtime state: %v", err)
		}
	})

	// Simulate what sweepStaleRuntimes has already done by the time it
	// calls FailTasksForOfflineRuntimes: the runtime row is offline.
	if _, err := testPool.Exec(ctx, `UPDATE agent_runtime SET status = 'offline' WHERE id = $1`, runtimeID); err != nil {
		t.Fatalf("mark runtime offline: %v", err)
	}

	queries := db.New(testPool)
	failed, err := queries.FailTasksForOfflineRuntimes(ctx)
	if err != nil {
		t.Fatalf("FailTasksForOfflineRuntimes: %v", err)
	}
	var sawOurTask bool
	for _, row := range failed {
		if util.UUIDToString(row.ID) == taskID {
			sawOurTask = true
		}
	}
	if !sawOurTask {
		t.Fatal("FailTasksForOfflineRuntimes did not reset our draining task — it would still display as 'running' indefinitely")
	}

	var status string
	var startedAtValid bool
	if err := testPool.QueryRow(ctx, `SELECT status, started_at IS NOT NULL FROM agent_inbox_event WHERE id = $1`, taskID).Scan(&status, &startedAtValid); err != nil {
		t.Fatalf("read task after sweep: %v", err)
	}
	if status != "pending" {
		t.Fatalf("task status after offline-runtime sweep = %q, want %q", status, "pending")
	}
	// taskToResponse (agent.go) maps status="pending" to the frontend-visible
	// "queued", never "running" — regardless of whether started_at is set.
	// This is the field the task #106 frontend fix's runningCount reads.
	if startedAtValid {
		t.Log("started_at still set on the reset row — harmless: taskToResponse's switch keys off status, not started_at, for the pending case")
	}
}
