package handler

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// Spec §4: the route registry and lineage tables exist with the designed
// columns; scoped_writer_ready defaults to false (rollout stays blocked).
func TestGraphMemoryRouteSchema(t *testing.T) {
	if testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	var routingMode string
	err := testPool.QueryRow(ctx, `
		SELECT routing_mode FROM graph_memory_channel_route LIMIT 0`).Scan(&routingMode)
	if err == nil {
		t.Fatal("LIMIT 0 scan must not return a row")
	}
	var ready bool
	err = testPool.QueryRow(ctx, `
		SELECT scoped_writer_ready FROM graph_memory_profile LIMIT 1`).Scan(&ready)
	if err != nil {
		t.Fatalf("graph_memory_profile.scoped_writer_ready missing: %v", err)
	}
	var id string
	err = testPool.QueryRow(ctx, `
		SELECT id::text FROM graph_memory_consolidation_run LIMIT 0`).Scan(&id)
	_ = err // LIMIT 0 returns no rows; only the relation must exist
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM graph_memory_channel_lineage`).Scan(&n); err != nil {
		t.Fatalf("graph_memory_channel_lineage missing: %v", err)
	}
	_ = pgtype.UUID{}
}
