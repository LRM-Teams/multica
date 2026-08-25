package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Task #53: insertMemoryCurationAgentRuns trusted agent_runtime.status
// directly (rt.status = 'online'), which can read "online" for up to ~180s
// after the runtime actually went silent (sweeper lag). This would queue
// self-review work against a runtime that's actually unreachable instead of
// correctly marking the run 'skipped' the way the check intends.
func TestInsertMemoryCurationAgentRuns_StaleHeartbeatSkipsNotQueues(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()

	var staleRuntimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at, updated_at) VALUES ($1,  'memory-curation-insert-stale-daemon',  'Memory Curation Insert Stale Runtime',  'local',  'codex',  'online', 
		          '',  '{}'::jsonb,  'private',  now() - interval '10 minutes',  now() - interval '9 minutes')
		RETURNING id::text
	`, testWorkspaceID).Scan(&staleRuntimeID); err != nil {
		t.Fatal(err)
	}

	bindTestRuntimeOwner(t, "memory-curation-insert-stale-daemon", testUserID)

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory-curation-insert-stale-agent', 'Memory Curation Insert Stale Agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	var runID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to, runtime_id, execution_owner
		) VALUES ($1, 'agent_self_review', 'manual', 'running', '2026-08-01', '2026-08-01', $2, 'daemon')
		RETURNING id::text
	`, testWorkspaceID, staleRuntimeID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_agent_run WHERE parent_run_id = $1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = $1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, staleRuntimeID)
	})

	if err := insertMemoryCurationAgentRuns(ctx, testPool, runID, testWorkspaceID, staleRuntimeID, []string{agentID}); err != nil {
		t.Fatalf("insertMemoryCurationAgentRuns: %v", err)
	}

	var status, errMsg string
	if err := testPool.QueryRow(ctx, `
		SELECT status, error FROM memory_curation_agent_run WHERE parent_run_id = $1 AND agent_id = $2
	`, runID, agentID).Scan(&status, &errMsg); err != nil {
		t.Fatalf("read inserted run: %v", err)
	}
	if status != "skipped" {
		t.Fatalf("stale-heartbeat runtime (status column still 'online'): agent run status = %q, want %q (must key off heartbeat freshness, not the raw status column); error=%q", status, "skipped", errMsg)
	}
}

func TestResolveMemoryCurationBackfillRangeDefaultsToOneMonth(t *testing.T) {
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	since, until, err := resolveMemoryCurationBackfillRange("", "", "Asia/Shanghai", now)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := formatDateUTC(until), "2026-07-21"; got != want {
		t.Fatalf("until = %s, want %s", got, want)
	}
	if got, want := formatDateUTC(since), "2026-06-22"; got != want {
		t.Fatalf("since = %s, want %s", got, want)
	}
}

func TestResolveMemoryCurationBackfillRangeRejectsOverThirtyDays(t *testing.T) {
	_, _, err := resolveMemoryCurationBackfillRange("2026-06-01", "2026-07-01", "UTC", time.Now())
	if err == nil {
		t.Fatal("expected error for >30 day range")
	}
}

