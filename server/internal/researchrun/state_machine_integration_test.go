package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestResearchExecutionStateTransitionMatrices(t *testing.T) {
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

	testTransitionMatrix(t, ctx, pool, "research_task_status_transition_allowed",
		[]string{"pending", "ready", "dispatching", "running", "succeeded", "failed", "blocked", "obsolete", "cancelled"},
		transitionSet(
			"pending>ready", "pending>blocked", "pending>obsolete", "pending>cancelled",
			"ready>pending", "ready>dispatching", "ready>obsolete", "ready>cancelled",
			"dispatching>running", "dispatching>succeeded", "dispatching>ready", "dispatching>failed", "dispatching>obsolete", "dispatching>cancelled",
			"running>succeeded", "running>ready", "running>failed", "running>obsolete", "running>cancelled",
			"failed>ready", "blocked>ready",
		))
	testTransitionMatrix(t, ctx, pool, "research_attempt_status_transition_allowed",
		[]string{"dispatching", "running", "cancelling", "succeeded", "failed", "cancelled", "lost"},
		transitionSet(
			"dispatching>running", "dispatching>cancelling", "dispatching>succeeded", "dispatching>failed", "dispatching>cancelled", "dispatching>lost",
			"running>cancelling", "running>succeeded", "running>failed", "running>cancelled", "running>lost",
			"cancelling>failed", "cancelling>cancelled", "cancelling>lost",
		))
	testTransitionMatrix(t, ctx, pool, "research_dispatch_status_transition_allowed",
		[]string{"pending", "delivering", "delivered", "failed", "cancelled"},
		transitionSet(
			"pending>delivering", "pending>delivered", "pending>failed", "pending>cancelled",
			"delivering>pending", "delivering>delivered", "delivering>failed", "delivering>cancelled",
		))
}

func testTransitionMatrix(t *testing.T, ctx context.Context, pool *pgxpool.Pool, function string, statuses []string, allowed map[string]bool) {
	t.Helper()
	for _, from := range statuses {
		for _, to := range statuses {
			var got bool
			query := "SELECT " + function + "($1, $2)"
			if err := pool.QueryRow(ctx, query, from, to).Scan(&got); err != nil {
				t.Fatalf("%s %s>%s: %v", function, from, to, err)
			}
			want := from == to || allowed[from+">"+to]
			if got != want {
				t.Errorf("%s %s>%s=%v want=%v", function, from, to, got, want)
			}
		}
	}
}

func transitionSet(items ...string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, item := range items {
		out[item] = true
	}
	return out
}

func TestResearchExecutionStateTransitionTriggersRejectTerminalReopen(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test transition guards",
		Title: "Transition guards", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	assertIllegalTransition(t, ctx, pool,
		`UPDATE research_task SET status = 'succeeded' WHERE id = $1::uuid`, tasks[0].ID,
		"research_task_status_transition_check")
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "test", Retryable: false}); err != nil {
		t.Fatal(err)
	}
	assertIllegalTransition(t, ctx, pool,
		`UPDATE research_task_attempt SET status = 'running' WHERE id = $1::uuid`, attempt.ID,
		"research_task_attempt_status_transition_check")
	assertIllegalTransition(t, ctx, pool,
		`UPDATE research_dispatch_outbox SET status = 'pending' WHERE attempt_id = $1::uuid`, attempt.ID,
		"research_dispatch_outbox_status_transition_check")
}

func assertIllegalTransition(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query, id, constraint string) {
	t.Helper()
	_, err := pool.Exec(ctx, query, id)
	if err == nil {
		t.Fatalf("illegal transition for %s succeeded", constraint)
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != constraint {
		t.Fatalf("illegal transition error=%v code=%q constraint=%q", err, pgErrCode(pgErr), pgErrConstraint(pgErr))
	}
}

func pgErrCode(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.Code
}

func pgErrConstraint(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.ConstraintName
}
