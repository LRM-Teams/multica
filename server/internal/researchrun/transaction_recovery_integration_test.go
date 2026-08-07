package researchrun

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
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

type transactionRecoveryRun struct {
	ctx         context.Context
	pool        *pgxpool.Pool
	store       *PostgresStore
	fixture     researchRunFixture
	taskID      string
	goalVersion int
	planVersion int
}

type transactionRecoveryOperation struct {
	invoke           func() error
	assertRolledBack func()
	assertCommitted  func()
	recover          func() error
}

type dispatchAttemptDatabaseState struct {
	outboxStatus     string
	leaseToken       string
	lastError        string
	deliveryAttempts int
	attemptStatus    string
	inboxTaskID      string
	taskStatus       string
	cancelRequested  bool
	cancelCompleted  bool
}

func newTransactionRecoveryRun(t *testing.T, title string) *transactionRecoveryRun {
	t.Helper()
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	fixture := seedResearchRunFixture(t, ctx, pool)
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	})
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID,
		Goal: title, Title: title, DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	return &transactionRecoveryRun{
		ctx: ctx, pool: pool, store: store, fixture: fixture, taskID: tasks[0].ID,
		goalVersion: run.GoalVersion, planVersion: run.PlanVersion,
	}
}

func runTransactionRecoveryMatrix(
	t *testing.T,
	operation researchTxOperation,
	setup func(*testing.T, *transactionRecoveryRun) transactionRecoveryOperation,
) {
	t.Helper()
	for _, point := range []researchTxFaultPoint{txBeforeCommit, txAfterCommit} {
		t.Run(string(point), func(t *testing.T) {
			run := newTransactionRecoveryRun(t, "Recover "+string(operation))
			row := setup(t, run)
			beforeHash, beforeSequence := canonicalHashAndSequence(
				t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID,
			)
			injected := errors.New("injected " + string(operation) + " " + string(point))
			fault := &oneShotResearchTxFault{operation: operation, point: point, err: injected}
			run.store.txFaultHook = fault.hook

			err := row.invoke()
			if !errors.Is(err, injected) {
				t.Fatalf("injected call error=%v", err)
			}
			if !fault.fired {
				t.Fatalf("fault %s did not fire for %s", point, operation)
			}

			switch point {
			case txBeforeCommit:
				if errors.Is(err, ErrCommitOutcomeUnknown) {
					t.Fatalf("before_commit returned unknown outcome: %v", err)
				}
				afterHash, afterSequence := canonicalHashAndSequence(
					t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID,
				)
				if afterHash != beforeHash || afterSequence != beforeSequence {
					t.Fatalf(
						"rollback changed canonical state: hash %s -> %s, sequence %d -> %d",
						beforeHash, afterHash, beforeSequence, afterSequence,
					)
				}
				row.assertRolledBack()
				if err = row.invoke(); err != nil {
					t.Fatalf("identical retry: %v", err)
				}
				row.assertCommitted()

			case txAfterCommit:
				if !errors.Is(err, ErrCommitOutcomeUnknown) {
					t.Fatalf("after_commit error=%v, want unknown outcome", err)
				}
				row.assertCommitted()
				committedHash, committedSequence := canonicalHashAndSequence(
					t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID,
				)
				if err = row.recover(); err != nil {
					t.Fatalf("recover committed %s: %v", operation, err)
				}
				recoveredHash, recoveredSequence := canonicalHashAndSequence(
					t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID,
				)
				if recoveredHash != committedHash || recoveredSequence != committedSequence {
					t.Fatalf(
						"recovery duplicated canonical state: hash %s -> %s, sequence %d -> %d",
						committedHash, recoveredHash, committedSequence, recoveredSequence,
					)
				}
				row.assertCommitted()
			}
		})
	}
}

func mustCreateRecoveryDispatch(t *testing.T, run *transactionRecoveryRun) (Attempt, string) {
	t.Helper()
	attempt, _, err := run.store.CreateDispatchIntent(run.ctx, testDispatchIntentInput(
		t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID, run.taskID, run.fixture.agentID,
	))
	if err != nil {
		t.Fatal(err)
	}
	var intentID string
	if err = run.pool.QueryRow(run.ctx, `
		SELECT id::text FROM research_dispatch_outbox WHERE attempt_id = $1::uuid
	`, attempt.ID).Scan(&intentID); err != nil {
		t.Fatal(err)
	}
	return attempt, intentID
}

