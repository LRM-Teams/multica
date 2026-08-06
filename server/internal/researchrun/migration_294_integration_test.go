package researchrun

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigration294DownSettlesCancellingAttemptAndRestoresRetryableTask(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test migration rollback settlement",
		Title: "Migration rollback settlement", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_task_attempt
		SET status = 'cancelling', pending_failure_class = 'task_timeout',
		    pending_failure_diagnostics = 'execution exceeded 30 seconds',
		    pending_failure_retryable = true
		WHERE id = $1::uuid
	`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET status = 'running' WHERE id = $1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	downSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "294_research_attempt_runtime_lease.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	upSQL, err := os.ReadFile(filepath.Join("..", "..", "migrations", "294_research_attempt_runtime_lease.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 294 down: %v", err)
	}
	var attemptStatus, failureClass, taskStatus string
	var cancellationCompleted bool
	if err = tx.QueryRow(ctx, `
		SELECT attempt.status, attempt.failure_class,
		       attempt.cancellation_completed_at IS NOT NULL, task.status
		FROM research_task_attempt attempt
		JOIN research_task task ON task.id = attempt.task_id
		WHERE attempt.id = $1::uuid
	`, attempt.ID).Scan(&attemptStatus, &failureClass, &cancellationCompleted, &taskStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != "failed" || failureClass != "task_timeout" || !cancellationCompleted || taskStatus != "ready" {
		t.Fatalf("rollback settlement attempt=%q failure=%q cancellation_completed=%v task=%q", attemptStatus, failureClass, cancellationCompleted, taskStatus)
	}
	if _, err = tx.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 294 up: %v", err)
	}
}
