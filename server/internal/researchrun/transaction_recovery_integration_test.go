package researchrun

import (
	"context"
	"encoding/json"
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
		tx, err := run.pool.Begin(run.ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(run.ctx, `
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
			_ = tx.Rollback(run.ctx)
			t.Fatal(err)
		}
		for _, taskID := range []string{failedID, blockedID, readyID} {
			gv, pv := run.goalVersion, run.planVersion
			backfillIntegrationArtifactPassport(t, run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID, taskID, string(ArtifactKindTask), &gv, &pv)
		}
		if err = tx.Commit(run.ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := run.pool.Exec(run.ctx, `
			INSERT INTO research_task_dependency (workspace_id, session_id, task_id, depends_on_task_id)
			VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)
		`, run.fixture.workspaceID, run.fixture.sessionID, blockedID, failedID); err != nil {
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
				if err == nil && (attached.ID != attempt.ID || event.Type != "task_dispatched") {
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
				if err == nil && (attached.ID != attempt.ID || event.Type != "task_dispatched") {
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

type resultAcceptanceRecoveryCounts struct {
	sourceSnapshots int
	sources         int
	observations    int
	claims          int
	reports         int
	evaluations     int
	decisions       int
	repairs         int
	questions       int
	tasks           int
	acceptedEvents  int
	attemptStatus   string
	resultHash      string
}

func loadResultAcceptanceRecoveryCounts(t *testing.T, run *transactionRecoveryRun, attemptID string) resultAcceptanceRecoveryCounts {
	t.Helper()
	var counts resultAcceptanceRecoveryCounts
	if err := run.pool.QueryRow(run.ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_source_snapshot WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_source WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_observation WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_claim WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_report WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_decision WHERE session_id = $1::uuid AND decision_kind IN ('quality_gate', 'citation_audit')),
		  (SELECT count(*)::int FROM research_decision WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_target_repair WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_question WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid AND event_type = 'task_result_accepted'),
		  attempt.status, COALESCE(attempt.result_hash, '')
		FROM research_task_attempt attempt WHERE attempt.id = $2::uuid
	`, run.fixture.sessionID, attemptID).Scan(
		&counts.sourceSnapshots, &counts.sources, &counts.observations, &counts.claims,
		&counts.reports, &counts.evaluations, &counts.decisions, &counts.repairs,
		&counts.questions, &counts.tasks, &counts.acceptedEvents,
		&counts.attemptStatus, &counts.resultHash,
	); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestAcceptResultTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpResultAccept, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, _ := mustCreateRecoveryDispatch(t, run)
		inboxID := mustCreateRecoveryInbox(t, run)
		if _, _, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID); err != nil {
			t.Fatal(err)
		}
		current, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := run.store.ListTasks(run.ctx, run.fixture.sessionID)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("tasks=%+v err=%v", tasks, err)
		}
		result := upgradeResultToV5(validV4PlanResult(t))
		result.ClientRequestID = "tx-result-" + uuid.NewString()
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		validated, hash, err := DecodeAndValidateResultForVersion(current.OrchestratorVersion, raw, tasks[0], current.Config)
		if err != nil {
			t.Fatal(err)
		}
		input := AcceptResultInput{
			SessionID: run.fixture.sessionID, AttemptID: attempt.ID, AgentID: run.fixture.agentID,
			InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
		}
		invoke := func() error {
			_, invokeErr := run.store.AcceptResult(run.ctx, input)
			return invokeErr
		}
		assertState := func(committed bool) {
			t.Helper()
			counts := loadResultAcceptanceRecoveryCounts(t, run, attempt.ID)
			if !committed {
				if counts != (resultAcceptanceRecoveryCounts{questions: 1, tasks: 1, attemptStatus: string(AttemptStatusDispatching)}) {
					t.Fatalf("rolled-back result state=%+v", counts)
				}
				return
			}
			want := resultAcceptanceRecoveryCounts{
				decisions: 1, questions: 2, tasks: 6, acceptedEvents: 1,
				attemptStatus: string(AttemptStatusSucceeded), resultHash: hash,
			}
			if counts != want {
				t.Fatalf("committed result state=%+v want=%+v", counts, want)
			}
			conflicting := result
			conflicting.Summary += " conflicting payload"
			conflictingRaw, marshalErr := json.Marshal(conflicting)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			conflictingResult, conflictingHash, decodeErr := DecodeAndValidateResultForVersion(
				current.OrchestratorVersion, conflictingRaw, tasks[0], current.Config,
			)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			_, conflictErr := run.store.AcceptResult(run.ctx, AcceptResultInput{
				SessionID: run.fixture.sessionID, AttemptID: attempt.ID, AgentID: run.fixture.agentID,
				InboxTaskID: inboxID, Raw: conflictingRaw, Result: conflictingResult, Hash: conflictingHash,
			})
			if !errors.Is(conflictErr, ErrResultConflict) {
				t.Fatalf("same result request with different payload error=%v", conflictErr)
			}
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				assertState(false)
			},
			assertCommitted: func() {
				assertState(true)
			},
			recover: func() error {
				replayed, replayErr := run.store.AcceptResult(run.ctx, input)
				if replayErr == nil && !replayed.Replayed {
					return fmt.Errorf("result replay=%+v, want replayed", replayed)
				}
				return replayErr
			},
		}
	})
}

