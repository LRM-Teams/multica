package handler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresUpdateStoreLifecyclePersistsAcrossStoreInstances(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	firstStore := NewPostgresUpdateStore(testPool)
	secondStore := NewPostgresUpdateStore(testPool)

	created, err := firstStore.Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != UpdatePending {
		t.Fatalf("created status = %q, want pending", created.Status)
	}
	if pending, err := secondStore.HasPending(ctx, runtimeID); err != nil || !pending {
		t.Fatalf("HasPending from second store = %v, err=%v", pending, err)
	}
	if _, err := secondStore.Create(ctx, runtimeID, "v0.3.71"); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("concurrent active Create error = %v, want errUpdateInProgress", err)
	}

	running, err := secondStore.PopPending(ctx, runtimeID)
	if err != nil {
		t.Fatalf("PopPending: %v", err)
	}
	if running == nil || running.ID != created.ID || running.Status != UpdateRunning || running.RunStartedAt == nil {
		t.Fatalf("running update = %+v", running)
	}
	if duplicate, err := firstStore.PopPending(ctx, runtimeID); err != nil || duplicate != nil {
		t.Fatalf("duplicate PopPending = %+v, err=%v", duplicate, err)
	}

	if err := firstStore.ReadyToApply(ctx, created.ID, "downloaded and verified"); err != nil {
		t.Fatalf("ReadyToApply: %v", err)
	}
	ready, err := secondStore.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get ready: %v", err)
	}
	if ready == nil || ready.Status != UpdateReady || ready.Output != "downloaded and verified" {
		t.Fatalf("ready update = %+v", ready)
	}
	if _, err := secondStore.Create(ctx, runtimeID, "v0.3.71"); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("Create while ready error = %v, want errUpdateInProgress", err)
	}

	if err := secondStore.Complete(ctx, created.ID, "registered new version"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	completed, err := firstStore.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get completed: %v", err)
	}
	if completed == nil || completed.Status != UpdateCompleted || completed.Output != "registered new version" {
		t.Fatalf("completed update = %+v", completed)
	}

	next, err := firstStore.Create(ctx, runtimeID, "v0.3.71")
	if err != nil {
		t.Fatalf("Create after terminal: %v", err)
	}
	if _, err := firstStore.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending next update: %v", err)
	}
	if err := secondStore.Fail(ctx, next.ID, "checksum mismatch"); err != nil {
		t.Fatalf("Fail: %v", err)
	}
	latest, err := firstStore.LatestForRuntime(ctx, runtimeID)
	if err != nil {
		t.Fatalf("LatestForRuntime: %v", err)
	}
	if latest == nil || latest.ID != next.ID || latest.Status != UpdateFailed || latest.Error != "checksum mismatch" {
		t.Fatalf("latest update = %+v", latest)
	}
}

