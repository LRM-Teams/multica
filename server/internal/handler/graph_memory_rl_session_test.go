// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/arealrl"
	"github.com/multica-ai/multica/server/internal/service"
)

// Spec §6/§7, brief D13, acceptance A12/A25/A29: one durable AReaL session
// per online_rl explore trajectory, opened through a fenced intent row so a
// crash mid-open reconciles without duplicate effective sessions; rewards are
// delivered through a durable outbox (CAS claim, idempotent re-delivery of
// the same value); the proxy key is cleared only after the durable terminal
// ack; a stale-session reaper owns eventual AReaL-side cleanup.

type fakeRLStarter struct {
	mu     sync.Mutex
	calls  int
	fail   bool
	opened []string
}

func (f *fakeRLStarter) StartSession(_ context.Context, sessionRef, _ string) (arealrl.SessionCreds, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.fail {
		return arealrl.SessionCreds{}, fmt.Errorf("fake starter: bridge unavailable")
	}
	f.opened = append(f.opened, sessionRef)
	return arealrl.SessionCreds{
		SessionID: fmt.Sprintf("sess-%d", f.calls),
		ProxyKey:  "sk-test-proxy-key",
	}, nil
}

func (f *fakeRLStarter) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakeRLRemover struct {
	mu      sync.Mutex
	removed []string
}

func (f *fakeRLRemover) RemoveSession(_ context.Context, sessionID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removed = append(f.removed, sessionID)
	return nil
}

func (f *fakeRLRemover) removedIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.removed...)
}

// mustOnlineRLFixture writes an online_rl recall with k running trajectories.
func mustOnlineRLFixture(t *testing.T, traceID string, k int) pgtype.UUID {
	t.Helper()
	_, recallID := mustGraphMemoryDiveFixture(t, traceID, k)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_recall SET training_mode = 'online_rl' WHERE id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	return recallID
}

func rlSessionRow(t *testing.T, trajectoryID string) (status, sessionID, proxyKey string, generation int) {
	t.Helper()
	err := testPool.QueryRow(context.Background(), `
		SELECT status, session_id, proxy_key, generation
		FROM graph_memory_rl_session WHERE trajectory_id = $1
	`, trajectoryID).Scan(&status, &sessionID, &proxyKey, &generation)
	if err != nil {
		t.Fatal(err)
	}
	return status, sessionID, proxyKey, generation
}

