package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestEffectiveSandboxNodeStatus(t *testing.T) {
	t.Parallel()
	recent := pgtype.Timestamptz{Time: time.Now().Add(-5 * time.Second), Valid: true}
	stale := pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Minute), Valid: true}

	if got := effectiveSandboxNodeStatus("offline", recent); got != "offline" {
		t.Fatalf("stored offline: got %q", got)
	}
	if got := effectiveSandboxNodeStatus("online", recent); got != "online" {
		t.Fatalf("recent heartbeat: got %q", got)
	}
	if got := effectiveSandboxNodeStatus("online", stale); got != "offline" {
		t.Fatalf("stale heartbeat: got %q", got)
	}
	if got := effectiveSandboxNodeStatus("online", pgtype.Timestamptz{}); got != "offline" {
		t.Fatalf("missing last_seen_at: got %q", got)
	}
}

func TestSandboxNodeUnreachableForJobs(t *testing.T) {
	t.Parallel()
	recent := pgtype.Timestamptz{Time: time.Now().Add(-5 * time.Second), Valid: true}
	stale := pgtype.Timestamptz{Time: time.Now().Add(-2 * time.Minute), Valid: true}
	deleted := pgtype.Timestamptz{Time: time.Now(), Valid: true}

	if sandboxNodeUnreachableForJobs("online", recent, pgtype.Timestamptz{}) {
		t.Fatal("online node with fresh heartbeat should be reachable")
	}
	if !sandboxNodeUnreachableForJobs("online", stale, pgtype.Timestamptz{}) {
		t.Fatal("stale heartbeat should be unreachable")
	}
	if !sandboxNodeUnreachableForJobs("offline", recent, pgtype.Timestamptz{}) {
		t.Fatal("offline node should be unreachable")
	}
	if !sandboxNodeUnreachableForJobs("online", recent, deleted) {
		t.Fatal("soft-deleted node should be unreachable")
	}
}