func TestPostgresUpdateStoreEnforcesLifecycleTransitionsAndIdempotency(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	store := NewPostgresUpdateStore(testPool)

	created, err := store.Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := store.ReadyToApply(ctx, created.ID, "too early"); err == nil {
		t.Fatal("pending -> ready_to_apply unexpectedly succeeded")
	}
	if err := store.Complete(ctx, created.ID, "too early"); err == nil {
		t.Fatal("pending -> completed unexpectedly succeeded")
	}
	if err := store.Fail(ctx, created.ID, "too early"); err == nil {
		t.Fatal("pending -> failed unexpectedly succeeded")
	}

	if _, err := store.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending: %v", err)
	}
	if err := store.ReadyToApply(ctx, created.ID, "verified"); err != nil {
		t.Fatalf("running -> ready_to_apply: %v", err)
	}
	if err := store.ReadyToApply(ctx, created.ID, "late duplicate output"); err != nil {
		t.Fatalf("idempotent ready_to_apply duplicate: %v", err)
	}
	ready, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get ready: %v", err)
	}
	if ready == nil || ready.Status != UpdateReady || ready.Output != "verified" {
		t.Fatalf("ready update after duplicate = %+v, want original output preserved", ready)
	}
	// #815 B-cutover: ready_to_apply → failed is legal (path A drain_timeout).
	// Keep a separate request for the completed path below.
	readyFailID := created.ID
	if err := store.Fail(ctx, readyFailID, DrainTimeoutError); err != nil {
		t.Fatalf("ready_to_apply -> failed: %v", err)
	}
	failedReady, err := store.Get(ctx, readyFailID)
	if err != nil {
		t.Fatalf("Get after ready fail: %v", err)
	}
	if failedReady == nil || failedReady.Status != UpdateFailed || failedReady.Error != DrainTimeoutError {
		t.Fatalf("ready→failed = %+v", failedReady)
	}

	// New request for completed path.
	created, err = store.Create(ctx, runtimeID, "v0.3.79")
	if err != nil {
		t.Fatalf("Create for completed path: %v", err)
	}
	if _, err := store.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending completed path: %v", err)
	}
	if err := store.ReadyToApply(ctx, created.ID, "verified-2"); err != nil {
		t.Fatalf("ready completed path: %v", err)
	}

	if err := store.Complete(ctx, created.ID, "registered"); err != nil {
		t.Fatalf("ready_to_apply -> completed: %v", err)
	}
	if err := store.Complete(ctx, created.ID, "late duplicate completion"); err != nil {
		t.Fatalf("idempotent completed duplicate: %v", err)
	}
	if err := store.Fail(ctx, created.ID, "late terminal overwrite"); err == nil {
		t.Fatal("completed -> failed unexpectedly succeeded")
	}
	completed, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get completed: %v", err)
	}
	if completed == nil ||
		completed.Status != UpdateCompleted ||
		completed.Output != "registered" ||
		completed.Error != "" {
		t.Fatalf("completed update after late reports = %+v", completed)
	}
}

func TestPostgresUpdateStoreConcurrentTerminalReportsCannotOverwriteWinner(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	stores := []*PostgresUpdateStore{
		NewPostgresUpdateStore(testPool),
		NewPostgresUpdateStore(testPool),
	}
	created, err := stores[0].Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := stores[0].PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending: %v", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	go func() {
		<-start
		results <- stores[0].Complete(ctx, created.ID, "registered")
	}()
	go func() {
		<-start
		results <- stores[1].Fail(ctx, created.ID, "late failure")
	}()
	close(start)

	var successes int
	for range 2 {
		if err := <-results; err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful competing terminal transitions = %d, want exactly 1", successes)
	}

	got, err := stores[0].Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get terminal update: %v", err)
	}
	if got == nil {
		t.Fatal("terminal update missing")
	}
	switch got.Status {
	case UpdateCompleted:
		if got.Output != "registered" || got.Error != "" {
			t.Fatalf("completed winner was overwritten: %+v", got)
		}
	case UpdateFailed:
		if got.Error != "late failure" || got.Output != "" {
			t.Fatalf("failed winner was overwritten: %+v", got)
		}
	default:
		t.Fatalf("terminal status = %q, want completed or failed", got.Status)
	}
}

func TestPostgresUpdateStore_ReadyWithinTTLStillBlocks(t *testing.T) {
	// Fresh ready (and ready younger than 20m) still holds the active slot.
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	store := NewPostgresUpdateStore(testPool)

	ready, err := store.Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create ready update: %v", err)
	}
	if _, err := store.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending ready update: %v", err)
	}
	if err := store.ReadyToApply(ctx, ready.ID, "verified"); err != nil {
		t.Fatalf("ReadyToApply: %v", err)
	}
	// Still inside 20m window.
	if _, err := testPool.Exec(ctx, `
		UPDATE daemon_runtime_update
		SET updated_at = now() - ($2 * interval '1 second')
		WHERE id = $1
	`, ready.ID, (updateReadyTimeout - time.Minute).Seconds()); err != nil {
		t.Fatalf("age ready update within TTL: %v", err)
	}
	got, err := store.Get(ctx, ready.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.Status != UpdateReady || got.Output != "verified" {
		t.Fatalf("ready within TTL = %+v", got)
	}
	if _, err := store.Create(ctx, runtimeID, "v0.3.72"); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("Create while ready error = %v, want errUpdateInProgress", err)
	}
}

