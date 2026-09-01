// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §7, acceptance A7/A9: applying a Dive result stores the per-dimension
// scores, the min-dimension overall, and the unclamped reward — computed with
// the SERVER-counted explore rounds — on each normal trajectory; bypassed
// runs receive reward 0 without grading; an incomplete Dive still supplies
// rewards but no authoritative ground truth.

// mustTerminalTrajectoryWithRounds marks a trajectory terminal with a fixed
// server-counted round count.
func mustTerminalTrajectoryWithRounds(t *testing.T, recallID pgtype.UUID, seed int, status string, rounds int) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE graph_memory_trajectory SET status = $3, rounds = $4, terminal_at = now()
		WHERE recall_id = $1 AND seed_index = $2
	`, recallID, seed, status, rounds); err != nil {
		t.Fatal(err)
	}
}

func trajectoryIDBySeed(t *testing.T, recallID pgtype.UUID, seed int) string {
	t.Helper()
	var id pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT id FROM graph_memory_trajectory WHERE recall_id = $1 AND seed_index = $2
	`, recallID, seed).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return util.UUIDToString(id)
}

func TestApplyDiveResultGradesAndRewards(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-reward-"+uuid.NewString()[:8], 3)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 3)
	mustTerminalTrajectoryWithRounds(t, recallID, 1, "miss", 5)
	mustTerminalTrajectoryWithRounds(t, recallID, 2, "error", 0)
	t1 := trajectoryIDBySeed(t, recallID, 0)
	t2 := trajectoryIDBySeed(t, recallID, 1)
	t3 := trajectoryIDBySeed(t, recallID, 2)

	svc := service.NewGraphMemoryDiveService(testPool)
	if _, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	job, err := svc.Lease(ctx, "worker-a", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}

	res := &memorygraph.DiveResult{
		Scores: []memorygraph.DiveTrajectoryScore{
			// overall = min(0.9, 0.4, 0.7) = 0.4; reward = 0.4 − 0.1×3 = 0.1
			{TrajectoryID: t1, Relevance: 0.9, Groundedness: 0.4, Completeness: 0.7},
			// overall = 0.2; reward = 0.2 − 0.1×5 = −0.3 (negative, unclamped)
			{TrajectoryID: t2, Relevance: 0.2, Groundedness: 0.2, Completeness: 0.2},
		},
		Bypassed: []memorygraph.DiveRunInput{{TrajectoryID: t3, Status: "error"}},
	}
	ok, err := svc.ApplyDiveResult(ctx, job.ID, "worker-a", res, 0.1)
	if err != nil {
		t.Fatalf("ApplyDiveResult: %v", err)
	}
	if !ok {
		t.Fatal("ApplyDiveResult fenced out the live lease holder")
	}

	type row struct {
		diveStatus                    string
		rewardStatus                  string
		rewardRevision                int
		relevance, groundedness, comp *float64
		overall, reward               *float64
	}
	read := func(tid string) row {
		var r row
		if err := testPool.QueryRow(ctx, `
			SELECT dive_status, reward_status, reward_revision,
			       score_relevance, score_groundedness, score_completeness, overall_score, reward
			FROM graph_memory_trajectory WHERE id = $1
		`, tid).Scan(&r.diveStatus, &r.rewardStatus, &r.rewardRevision,
			&r.relevance, &r.groundedness, &r.comp, &r.overall, &r.reward); err != nil {
			t.Fatal(err)
		}
		return r
	}

	r1 := read(t1)
	if r1.diveStatus != "graded" || r1.rewardStatus != "graded" || r1.rewardRevision != 1 ||
		r1.relevance == nil || *r1.relevance != 0.9 ||
		r1.overall == nil || *r1.overall != 0.4 || r1.reward == nil || *r1.reward < 0.1-1e-9 || *r1.reward > 0.1+1e-9 {
		t.Fatalf("t1 row = %+v, want graded/rev 1/0.9 dims/overall 0.4/reward 0.1", r1)
	}
	r2 := read(t2)
	if r2.diveStatus != "graded" || r2.rewardStatus != "graded" || r2.rewardRevision != 1 ||
		r2.reward == nil || *r2.reward < -0.3-1e-9 || *r2.reward > -0.3+1e-9 {
		t.Fatalf("t2 row = %+v, want graded/rev 1 with reward −0.3 (negative allowed)", r2)
	}
	r3 := read(t3)
	// Task 19: the explore agent's own terminal violation is a deterministic
	// negative, never a neutral 0; no scores are graded.
	wantBypass := memorygraph.DeterministicViolationReward(0.1, 0)
	if r3.diveStatus != "bypassed" || r3.rewardStatus != "deterministic" || r3.rewardRevision != 1 ||
		r3.reward == nil || *r3.reward < wantBypass-1e-9 || *r3.reward > wantBypass+1e-9 || r3.overall != nil {
		t.Fatalf("t3 row = %+v, want bypassed/deterministic %v/no scores", r3, wantBypass)
	}

	// Every trajectory carries its immutable ledger revision, graded and
	// deterministic values alike (Task 19 spec 14.2/14.4).
	for i, tid := range []string{t1, t2, t3} {
		var status string
		var value *float64
		var policy string
		if err := testPool.QueryRow(ctx, `
			SELECT status, value, policy_version FROM graph_memory_reward_record
			WHERE trajectory_id = $1 AND reward_kind = 'explore' AND revision = 1
		`, tid).Scan(&status, &value, &policy); err != nil {
			t.Fatalf("t%d ledger record: %v", i+1, err)
		}
		if status != "available" || value == nil || policy != memorygraph.ExploreRewardPolicyVersion {
			t.Fatalf("t%d ledger record = (%q, %v, %q), want available value/explore policy", i+1, status, value, policy)
		}
	}

	// Completion follows application so online-RL reward outboxing can be
	// durably recorded before the recall terminalizes.
	if ok, err := svc.Complete(ctx, job.ID, "worker-a", false, []byte(`{}`)); err != nil || !ok {
		t.Fatalf("Complete: ok=%v err=%v", ok, err)
	}
	if status := recallStatus(t, recallID); status != "completed" {
		t.Fatalf("recall status = %s, want completed", status)
	}
	var jobStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT status FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&jobStatus); err != nil {
		t.Fatal(err)
	}
	if jobStatus != "completed" {
		t.Fatalf("job status = %s, want completed", jobStatus)
	}
}

