package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestNodeRetryClearsTerminalReasonWithoutViolatingTaskConstraint(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	_, tasks := initializeCircuitFixture(t, ctx, store, fixture, 1)
	if _, err = pool.Exec(ctx, `UPDATE research_task SET status = 'dispatching' WHERE id = $1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET status = 'running' WHERE id = $1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_task
		SET status = 'failed', terminal_reason = 'attempt_budget_exhausted', completed_at = now()
		WHERE id = $1::uuid
	`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}

	outcome, err := store.NodeCommand(ctx, NodeCommandInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		NodeID: "task:" + tasks[0].ID, Action: NodeActionRetry,
		ClientRequestID: uuid.NewString(), ActorType: "user", ActorID: fixture.userID,
		AnchorKind: "task", AnchorTaskID: tasks[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Task == nil || outcome.Task.Status != TaskStatusReady || outcome.Task.TerminalReason != "" {
		t.Fatalf("retry outcome=%+v", outcome)
	}
	var status, terminalReason string
	if err = pool.QueryRow(ctx, `SELECT status, terminal_reason FROM research_task WHERE id = $1::uuid`, tasks[0].ID).Scan(&status, &terminalReason); err != nil {
		t.Fatal(err)
	}
	if status != string(TaskStatusReady) || terminalReason != "" {
		t.Fatalf("task status=%q terminal_reason=%q", status, terminalReason)
	}
}
