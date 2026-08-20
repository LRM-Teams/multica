package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §13/A29: the audit view is derived from the authoritative PostgreSQL
// ledger, never from legacy query-log files, and is isolated by workspace.
func TestGraphMemoryAuditLedger(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	other := mustGraphMemoryRecallFixture(t)

	recallOne := mustInsertGraphMemoryRecall(t, fx, "audit-ledger-one-"+uuid.NewString()[:8])
	recallTwo := mustInsertGraphMemoryRecall(t, fx, "audit-ledger-two-"+uuid.NewString()[:8])
	otherRecall := mustInsertGraphMemoryRecall(t, other, "audit-ledger-other-"+uuid.NewString()[:8])
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_recall
		SET status = 'completed', training_mode = 'offline_rl', terminal_at = now()
		WHERE id = $1
	`, recallOne); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'failed', training_mode = 'offline_rl' WHERE id = $1
	`, recallTwo); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE graph_memory_recall SET status = 'completed', training_mode = 'offline_rl' WHERE id = $1
	`, otherRecall); err != nil {
		t.Fatal(err)
	}

	trajectoryOne := mustInsertGraphMemoryTrajectory(t, fx, recallOne, 0)
	trajectoryTwo := mustInsertGraphMemoryTrajectory(t, fx, recallOne, 1)
	trajectoryThree := mustInsertGraphMemoryTrajectory(t, fx, recallTwo, 0)
	otherTrajectory := mustInsertGraphMemoryTrajectory(t, other, otherRecall, 0)
	for _, update := range []struct {
		id         string
		status     string
		diveStatus string
		rounds     int
		reward     float64
	}{
		{util.UUIDToString(trajectoryOne), "found", "graded", 2, 0.8},
		{util.UUIDToString(trajectoryTwo), "miss", "graded", 6, 0.4},
		{util.UUIDToString(trajectoryThree), "error", "bypassed", 0, 0},
		{util.UUIDToString(otherTrajectory), "found", "graded", 99, 0.9},
	} {
		if _, err := testPool.Exec(ctx, `
			UPDATE graph_memory_trajectory
			SET status = $2, dive_status = $3, rounds = $4,
			    score_relevance = CASE WHEN $3 = 'graded' THEN 0.9 ELSE NULL END,
			    score_groundedness = CASE WHEN $3 = 'graded' THEN 0.8 ELSE NULL END,
			    score_completeness = CASE WHEN $3 = 'graded' THEN 0.7 ELSE NULL END,
			    overall_score = CASE WHEN $3 = 'graded' THEN $5::double precision ELSE NULL END,
			    reward = $5::double precision
			WHERE id = $1::uuid
		`, update.id, update.status, update.diveStatus, update.rounds, update.reward); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_dive_job
		  (recall_id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, status, attempts, incomplete)
		SELECT id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, 'completed', 2, false
		FROM graph_memory_recall WHERE id = $1
	`, recallOne); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_dive_job
		  (recall_id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, status, attempts, error_kind, last_error)
		SELECT id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, 'failed', 3, 'backend', 'provider failure'
		FROM graph_memory_recall WHERE id = $1
	`, recallTwo); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_dive_job
		  (recall_id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, status, attempts)
		SELECT id, workspace_id, trace_id, graph_kind, graph_owner_id, graph_version, 'completed', 77
		FROM graph_memory_recall WHERE id = $1
	`, otherRecall); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_reward_outbox
		  (workspace_id, trajectory_id, reward, status, attempts, next_attempt_at, created_at)
		VALUES ($1, $2, 0.8, 'pending', 1, now(), now() - interval '2 hours')
	`, fx.workspaceID, trajectoryOne); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_reward_outbox
		  (workspace_id, trajectory_id, reward, status, attempts)
		VALUES ($1, $2, 0.9, 'delivered', 71)
	`, other.workspaceID, otherTrajectory); err != nil {
		t.Fatal(err)
	}

	var authoritativeItem string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO graph_memory_info_item
		  (workspace_id, graph_kind, graph_owner_id, statement, statement_hash, status)
		VALUES ($1, 'project', $2, 'authoritative fact', $3, 'authoritative')
		RETURNING id::text
	`, fx.workspaceID, fx.projectID, "audit-ledger-authoritative-"+uuid.NewString()).Scan(&authoritativeItem); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_recall_info_item (recall_id, item_id) VALUES ($1, $2::uuid)
	`, recallOne, authoritativeItem); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_info_item
		  (workspace_id, graph_kind, graph_owner_id, statement, statement_hash, status)
		VALUES ($1, 'project', $2, 'legacy_non_authoritative file row', $3, 'legacy_non_authoritative')
	`, fx.workspaceID, fx.projectID, "audit-ledger-legacy-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}

	svc := service.NewGraphMemoryAuditServiceWithPool(testPool, t.TempDir())
	sum, err := svc.Summary(ctx, util.UUIDToString(fx.workspaceID))
	if err != nil {
		t.Fatal(err)
	}
	if got := sum.Ledger.RecallsByStatus; got["completed"] != 1 || got["failed"] != 1 || len(got) != 2 {
		t.Fatalf("recalls by status = %#v, want completed=1 failed=1", got)
	}
	if got := sum.Ledger.TrajectoriesByStatus; got["found"] != 1 || got["miss"] != 1 || got["error"] != 1 || len(got) != 3 {
		t.Fatalf("trajectories by status = %#v, want workspace-only rows", got)
	}
	if sum.Ledger.AvgRounds != 4 || sum.Ledger.P95Rounds != 5.8 {
		t.Fatalf("rounds avg/p95 = %v/%v, want 4/5.8", sum.Ledger.AvgRounds, sum.Ledger.P95Rounds)
	}
	if sum.Ledger.GradedTrajectories != 2 || sum.Ledger.OverallRewardMin != 0.4 || sum.Ledger.OverallRewardAvg < 0.599999 || sum.Ledger.OverallRewardAvg > 0.600001 {
		t.Fatalf("graded reward stats = %+v", sum.Ledger)
	}
	if got := sum.Ledger.DiveJobsByStatus; got["completed"] != 1 || got["failed"] != 1 || len(got) != 2 {
		t.Fatalf("dive jobs by status = %#v", got)
	}
	if sum.Ledger.DiveJobAttempts != 5 || sum.Ledger.LastFailure.Kind != "backend" || sum.Ledger.LastFailure.Message != "provider failure" {
		t.Fatalf("dive failure summary = %+v", sum.Ledger)
	}
	if got := sum.Ledger.RewardOutboxByStatus; got["pending"] != 1 || len(got) != 1 || sum.Ledger.OldestPendingAgeSeconds < 3600 {
		t.Fatalf("outbox summary = %+v", sum.Ledger)
	}
	if sum.Ledger.OfflineExportEligible != 2 || sum.Ledger.CatalogItems != 2 || sum.Ledger.DiveGroundTruthItems != 1 || sum.Ledger.AuditOnlyItems != 1 {
		t.Fatalf("eligibility/catalog summary = %+v", sum.Ledger)
	}

	// The elapsed-age assertion is deliberately tolerant of clock scheduling.
	_ = time.Second
}

// Spec §13/A29: a provider error may echo credentials, but neither the job
// ledger nor its served audit projection may retain the secret.
func TestGraphMemoryRedactionCanary(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	const canary = "sk-live-CANARYKEY123456"
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-redaction-"+uuid.NewString()[:8], 1)
	mustEnqueueReadyDive(t, recallID, "found")
	if _, err := testPool.Exec(ctx, `UPDATE graph_memory_dive_job SET max_attempts = 1 WHERE recall_id = $1`, recallID); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	mustSeedPinnedGraph(t, root, fx)
	worker := newTestDiveWorker(t, root, nil, &scriptedDiveBackend{err: fmt.Errorf("provider rejected proxy %s", canary)}, service.GraphMemoryLimits{})
	if did, err := worker.RunOnce(ctx, "worker-redaction"); err != nil || !did {
		t.Fatalf("RunOnce: did=%v err=%v", did, err)
	}
	var persisted string
	if err := testPool.QueryRow(ctx, `SELECT last_error FROM graph_memory_dive_job WHERE recall_id = $1`, recallID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, canary) || !strings.Contains(persisted, "[REDACTED]") {
		t.Fatalf("persisted Dive error leaked canary: %q", persisted)
	}
	sum, err := service.NewGraphMemoryAuditServiceWithPool(testPool, root).Summary(ctx, util.UUIDToString(fx.workspaceID))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sum.Ledger.LastFailure.Message, canary) || !strings.Contains(sum.Ledger.LastFailure.Message, "[REDACTED]") {
		t.Fatalf("audit failure leaked canary: %+v", sum.Ledger.LastFailure)
	}
}