type controlTaskRecoveryState struct {
	taskID    string
	tasks     int
	decisions int
	events    int
}

func loadControlTaskRecoveryState(t *testing.T, run *transactionRecoveryRun, objective string) controlTaskRecoveryState {
	t.Helper()
	var state controlTaskRecoveryState
	if err := run.pool.QueryRow(run.ctx, `
		SELECT COALESCE((SELECT id::text FROM research_task WHERE session_id = $1::uuid AND objective = $2 LIMIT 1), ''),
		       (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid AND objective = $2),
		       (SELECT count(*)::int FROM research_decision WHERE session_id = $1::uuid AND decision_kind = 'remediation_routing'),
		       (SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid AND event_type = 'control_task_created' AND payload->>'objective' = $2)
	`, run.fixture.sessionID, objective).Scan(&state.taskID, &state.tasks, &state.decisions, &state.events); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestCreateControlTaskTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpControlTaskCreate, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		input := ControlTaskInput{
			SessionID: run.fixture.sessionID, Kind: TaskKindDiscover,
			Objective: "transaction recovery control " + uuid.NewString(), Capability: "scout", Priority: 0.9,
			Findings: []GateFinding{{Code: "required_questions_unanswered"}},
		}
		var createdID string
		invoke := func() error {
			task, _, err := run.store.CreateControlTask(run.ctx, input)
			if err == nil {
				createdID = task.ID
			}
			return err
		}
		assertCommitted := func() {
			t.Helper()
			state := loadControlTaskRecoveryState(t, run, input.Objective)
			if state.taskID == "" || state.tasks != 1 || state.decisions != 1 || state.events != 1 {
				t.Fatalf("committed control task state=%+v", state)
			}
			if createdID != "" && createdID != state.taskID {
				t.Fatalf("created control task=%s committed=%s", createdID, state.taskID)
			}
			createdID = state.taskID
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if state := loadControlTaskRecoveryState(t, run, input.Objective); state != (controlTaskRecoveryState{}) {
					t.Fatalf("rolled-back control task state=%+v", state)
				}
			},
			assertCommitted: assertCommitted,
			recover: func() error {
				task, event, err := run.store.CreateControlTask(run.ctx, input)
				if err == nil && (task.ID != createdID || event.ID != "") {
					return fmt.Errorf("control task replay task=%+v event=%+v want task %s and no event", task, event, createdID)
				}
				return err
			},
		}
	})
}

type nodeCommandRecoveryState struct {
	tasks            int
	questions        int
	commandEvents    int
	createdTasks     int
	createdQuestions int
	taskStatus       string
	terminalReason   string
	assignedAgent    string
	eventID          string
}

