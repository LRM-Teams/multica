package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/memorycuration"
)

// Task #53: scheduleDefaultAgentSelfReviewRuns joined on rt.status = 'online'
// to decide both the parent run status and whether to queue or skip each
// child. agent_runtime.status can read "online" for up to ~180s after the
// runtime actually went silent (sweeper lag), so a stale-but-status-online
// runtime would wrongly get queued instead of skipped.
func TestMemoryCurationSchedulerDefaultSelfReviewSkipsStaleHeartbeat(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, targetAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Stale Self Review "+suffix, "stale-self-review-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Stale Self Review "+suffix, "stale-self-review-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	// status stays 'online'; only last_seen_at is stale. If the scheduler
	// still trusts the status column, this runtime looks reachable.
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at, updated_at)
		VALUES ($1, $2, 'Stale Self Review Runtime', 'local', 'codex', 'online', 'test', $3, now() - interval '10 minutes', now() - interval '9 minutes')
		RETURNING id::text
	`, workspaceID, "stale-self-review-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "stale_self_review_target_"+suffix, runtimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Stale self-review activity', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'acked', '2026-07-09 12:00:00+00')
	`, targetAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	plan := time.Date(2026, 7, 10, defaultAgentSelfReviewScheduleHour, 0, 0, 0, loc).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageAgentSelfReview, 0)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var runID, status string
	if err := pool.QueryRow(ctx, `
		SELECT id::text, status FROM memory_curation_run
		 WHERE workspace_id = $1 AND profile_id IS NULL AND stage = 'agent_self_review'
	`, workspaceID).Scan(&runID, &status); err != nil {
		t.Fatal(err)
	}
	if status != "waiting_runtime" {
		t.Fatalf("stale-heartbeat runtime (status column still 'online'): run status = %q, want %q (must key off heartbeat freshness, not the raw status column)", status, "waiting_runtime")
	}
	var childStatus string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM memory_curation_agent_run
		 WHERE parent_run_id = $1 AND agent_id = $2 AND runtime_id = $3
	`, runID, targetAgentID, runtimeID).Scan(&childStatus); err != nil {
		t.Fatal(err)
	}
	if childStatus != "skipped" {
		t.Fatalf("stale-heartbeat runtime (status column still 'online'): child status = %q, want %q (must key off heartbeat freshness, not the raw status column)", childStatus, "skipped")
	}
}

// Task #53: activeMemoryCurationAgentIDs joined on rt.status = 'online' to
// decide which agents are "active curation targets" for a given day. A
// stale-heartbeat runtime whose status column still says "online" would
// wrongly make its agent eligible.
func TestActiveMemoryCurationAgentIDsExcludesStaleHeartbeat(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, runtimeID, targetAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Stale Active Targets "+suffix, "stale-active-targets-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Stale Active Targets "+suffix, "stale-active-targets-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at, updated_at)
		VALUES ($1, $2, 'Stale Active Targets Runtime', 'local', 'codex', 'online', 'test', $3, now() - interval '10 minutes', now() - interval '9 minutes')
		RETURNING id::text
	`, workspaceID, "stale-active-targets-"+suffix, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "stale_active_targets_agent_"+suffix, runtimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	day := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Stale active targets activity', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at, completed_at)
		VALUES ($1, $2, $3, 'acked', $4, $4)
	`, targetAgentID, issueID, runtimeID, day.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}

	ids, err := activeMemoryCurationAgentIDs(ctx, pool, workspaceID, userID, "owned_all", "00000000-0000-0000-0000-000000000000", day, day.AddDate(0, 0, 1))
	if err != nil {
		t.Fatalf("activeMemoryCurationAgentIDs: %v", err)
	}
	for _, id := range ids {
		if id == targetAgentID {
			t.Fatalf("stale-heartbeat runtime (status column still 'online'): agent %s returned as active target, want excluded (must key off heartbeat freshness, not the raw status column)", targetAgentID)
		}
	}
}

// Task #53: the profile-driven scheduling path (used for team_curation, and
// for agent_self_review when the memory_curation_agent_run table is
// unavailable) read the curator's own runtime.status to decide whether the
// created run is immediately 'queued' or 'waiting_runtime'. A stale-heartbeat
// curator runtime whose status column still says "online" would wrongly
// produce 'queued'.
func TestMemoryCurationSchedulerProfileDrivenRunIsWaitingRuntimeForStaleHeartbeat(t *testing.T) {
	pool := integrationPool(t)
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	var userID, workspaceID, curatorRuntimeID, targetRuntimeID, curatorAgentID, targetAgentID string
	if err := pool.QueryRow(ctx, `INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id::text`, "Stale Profile Driven "+suffix, "stale-profile-driven-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO workspace (name, slug) VALUES ($1, $2) RETURNING id::text`, "Stale Profile Driven "+suffix, "stale-profile-driven-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := pool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	// Curator runtime: status column still says 'online', but heartbeat is
	// stale. This is the runtime under test.
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at, updated_at)
		VALUES ($1, $2, 'Stale Profile Driven Curator Runtime', 'local', 'codex', 'online', 'test', $3, now() - interval '10 minutes', now() - interval '9 minutes')
		RETURNING id::text
	`, workspaceID, "stale-profile-driven-curator-"+suffix, userID).Scan(&curatorRuntimeID); err != nil {
		t.Fatal(err)
	}
	// Target runtime: fresh, so the target agent is a valid active target and
	// this test isolates the curator-runtime freshness check under test.
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at)
		VALUES ($1, $2, 'Stale Profile Driven Target Runtime', 'local', 'codex', 'online', 'test', $3, now())
		RETURNING id::text
	`, workspaceID, "stale-profile-driven-target-"+suffix, userID).Scan(&targetRuntimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "stale_profile_driven_curator_"+suffix, curatorRuntimeID, userID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, $2, 'local', $3, $4, 'composer-1.5') RETURNING id::text
	`, workspaceID, "stale_profile_driven_target_"+suffix, targetRuntimeID, userID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id)
		VALUES ($1, 'Stale profile-driven activity', 'todo', 'none', 'member', $2)
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	var issueID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM issue WHERE workspace_id = $1 ORDER BY created_at DESC LIMIT 1`, workspaceID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'acked', '2026-07-09 12:00:00+00')
	`, targetAgentID, issueID, targetRuntimeID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO memory_curator_profile (
		  workspace_id, user_id, enabled, self_review_enabled, team_curation_enabled,
		  mode, runtime_id, curator_agent_id, target_scope, timezone, schedule_hour, catch_up_enabled
		) VALUES ($1, $2, true, false, true, 'review', $3, $4, 'owned_all', 'Asia/Shanghai', 2, true)
	`, workspaceID, userID, curatorRuntimeID, curatorAgentID); err != nil {
		t.Fatal(err)
	}

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	// hourOffset=1 for team_curation: cycleLocal = plan - 1h, so plan hour 3
	// lands on the profile's schedule_hour=2.
	plan := time.Date(2026, 7, 10, 3, 0, 0, 0, loc).UTC()
	res, err := makeMemoryCurationIntentHandler(pool, memorycuration.StageTeamCuration, 1)(ctx, HandlerInput{PlanTime: plan})
	if err != nil {
		t.Fatal(err)
	}
	if res.RowsAffected != 1 {
		t.Fatalf("RowsAffected = %d, want 1 (%#v)", res.RowsAffected, res.Result)
	}
	var status string
	if err := pool.QueryRow(ctx, `
		SELECT status FROM memory_curation_run
		 WHERE workspace_id = $1 AND stage = 'team_curation'
	`, workspaceID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "waiting_runtime" {
		t.Fatalf("stale-heartbeat curator runtime (status column still 'online'): run status = %q, want %q (must key off heartbeat freshness, not the raw status column)", status, "waiting_runtime")
	}
}
