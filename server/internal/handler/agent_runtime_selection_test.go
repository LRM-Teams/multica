package handler

import (
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/internal/service"
)

func TestRuntimeIsPickableOnlineUsesHeartbeatFreshness(t *testing.T) {
	now := time.Now()
	stale := pgtype.Timestamptz{Time: now.Add(-10 * time.Minute), Valid: true}
	fresh := pgtype.Timestamptz{Time: now.Add(-5 * time.Second), Valid: true}

	staleOnline := db.AgentRuntime{Status: "online", LastSeenAt: stale}
	if runtimeIsPickableOnline(staleOnline, now) {
		t.Fatal("status=online with stale last_seen must not be pickable")
	}

	freshOnline := db.AgentRuntime{Status: "online", LastSeenAt: fresh}
	if !runtimeIsPickableOnline(freshOnline, now) {
		t.Fatal("status=online with fresh last_seen must be pickable")
	}

	offline := db.AgentRuntime{Status: "offline", LastSeenAt: fresh, UpdatedAt: stale}
	if runtimeIsPickableOnline(offline, now) {
		t.Fatal("explicit offline must not be pickable")
	}

	// Threshold is the shared constant — no third clock.
	if service.AgentHealthStaleThreshold != 150*time.Second {
		t.Fatalf("AgentHealthStaleThreshold = %v, unexpected (pick must stay in lockstep with health)", service.AgentHealthStaleThreshold)
	}
}

func TestRuntimeIsPickableOnlineBoundary(t *testing.T) {
	now := time.Now()
	// Just inside the window
	justFresh := pgtype.Timestamptz{Time: now.Add(-service.AgentHealthStaleThreshold + time.Second), Valid: true}
	if !runtimeIsPickableOnline(db.AgentRuntime{Status: "online", LastSeenAt: justFresh}, now) {
		t.Fatal("last_seen just under threshold must still be pickable")
	}
	// At/over threshold is stale
	justStale := pgtype.Timestamptz{Time: now.Add(-service.AgentHealthStaleThreshold), Valid: true}
	if runtimeIsPickableOnline(db.AgentRuntime{Status: "online", LastSeenAt: justStale}, now) {
		t.Fatal("last_seen at threshold must not be pickable")
	}
}