func rlSessionRowCount(t *testing.T, trajectoryID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM graph_memory_rl_session WHERE trajectory_id = $1
	`, trajectoryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestGraphMemoryRLSessionOpenAndReuse(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "rl-open-"+uuid.NewString()[:8], 1)
	trajID := trajectoryIDBySeed(t, recallID, 0)
	starter := &fakeRLStarter{}
	svc := service.NewGraphMemoryRLSessionService(testPool, starter, &fakeRLRemover{})
	ctx := context.Background()

	sessionID, err := svc.OpenForTrajectory(ctx, trajID, "")
	if err != nil {
		t.Fatalf("OpenForTrajectory: %v", err)
	}
	if sessionID != "sess-1" {
		t.Fatalf("session id = %q, want sess-1", sessionID)
	}
	status, dbSession, proxyKey, generation := rlSessionRow(t, trajID)
	if status != "open" || dbSession != "sess-1" {
		t.Fatalf("row = (%q, %q), want (open, sess-1)", status, dbSession)
	}
	if proxyKey != "sk-test-proxy-key" {
		t.Fatal("proxy key must be persisted in the durable session mapping")
	}
	if generation != 1 {
		t.Fatalf("generation = %d, want 1", generation)
	}

	// Second open reuses the durable mapping without a new StartSession RPC.
	again, err := svc.OpenForTrajectory(ctx, trajID, "")
	if err != nil {
		t.Fatalf("OpenForTrajectory (reuse): %v", err)
	}
	if again != "sess-1" {
		t.Fatalf("reused session id = %q, want sess-1", again)
	}
	if starter.callCount() != 1 {
		t.Fatalf("StartSession calls = %d, want exactly 1", starter.callCount())
	}
	if rlSessionRowCount(t, trajID) != 1 {
		t.Fatal("exactly one session row per trajectory")
	}
}

func TestGraphMemoryRLSessionReconcileAfterCrash(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "rl-recon-"+uuid.NewString()[:8], 1)
	trajID := trajectoryIDBySeed(t, recallID, 0)
	starter := &fakeRLStarter{fail: true}
	svc := service.NewGraphMemoryRLSessionService(testPool, starter, &fakeRLRemover{})
	ctx := context.Background()

	// Opening intent persisted, RPC fails: row lands in failed, generation 1.
	if _, err := svc.OpenForTrajectory(ctx, trajID, ""); err == nil {
		t.Fatal("expected error when the bridge is unavailable")
	}
	status, _, _, generation := rlSessionRow(t, trajID)
	if status != "failed" || generation != 1 {
		t.Fatalf("row = (%q, gen %d), want (failed, 1)", status, generation)
	}

	// Recovery: the next open fences a new generation and succeeds.
	starter.mu.Lock()
	starter.fail = false
	starter.mu.Unlock()
	sessionID, err := svc.OpenForTrajectory(ctx, trajID, "")
	if err != nil {
		t.Fatalf("OpenForTrajectory (recovery): %v", err)
	}
	if sessionID != "sess-2" {
		t.Fatalf("session id = %q, want sess-2", sessionID)
	}
	status, _, _, generation = rlSessionRow(t, trajID)
	if status != "open" || generation != 2 {
		t.Fatalf("row = (%q, gen %d), want (open, 2)", status, generation)
	}

	// Crash mid-open: row stuck in opening must be reconciled by re-opening
	// under a new generation, not by erroring forever.
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_rl_session SET status = 'opening', session_id = '', proxy_key = ''
		WHERE trajectory_id = $1
	`, trajID); err != nil {
		t.Fatal(err)
	}
	reopened, err := svc.OpenForTrajectory(ctx, trajID, "")
	if err != nil {
		t.Fatalf("OpenForTrajectory (stuck opening): %v", err)
	}
	if reopened != "sess-3" {
		t.Fatalf("session id = %q, want sess-3", reopened)
	}
	status, _, _, generation = rlSessionRow(t, trajID)
	if status != "open" || generation != 3 {
		t.Fatalf("row = (%q, gen %d), want (open, 3)", status, generation)
	}
}

func TestGraphMemoryRLSessionRequiresOnlineRL(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	// Default fixture training_mode is offline_capture.
	_, recallID := mustGraphMemoryDiveFixture(t, "rl-mode-"+uuid.NewString()[:8], 1)
	trajID := trajectoryIDBySeed(t, recallID, 0)
	svc := service.NewGraphMemoryRLSessionService(testPool, &fakeRLStarter{}, &fakeRLRemover{})

	if _, err := svc.OpenForTrajectory(context.Background(), trajID, ""); err == nil {
		t.Fatal("expected error opening an online session for a non-online_rl recall")
	}
	if rlSessionRowCount(t, trajID) != 0 {
		t.Fatal("no session row may be written for a non-online_rl recall")
	}
}

func outboxRow(t *testing.T, trajectoryID string) (status string, reward float64, attempts int, lastErr string) {
	t.Helper()
	err := testPool.QueryRow(context.Background(), `
		SELECT status, reward, attempts, last_error
		FROM graph_memory_reward_outbox WHERE trajectory_id = $1
	`, trajectoryID).Scan(&status, &reward, &attempts, &lastErr)
	if err != nil {
		t.Fatal(err)
	}
	return status, reward, attempts, lastErr
}

