package scheduler

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/memorycuration"
)

// singleConnPool returns a *pgxpool.Pool capped at maxConns, pointed at the
// same test database as integrationPool. This is how the #1803
// attachAgentRuntimeNames deadlock (an open rows cursor from one Query() plus
// a second connection acquired before Close()) reproduces deterministically
// instead of only showing up under real concurrent load: with maxConns=1,
// the second acquire has zero spare connections the moment the first row is
// scanned, guaranteed, every run. Mirrors singleConnHandler in
// internal/handler/cursor_pool_deadlock_test.go.
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

// TestMemoryCurationProfileDriven_SingleConnPoolDoesNotDeadlock pins the fix
// for makeMemoryCurationIntentHandler's profile-driven branch (team_curation,
// and agent_self_review without the memory_curation_agent_run table) calling
// activeMemoryCurationAgentIDs and then pool.Exec/QueryRow (each a second
// pool acquire) from inside the outer rows.Next() loop over memory_curator_profile
// while that outer cursor was still open (task #90, same shape as the #1803
// attachAgentRuntimeNames bug). Confirm-broken: reverting the fix hangs until
// the context deadline and never creates the expected memory_curation_run row.
func TestMemoryCurationProfileDriven_SingleConnPoolDoesNotDeadlock(t *testing.T) {
	setupPool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, curatorAgentID, targetAgentID string
	if err := setupPool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Deadlock Profile "+suffix, "deadlock-profile-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := setupPool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Deadlock Profile "+suffix, "deadlock-profile-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = setupPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = setupPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := setupPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	if err := setupPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, last_seen_at)
		VALUES ($1, $2, 'Deadlock Profile Runtime', 'local', 'codex', 'online', 'test', now())
		RETURNING id::text
	`, workspaceID, "deadlock-profile-"+suffix).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := setupPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "deadlock_profile_curator_"+suffix, runtimeID, userID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := setupPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "deadlock_profile_target_"+suffix, runtimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := setupPool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Deadlock profile-driven activity', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := setupPool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := setupPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'acked', '2026-07-09 12:00:00+00')
	`, targetAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := setupPool.Exec(ctx, `
		INSERT INTO memory_curator_profile (
		  workspace_id, user_id, enabled, self_review_enabled, team_curation_enabled,
		  mode, runtime_id, curator_agent_id, target_scope, timezone, schedule_hour, catch_up_enabled
		) VALUES ($1, $2, true, false, true, 'review', $3, $4, 'owned_all', 'Asia/Shanghai', 2, true)
	`, workspaceID, userID, runtimeID, curatorAgentID); err != nil {
		t.Fatal(err)
	}

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	// hourOffset=1 for team_curation: cycleLocal = plan - 1h, so plan hour 3
	// lands on the profile's schedule_hour=2.
	plan := time.Date(2026, 7, 10, 3, 0, 0, 0, loc).UTC()

	pool := singleConnPool(t, 1)
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	start := time.Now()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageTeamCuration, 1)(reqCtx, HandlerInput{PlanTime: plan})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("makeMemoryCurationIntentHandler: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("makeMemoryCurationIntentHandler took %s with a single-connection pool — cursor held open across a second Query()/Exec() acquire (pool deadlock)", elapsed)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var status string
	if err := setupPool.QueryRow(ctx, `
		SELECT status FROM memory_curation_run
		 WHERE workspace_id = $1 AND stage = 'team_curation'
	`, workspaceID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("run status = %q, want queued", status)
	}
}
