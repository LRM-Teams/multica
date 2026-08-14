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

func TestPostgresStoreReconcileLeaseFencesExpiredWorker(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Fence concurrent reconciliation",
		Title: "Lease fencing", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}

	_, first, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed || first.Generation <= 0 || first.ExpiresAt.IsZero() {
		t.Fatalf("first lease=%+v claimed=%v err=%v", first, claimed, err)
	}
	if _, _, claimed, err = store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute); err != nil || claimed {
		t.Fatalf("concurrent claim claimed=%v err=%v", claimed, err)
	}
	renewed, err := store.RenewRunLease(ctx, first, 2*time.Minute)
	if err != nil || !renewed.ExpiresAt.After(first.ExpiresAt) {
		t.Fatalf("renewed lease=%+v first=%+v err=%v", renewed, first, err)
	}
	if _, _, claimed, err = store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute); err != nil || claimed {
		t.Fatalf("claim during renewed lease claimed=%v err=%v", claimed, err)
	}

	if _, err = pool.Exec(ctx, `
		UPDATE research_session
		SET reconcile_lease_expires_at = now() - interval '1 second'
		WHERE id = $1::uuid
	`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RenewRunLease(ctx, renewed, time.Minute); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("expired owner renewed err=%v", err)
	}
	_, second, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed || second.Generation != first.Generation+1 {
		t.Fatalf("second lease=%+v first=%+v claimed=%v err=%v", second, first, claimed, err)
	}

	staleCtx := withRunLease(ctx, first)
	pendingEvents, err := store.ListUnprojectedEvents(ctx, fixture.sessionID, 1)
	if err != nil || len(pendingEvents) != 1 {
		t.Fatalf("pending events=%v err=%v", pendingEvents, err)
	}
	if err = store.MarkEventProjected(staleCtx, pendingEvents[0].ID); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("stale projection acknowledgment err=%v", err)
	}
	if _, err = store.RecordBudgetExhausted(staleCtx, fixture.sessionID, "stale_worker", "must roll back"); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("stale mutation err=%v", err)
	}
	var staleEvents int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE session_id = $1::uuid AND idempotency_key LIKE 'budget-exhausted:stale_worker:%'
	`, fixture.sessionID).Scan(&staleEvents); err != nil || staleEvents != 0 {
		t.Fatalf("stale events=%d err=%v", staleEvents, err)
	}
	if err = store.ReleaseRun(ctx, first, time.Now().UTC(), "stale worker must not overwrite diagnostics"); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("stale release err=%v", err)
	}

	secondCtx := withRunLease(ctx, second)
	if _, err = store.RecordBudgetExhausted(secondCtx, fixture.sessionID, "current_worker", "must commit"); err != nil {
		t.Fatal(err)
	}
	if err = store.ReleaseRun(ctx, second, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}

	_, third, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed || third.Generation != second.Generation+1 {
		t.Fatalf("third lease=%+v second=%+v claimed=%v err=%v", third, second, claimed, err)
	}
	if _, _, _, err = store.Pause(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.RenewRunLease(ctx, third, time.Minute); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("user mutation did not revoke lease: %v", err)
	}
	if err = store.ReleaseRun(ctx, third, time.Now().UTC(), "revoked worker must not overwrite diagnostics"); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("revoked release err=%v", err)
	}
}

func TestEngineReconcilePersistsFailureAndClearsItAfterRecovery(t *testing.T) {
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
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Persist reconcile diagnostics",
		Title: "Reconcile diagnostics", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}

	injected := errors.New("injected manifest dispatch rollback")
	fault := &oneShotResearchTxFault{
		operation: txOpDispatchIntentCreate,
		point:     txBeforeCommit,
		err:       injected,
	}
	store.txFaultHook = fault.hook
	engine := newEngine(store, &recordingCancellationDispatcher{}, nil)
	if err = engine.ReconcileSession(ctx, fixture.sessionID); !errors.Is(err, injected) {
		t.Fatalf("ReconcileSession error=%v want injected dispatch rollback", err)
	}
	if !fault.fired {
		t.Fatal("dispatch transaction fault did not fire")
	}
	failed, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != RunStatusRunning || failed.LastError != injected.Error() {
		t.Fatalf("failed run status=%q last_error=%q", failed.Status, failed.LastError)
	}
	if attempts, listErr := store.ListAttempts(ctx, fixture.sessionID); listErr != nil || len(attempts) != 0 {
		t.Fatalf("rolled-back attempts=%+v err=%v", attempts, listErr)
	}

	store.txFaultHook = nil
	if err = engine.ReconcileSession(ctx, fixture.sessionID); err != nil {
		t.Fatalf("reconcile recovery: %v", err)
	}
	recovered, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.LastError != "" {
		t.Fatalf("recovered last_error=%q want empty", recovered.LastError)
	}
}

func TestEnginePauseProjectsOnlyAfterClaimingRunLease(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Project a paused run under lease",
		Title: "Pause projection", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	projector := &leaseCheckingProjector{store: store}
	engine := newEngine(store, &recordingCancellationDispatcher{}, projector)
	run, err := engine.Pause(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID)
	if err != nil || run.Status != RunStatusPaused {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	if projector.calls == 0 {
		t.Fatal("pause events were not projected")
	}
	var pending int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid AND projected_at IS NULL`, fixture.sessionID).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending projections=%d err=%v", pending, err)
	}
}

