package researchrun

import (
	"context"
	"errors"
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

	requestID := uuid.NewString()
	input := NodeCommandInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		NodeID: "task:" + tasks[0].ID, Action: NodeActionRetry,
		ClientRequestID: requestID, ActorType: "user", ActorID: fixture.userID,
		AnchorKind: "task", AnchorTaskID: tasks[0].ID,
	}
	outcome, err := store.NodeCommand(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Task == nil || outcome.Task.Status != TaskStatusReady || outcome.Task.TerminalReason != "" {
		t.Fatalf("retry outcome=%+v", outcome)
	}
	replayed, err := store.NodeCommand(ctx, input)
	if err != nil || !replayed.Replayed || replayed.CommandID != outcome.CommandID {
		t.Fatalf("identical replay=%+v err=%v", replayed, err)
	}
	changed := input
	changed.Objective = "same request ID, different objective"
	_, err = store.NodeCommand(ctx, changed)
	var denied *NodeCommandDenied
	if !errors.As(err, &denied) || denied.MachineCode != NodeCmdCodeIdempotencyConflict {
		t.Fatalf("different payload with same request ID err=%v denied=%+v", err, denied)
	}
	var commandEvents int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE session_id = $1::uuid AND idempotency_key = $2
	`, fixture.sessionID, nodeCommandClientKey(requestID, "event")).Scan(&commandEvents); err != nil {
		t.Fatal(err)
	}
	if commandEvents != 1 {
		t.Fatalf("node command events=%d, want 1", commandEvents)
	}
	var status, terminalReason string
	if err = pool.QueryRow(ctx, `SELECT status, terminal_reason FROM research_task WHERE id = $1::uuid`, tasks[0].ID).Scan(&status, &terminalReason); err != nil {
		t.Fatal(err)
	}
	if status != string(TaskStatusReady) || terminalReason != "" {
		t.Fatalf("task status=%q terminal_reason=%q", status, terminalReason)
	}
}

func TestNodeCommandRejectsCrossWorkspaceMutation(t *testing.T) {
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

	var eventsBefore int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid`, fixture.sessionID).Scan(&eventsBefore); err != nil {
		t.Fatal(err)
	}
	_, err = store.NodeCommand(ctx, NodeCommandInput{
		SessionID: fixture.sessionID, WorkspaceID: uuid.NewString(),
		NodeID: "task:" + tasks[0].ID, Action: NodeActionContinue,
		ClientRequestID: uuid.NewString(), ActorType: "user", ActorID: fixture.userID,
		AnchorKind: "task", AnchorTaskID: tasks[0].ID,
	})
	if !errors.Is(err, ErrRunNotFound) {
		t.Fatalf("cross-workspace node command error=%v, want ErrRunNotFound", err)
	}
	var eventsAfter int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid`, fixture.sessionID).Scan(&eventsAfter); err != nil {
		t.Fatal(err)
	}
	if eventsAfter != eventsBefore {
		t.Fatalf("cross-workspace command changed event count from %d to %d", eventsBefore, eventsAfter)
	}
}
