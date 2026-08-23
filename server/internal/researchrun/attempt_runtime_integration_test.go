package researchrun

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAttemptRuntimeLeaseSeparatesQueueExecutionAndCancellationSettlement(t *testing.T) {
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
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test runtime lease semantics",
		Title: "Runtime lease semantics", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET timeout_seconds = 300 WHERE id = $1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	input := testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	claimed, err := store.ClaimDispatchIntents(ctx, fixture.sessionID, token, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'pending')
	`, inboxID, fixture.workspaceID, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	accepted, attached, event, err := store.AcknowledgeDispatchIntent(ctx, claimed[0].ID, token, inboxID)
	if err != nil || !accepted || attached.Status != AttemptStatusDispatching || attached.StartedAt != nil || event.Type != "task_dispatched" {
		t.Fatalf("accepted=%v attempt=%+v event=%+v err=%v", accepted, attached, event, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET dispatched_at = now() - interval '10 minutes' WHERE id = $1::uuid`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	queuedAt := time.Now().UTC()
	queuedLeaseUntil := queuedAt.Add(2 * time.Minute)
	if events, reconcileErr := store.ReconcileAttempts(ctx, fixture.sessionID, map[string]InboxTaskState{
		inboxID: {ID: inboxID, Status: "queued", ObservedAt: queuedAt, LeaseExpiresAt: &queuedLeaseUntil, HasActiveLease: true},
	}); reconcileErr != nil || len(events) != 0 {
		t.Fatalf("queued reconcile events=%+v err=%v", events, reconcileErr)
	}
	staleObservation := queuedAt.Add(-time.Minute)
	staleLeaseUntil := queuedAt.Add(30 * time.Second)
	if events, reconcileErr := store.ReconcileAttempts(ctx, fixture.sessionID, map[string]InboxTaskState{
		inboxID: {ID: inboxID, Status: "queued", ObservedAt: staleObservation, LeaseExpiresAt: &staleLeaseUntil, HasActiveLease: true},
	}); reconcileErr != nil || len(events) != 0 {
		t.Fatalf("stale queued reconcile events=%+v err=%v", events, reconcileErr)
	}
	attempts, err := store.ListAttempts(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != AttemptStatusDispatching || attempts[0].RuntimeStartedAt != nil ||
		attempts[0].RuntimeObservedAt == nil || attempts[0].RuntimeObservedAt.Sub(queuedAt).Abs() > time.Millisecond ||
		attempts[0].RuntimeLeaseUntil == nil || attempts[0].RuntimeLeaseUntil.Sub(queuedLeaseUntil).Abs() > time.Millisecond {
		t.Fatalf("queued attempt=%+v err=%v", attempts, err)
	}

	startedAt := time.Now().UTC().Add(-2 * time.Minute)
	leaseUntil := time.Now().UTC().Add(time.Minute)
	events, err := store.ReconcileAttempts(ctx, fixture.sessionID, map[string]InboxTaskState{
		inboxID: {
			ID: inboxID, Status: "running", StartedAt: &startedAt,
			ObservedAt: time.Now().UTC(), LeaseExpiresAt: &leaseUntil, HasActiveLease: true,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Type != "task_started" {
		t.Fatalf("runtime events=%+v", events)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET timeout_seconds = 30 WHERE id = $1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	changedStartedAt := time.Now().UTC()
	events, err = store.ReconcileAttempts(ctx, fixture.sessionID, map[string]InboxTaskState{
		inboxID: {
			ID: inboxID, Status: "running", StartedAt: &changedStartedAt,
			ObservedAt: time.Now().UTC(), LeaseExpiresAt: &leaseUntil, HasActiveLease: true,
		},
	})
	if err != nil || len(events) != 1 || events[0].Type != "task_attempt_cancelling" {
		t.Fatalf("timeout events=%+v err=%v", events, err)
	}
	attempts, err = store.ListAttempts(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != AttemptStatusCancelling || attempts[0].PendingFailure != string(FailureTimeout) || attempts[0].CancelCompletedAt != nil ||
		attempts[0].RuntimeStartedAt == nil || attempts[0].RuntimeStartedAt.Sub(startedAt).Abs() > time.Millisecond {
		t.Fatalf("timed out attempt=%+v err=%v", attempts, err)
	}
	tasks, err = store.ListTasks(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || tasks[0].Status != TaskStatusRunning {
		t.Fatalf("task retried before cancellation acknowledgement: %+v err=%v", tasks, err)
	}

	dispatcher := &recordingCancellationDispatcher{states: map[string]InboxTaskState{
		inboxID: {ID: inboxID, Status: "running", HasActiveLease: true},
	}}
	engine := newEngine(store, dispatcher, nil)
	if pending, cancelErr := engine.cancelPendingAttempts(ctx, run, "task_timeout"); cancelErr != nil || !pending {
		t.Fatalf("pending=%v err=%v", pending, cancelErr)
	}
	if len(dispatcher.cancelled) != 1 || dispatcher.cancelled[0] != inboxID {
		t.Fatalf("cancelled=%v", dispatcher.cancelled)
	}
	dispatcher.states[inboxID] = InboxTaskState{ID: inboxID, Status: "cancelled"}
	if pending, cancelErr := engine.cancelPendingAttempts(ctx, run, "task_timeout"); cancelErr != nil || pending {
		t.Fatalf("pending=%v err=%v", pending, cancelErr)
	}
	attempts, err = store.ListAttempts(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || attempts[0].Status != AttemptStatusFailed || attempts[0].FailureClass != string(FailureTimeout) || attempts[0].CancelCompletedAt == nil {
		t.Fatalf("settled attempt=%+v err=%v", attempts, err)
	}
	tasks, err = store.ListTasks(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || tasks[0].Status != TaskStatusReady {
		t.Fatalf("retry was not released after cancellation acknowledgement: %+v err=%v", tasks, err)
	}
}

func TestCreateDispatchIntentRejectsPendingPriorCancellation(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test cancellation dispatch fence",
		Title: "Cancellation dispatch fence", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		UPDATE research_task_attempt
		SET status = 'cancelled', failure_class = 'reassigned', completed_at = now()
		WHERE id = $1::uuid
	`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_dispatch_outbox
		SET status = 'cancelled'
		WHERE attempt_id = $1::uuid AND status = 'pending'
	`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_task
		SET status = 'ready', ready_at = now()
		WHERE id = $1::uuid
	`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	_, _, err = store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if !errors.Is(err, ErrInvalidTransition) || !strings.Contains(err.Error(), "prior attempt cancellation") {
		t.Fatalf("dispatch error=%v", err)
	}
	if _, err = store.CompleteCancellations(ctx, fixture.sessionID, []string{attempt.ID}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)); err != nil {
		t.Fatalf("dispatch remained fenced after cancellation acknowledgement: %v", err)
	}
}

// Two cancellation actors can observe the same pending Attempt around an
// external Cancel call. Durable settlement is idempotent: one actor performs
// the transition and every loser observes the already-completed fact as
// success, rather than reporting a false control failure.
func TestConcurrentCancellationSettlementIsIdempotent(t *testing.T) {
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
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test concurrent cancellation",
		Title: "Concurrent cancellation", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID,
	))
	if err != nil {
		t.Fatal(err)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'pending')
	`, inboxID, fixture.workspaceID, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_task_attempt
		SET status='cancelling', pending_failure_class=$2,
		    pending_failure_diagnostics='timeout', pending_failure_retryable=true
		WHERE id=$1::uuid
	`, attempt.ID, string(FailureTimeout)); err != nil {
		t.Fatal(err)
	}
	request := CancellationRequest{AttemptID: attempt.ID, InboxTaskID: inboxID}
	if err = store.MarkCancellationsRequested(ctx, fixture.sessionID, fixture.workspaceID, []CancellationRequest{request}); err != nil {
		t.Fatal(err)
	}

	const workers = 4
	start := make(chan struct{})
	errs := make([]error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			_, errs[index] = store.CompleteCancellations(context.Background(), fixture.sessionID, []string{attempt.ID})
		}(index)
	}
	close(start)
	wait.Wait()
	for index, settleErr := range errs {
		if settleErr != nil {
			t.Fatalf("worker %d reported false cancellation failure: %v", index, settleErr)
		}
	}
	// A late marker can occur after another worker observed the terminal Inbox
	// state and completed settlement. It is the same cancellation fact.
	if err = store.MarkCancellationsRequested(ctx, fixture.sessionID, fixture.workspaceID, []CancellationRequest{request}); err != nil {
		t.Fatalf("late cancellation marker was not idempotent: %v", err)
	}
	mismatched := CancellationRequest{AttemptID: attempt.ID, InboxTaskID: uuid.NewString()}
	if err = store.MarkCancellationsRequested(ctx, fixture.sessionID, fixture.workspaceID, []CancellationRequest{mismatched}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("mismatched Inbox identity error=%v, want ErrInvalidTransition", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET inbox_task_id=NULL WHERE id=$1::uuid`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if err = store.MarkCancellationsRequested(ctx, fixture.sessionID, fixture.workspaceID, []CancellationRequest{request}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("missing persisted Inbox identity error=%v, want ErrInvalidTransition", err)
	}
	if _, err = store.CompleteCancellations(ctx, fixture.sessionID, []string{uuid.NewString()}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("missing cancellation attempt error=%v, want ErrInvalidTransition", err)
	}

	attempts, err := store.ListAttempts(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != AttemptStatusFailed || attempts[0].CancelCompletedAt == nil {
		t.Fatalf("settled attempt=%+v err=%v", attempts, err)
	}
	var failedEvents int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE session_id=$1::uuid AND event_type='task_attempt_failed' AND payload->>'attempt_id'=$2
	`, fixture.sessionID, attempt.ID).Scan(&failedEvents); err != nil {
		t.Fatal(err)
	}
	if failedEvents != 1 {
		t.Fatalf("task_attempt_failed events=%d, want 1", failedEvents)
	}
	repairs, err := store.ListTargetRepairs(ctx, fixture.sessionID)
	if err != nil || len(repairs) != 1 || repairs[0].OccurrenceCount != 1 {
		t.Fatalf("repairs=%+v err=%v", repairs, err)
	}

	if _, err = pool.Exec(ctx, `UPDATE research_task SET ready_at=now() WHERE id=$1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	corrupt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID,
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_task_attempt SET cancellation_completed_at=now() WHERE id=$1::uuid
	`, corrupt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.CompleteCancellations(ctx, fixture.sessionID, []string{corrupt.ID}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("completion marker on dispatching attempt error=%v, want ErrInvalidTransition", err)
	}
}