func TestApplyDiveResultFencingAndValidation(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-fence-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 2)
	t1 := trajectoryIDBySeed(t, recallID, 0)

	svc := service.NewGraphMemoryDiveService(testPool)
	if _, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	job, err := svc.Lease(ctx, "worker-a", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}
	res := &memorygraph.DiveResult{
		Scores: []memorygraph.DiveTrajectoryScore{{TrajectoryID: t1, Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5}},
	}

	// Wrong worker: fenced out, no mutation.
	ok, err := svc.ApplyDiveResult(ctx, job.ID, "worker-b", res, 0.1)
	if err != nil {
		t.Fatalf("fenced apply: %v", err)
	}
	if ok {
		t.Fatal("foreign worker must be fenced out")
	}
	var diveStatus string
	if err := testPool.QueryRow(ctx, `
		SELECT dive_status FROM graph_memory_trajectory WHERE id = $1
	`, t1).Scan(&diveStatus); err != nil {
		t.Fatal(err)
	}
	if diveStatus != "" {
		t.Fatalf("fenced apply mutated the trajectory: dive_status = %q", diveStatus)
	}

	// A score for a trajectory outside this recall fails the apply (the job
	// layer turns it into a bounded retry).
	foreign, foreignRecall := mustGraphMemoryDiveFixture(t, "trace-dive-foreign-"+uuid.NewString()[:8], 1)
	_ = foreign
	bad := &memorygraph.DiveResult{
		Scores: []memorygraph.DiveTrajectoryScore{{TrajectoryID: trajectoryIDBySeed(t, foreignRecall, 0), Relevance: 0.5, Groundedness: 0.5, Completeness: 0.5}},
	}
	if _, err := svc.ApplyDiveResult(ctx, job.ID, "worker-a", bad, 0.1); err == nil {
		t.Fatal("score for a foreign trajectory must fail the apply")
	}

	// The live holder still succeeds afterwards.
	ok, err = svc.ApplyDiveResult(ctx, job.ID, "worker-a", res, 0.1)
	if err != nil || !ok {
		t.Fatalf("live apply: ok=%v err=%v", ok, err)
	}
}

func TestApplyDiveResultIncompleteStillRewards(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	_, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-inc-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectoryWithRounds(t, recallID, 0, "found", 1)
	t1 := trajectoryIDBySeed(t, recallID, 0)

	svc := service.NewGraphMemoryDiveService(testPool)
	if _, err := svc.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID)); err != nil {
		t.Fatal(err)
	}
	job, err := svc.Lease(ctx, "worker-a", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}
	res := &memorygraph.DiveResult{
		Scores:     []memorygraph.DiveTrajectoryScore{{TrajectoryID: t1, Relevance: 0.6, Groundedness: 0.6, Completeness: 0.6}},
		Incomplete: true,
	}
	ok, err := svc.ApplyDiveResult(ctx, job.ID, "worker-a", res, 0.1)
	if err != nil || !ok {
		t.Fatalf("incomplete apply: ok=%v err=%v", ok, err)
	}
	// A9: rewards stand (0.6 − 0.1×1 = 0.5) even though the dive is
	// incomplete; the job records the incomplete marker so no authoritative
	// ground truth is derived from it.
	var reward *float64
	if err := testPool.QueryRow(ctx, `
		SELECT reward FROM graph_memory_trajectory WHERE id = $1
	`, t1).Scan(&reward); err != nil {
		t.Fatal(err)
	}
	if reward == nil || *reward < 0.5-1e-9 || *reward > 0.5+1e-9 {
		t.Fatalf("incomplete dive reward = %v, want 0.5", reward)
	}
	if ok, err := svc.Complete(ctx, job.ID, "worker-a", true, []byte(`{}`)); err != nil || !ok {
		t.Fatalf("Complete incomplete: ok=%v err=%v", ok, err)
	}
	var incomplete bool
	if err := testPool.QueryRow(ctx, `
		SELECT incomplete FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&incomplete); err != nil {
		t.Fatal(err)
	}
	if !incomplete {
		t.Fatal("job incomplete flag not persisted")
	}
}