func mustClaimRecoveryDispatch(t *testing.T, run *transactionRecoveryRun, token string) (Attempt, string) {
	t.Helper()
	attempt, intentID := mustCreateRecoveryDispatch(t, run)
	claims, err := run.store.ClaimDispatchIntents(run.ctx, run.fixture.sessionID, token, time.Minute, 1)
	if err != nil || len(claims) != 1 || claims[0].ID != intentID {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
	return attempt, intentID
}

func mustCreateRecoveryInbox(t *testing.T, run *transactionRecoveryRun) string {
	t.Helper()
	inboxID := uuid.NewString()
	if _, err := run.pool.Exec(run.ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'pending')
	`, inboxID, run.fixture.workspaceID, run.fixture.agentID); err != nil {
		t.Fatal(err)
	}
	return inboxID
}

func loadDispatchAttemptDatabaseState(t *testing.T, run *transactionRecoveryRun, attemptID string) dispatchAttemptDatabaseState {
	t.Helper()
	var state dispatchAttemptDatabaseState
	if err := run.pool.QueryRow(run.ctx, `
		SELECT outbox.status, COALESCE(outbox.lease_token::text, ''), outbox.last_error,
		       outbox.delivery_attempts, attempt.status, COALESCE(attempt.inbox_task_id::text, ''),
		       task.status, attempt.cancellation_requested_at IS NOT NULL,
		       attempt.cancellation_completed_at IS NOT NULL
		FROM research_dispatch_outbox outbox
		JOIN research_task_attempt attempt ON attempt.id = outbox.attempt_id
		JOIN research_task task ON task.id = attempt.task_id
		WHERE attempt.id = $1::uuid
	`, attemptID).Scan(
		&state.outboxStatus, &state.leaseToken, &state.lastError, &state.deliveryAttempts,
		&state.attemptStatus, &state.inboxTaskID, &state.taskStatus,
		&state.cancelRequested, &state.cancelCompleted,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func countAttemptEvents(t *testing.T, run *transactionRecoveryRun, attemptID, eventType string) int {
	t.Helper()
	var count int
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE session_id = $1::uuid AND event_type = $2 AND payload->>'attempt_id' = $3
	`, run.fixture.sessionID, eventType, attemptID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestActivateReadyTasksTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpTaskActivateReady, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		failedID, blockedID, readyID := uuid.NewString(), uuid.NewString(), uuid.NewString()
		if _, err := run.pool.Exec(run.ctx, `
			INSERT INTO research_task (
			  id, workspace_id, session_id, client_key, kind, objective,
			  required_capability, expected_result, status, terminal_reason,
			  goal_version, plan_version, max_attempts, timeout_seconds, completed_at
			) VALUES
			  ($1::uuid, $4::uuid, $5::uuid, 'failed-parent', 'discover', 'failed parent',
			   'lead', 'research_evidence_v1', 'failed', 'test_failure', $6, $7, 1, 300, now()),
			  ($2::uuid, $4::uuid, $5::uuid, 'blocked-child', 'verify', 'blocked child',
			   'lead', 'research_evidence_v1', 'pending', '', $6, $7, 1, 300, NULL),
			  ($3::uuid, $4::uuid, $5::uuid, 'ready-child', 'discover', 'ready child',
			   'lead', 'research_evidence_v1', 'pending', '', $6, $7, 1, 300, NULL)
		`, failedID, blockedID, readyID, run.fixture.workspaceID, run.fixture.sessionID, run.goalVersion, run.planVersion); err != nil {
			t.Fatal(err)
		}
		if _, err := run.pool.Exec(run.ctx, `
			INSERT INTO research_task_dependency (task_id, depends_on_task_id) VALUES ($1::uuid, $2::uuid)
		`, blockedID, failedID); err != nil {
			t.Fatal(err)
		}
		assertState := func(blockedStatus, blockedReason, readyStatus string, blockedEvents int) {
			t.Helper()
			var gotBlockedStatus, gotBlockedReason, gotReadyStatus string
			if err := run.pool.QueryRow(run.ctx, `
				SELECT blocked.status, blocked.terminal_reason, ready.status
				FROM research_task blocked, research_task ready
				WHERE blocked.id = $1::uuid AND ready.id = $2::uuid
			`, blockedID, readyID).Scan(&gotBlockedStatus, &gotBlockedReason, &gotReadyStatus); err != nil {
				t.Fatal(err)
			}
			var gotEvents int
			if err := run.pool.QueryRow(run.ctx, `
				SELECT count(*)::int FROM research_run_event
				WHERE session_id = $1::uuid AND event_type = 'task_blocked' AND payload->>'task_id' = $2
			`, run.fixture.sessionID, blockedID).Scan(&gotEvents); err != nil {
				t.Fatal(err)
			}
			if gotBlockedStatus != blockedStatus || gotBlockedReason != blockedReason || gotReadyStatus != readyStatus || gotEvents != blockedEvents {
				t.Fatalf("blocked=%q/%q ready=%q events=%d", gotBlockedStatus, gotBlockedReason, gotReadyStatus, gotEvents)
			}
		}
		invoke := func() error {
			_, err := run.store.ActivateReadyTasks(run.ctx, run.fixture.sessionID)
			return err
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertState("pending", "", "pending", 0) },
			assertCommitted:  func() { assertState("blocked", "dependency_terminal", "ready", 1) },
			recover:          invoke,
		}
	})
}

func TestClaimDispatchIntentsTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpDispatchIntentClaim, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, intentID := mustCreateRecoveryDispatch(t, run)
		token := uuid.NewString()
		invoke := func() error {
			claims, err := run.store.ClaimDispatchIntents(run.ctx, run.fixture.sessionID, token, time.Minute, 1)
			if err == nil && (len(claims) != 1 || claims[0].ID != intentID) {
				return fmt.Errorf("claims=%+v, want intent %s", claims, intentID)
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
				if state.outboxStatus != "pending" || state.leaseToken != "" || state.deliveryAttempts != 0 {
					t.Fatalf("rolled-back claim state=%+v", state)
				}
			},
			assertCommitted: func() {
				state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
				if state.outboxStatus != "delivering" || state.leaseToken != token || state.deliveryAttempts != 1 {
					t.Fatalf("committed claim state=%+v", state)
				}
			},
			recover: func() error {
				claims, err := run.store.ClaimDispatchIntents(run.ctx, run.fixture.sessionID, token, time.Minute, 1)
				if err == nil && len(claims) != 0 {
					return fmt.Errorf("committed claim replayed work: %+v", claims)
				}
				return err
			},
		}
	})
}

func TestRescheduleDispatchIntentTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpDispatchIntentReschedule, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		token := uuid.NewString()
		attempt, intentID := mustClaimRecoveryDispatch(t, run, token)
		diagnostics := "temporary dispatcher failure"
		next := time.Now().UTC().Add(10 * time.Minute)
		invoke := func() error {
			changed, err := run.store.RescheduleDispatchIntent(run.ctx, intentID, token, diagnostics, next)
			if err == nil && !changed {
				return errors.New("reschedule did not change the claimed intent")
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
				if state.outboxStatus != "delivering" || state.leaseToken != token || state.lastError != "" {
					t.Fatalf("rolled-back reschedule state=%+v", state)
				}
			},
			assertCommitted: func() {
				state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
				if state.outboxStatus != "pending" || state.leaseToken != "" || state.lastError != diagnostics {
					t.Fatalf("committed reschedule state=%+v", state)
				}
			},
			recover: func() error {
				changed, err := run.store.RescheduleDispatchIntent(run.ctx, intentID, token, diagnostics, next)
				if err == nil && changed {
					return errors.New("reschedule replay changed the committed intent")
				}
				return err
			},
		}
	})
}

func TestFailDispatchIntentTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpDispatchIntentFail, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		token := uuid.NewString()
		attempt, intentID := mustClaimRecoveryDispatch(t, run, token)
		failure := AttemptFailure{AttemptID: attempt.ID, FailureClass: "dispatch_contract", Diagnostics: "invalid dispatch", Retryable: false}
		invoke := func() error {
			changed, event, err := run.store.FailDispatchIntent(run.ctx, intentID, token, failure)
			if err == nil && (!changed || event.Type != "task_attempt_failed") {
				return fmt.Errorf("changed=%v event=%+v", changed, event)
			}
			return err
		}
		assertState := func(outbox, attemptStatus, taskStatus string, events int) {
			t.Helper()
			state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
			if state.outboxStatus != outbox || state.attemptStatus != attemptStatus || state.taskStatus != taskStatus ||
				countAttemptEvents(t, run, attempt.ID, "task_attempt_failed") != events {
				t.Fatalf("fail dispatch state=%+v events=%d", state, countAttemptEvents(t, run, attempt.ID, "task_attempt_failed"))
			}
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertState("delivering", "dispatching", "dispatching", 0) },
			assertCommitted:  func() { assertState("failed", "failed", "failed", 1) },
			recover: func() error {
				changed, event, err := run.store.FailDispatchIntent(run.ctx, intentID, token, failure)
				if err == nil && (changed || event.ID != "") {
					return fmt.Errorf("terminal dispatch failure replay changed=%v event=%+v", changed, event)
				}
				return err
			},
		}
	})
}

func TestAcknowledgeDispatchIntentTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpDispatchIntentAcknowledge, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		token := uuid.NewString()
		attempt, intentID := mustClaimRecoveryDispatch(t, run, token)
		inboxID := mustCreateRecoveryInbox(t, run)
		invoke := func() error {
			accepted, attached, event, err := run.store.AcknowledgeDispatchIntent(run.ctx, intentID, token, inboxID)
			if err == nil && (!accepted || attached.InboxTaskID != inboxID || event.Type != "task_dispatched") {
				return fmt.Errorf("accepted=%v attempt=%+v event=%+v", accepted, attached, event)
			}
			return err
		}
		assertState := func(outbox, lease, inbox string, events int) {
			t.Helper()
			state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
			if state.outboxStatus != outbox || state.leaseToken != lease || state.inboxTaskID != inbox ||
				countAttemptEvents(t, run, attempt.ID, "task_dispatched") != events {
				t.Fatalf("acknowledge state=%+v events=%d", state, countAttemptEvents(t, run, attempt.ID, "task_dispatched"))
			}
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertState("delivering", token, "", 0) },
			assertCommitted:  func() { assertState("delivered", "", inboxID, 1) },
			recover: func() error {
				attached, event, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID)
				if err == nil && (attached.ID != attempt.ID || event.ID != "") {
					return fmt.Errorf("legacy acknowledgement recovery attempt=%+v event=%+v", attached, event)
				}
				return err
			},
		}
	})
}

func TestAttachInboxTaskTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpAttemptAttachInbox, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, _ := mustCreateRecoveryDispatch(t, run)
		inboxID := mustCreateRecoveryInbox(t, run)
		invoke := func() error {
			attached, event, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID)
			if err == nil && (attached.InboxTaskID != inboxID || event.Type != "task_dispatched") {
				return fmt.Errorf("attempt=%+v event=%+v", attached, event)
			}
			return err
		}
		assertState := func(outbox, inbox string, events int) {
			t.Helper()
			state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
			if state.outboxStatus != outbox || state.inboxTaskID != inbox ||
				countAttemptEvents(t, run, attempt.ID, "task_dispatched") != events {
				t.Fatalf("attach state=%+v events=%d", state, countAttemptEvents(t, run, attempt.ID, "task_dispatched"))
			}
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertState("pending", "", 0) },
			assertCommitted:  func() { assertState("delivered", inboxID, 1) },
			recover: func() error {
				attached, event, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID)
				if err == nil && (attached.ID != attempt.ID || event.ID != "") {
					return fmt.Errorf("attach replay attempt=%+v event=%+v", attached, event)
				}
				return err
			},
		}
	})
}

func TestFailAttemptTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpAttemptFail, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, _ := mustCreateRecoveryDispatch(t, run)
		failure := AttemptFailure{AttemptID: attempt.ID, FailureClass: "terminal_test", Diagnostics: "terminal failure", Retryable: false}
		invoke := func() error {
			event, err := run.store.FailAttempt(run.ctx, failure)
			if err == nil && event.Type != "task_attempt_failed" {
				return fmt.Errorf("event=%+v", event)
			}
			return err
		}
		assertState := func(outbox, attemptStatus, taskStatus string, events int) {
			t.Helper()
			state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
			if state.outboxStatus != outbox || state.attemptStatus != attemptStatus || state.taskStatus != taskStatus ||
				countAttemptEvents(t, run, attempt.ID, "task_attempt_failed") != events {
				t.Fatalf("fail attempt state=%+v events=%d", state, countAttemptEvents(t, run, attempt.ID, "task_attempt_failed"))
			}
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertState("pending", "dispatching", "dispatching", 0) },
			assertCommitted:  func() { assertState("failed", "failed", "failed", 1) },
			recover: func() error {
				_, err := run.store.FailAttempt(run.ctx, failure)
				if !errors.Is(err, ErrInvalidTransition) {
					return fmt.Errorf("terminal observation error=%v, want ErrInvalidTransition", err)
				}
				return nil
			},
		}
	})
}

func prepareCancellationRecovery(t *testing.T, run *transactionRecoveryRun) (Attempt, string) {
	t.Helper()
	attempt, _ := mustCreateRecoveryDispatch(t, run)
	inboxID := mustCreateRecoveryInbox(t, run)
	if _, _, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	if _, err := run.pool.Exec(run.ctx, `
		UPDATE research_task_attempt
		SET status = 'cancelling', pending_failure_class = 'timeout',
		    pending_failure_diagnostics = 'runtime timeout', pending_failure_retryable = false
		WHERE id = $1::uuid
	`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	return attempt, inboxID
}

func TestMarkCancellationsRequestedTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpAttemptCancelRequest, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, inboxID := prepareCancellationRecovery(t, run)
		request := CancellationRequest{AttemptID: attempt.ID, InboxTaskID: inboxID}
		invoke := func() error {
			return run.store.MarkCancellationsRequested(run.ctx, run.fixture.sessionID, []CancellationRequest{request})
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
				if state.cancelRequested {
					t.Fatalf("rolled-back cancellation request state=%+v", state)
				}
			},
			assertCommitted: func() {
				state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
				if !state.cancelRequested || state.cancelCompleted || state.inboxTaskID != inboxID {
					t.Fatalf("committed cancellation request state=%+v", state)
				}
			},
			recover: invoke,
		}
	})
}

func TestCompleteCancellationsTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpAttemptCancelComplete, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, inboxID := prepareCancellationRecovery(t, run)
		request := CancellationRequest{AttemptID: attempt.ID, InboxTaskID: inboxID}
		if err := run.store.MarkCancellationsRequested(run.ctx, run.fixture.sessionID, []CancellationRequest{request}); err != nil {
			t.Fatal(err)
		}
		invoke := func() error {
			events, err := run.store.CompleteCancellations(run.ctx, run.fixture.sessionID, []string{attempt.ID})
			if err == nil && (len(events) != 1 || events[0].Type != "task_attempt_failed") {
				return fmt.Errorf("events=%+v", events)
			}
			return err
		}
		assertState := func(attemptStatus, taskStatus string, completed bool, events int) {
			t.Helper()
			state := loadDispatchAttemptDatabaseState(t, run, attempt.ID)
			if state.attemptStatus != attemptStatus || state.taskStatus != taskStatus || state.cancelCompleted != completed ||
				countAttemptEvents(t, run, attempt.ID, "task_attempt_failed") != events {
				t.Fatalf("complete cancellation state=%+v events=%d", state, countAttemptEvents(t, run, attempt.ID, "task_attempt_failed"))
			}
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() { assertState("cancelling", "dispatching", false, 0) },
			assertCommitted:  func() { assertState("failed", "failed", true, 1) },
			recover: func() error {
				events, err := run.store.CompleteCancellations(run.ctx, run.fixture.sessionID, []string{attempt.ID})
				if err == nil && len(events) != 0 {
					return fmt.Errorf("completion replay events=%+v", events)
				}
				return err
			},
		}
	})
}