func TestPostgresUpdateStoreActiveExclusionIsAtomic(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	stores := []*PostgresUpdateStore{
		NewPostgresUpdateStore(testPool),
		NewPostgresUpdateStore(testPool),
	}

	const workers = 16
	var successes atomic.Int32
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			if _, err := stores[index%len(stores)].Create(ctx, runtimeID, "v0.3.70"); err == nil {
				successes.Add(1)
			} else if !errors.Is(err, errUpdateInProgress) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("Create returned unexpected error: %v", err)
	}
	if got := successes.Load(); got != 1 {
		t.Fatalf("successful creates = %d, want exactly 1", got)
	}
}

func TestPostgresUpdateStorePopPendingIsAtomicAcrossInstances(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	stores := []*PostgresUpdateStore{
		NewPostgresUpdateStore(testPool),
		NewPostgresUpdateStore(testPool),
	}
	created, err := stores[0].Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	const workers = 12
	results := make(chan *UpdateRequest, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			result, err := stores[index%len(stores)].PopPending(ctx, runtimeID)
			if err != nil {
				errs <- err
				return
			}
			if result != nil {
				results <- result
			}
		}(i)
	}
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		t.Errorf("PopPending returned unexpected error: %v", err)
	}
	var claimed []*UpdateRequest
	for result := range results {
		claimed = append(claimed, result)
	}
	if len(claimed) != 1 || claimed[0].ID != created.ID || claimed[0].Status != UpdateRunning {
		t.Fatalf("claimed updates = %+v, want one running %s", claimed, created.ID)
	}
}

func TestPostgresUpdateStoreTimeoutReleasesActiveExclusion(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	store := NewPostgresUpdateStore(testPool)

	pending, err := store.Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create pending: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE daemon_runtime_update
		SET created_at = now() - ($2 * interval '1 second')
		WHERE id = $1
	`, pending.ID, (updatePendingTimeout + time.Second).Seconds()); err != nil {
		t.Fatalf("age pending update: %v", err)
	}
	if hasPending, err := store.HasPending(ctx, runtimeID); err != nil || hasPending {
		t.Fatalf("HasPending after timeout = %v, err=%v", hasPending, err)
	}
	timedOut, err := store.Get(ctx, pending.ID)
	if err != nil {
		t.Fatalf("Get pending timeout: %v", err)
	}
	if timedOut == nil || timedOut.Status != UpdateTimeout || !strings.Contains(timedOut.Error, "120 seconds") {
		t.Fatalf("pending timeout = %+v", timedOut)
	}

	running, err := store.Create(ctx, runtimeID, "v0.3.71")
	if err != nil {
		t.Fatalf("Create running candidate: %v", err)
	}
	if _, err := store.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending running candidate: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE daemon_runtime_update
		SET run_started_at = now() - ($2 * interval '1 second')
		WHERE id = $1
	`, running.ID, (updateRunningTimeout + time.Second).Seconds()); err != nil {
		t.Fatalf("age running update: %v", err)
	}
	timedOut, err = store.Get(ctx, running.ID)
	if err != nil {
		t.Fatalf("Get running timeout: %v", err)
	}
	if timedOut == nil || timedOut.Status != UpdateTimeout || !strings.Contains(timedOut.Error, "150 seconds") {
		t.Fatalf("running timeout = %+v", timedOut)
	}
	if _, err := store.Create(ctx, runtimeID, "v0.3.72"); err != nil {
		t.Fatalf("Create after running timeout: %v", err)
	}
}