func TestResolveMemoryCurationBackfillRangeAllowsThirtyInclusiveDays(t *testing.T) {
	since, until, err := resolveMemoryCurationBackfillRange("2026-06-22", "2026-07-21", "UTC", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if formatDateUTC(since) != "2026-06-22" || formatDateUTC(until) != "2026-07-21" {
		t.Fatalf("range = %s..%s", formatDateUTC(since), formatDateUTC(until))
	}
}

func TestDefaultMemoryCurationPlanDayUsesBeijingYesterday(t *testing.T) {
	// 2026-07-24 01:00 UTC == 2026-07-24 09:00 Asia/Shanghai → yesterday 2026-07-23
	now := time.Date(2026, 7, 24, 1, 0, 0, 0, time.UTC)
	since, until := defaultMemoryCurationPlanDay("Asia/Shanghai", now)
	if formatDateUTC(since) != "2026-07-23" || formatDateUTC(until) != "2026-07-23" {
		t.Fatalf("plan day = %s..%s, want 2026-07-23", formatDateUTC(since), formatDateUTC(until))
	}
	// Near UTC midnight still uses local yesterday.
	now = time.Date(2026, 7, 24, 16, 0, 0, 0, time.UTC) // 2026-07-25 00:00 CST
	since, until = defaultMemoryCurationPlanDay("Asia/Shanghai", now)
	if formatDateUTC(since) != "2026-07-24" || formatDateUTC(until) != "2026-07-24" {
		t.Fatalf("plan day after local midnight = %s..%s, want 2026-07-24", formatDateUTC(since), formatDateUTC(until))
	}
}

func TestSucceededCurationDaysMarksAllAsBoth(t *testing.T) {
	days := succeededCurationDays{
		self: map[string]struct{}{"2026-07-14": {}},
		team: map[string]struct{}{"2026-07-14": {}},
	}
	if !days.hasSelf("2026-07-14") || !days.hasTeam("2026-07-14") {
		t.Fatal("all success should cover both stages")
	}
	if days.hasSelf("2026-07-15") || days.hasTeam("2026-07-15") {
		t.Fatal("missing day should not be marked succeeded")
	}
}

func TestMemoryCurationBackfillSkipsIdleAndSucceededDays(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, visibility, last_seen_at) VALUES ($1,  'memory-curator-backfill-daemon',  'Memory Curator Backfill Runtime',  'local',  'cursor',  'online', 
		          'memory curator backfill',  jsonb_build_object('capabilities', jsonb_build_array($2::text)),  'private',  now())
		RETURNING id::text
	`, testWorkspaceID, protocol.DaemonCapabilityMemoryCuration).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}

	bindTestRuntimeOwner(t, "memory-curator-backfill-daemon", testUserID)

	var curatorAgentID, targetAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, instructions, model)
		VALUES ($1, 'memory_curator_backfill', 'Memory Curator Backfill', 'local', $2, $3, 'Keep durable facts concise.', 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, testUserID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory_target_backfill', 'Memory Target Backfill', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, testUserID).Scan(&targetAgentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE runtime_id = $1`, runtimeID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curator_profile WHERE runtime_id = $1`, runtimeID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = ANY($1::uuid[])`, []string{curatorAgentID, targetAgentID})
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	w := httptest.NewRecorder()
	profileReq := withURLParam(newRequest(http.MethodPut, "/api/workspaces/"+testWorkspaceID+"/memory-curation/profile", map[string]any{
		"enabled": true, "self_review_enabled": true, "team_curation_enabled": true,
		"mode": "auto_safe", "runtime_id": runtimeID,
		"curator_agent_id": curatorAgentID, "target_scope": "selected",
		"target_agent_ids": []string{targetAgentID}, "timezone": "Asia/Shanghai",
		"schedule_hour": 1, "catch_up_enabled": true, "confidence_threshold": 0.8,
	}), "id", testWorkspaceID)
	testHandler.UpdateMemoryCuratorProfile(w, profileReq)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateMemoryCuratorProfile: status=%d body=%s", w.Code, w.Body.String())
	}
	var profile memoryCuratorProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}

	activeDay := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	succeededDay := time.Date(2026, 7, 19, 0, 0, 0, 0, time.UTC)
	var issueID string
	if err := testPool.QueryRow(ctx, `
		WITH bumped AS (
			UPDATE workspace SET issue_counter = issue_counter + 1
			WHERE id = $1 RETURNING issue_counter
		)
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		SELECT $1, 'memory curation backfill active', 'todo', 'none', 'member', $2, bumped.issue_counter
		FROM bumped
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at, completed_at)
		VALUES ($1, $2, $3, 'acked', $4, $4)
	`, targetAgentID, issueID, runtimeID, activeDay.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at, completed_at)
		VALUES ($1, $2, $3, 'acked', $4, $4)
	`, targetAgentID, issueID, runtimeID, succeededDay.Add(12*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to,
		  profile_id, owner_user_id, runtime_id, curator_agent_id, target_agent_ids, execution_owner
		) VALUES ($1, 'all', 'manual', 'succeeded', $2, $2, $3, $4, $5, $6, ARRAY[$7]::uuid[], 'daemon')
	`, testWorkspaceID, succeededDay, profile.ID, testUserID, runtimeID, curatorAgentID, targetAgentID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	w = httptest.NewRecorder()
	backfillReq := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory-curation/backfill", map[string]any{
		"since": "2026-07-17", "until": "2026-07-19", "dry_run": false,
	}), "id", testWorkspaceID)
	testHandler.StartMemoryCurationBackfill(w, backfillReq)
	if w.Code != http.StatusAccepted && w.Code != http.StatusOK {
		t.Fatalf("StartMemoryCurationBackfill: status=%d body=%s", w.Code, w.Body.String())
	}
	var body memoryCurationBackfillResponse
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.QueuedDays != 1 || len(body.Queued) != 1 || body.Queued[0].Date != "2026-07-18" || body.Queued[0].Stage != "all" {
		t.Fatalf("queued = %+v", body.Queued)
	}
	skipReasons := map[string]string{}
	for _, skip := range body.Skipped {
		skipReasons[skip.Date] = skip.Reason
	}
	if skipReasons["2026-07-17"] != "no_activity" {
		t.Fatalf("skip 2026-07-17 = %q, want no_activity; body=%+v", skipReasons["2026-07-17"], body.Skipped)
	}
	if skipReasons["2026-07-19"] != "already_succeeded" {
		t.Fatalf("skip 2026-07-19 = %q, want already_succeeded; body=%+v", skipReasons["2026-07-19"], body.Skipped)
	}
	if body.Queued[0].RunID == "" || body.Queued[0].Status != "queued" {
		t.Fatalf("queued run = %+v", body.Queued[0])
	}
}