func TestEngineReconcileHeartbeatRenewsDuringSlowDispatch(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Renew during a slow dispatch",
		Title: "Heartbeat renewal", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	dispatcher := &leaseBlockingDispatcher{
		started: make(chan struct{}), release: make(chan struct{}),
		inboxTaskID: uuid.NewString(), pool: pool,
		workspaceID: fixture.workspaceID, agentID: fixture.agentID,
	}
	engine := newEngine(store, dispatcher, nil)
	engine.leaseDuration = 250 * time.Millisecond
	engine.leaseRenewInterval = 50 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- engine.ReconcileSession(ctx, fixture.sessionID) }()
	select {
	case <-dispatcher.started:
	case err = <-done:
		t.Fatalf("reconcile stopped before dispatch: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch did not start")
	}
	var initialExpiry time.Time
	if err = pool.QueryRow(ctx, `SELECT reconcile_lease_expires_at FROM research_session WHERE id = $1::uuid`, fixture.sessionID).Scan(&initialExpiry); err != nil {
		t.Fatal(err)
	}
	time.Sleep(400 * time.Millisecond)
	var renewedExpiry time.Time
	if err = pool.QueryRow(ctx, `SELECT reconcile_lease_expires_at FROM research_session WHERE id = $1::uuid`, fixture.sessionID).Scan(&renewedExpiry); err != nil {
		t.Fatal(err)
	}
	if !renewedExpiry.After(initialExpiry) {
		t.Fatalf("heartbeat did not extend expiry: initial=%s renewed=%s", initialExpiry, renewedExpiry)
	}
	if _, _, claimed, claimErr := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute); claimErr != nil || claimed {
		t.Fatalf("competitor claimed renewed run: claimed=%v err=%v", claimed, claimErr)
	}
	close(dispatcher.release)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reconcile did not finish")
	}
}

func TestEngineReconcileCancelsWorkWhenLeaseIsTakenOver(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Cancel a stale reconciler",
		Title: "Lease takeover", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	dispatcher := &leaseBlockingDispatcher{
		started: make(chan struct{}), release: make(chan struct{}),
		inboxTaskID: uuid.NewString(), pool: pool,
		workspaceID: fixture.workspaceID, agentID: fixture.agentID,
	}
	engine := newEngine(store, dispatcher, nil)
	engine.leaseDuration = 300 * time.Millisecond
	engine.leaseRenewInterval = 40 * time.Millisecond
	done := make(chan error, 1)
	go func() { done <- engine.ReconcileSession(ctx, fixture.sessionID) }()
	select {
	case <-dispatcher.started:
	case <-time.After(5 * time.Second):
		t.Fatal("dispatch did not start")
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET reconcile_lease_expires_at = now() - interval '1 second' WHERE id = $1::uuid`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, successor, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("successor lease=%+v claimed=%v err=%v", successor, claimed, err)
	}
	select {
	case err = <-done:
		if !errors.Is(err, ErrRunLeaseLost) {
			t.Fatalf("stale reconcile error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stale reconcile was not cancelled")
	}
	if err = store.AssertRunLease(withRunLease(ctx, successor), fixture.sessionID); err != nil {
		t.Fatalf("stale release cleared successor: %v", err)
	}
	if err = store.ReleaseRun(ctx, successor, time.Now().UTC(), ""); err != nil {
		t.Fatal(err)
	}
}

type leaseBlockingDispatcher struct {
	started     chan struct{}
	release     chan struct{}
	inboxTaskID string
	pool        *pgxpool.Pool
	workspaceID string
	agentID     string
}

func (dispatcher *leaseBlockingDispatcher) Dispatch(ctx context.Context, _ DispatchRequest) (DispatchResult, error) {
	close(dispatcher.started)
	select {
	case <-dispatcher.release:
		if _, err := dispatcher.pool.Exec(ctx, `
			INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status, priority)
			VALUES ($1::uuid, $2::uuid, $3::uuid, 'quick_create', 'pending', 0)
		`, dispatcher.inboxTaskID, dispatcher.workspaceID, dispatcher.agentID); err != nil {
			return DispatchResult{}, err
		}
		return DispatchResult{InboxTaskID: dispatcher.inboxTaskID}, nil
	case <-ctx.Done():
		return DispatchResult{}, ctx.Err()
	}
}

func (*leaseBlockingDispatcher) Inspect(context.Context, []string) (map[string]InboxTaskState, error) {
	return map[string]InboxTaskState{}, nil
}

func (*leaseBlockingDispatcher) Cancel(context.Context, []string, string) error { return nil }

type leaseCheckingProjector struct {
	store *PostgresStore
	calls int
}

func (projector *leaseCheckingProjector) Project(ctx context.Context, event RunEvent) error {
	if err := projector.store.AssertRunLease(ctx, event.SessionID); err != nil {
		return err
	}
	projector.calls++
	return nil
}
