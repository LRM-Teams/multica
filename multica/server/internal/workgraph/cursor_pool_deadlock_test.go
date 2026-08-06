package workgraph

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// singleConnPool returns a *pgxpool.Pool capped at maxConns, pointed at the
// same test database as testPool. This is how the #1803 attachAgentRuntimeNames
// deadlock (an open rows cursor from one Query() plus a second connection
// acquired before Close()) reproduces deterministically instead of only
// showing up under real concurrent load: with maxConns=1, the second acquire
// has zero spare connections the moment the first row is scanned, guaranteed,
// every run. Mirrors singleConnHandler in internal/handler/cursor_pool_deadlock_test.go.
func singleConnPool(t *testing.T, maxConns int32) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	cfg, err := pgxpool.ParseConfig(dbURL)
	if err != nil {
		t.Fatalf("parse constrained pool config: %v", err)
	}
	cfg.MaxConns = maxConns
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("create constrained pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestHandleHumanRework_SingleConnPoolDoesNotDeadlock pins the fix for
// HandleHumanRework calling s.pool.Exec (a second pool acquire) from inside
// the outer rows.Next() loop while that outer cursor was still open (task
// #90, same shape as the #1803 attachAgentRuntimeNames bug). Confirm-broken:
// reverting the fix hangs until the context deadline and leaves the node's
// status unchanged, since the Exec never runs.
func TestHandleHumanRework_SingleConnPoolDoesNotDeadlock(t *testing.T) {
	ctx := t.Context()
	workspaceID := pgUUID(uuid.New())
	channelID := pgUUID(uuid.New())
	agentID := pgUUID(uuid.New())
	createWorkgraphWorkspace(t, ctx, workspaceID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	createWorkgraphChannel(t, ctx, workspaceID, channelID)

	var nodeID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		INSERT INTO work_node (workspace_id, kind, title, owner_type, owner_id, status, primary_channel_id)
		VALUES ($1, 'chat_commitment', 'deadlock test node', 'agent', $2, 'active', $3)
		RETURNING id
	`, workspaceID, agentID, channelID).Scan(&nodeID); err != nil {
		t.Fatalf("seed work_node: %v", err)
	}

	pool := singleConnPool(t, 1)
	store := NewStore(pool)
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	err := store.HandleHumanRework(reqCtx, workspaceID, channelID, []pgtype.UUID{agentID})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("HandleHumanRework: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("HandleHumanRework took %s with a single-connection pool — cursor held open across a second Exec() acquire (pool deadlock)", elapsed)
	}

	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM work_node WHERE id = $1`, nodeID).Scan(&status); err != nil {
		t.Fatalf("read node status: %v", err)
	}
	if status != "needs_rework" {
		t.Fatalf("node status = %q, want needs_rework", status)
	}
}