func TestPostgresUpdateStore_ReadyTimeoutReleasesChannel(t *testing.T) {
	// B0: ready_to_apply past 20m → timeout → Create succeeds (D7).
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	store := NewPostgresUpdateStore(testPool)

	created, err := store.Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := store.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending: %v", err)
	}
	if err := store.ReadyToApply(ctx, created.ID, "verified"); err != nil {
		t.Fatalf("ReadyToApply: %v", err)
	}
	// Fresh ready still blocks.
	if _, err := store.Create(ctx, runtimeID, "v0.3.71"); !errors.Is(err, errUpdateInProgress) {
		t.Fatalf("Create while fresh ready error = %v, want errUpdateInProgress", err)
	}
	// Age only updated_at (clock for ready TTL).
	if _, err := testPool.Exec(ctx, `
		UPDATE daemon_runtime_update
		SET updated_at = now() - ($2 * interval '1 second')
		WHERE id = $1
	`, created.ID, (updateReadyTimeout + time.Second).Seconds()); err != nil {
		t.Fatalf("age ready update: %v", err)
	}
	timedOut, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if timedOut == nil || timedOut.Status != UpdateTimeout || timedOut.Error != updateReadyTimeoutError {
		t.Fatalf("ready timeout = %+v", timedOut)
	}
	next, err := store.Create(ctx, runtimeID, "v0.3.72")
	if err != nil {
		t.Fatalf("Create after ready timeout: %v", err)
	}
	if next.Status != UpdatePending {
		t.Fatalf("next = %+v", next)
	}
	// Terminal history retained.
	got, err := store.Get(ctx, created.ID)
	if err != nil || got == nil || got.Status != UpdateTimeout {
		t.Fatalf("timed-out history Get = %+v err=%v", got, err)
	}
}

func TestPostgresUpdateStoreSurvivesPoolReplacement(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	ctx := context.Background()
	firstStore := NewPostgresUpdateStore(testPool)
	created, err := firstStore.Create(ctx, runtimeID, "v0.3.70")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := firstStore.PopPending(ctx, runtimeID); err != nil {
		t.Fatalf("PopPending: %v", err)
	}
	if err := firstStore.ReadyToApply(ctx, created.ID, "ready across restart"); err != nil {
		t.Fatalf("ReadyToApply: %v", err)
	}

	reopenedPool, err := pgxpool.New(ctx, testPool.Config().ConnString())
	if err != nil {
		t.Fatalf("open replacement pool: %v", err)
	}
	t.Cleanup(reopenedPool.Close)
	if err := reopenedPool.Ping(ctx); err != nil {
		t.Fatalf("ping replacement pool: %v", err)
	}
	restartedStore := NewPostgresUpdateStore(reopenedPool)
	got, err := restartedStore.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("Get through replacement pool: %v", err)
	}
	if got == nil || got.Status != UpdateReady || got.Output != "ready across restart" {
		t.Fatalf("persisted update after pool replacement = %+v", got)
	}
	latest, err := restartedStore.LatestForRuntime(ctx, runtimeID)
	if err != nil {
		t.Fatalf("LatestForRuntime through replacement pool: %v", err)
	}
	if latest == nil || latest.ID != created.ID {
		t.Fatalf("latest persisted update = %+v", latest)
	}
}

func TestProductionUpdateStoreHasOnePostgresSource(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	if _, ok := testHandler.UpdateStore.(*PostgresUpdateStore); !ok {
		t.Fatalf("Handler.New UpdateStore = %T, want *PostgresUpdateStore", testHandler.UpdateStore)
	}

	routerPath := filepath.Join("..", "..", "cmd", "server", "router.go")
	raw, err := os.ReadFile(routerPath)
	if err != nil {
		t.Fatalf("read production router: %v", err)
	}
	source := string(raw)
	if strings.Contains(source, "UpdateStore =") ||
		strings.Contains(source, "NewRedisUpdateStore") ||
		strings.Contains(source, "NewInMemoryUpdateStore") {
		t.Fatalf("production router overrides canonical PostgreSQL UpdateStore")
	}
}

func newPostgresUpdateTestRuntime(t *testing.T) string {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeName := "update-store-" + uuid.NewString()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id,
			name,
			runtime_mode,
			provider,
			status,
			device_info,
			metadata,
			last_seen_at
		)
		VALUES ($1, $2, 'cloud', 'update_store_test', 'online', $3, '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, runtimeName, runtimeName).Scan(&runtimeID); err != nil {
		t.Fatalf("create update test runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}
