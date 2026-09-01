// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §5/§6, acceptance A8/A25: one durable Dive job per recall, enqueued
// only after all K explore trajectories are terminal (the K barrier);
// trace-idempotent; lease/attempt fencing so a crashed worker's job resumes
// with exactly one effective outcome; bounded retries end in judge_failed
// with reward 0 for normally completed runs and no ground truth.

// mustGraphMemoryDiveFixture writes a recall with k running trajectories and
// returns the recall id.
func mustGraphMemoryDiveFixture(t *testing.T, traceID string, k int) (recallLedgerFixture, pgtype.UUID) {
	t.Helper()
	fx := mustGraphMemoryRecallFixture(t)
	recallID := mustInsertGraphMemoryRecall(t, fx, traceID)
	ctx := context.Background()
	for seed := 0; seed < k; seed++ {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO graph_memory_trajectory (recall_id, workspace_id, seed_index)
			VALUES ($1, $2, $3)
		`, recallID, fx.workspaceID, seed); err != nil {
			t.Fatal(err)
		}
	}
	return fx, recallID
}

func mustTerminalTrajectory(t *testing.T, recallID pgtype.UUID, seed int, status string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_trajectory SET status = $3, terminal_at = now()
		WHERE recall_id = $1 AND seed_index = $2
	`, recallID, seed, status); err != nil {
		t.Fatal(err)
	}
}

func diveJobCount(t *testing.T, recallID pgtype.UUID) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func recallStatus(t *testing.T, recallID pgtype.UUID) string {
	t.Helper()
	var status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status FROM graph_memory_recall WHERE id = $1
	`, recallID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestGraphMemoryDiveBarrierEnqueue(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-barrier-"+uuid.NewString()[:8], 2)
	svc := service.NewGraphMemoryDiveService(testPool)

	// One trajectory still running: the K barrier is not met, no job.
	mustTerminalTrajectory(t, recallID, 0, "found")
	enqueued, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("EnqueueIfBarrierMet: %v", err)
	}
	if enqueued || diveJobCount(t, recallID) != 0 {
		t.Fatalf("barrier met with a running trajectory: enqueued=%v jobs=%d", enqueued, diveJobCount(t, recallID))
	}

	// All K terminal: exactly one job, pinned to the recall's graph version,
	// and the recall moves to dive_queued.
	mustTerminalTrajectory(t, recallID, 1, "miss")
	enqueued, err = svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("EnqueueIfBarrierMet: %v", err)
	}
	if !enqueued || diveJobCount(t, recallID) != 1 {
		t.Fatalf("barrier enqueue: enqueued=%v jobs=%d, want (true, 1)", enqueued, diveJobCount(t, recallID))
	}
	if status := recallStatus(t, recallID); status != "dive_queued" {
		t.Fatalf("recall status = %s, want dive_queued", status)
	}
	var jobVersion int
	if err := testPool.QueryRow(ctx, `
		SELECT graph_version FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&jobVersion); err != nil {
		t.Fatal(err)
	}
	if jobVersion != 1 {
		t.Fatalf("dive job pinned version = %d, want the recall's 1", jobVersion)
	}

	// Trace-idempotent: re-enqueue creates no second job.
	enqueued, err = svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if enqueued || diveJobCount(t, recallID) != 1 {
		t.Fatalf("idempotent re-enqueue: enqueued=%v jobs=%d, want (false, 1)", enqueued, diveJobCount(t, recallID))
	}
}

func TestGraphMemoryDiveEnqueueSkipsTerminalRecall(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-term-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectory(t, recallID, 0, "found")
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'completed', terminal_at = now() WHERE id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}
	svc := service.NewGraphMemoryDiveService(testPool)
	enqueued, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("EnqueueIfBarrierMet on terminal recall: %v", err)
	}
	if enqueued || diveJobCount(t, recallID) != 0 {
		t.Fatalf("terminal recall must not gain a dive job: enqueued=%v jobs=%d", enqueued, diveJobCount(t, recallID))
	}
}