func outboxRowCount(t *testing.T, trajectoryID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM graph_memory_reward_outbox WHERE trajectory_id = $1
	`, trajectoryID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func mustOpenRLSession(t *testing.T, svc *service.GraphMemoryRLSessionService, trajID string) {
	t.Helper()
	if _, err := svc.OpenForTrajectory(context.Background(), trajID, ""); err != nil {
		t.Fatalf("OpenForTrajectory: %v", err)
	}
}

func TestGraphMemoryRewardOutboxEnqueueIdempotent(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "rl-enq-"+uuid.NewString()[:8], 2)
	traj0 := trajectoryIDBySeed(t, recallID, 0)
	traj1 := trajectoryIDBySeed(t, recallID, 1)
	svc := service.NewGraphMemoryRLSessionService(testPool, &fakeRLStarter{}, &fakeRLRemover{})
	ctx := context.Background()
	mustOpenRLSession(t, svc, traj0)

	if err := svc.EnqueueReward(ctx, traj0, 0.4); err != nil {
		t.Fatalf("EnqueueReward: %v", err)
	}
	if err := svc.EnqueueReward(ctx, traj0, 0.9); err != nil {
		t.Fatalf("EnqueueReward (duplicate): %v", err)
	}
	if n := outboxRowCount(t, traj0); n != 1 {
		t.Fatalf("outbox rows = %d, want exactly 1", n)
	}
	status, reward, _, _ := outboxRow(t, traj0)
	if status != "pending" || reward != 0.4 {
		t.Fatalf("row = (%q, %v), want (pending, 0.4) — first write wins", status, reward)
	}

	// A trajectory without an online session (never opened) rejects enqueue.
	if err := svc.EnqueueReward(ctx, traj1, 0.1); err == nil {
		t.Fatal("expected error enqueueing a reward without an online session")
	}
}

func TestGraphMemoryRewardOutboxDeliverClearsKey(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "rl-del-"+uuid.NewString()[:8], 1)
	trajID := trajectoryIDBySeed(t, recallID, 0)
	svc := service.NewGraphMemoryRLSessionService(testPool, &fakeRLStarter{}, &fakeRLRemover{})
	ctx := context.Background()
	mustOpenRLSession(t, svc, trajID)
	if err := svc.EnqueueReward(ctx, trajID, -0.3); err != nil {
		t.Fatalf("EnqueueReward: %v", err)
	}

	claims, err := svc.ClaimPending(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(claims) != 1 {
		t.Fatalf("claimed %d rows, want 1", len(claims))
	}
	claim := claims[0]
	if claim.TrajectoryID != trajID || claim.Reward != -0.3 {
		t.Fatalf("claim = (%q, %v), want (%q, -0.3)", claim.TrajectoryID, claim.Reward, trajID)
	}
	if claim.ProxyKey != "sk-test-proxy-key" {
		t.Fatal("claim must carry the durable proxy key for delivery")
	}

	if err := svc.MarkDelivered(ctx, claim.OutboxID); err != nil {
		t.Fatalf("MarkDelivered: %v", err)
	}
	status, _, _, _ := outboxRow(t, trajID)
	if status != "delivered" {
		t.Fatalf("outbox status = %q, want delivered", status)
	}
	// Durable terminal ack reached: key cleared, session advanced to rewarded.
	sessStatus, _, proxyKey, _ := rlSessionRow(t, trajID)
	if proxyKey != "" {
		t.Fatal("proxy key must be cleared after the durable terminal ack")
	}
	if sessStatus != "rewarded" {
		t.Fatalf("session status = %q, want rewarded", sessStatus)
	}
	var keyClearedAt *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT key_cleared_at FROM graph_memory_rl_session WHERE trajectory_id = $1
	`, trajID).Scan(&keyClearedAt); err != nil {
		t.Fatal(err)
	}
	if keyClearedAt == nil {
		t.Fatal("key_cleared_at must be recorded with the durable ack")
	}
}