func loadNodeCommandRecoveryState(t *testing.T, run *transactionRecoveryRun, input NodeCommandInput) nodeCommandRecoveryState {
	t.Helper()
	var state nodeCommandRecoveryState
	if err := run.pool.QueryRow(run.ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_question WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid AND idempotency_key = $2),
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid AND client_key = $3),
		  (SELECT count(*)::int FROM research_question WHERE session_id = $1::uuid AND client_key = $4),
		  task.status, task.terminal_reason, COALESCE(task.assigned_agent_id::text, ''),
		  COALESCE((SELECT id::text FROM research_run_event WHERE session_id = $1::uuid AND idempotency_key = $2), '')
		FROM research_task task WHERE task.id = $5::uuid
	`, run.fixture.sessionID, nodeCommandClientKey(input.ClientRequestID, "event"),
		nodeCommandClientKey(input.ClientRequestID, "task"), nodeCommandClientKey(input.ClientRequestID, "question"),
		input.AnchorTaskID).Scan(
		&state.tasks, &state.questions, &state.commandEvents, &state.createdTasks, &state.createdQuestions,
		&state.taskStatus, &state.terminalReason, &state.assignedAgent, &state.eventID,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestNodeCommandTransactionRecovery(t *testing.T) {
	for _, action := range []string{NodeActionContinue, NodeActionFork, NodeActionRetry, NodeActionReassign} {
		t.Run(action, func(t *testing.T) {
			runTransactionRecoveryMatrix(t, txOpNodeCommand, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
				var rootQuestionID string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT id::text FROM research_question WHERE session_id = $1::uuid AND client_key = 'root'
				`, run.fixture.sessionID).Scan(&rootQuestionID); err != nil {
					t.Fatal(err)
				}
				input := NodeCommandInput{
					SessionID: run.fixture.sessionID, WorkspaceID: run.fixture.workspaceID,
					NodeID: "task:" + run.taskID, Action: action, ClientRequestID: uuid.NewString(),
					ActorType: "user", ActorID: run.fixture.userID,
					AnchorKind: "task", AnchorTaskID: run.taskID, AnchorQuestionID: rootQuestionID,
					Objective: "transaction recovery " + action,
				}
				switch action {
				case NodeActionRetry:
					if _, err := run.pool.Exec(run.ctx, `UPDATE research_task SET status = 'dispatching' WHERE id = $1::uuid`, run.taskID); err != nil {
						t.Fatal(err)
					}
					if _, err := run.pool.Exec(run.ctx, `UPDATE research_task SET status = 'running' WHERE id = $1::uuid`, run.taskID); err != nil {
						t.Fatal(err)
					}
					if _, err := run.pool.Exec(run.ctx, `
						UPDATE research_task SET status = 'failed', terminal_reason = 'test_failure', completed_at = now()
						WHERE id = $1::uuid
					`, run.taskID); err != nil {
						t.Fatal(err)
					}
				case NodeActionReassign:
					input.TargetAgentID = run.fixture.reporterID
				}
				baseline := loadNodeCommandRecoveryState(t, run, input)
				var firstOutcome NodeCommandOutcome
				invoke := func() error {
					outcome, err := run.store.NodeCommand(run.ctx, input)
					if err == nil {
						if outcome.Replayed || outcome.Event.ID == "" {
							return fmt.Errorf("fresh node command outcome=%+v", outcome)
						}
						firstOutcome = outcome
					}
					return err
				}
				assertCommitted := func() {
					t.Helper()
					state := loadNodeCommandRecoveryState(t, run, input)
					wantTasks := baseline.tasks
					wantQuestions := baseline.questions
					wantCreatedTasks := 0
					wantCreatedQuestions := 0
					if action == NodeActionContinue || action == NodeActionFork {
						wantTasks++
						wantCreatedTasks = 1
					}
					if action == NodeActionFork {
						wantQuestions++
						wantCreatedQuestions = 1
					}
					if state.tasks != wantTasks || state.questions != wantQuestions || state.commandEvents != 1 ||
						state.createdTasks != wantCreatedTasks || state.createdQuestions != wantCreatedQuestions || state.eventID == "" {
						t.Fatalf("committed %s state=%+v baseline=%+v", action, state, baseline)
					}
					if action == NodeActionRetry && (state.taskStatus != string(TaskStatusReady) || state.terminalReason != "") {
						t.Fatalf("retry task state=%+v", state)
					}
					if action == NodeActionReassign && (state.taskStatus != string(TaskStatusReady) || state.assignedAgent != run.fixture.reporterID) {
						t.Fatalf("reassign task state=%+v", state)
					}
					if firstOutcome.CommandID != "" && firstOutcome.CommandID != state.eventID {
						t.Fatalf("fresh command id=%s committed event=%s", firstOutcome.CommandID, state.eventID)
					}
				}
				return transactionRecoveryOperation{
					invoke: invoke,
					assertRolledBack: func() {
						if state := loadNodeCommandRecoveryState(t, run, input); state != baseline {
							t.Fatalf("rolled-back %s state=%+v baseline=%+v", action, state, baseline)
						}
					},
					assertCommitted: assertCommitted,
					recover: func() error {
						state := loadNodeCommandRecoveryState(t, run, input)
						replayed, err := run.store.NodeCommand(run.ctx, input)
						if err != nil {
							return err
						}
						if !replayed.Replayed || replayed.CommandID != state.eventID {
							return fmt.Errorf("%s replay=%+v want event %s", action, replayed, state.eventID)
						}
						conflicting := input
						conflicting.Objective += " conflicting payload"
						_, conflictErr := run.store.NodeCommand(run.ctx, conflicting)
						var denied *NodeCommandDenied
						if !errors.As(conflictErr, &denied) || denied.MachineCode != NodeCmdCodeIdempotencyConflict {
							return fmt.Errorf("%s semantic conflict error=%v", action, conflictErr)
						}
						return nil
					},
				}
			})
		})
	}
}

