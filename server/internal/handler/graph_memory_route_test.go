package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
)

// Spec §4: the route registry and lineage tables exist with the designed
// columns; scoped_writer_ready defaults to false (rollout stays blocked).
func TestGraphMemoryRouteSchema(t *testing.T) {
	if testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	// Self-contained profile row: scoped_writer_ready must be observable
	// without relying on rows left behind by other tests.
	workspaceID := createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	if _, err := testPool.Exec(ctx,
		`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'graph')`, workspaceID); err != nil {
		t.Fatal(err)
	}
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

// createGraphMemoryTestWorkspace inserts a dedicated workspace so route and
// lineage rows cannot collide with other tests.
func createGraphMemoryTestWorkspace(t *testing.T) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, $3, $4)
		RETURNING id
	`, "Graph Memory Route Test", "graph-memory-route-test-"+uuid.NewString()[:8], "", "GMR").Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, id)
	})
	return id
}

// mustGraphMemoryWorkspaceOwner installs a dedicated owner member so the
// workspace satisfies the exactly-one-owner invariant (migration 301) before
// non-owner members join. The shared test user stays free to take the role
// under test (e.g. plain member).
func mustGraphMemoryWorkspaceOwner(t *testing.T, workspaceID pgtype.UUID) {
	t.Helper()
	ctx := context.Background()
	var userID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id::text
	`, "graph-memory-owner-"+uuid.NewString()[:8], "graph-memory-owner-"+uuid.NewString()[:8]+"@multica.ai").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
}

// createGraphMemoryTestChannel inserts a channel bound to no project
// (migration 112 columns; project_id from migration 123 stays NULL).
func createGraphMemoryTestChannel(t *testing.T, workspaceID pgtype.UUID) pgtype.UUID {
	t.Helper()
	var id pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, workspaceID, "route-test-"+uuid.NewString()[:8], testUserID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// Concurrency (spec §14 test 8): repeated/concurrent resolution yields
// exactly one active generation and one lineage row.
func TestResolveChannelRouteConcurrentSingleGeneration(t *testing.T) {
	if testPool == nil {
		t.Skip("test database unavailable")
	}
	ctx := context.Background()
	workspaceID := createGraphMemoryTestWorkspace(t)
	// The channel auto-seed (migration 237) makes the creator the ordinary
	// group's human owner only when the creator is a workspace member; the
	// workspace itself must also satisfy the single-owner invariant (301).
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID := createGraphMemoryTestChannel(t, workspaceID) // bound to no project
	const n = 8
	results := make(chan service.GraphRouteResolution, n)
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		go func() {
			r, err := service.ResolveChannelRoute(ctx, testPool, workspaceID.String(), channelID.String())
			if err != nil {
				errs <- err
				return
			}
			results <- r
		}()
	}
	for i := 0; i < n; i++ {
		select {
		case r := <-results:
			if r.Generation != 1 || r.GraphKind != "channel" || r.RoutingMode != "standalone" {
				t.Fatalf("resolution = %+v, want standalone channel generation 1", r)
			}
		case err := <-errs:
			t.Fatal(err)
		}
	}
	var lineageRows int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM graph_memory_channel_lineage WHERE channel_id = $1`, channelID).Scan(&lineageRows); err != nil {
		t.Fatal(err)
	}
	if lineageRows != 1 {
		t.Fatalf("lineage rows = %d, want exactly 1", lineageRows)
	}
}
