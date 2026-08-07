package researchrun

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type oneShotResearchTxFault struct {
	mu        sync.Mutex
	operation researchTxOperation
	point     researchTxFaultPoint
	err       error
	fired     bool
}

func (fault *oneShotResearchTxFault) hook(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
	fault.mu.Lock()
	defer fault.mu.Unlock()
	if fault.fired || operation != fault.operation || point != fault.point {
		return nil
	}
	fault.fired = true
	return fault.err
}

func canonicalHashAndSequence(t *testing.T, ctx context.Context, store *PostgresStore, sessionID, workspaceID string) (string, int64) {
	t.Helper()
	snapshot, err := store.CanonicalState(ctx, sessionID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	hash, err := snapshot.Hash()
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.ListRunEvents(ctx, sessionID, workspaceID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var sequence int64
	if len(events) > 0 {
		sequence = events[len(events)-1].Sequence
	}
	return hash, sequence
}

func assertDispatchIntentCounts(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	sessionID, taskID string,
	wantAttempts, wantOutboxes, wantEvents int,
	wantTaskStatus TaskStatus,
) {
	t.Helper()
	var attempts, outboxes, events int
	var taskStatus string
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM research_task_attempt WHERE task_id = $1::uuid`, taskID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM research_dispatch_outbox WHERE task_id = $1::uuid`, taskID).Scan(&outboxes); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE session_id = $1::uuid AND event_type = 'task_dispatching'
	`, sessionID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT status FROM research_task WHERE id = $1::uuid`, taskID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if attempts != wantAttempts || outboxes != wantOutboxes || events != wantEvents || taskStatus != string(wantTaskStatus) {
		t.Fatalf(
			"attempts=%d outboxes=%d dispatch_events=%d task_status=%q; want %d/%d/%d/%q",
			attempts, outboxes, events, taskStatus,
			wantAttempts, wantOutboxes, wantEvents, wantTaskStatus,
		)
	}
}

func TestCreateDispatchIntentTransactionRecovery(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}

	for _, test := range []struct {
		name  string
		point researchTxFaultPoint
	}{
		{name: "before_commit_rolls_back_and_identical_retry_succeeds", point: txBeforeCommit},
		{name: "after_commit_reports_unknown_and_identical_replay_converges", point: txAfterCommit},
	} {
		t.Run(test.name, func(t *testing.T) {
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
				CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
				Goal: "Prove dispatch transaction recovery", Title: "Dispatch transaction recovery",
				DepthTier: "standard", Language: "English",
			}, DefaultRunConfig("standard")); err != nil {
				t.Fatal(err)
			}
			tasks, err := store.ListTasks(ctx, fixture.sessionID)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
			input := testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
			beforeHash, beforeSequence := canonicalHashAndSequence(t, ctx, store, fixture.sessionID, fixture.workspaceID)
			injected := errors.New("injected " + string(test.point))
			fault := &oneShotResearchTxFault{
				operation: txOpDispatchIntentCreate,
				point:     test.point,
				err:       injected,
			}
			store.txFaultHook = fault.hook

			attempt, event, err := store.CreateDispatchIntent(ctx, input)
			if !errors.Is(err, injected) {
				t.Fatalf("injected call error=%v", err)
			}
			if !fault.fired {
				t.Fatalf("fault %s did not fire", test.point)
			}

			switch test.point {
			case txBeforeCommit:
				if errors.Is(err, ErrCommitOutcomeUnknown) {
					t.Fatalf("before_commit returned unknown outcome: %v", err)
				}
				afterFailureHash, afterFailureSequence := canonicalHashAndSequence(t, ctx, store, fixture.sessionID, fixture.workspaceID)
				if afterFailureHash != beforeHash || afterFailureSequence != beforeSequence {
					t.Fatalf(
						"rollback changed canonical state: hash %s -> %s, sequence %d -> %d",
						beforeHash, afterFailureHash, beforeSequence, afterFailureSequence,
					)
				}
				assertDispatchIntentCounts(t, ctx, pool, fixture.sessionID, tasks[0].ID, 0, 0, 0, TaskStatusReady)

				attempt, event, err = store.CreateDispatchIntent(ctx, input)
				if err != nil {
					t.Fatalf("identical retry: %v", err)
				}
				if attempt.ID != input.AttemptID || event.IdempotencyKey != input.Request.Key {
					t.Fatalf("retry attempt=%+v event=%+v", attempt, event)
				}
				assertDispatchIntentCounts(t, ctx, pool, fixture.sessionID, tasks[0].ID, 1, 1, 1, TaskStatusDispatching)

			case txAfterCommit:
				if !errors.Is(err, ErrCommitOutcomeUnknown) {
					t.Fatalf("after_commit error=%v, want unknown outcome", err)
				}
				if attempt.ID != "" || event.ID != "" {
					t.Fatalf("unknown outcome returned committed objects: attempt=%+v event=%+v", attempt, event)
				}
				committedHash, committedSequence := canonicalHashAndSequence(t, ctx, store, fixture.sessionID, fixture.workspaceID)
				if committedHash == beforeHash || committedSequence != beforeSequence+1 {
					t.Fatalf(
						"commit did not land exactly once: hash %s -> %s, sequence %d -> %d",
						beforeHash, committedHash, beforeSequence, committedSequence,
					)
				}
				assertDispatchIntentCounts(t, ctx, pool, fixture.sessionID, tasks[0].ID, 1, 1, 1, TaskStatusDispatching)

				replayedAttempt, replayedEvent, replayErr := store.CreateDispatchIntent(ctx, input)
				if replayErr != nil {
					t.Fatalf("identical replay: %v", replayErr)
				}
				if replayedAttempt.ID != input.AttemptID || replayedEvent.IdempotencyKey != input.Request.Key {
					t.Fatalf("replay attempt=%+v event=%+v", replayedAttempt, replayedEvent)
				}
				replayedHash, replayedSequence := canonicalHashAndSequence(t, ctx, store, fixture.sessionID, fixture.workspaceID)
				if replayedHash != committedHash || replayedSequence != committedSequence {
					t.Fatalf(
						"replay changed canonical state: hash %s -> %s, sequence %d -> %d",
						committedHash, replayedHash, committedSequence, replayedSequence,
					)
				}
				assertDispatchIntentCounts(t, ctx, pool, fixture.sessionID, tasks[0].ID, 1, 1, 1, TaskStatusDispatching)
			}
		})
	}
}
