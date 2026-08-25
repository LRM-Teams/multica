// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Spec §15, A26/D29: durable version-retention leases are idempotent per
// consumer tuple, fail closed on unknown ids / invalid kinds, and Dive
// pins the recall's graph version until the job terminalizes.

func TestGraphMemoryLeaseAcquireIdempotent(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	svc := service.NewGraphMemoryLeaseService(testPool)
	owner := util.UUIDToString(fx.projectID)
	consumerA := uuid.NewString()
	consumerB := uuid.NewString()

	id1, err := svc.AcquireVersionLease(ctx, "project", owner, 1, "export", consumerA)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if id1 == "" {
		t.Fatal("first acquire returned empty lease id")
	}
	id2, err := svc.AcquireVersionLease(ctx, "project", owner, 1, "export", consumerA)
	if err != nil {
		t.Fatalf("duplicate acquire: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("duplicate acquire id = %s, want %s", id2, id1)
	}
	if n := openLeaseCount(t, "export", consumerA); n != 1 {
		t.Fatalf("open rows for consumer A = %d, want 1", n)
	}

	idB, err := svc.AcquireVersionLease(ctx, "project", owner, 1, "export", consumerB)
	if err != nil {
		t.Fatalf("second consumer acquire: %v", err)
	}
	if idB == "" || idB == id1 {
		t.Fatalf("different consumer_id must get its own row, got %s", idB)
	}
	if n := openLeaseCount(t, "export", consumerB); n != 1 {
		t.Fatalf("open rows for consumer B = %d, want 1", n)
	}
}

func TestGraphMemoryLeaseReleaseAndOpenVersions(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	svc := service.NewGraphMemoryLeaseService(testPool)
	owner := util.UUIDToString(fx.projectID)
	consumer := uuid.NewString()

	leaseID, err := svc.AcquireVersionLease(ctx, "project", owner, 1, "backtest", consumer)
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	open, err := svc.OpenLeasedVersions(ctx, "project", owner)
	if err != nil {
		t.Fatalf("OpenLeasedVersions: %v", err)
	}
	if !open[1] {
		t.Fatalf("open versions = %v, want version 1 while leased", open)
	}

	if err := svc.ReleaseVersionLease(ctx, leaseID); err != nil {
		t.Fatalf("first release: %v", err)
	}
	firstReleased := leaseReleasedAt(t, leaseID)
	if firstReleased == nil {
		t.Fatal("released_at unset after first release")
	}
	if err := svc.ReleaseVersionLease(ctx, leaseID); err != nil {
		t.Fatalf("second release: %v", err)
	}
	secondReleased := leaseReleasedAt(t, leaseID)
	if secondReleased == nil || !secondReleased.Equal(*firstReleased) {
		t.Fatalf("released_at changed on idempotent release: first=%v second=%v", firstReleased, secondReleased)
	}

	if err := svc.ReleaseVersionLease(ctx, uuid.NewString()); err == nil {
		t.Fatal("unknown lease id must error")
	}

	open, err = svc.OpenLeasedVersions(ctx, "project", owner)
	if err != nil {
		t.Fatalf("OpenLeasedVersions after release: %v", err)
	}
	if open[1] {
		t.Fatalf("open versions still contain 1 after release: %v", open)
	}
}

func TestGraphMemoryLeaseInvalidConsumerKind(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx := mustGraphMemoryRecallFixture(t)
	svc := service.NewGraphMemoryLeaseService(testPool)

	_, err := svc.AcquireVersionLease(ctx, "project", util.UUIDToString(fx.projectID), 1, "bogus", uuid.NewString())
	if err == nil {
		t.Fatal("bogus consumer_kind must fail")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "consumer") {
		t.Fatalf("invalid kind error = %v, want a consumer_kind validation failure before SQL", err)
	}
}

func TestGraphMemoryDiveLeaseWiring(t *testing.T) {
	if testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	fx, recallID := mustGraphMemoryDiveFixture(t, "trace-dive-lease-"+uuid.NewString()[:8], 1)
	mustTerminalTrajectory(t, recallID, 0, "found")
	dive := service.NewGraphMemoryDiveService(testPool)
	leases := service.NewGraphMemoryLeaseService(testPool)
	owner := util.UUIDToString(fx.projectID)

	enqueued, err := dive.EnqueueIfBarrierMet(ctx, util.UUIDToString(recallID))
	if err != nil {
		t.Fatalf("EnqueueIfBarrierMet: %v", err)
	}
	if !enqueued {
		t.Fatal("expected dive job enqueue")
	}

	var jobID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM graph_memory_dive_job WHERE recall_id = $1
	`, recallID).Scan(&jobID); err != nil {
		t.Fatal(err)
	}
	if n := openLeaseCount(t, "dive", util.UUIDToString(jobID)); n != 1 {
		t.Fatalf("open dive leases for job = %d, want 1", n)
	}
	open, err := leases.OpenLeasedVersions(ctx, "project", owner)
	if err != nil {
		t.Fatalf("OpenLeasedVersions: %v", err)
	}
	if !open[1] {
		t.Fatalf("open versions = %v, want the recall's pinned version 1", open)
	}

	job, err := dive.Lease(ctx, "worker-lease", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("lease: job=%v err=%v", job, err)
	}
	t1 := trajectoryIDBySeed(t, recallID, 0)
	ok, err := dive.ApplyDiveResult(ctx, job.ID, "worker-lease", &memorygraph.DiveResult{
		Scores: []memorygraph.DiveTrajectoryScore{
			{TrajectoryID: t1, Relevance: 1, Groundedness: 1, Completeness: 1},
		},
	}, 0.1)
	if err != nil {
		t.Fatalf("ApplyDiveResult: %v", err)
	}
	if !ok {
		t.Fatal("ApplyDiveResult fenced out the live worker")
	}
	if ok, err := dive.Complete(ctx, job.ID, "worker-lease", false, []byte(`{}`)); err != nil || !ok {
		t.Fatalf("Complete: ok=%v err=%v", ok, err)
	}

	if n := openLeaseCount(t, "dive", job.ID); n != 0 {
		t.Fatalf("dive lease still open after terminal complete: %d", n)
	}
	var releasedAt *time.Time
	if err := testPool.QueryRow(ctx, `
		SELECT released_at FROM graph_memory_version_lease
		WHERE consumer_kind = 'dive' AND consumer_id = $1
	`, job.ID).Scan(&releasedAt); err != nil {
		t.Fatalf("load dive lease: %v", err)
	}
	if releasedAt == nil {
		t.Fatal("dive lease row must be released after ApplyDiveResult")
	}
	open, err = leases.OpenLeasedVersions(ctx, "project", owner)
	if err != nil {
		t.Fatalf("OpenLeasedVersions after complete: %v", err)
	}
	if open[1] {
		t.Fatalf("version 1 still open after dive lease release (no recall lease in fixture): %v", open)
	}
}

func openLeaseCount(t *testing.T, kind, consumerID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM graph_memory_version_lease
		WHERE consumer_kind = $1 AND consumer_id = $2 AND released_at IS NULL
	`, kind, consumerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func leaseReleasedAt(t *testing.T, leaseID string) *time.Time {
	t.Helper()
	var ts *time.Time
	if err := testPool.QueryRow(context.Background(), `
		SELECT released_at FROM graph_memory_version_lease WHERE id = $1
	`, leaseID).Scan(&ts); err != nil {
		t.Fatal(err)
	}
	return ts
}