func countSessionEvents(t *testing.T, run *transactionRecoveryRun, eventType string) int {
	t.Helper()
	var count int
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE session_id = $1::uuid AND event_type = $2
	`, run.fixture.sessionID, eventType).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestRecordBudgetExhaustedTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpBudgetExhausted, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		kind, details := "tasks", "task budget exhausted"
		var firstEventID string
		invoke := func() error {
			event, err := run.store.RecordBudgetExhausted(run.ctx, run.fixture.sessionID, kind, details)
			if err == nil {
				if firstEventID == "" {
					firstEventID = event.ID
				} else if event.ID != firstEventID {
					return fmt.Errorf("budget event id changed: %s -> %s", firstEventID, event.ID)
				}
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if count := countSessionEvents(t, run, "budget_exhausted"); count != 0 {
					t.Fatalf("rolled back budget events=%d", count)
				}
			},
			assertCommitted: func() {
				if count := countSessionEvents(t, run, "budget_exhausted"); count != 1 {
					t.Fatalf("committed budget events=%d", count)
				}
			},
			recover: invoke,
		}
	})
}

func TestResumeTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunResume, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		if _, _, _, err := run.store.Pause(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID); err != nil {
			t.Fatal(err)
		}
		invoke := func() error {
			_, _, err := run.store.Resume(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusPaused {
					t.Fatalf("rolled back status=%q", runRow.Status)
				}
			},
			assertCommitted: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusRunning {
					t.Fatalf("committed status=%q", runRow.Status)
				}
				if count := countSessionEvents(t, run, "run_resumed"); count != 1 {
					t.Fatalf("resume events=%d", count)
				}
			},
			recover: invoke,
		}
	})
}

func TestMarkEventProjectedTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpProjectionAcknowledge, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		events, err := run.store.ListRunEvents(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, 0, 10)
		if err != nil || len(events) == 0 {
			t.Fatalf("events=%+v err=%v", events, err)
		}
		eventID := events[0].ID
		invoke := func() error {
			return run.store.MarkEventProjected(run.ctx, eventID)
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var projected bool
				if err := run.pool.QueryRow(run.ctx, `
					SELECT projected_at IS NOT NULL FROM research_run_event WHERE id = $1::uuid
				`, eventID).Scan(&projected); err != nil {
					t.Fatal(err)
				}
				if projected {
					t.Fatal("event projected after rollback")
				}
			},
			assertCommitted: func() {
				var projected bool
				if err := run.pool.QueryRow(run.ctx, `
					SELECT projected_at IS NOT NULL FROM research_run_event WHERE id = $1::uuid
				`, eventID).Scan(&projected); err != nil {
					t.Fatal(err)
				}
				if !projected {
					t.Fatal("event not projected after commit")
				}
			},
			recover: invoke,
		}
	})
}

func TestClaimRunTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpReconcileLeaseClaim, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		token := uuid.NewString()
		invoke := func() error {
			_, _, claimed, err := run.store.ClaimRun(run.ctx, run.fixture.sessionID, token, time.Minute)
			if err == nil && !claimed {
				return fmt.Errorf("expected claim success")
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var leaseToken *string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reconcile_lease_token::text FROM research_session WHERE id = $1::uuid
				`, run.fixture.sessionID).Scan(&leaseToken); err != nil {
					t.Fatal(err)
				}
				if leaseToken != nil {
					t.Fatalf("rolled back lease token=%v", *leaseToken)
				}
			},
			assertCommitted: func() {
				var leaseToken string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reconcile_lease_token::text FROM research_session WHERE id = $1::uuid
				`, run.fixture.sessionID).Scan(&leaseToken); err != nil {
					t.Fatal(err)
				}
				if leaseToken != token {
					t.Fatalf("committed lease token=%q want %q", leaseToken, token)
				}
			},
			recover: func() error {
				_, _, claimed, err := run.store.ClaimRun(run.ctx, run.fixture.sessionID, uuid.NewString(), time.Minute)
				if err == nil && claimed {
					return fmt.Errorf("committed lease allowed second claim")
				}
				return err
			},
		}
	})
}