// A25: a worker crash mid-attempt loses nothing and duplicates nothing — the
// expired lease is re-acquired, the stale worker's completion is fenced out,
// and exactly one terminal outcome is persisted.
func TestGraphMemoryDiveLeaseFencingCrashRecovery(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-crash-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectory(t, recallID, 0, "found")
	svc := service.NewGraphMemoryDiveService(testPool)
	if _, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}

	// Worker A leases the job, then "crashes" (never completes).
	jobA, err := svc.Lease(ctx, "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("lease A: %v", err)
	}
	if jobA == nil {
		t.Fatal("lease A: no job")
	}
	if jobA.Attempts != 1 {
		t.Fatalf("attempts after first lease = %d, want 1", jobA.Attempts)
	}
	if status := recallStatus(t, recallID); status != "diving" {
		t.Fatalf("recall status after lease = %s, want diving", status)
	}
	if jobA.GraphVersion != 1 || jobA.TraceID == "" {
		t.Fatalf("leased job identity = version %d trace %q", jobA.GraphVersion, jobA.TraceID)
	}

	// While A's lease is valid, nobody else can lease.
	jobB, err := svc.Lease(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("lease B during valid lease: %v", err)
	}
	if jobB != nil {
		t.Fatalf("concurrent lease handed out the same job: %+v", jobB)
	}

	// A crashes; the lease expires. B re-acquires the same job (attempt 2).
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_dive_job SET lease_expires_at = now() - interval '1 second' WHERE id = $1
	`, jobA.ID); err != nil {
		t.Fatal(err)
	}
	jobB, err = svc.Lease(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("lease B after expiry: %v", err)
	}
	if jobB == nil || jobB.ID != jobA.ID {
		t.Fatalf("recovery lease = %+v, want job %s", jobB, jobA.ID)
	}
	if jobB.Attempts != 2 {
		t.Fatalf("attempts after recovery lease = %d, want 2", jobB.Attempts)
	}

	// The stale worker's completion is fenced out; only B's lands.
	ok, err := svc.Complete(ctx, jobA.ID, "worker-a", false, nil)
	if err != nil {
		t.Fatalf("stale complete: %v", err)
	}
	if ok {
		t.Fatal("stale worker completion must be fenced out")
	}
	ok, err = svc.Complete(ctx, jobB.ID, "worker-b", false, nil)
	if err != nil {
		t.Fatalf("complete B: %v", err)
	}
	if !ok {
		t.Fatal("recovery worker completion must succeed")
	}
	if status := recallStatus(t, recallID); status != "completed" {
		t.Fatalf("recall status = %s, want completed", status)
	}
	var jobStatus string
	var terminalAt *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT status, terminal_at FROM graph_memory_dive_job WHERE id = $1
	`, jobA.ID).Scan(&jobStatus, &terminalAt); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "completed" || terminalAt == nil {
		t.Fatalf("job terminal state = (%s, %v), want (completed, set)", jobStatus, terminalAt)
	}

	// Terminal jobs are never re-leased and completions do not double-land.
	jobC, err := svc.Lease(ctx, "worker-c", time.Minute)
	if err != nil {
		t.Fatalf("lease C: %v", err)
	}
	if jobC != nil {
		t.Fatalf("terminal job re-leased: %+v", jobC)
	}
	ok, err = svc.Complete(ctx, jobB.ID, "worker-b", false, nil)
	if err != nil {
		t.Fatalf("repeat complete: %v", err)
	}
	if ok {
		t.Fatal("repeat completion on a terminal job must be a no-op")
	}
}