func TestGraphMemoryRewardOutboxClaimFencing(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "rl-cas-"+uuid.NewString()[:8], 1)
	trajID := trajectoryIDBySeed(t, recallID, 0)
	svc := service.NewGraphMemoryRLSessionService(testPool, &fakeRLStarter{}, &fakeRLRemover{})
	ctx := context.Background()
	mustOpenRLSession(t, svc, trajID)
	if err := svc.EnqueueReward(ctx, trajID, 0.7); err != nil {
		t.Fatalf("EnqueueReward: %v", err)
	}

	first, err := svc.ClaimPending(ctx, 10)
	if err != nil || len(first) != 1 {
		t.Fatalf("first claim = %d rows, err %v; want 1 row", len(first), err)
	}
	// In-flight rows are not reclaimable until their delivery goes stale.
	second, err := svc.ClaimPending(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(second) != 0 {
		t.Fatalf("second claim got %d rows, want 0 (CAS fencing)", len(second))
	}

	// Crash recovery: a stale in-flight row is reclaimable.
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_reward_outbox SET updated_at = now() - interval '10 minutes'
		WHERE trajectory_id = $1
	`, trajID); err != nil {
		t.Fatal(err)
	}
	third, err := svc.ClaimPending(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimPending: %v", err)
	}
	if len(third) != 1 {
		t.Fatalf("stale claim got %d rows, want 1 (crash reclaim)", len(third))
	}

	// Retry bookkeeping: transient failure requeues with the error recorded.
	if err := svc.MarkRetry(ctx, third[0].OutboxID, 1, time.Now().Add(time.Minute), fmt.Errorf("bridge 500")); err != nil {
		t.Fatalf("MarkRetry: %v", err)
	}
	status, _, attempts, lastErr := outboxRow(t, trajID)
	if status != "pending" || attempts != 1 || lastErr == "" {
		t.Fatalf("row = (%q, attempts %d, err %q), want (pending, 1, non-empty)", status, attempts, lastErr)
	}
}

func TestGraphMemoryRLSessionReapStale(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	recallID := mustOnlineRLFixture(t, "rl-reap-"+uuid.NewString()[:8], 3)
	traj0 := trajectoryIDBySeed(t, recallID, 0) // stale open -> reaped
	traj1 := trajectoryIDBySeed(t, recallID, 1) // rewarded -> reaped
	traj2 := trajectoryIDBySeed(t, recallID, 2) // fresh open -> untouched
	remover := &fakeRLRemover{}
	svc := service.NewGraphMemoryRLSessionService(testPool, &fakeRLStarter{}, remover)
	ctx := context.Background()
	mustOpenRLSession(t, svc, traj0)
	mustOpenRLSession(t, svc, traj1)
	mustOpenRLSession(t, svc, traj2)

	// traj0: stale open session.
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_rl_session SET updated_at = now() - interval '2 hours'
		WHERE trajectory_id = $1
	`, traj0); err != nil {
		t.Fatal(err)
	}
	// traj1: rewarded session (key already cleared by the durable ack).
	if err := svc.EnqueueReward(ctx, traj1, 0.1); err != nil {
		t.Fatal(err)
	}
	claims, err := svc.ClaimPending(ctx, 10)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim = %d rows, err %v; want 1", len(claims), err)
	}
	if err := svc.MarkDelivered(ctx, claims[0].OutboxID); err != nil {
		t.Fatal(err)
	}

	reaped, err := svc.ReapStaleSessions(ctx, time.Hour, 100)
	if err != nil {
		t.Fatalf("ReapStaleSessions: %v", err)
	}
	if reaped != 2 {
		t.Fatalf("reaped = %d, want 2 (stale open + rewarded)", reaped)
	}
	removed := remover.removedIDs()
	if len(removed) != 2 {
		t.Fatalf("RemoveSession calls = %v, want 2", removed)
	}
	status0, _, key0, _ := rlSessionRow(t, traj0)
	if status0 != "closed" || key0 != "" {
		t.Fatalf("traj0 = (%q, key %q), want (closed, cleared)", status0, key0)
	}
	status1, _, _, _ := rlSessionRow(t, traj1)
	if status1 != "closed" {
		t.Fatalf("traj1 status = %q, want closed", status1)
	}
	status2, _, key2, _ := rlSessionRow(t, traj2)
	if status2 != "open" || key2 == "" {
		t.Fatalf("traj2 = (%q, key %q), want (open, intact)", status2, key2)
	}
}