// A8: bounded retries; terminal Dive infrastructure failure moves the recall
// to judge_failed, assigns reward 0 to otherwise normal runs, and writes no
// ground truth.
func TestGraphMemoryDiveTerminalFailureJudgeFailed(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-fail-"+uuid.NewString()[:8], 2)
	mustTerminalTrajectory(t, recallID, 0, "found")
	mustTerminalTrajectory(t, recallID, 1, "miss")
	svc := service.NewGraphMemoryDiveService(testPool)
	if _, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	// Bound the retries tightly for the test.
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_dive_job SET max_attempts = 2 WHERE recall_id = $1
	`, recallID); err != nil {
		t.Fatal(err)
	}

	// Attempt 1 fails transiently: back to queued, recall back to dive_queued.
	job, err := svc.Lease(ctx, "worker-a", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease attempt 1: job=%v err=%v", job, err)
	}
	terminal, err := svc.Fail(ctx, job.ID, "worker-a", "infra", "model endpoint 503", true)
	if err != nil {
		t.Fatalf("fail attempt 1: %v", err)
	}
	if terminal {
		t.Fatal("attempt 1 of 2 must not be terminal")
	}
	if status := recallStatus(t, recallID); status != "dive_queued" {
		t.Fatalf("recall status after retryable failure = %s, want dive_queued", status)
	}

	// Attempt 2 exhausts the bounded retries: terminal judge_failed.
	job, err = svc.Lease(ctx, "worker-a", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease attempt 2: job=%v err=%v", job, err)
	}
	terminal, err = svc.Fail(ctx, job.ID, "worker-a", "infra", "model endpoint 503", true)
	if err != nil {
		t.Fatalf("fail attempt 2: %v", err)
	}
	if !terminal {
		t.Fatal("attempt 2 of 2 must be terminal")
	}
	if status := recallStatus(t, recallID); status != "judge_failed" {
		t.Fatalf("recall status = %s, want judge_failed", status)
	}

	// Normally completed runs become reward-unavailable with the judge_failed
	// marker — a judge infrastructure failure is never a synthetic numeric 0
	// (Task 19, A46); no ground truth was produced (job result stays empty).
	var diveStatuses []string
	rows, err := testPool.Query(ctx, `
		SELECT reward, dive_status, reward_status, reward_revision
		FROM graph_memory_trajectory WHERE recall_id = $1 ORDER BY seed_index
	`, recallID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			reward *float64
			ds, rs string
			rev    int
		)
		if err := rows.Scan(&reward, &ds, &rs, &rev); err != nil {
			t.Fatal(err)
		}
		if reward != nil {
			t.Fatal("normal run reward is numeric after judge_failed, want NULL (A46)")
		}
		if rs != "unavailable" {
			t.Fatalf("trajectory reward_status = %q, want unavailable", rs)
		}
		if rev != 1 {
			t.Fatalf("trajectory reward_revision = %d, want 1", rev)
		}
		diveStatuses = append(diveStatuses, ds)
	}
	if len(diveStatuses) != 2 {
		t.Fatalf("judged-affected trajectories = %d, want 2", len(diveStatuses))
	}
	for _, ds := range diveStatuses {
		if ds != "judge_failed" {
			t.Fatalf("trajectory dive_status = %q, want judge_failed", ds)
		}
	}
	// The immutable ledger carries one unavailable record per run (no value).
	var unavail int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_reward_record
		WHERE trajectory_id IN (SELECT id FROM graph_memory_trajectory WHERE recall_id = $1)
		  AND status = 'unavailable' AND value IS NULL
	`, recallID).Scan(&unavail); err != nil {
		t.Fatal(err)
	}
	if unavail != 2 {
		t.Fatalf("unavailable reward records = %d, want 2", unavail)
	}
	var result string
	if err := testPool.QueryRow(ctx, `
		SELECT result::text FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	if result != "{}" {
		t.Fatalf("judge_failed job result = %s, want empty (no ground truth)", result)
	}

	// An exhausted job is never leased again.
	job, err = svc.Lease(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("lease after terminal: %v", err)
	}
	if job != nil {
		t.Fatalf("terminally failed job re-leased: %+v", job)
	}
}
