package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestRuntimeHandlersRejectMalformedRuntimeID(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{
			name:   "usage",
			method: "GET",
			path:   "/api/runtimes/not-a-uuid/usage",
			handle: testHandler.GetRuntimeUsage,
		},
		{
			name:   "task activity",
			method: "GET",
			path:   "/api/runtimes/not-a-uuid/task-activity",
			handle: testHandler.GetRuntimeTaskActivity,
		},
		{
			name:   "delete",
			method: "DELETE",
			path:   "/api/runtimes/not-a-uuid",
			handle: testHandler.DeleteAgentRuntime,
		},
		{
			name:   "archive-agents-and-delete",
			method: "POST",
			path:   "/api/runtimes/not-a-uuid/archive-agents-and-delete",
			handle: testHandler.ArchiveAgentsAndDeleteRuntime,
		},
		{
			name:   "models",
			method: "POST",
			path:   "/api/runtimes/not-a-uuid/models",
			handle: testHandler.InitiateListModels,
		},
		{
			name:   "update",
			method: "POST",
			path:   "/api/runtimes/not-a-uuid/update",
			handle: testHandler.InitiateUpdate,
		},
		{
			name:   "restart",
			method: "POST",
			path:   "/api/runtimes/not-a-uuid/restart",
			handle: testHandler.InitiateRestart,
		},
		{
			name:   "local skills",
			method: "POST",
			path:   "/api/runtimes/not-a-uuid/local-skills",
			handle: testHandler.InitiateListLocalSkills,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest(tt.method, tt.path, nil)
			req = withURLParam(req, "runtimeId", "not-a-uuid")
			tt.handle(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("%s: expected 400 for malformed runtimeId, got %d: %s", tt.name, w.Code, w.Body.String())
			}
		})
	}
}

// TestGetRuntimeUsage_BucketsByUsageTime ensures a task that was enqueued on
// one calendar day but whose tokens were reported the next day (e.g. execution
// crossed midnight, or the task sat in the queue) is attributed to the day
// tokens were actually produced, not the enqueue day. It also verifies the
// ?days=N cutoff covers the full earliest calendar day, not just "now minus N
// days" which would clip the morning of that day.
func TestGetRuntimeUsage_BucketsByUsageTime(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	const model = "runtime-buckets-by-usage-time"

	// Pick a runtime bound to the fixture workspace.
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent_runtime WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&runtimeID); err != nil {
		t.Fatalf("fetch runtime: %v", err)
	}
	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("fetch agent: %v", err)
	}

	// Create an issue for the tasks to reference.
	var issueID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, creator_id, creator_type)
		VALUES ($1, 'runtime usage test', $2, 'member')
		RETURNING id
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM issue WHERE id = $1`, issueID)
	})

	// enqueued yesterday 23:58 UTC, finished today 00:05 UTC — tokens belong to today.
	now := time.Now().UTC()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	yesterdayLate := today.Add(-2 * time.Minute)
	todayEarly := today.Add(5 * time.Minute)
	// Task that ran entirely yesterday around 05:00 — used to verify the
	// ?days cutoff isn't clipping yesterday's morning.
	yesterdayMorning := today.Add(-19 * time.Hour)

	insertTaskWithUsage := func(enqueueAt, usageAt time.Time, inputTokens int64) string {
		var taskID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
			VALUES ($1, $2, $3, 'acked', $4)
			RETURNING id
		`, agentID, issueID, runtimeID, enqueueAt).Scan(&taskID); err != nil {
			t.Fatalf("insert task: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_execution (
				id, source_kind, source_event_id, source, workspace_id,
				runtime_id, agent_id, issue_id, started_at
			)
			VALUES ($1, 'inbox', $1, 'issue', $2, $3, $4, $5, $6)
		`, taskID, testWorkspaceID, runtimeID, agentID, issueID, enqueueAt); err != nil {
			t.Fatalf("insert task execution: %v", err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO agent_usage (execution_id, provider, model, input_tokens, output_tokens, created_at)
			VALUES ($1, 'claude', $2, $3, 0, $4)
		`, taskID, model, inputTokens, usageAt); err != nil {
			t.Fatalf("insert agent_usage: %v", err)
		}
		t.Cleanup(func() {
			testPool.Exec(ctx, `DELETE FROM agent_execution WHERE id = $1`, taskID)
			testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, taskID)
		})
		return taskID
	}

	insertTaskWithUsage(yesterdayLate, todayEarly, 1000)          // cross-midnight
	insertTaskWithUsage(yesterdayMorning, yesterdayMorning, 2000) // full-day yesterday

	// ListRuntimeUsage reads from agent_usage_hourly,
	// aggregated to per-(date, provider, model) at query time. The
	// window function is idempotent, so re-running this test rewrites
	// the same totals.
	if _, err := testPool.Exec(ctx, `
		SELECT rollup_agent_usage_hourly_window('-infinity'::timestamptz, 'infinity'::timestamptz)
	`); err != nil {
		t.Fatalf("rollup window: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `
			DELETE FROM agent_usage_hourly
			 WHERE runtime_id = $1
			   AND model = $2
		`, runtimeID, model)
	})

	// Call the handler with ?days=1 at whatever "now" is. That should include
	// both today and yesterday in full.
	w := httptest.NewRecorder()
	req := newRequest("GET", "/api/runtimes/"+runtimeID+"/usage?days=1", nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.GetRuntimeUsage(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetRuntimeUsage: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var resp []RuntimeUsageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	byDate := make(map[string]int64)
	for _, r := range resp {
		if r.Model == model {
			byDate[r.Date] += r.InputTokens
		}
	}

	todayKey := today.Format("2006-01-02")
	yesterdayKey := today.Add(-24 * time.Hour).Format("2006-01-02")

	// Cross-midnight task must attribute to today (tu.created_at), not yesterday
	// (atq.created_at). Before the fix this was 0 on today / 1000 on yesterday.
	if byDate[todayKey] != 1000 {
		t.Errorf("cross-midnight task: today bucket expected 1000 input tokens, got %d (full map: %v)", byDate[todayKey], byDate)
	}
	// Yesterday's morning task must still be included — this is what breaks
	// when ?days=N is interpreted as a rolling window instead of calendar days.
	if byDate[yesterdayKey] != 2000 {
		t.Errorf("yesterday morning task: yesterday bucket expected 2000 input tokens, got %d (full map: %v)", byDate[yesterdayKey], byDate)
	}
}

// TestListRuntimeUsageBucketsByViewerTimezone proves the runtime trend reads
// bucket the day boundary in the VIEWER's tz (the argument passed to
// listRuntimeUsage). The viewer tz is Asia/Shanghai; assertions only pass if
// listRuntimeUsage applies that tz correctly.
func TestListRuntimeUsageBucketsByViewerTimezone(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	runtimeID := handlerTestRuntimeID(t)

	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatalf("load location: %v", err)
	}
	cutoff := time.Date(2026, 5, 4, 0, 0, 0, 0, loc)
	cutoffDate := cutoff.Format("2006-01-02")
	extraDate := cutoff.AddDate(0, 0, -1).Format("2006-01-02")

	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM agent_usage_hourly WHERE runtime_id = $1 AND provider = 'cutoff-test'`, runtimeID)
	})

	// Seed agent_usage_hourly directly with one bucket per Shanghai calendar
	// day. Pick 04:00 local (= 20:00 UTC the previous day) to catch
	// off-by-one tz-cutoff bugs.
	var agentID pgtype.UUID
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM agent WHERE workspace_id = $1 ORDER BY id LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("pick fixture agent: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_usage_hourly (
			bucket_hour, workspace_id, runtime_id, agent_id, project_id,
			provider, model,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, event_count
		)
		VALUES
			(($1::date + interval '4 hours') AT TIME ZONE 'Asia/Shanghai', $3, $4, $5, NULL,
				'cutoff-test', 'old-day',    111, 0, 0, 0, 1),
			(($2::date + interval '4 hours') AT TIME ZONE 'Asia/Shanghai', $3, $4, $5, NULL,
				'cutoff-test', 'cutoff-day', 222, 0, 0, 0, 1)
		ON CONFLICT ON CONSTRAINT uq_agent_usage_hourly_key DO UPDATE
			SET input_tokens = EXCLUDED.input_tokens,
			    output_tokens = EXCLUDED.output_tokens,
			    cache_read_tokens = EXCLUDED.cache_read_tokens,
			    cache_write_tokens = EXCLUDED.cache_write_tokens,
			    event_count = EXCLUDED.event_count
	`, extraDate, cutoffDate, testWorkspaceID, runtimeID, agentID); err != nil {
		t.Fatalf("seed hourly rows: %v", err)
	}

	resp, err := testHandler.listRuntimeUsage(ctx, parseUUID(runtimeID), "Asia/Shanghai", pgtype.Timestamptz{
		Time:  cutoff,
		Valid: true,
	})
	if err != nil {
		t.Fatalf("listRuntimeUsage: %v", err)
	}
	byDate := make(map[string]int64)
	for _, row := range resp {
		if row.Provider == "cutoff-test" {
			byDate[row.Date] += row.InputTokens
		}
	}
	if byDate[cutoffDate] != 222 {
		t.Fatalf("expected cutoff date %s to be included with 222 tokens, got map %v", cutoffDate, byDate)
	}
	if byDate[extraDate] != 0 {
		t.Fatalf("expected extra date %s to be excluded, got map %v", extraDate, byDate)
	}
}

// TestResolveViewingTZ covers the three legs of resolveViewingTZ:
// explicit `?tz=` query param, the authenticated user's stored
// user.timezone, and the UTC fallback when neither yields a valid zone.
func TestResolveViewingTZ(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var userID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email, timezone)
		 VALUES ('TZ Resolve', 'tz-resolve@multica.ai', 'Asia/Tokyo') RETURNING id`,
	).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, userID) })

	// Explicit ?tz= wins over the stored preference.
	req := newRequest("GET", "/api/dashboard/usage/daily?tz=America/New_York", nil)
	req.Header.Set("X-User-ID", userID)
	if got := testHandler.resolveViewingTZ(req); got != "America/New_York" {
		t.Fatalf("query param: expected America/New_York, got %q", got)
	}

	// No ?tz= → falls back to the authenticated user's stored timezone.
	req = newRequest("GET", "/api/dashboard/usage/daily", nil)
	req.Header.Set("X-User-ID", userID)
	if got := testHandler.resolveViewingTZ(req); got != "Asia/Tokyo" {
		t.Fatalf("stored fallback: expected Asia/Tokyo, got %q", got)
	}

	// An unparseable ?tz= is ignored, falling through to the stored value.
	req = newRequest("GET", "/api/dashboard/usage/daily?tz=Mars/Olympus", nil)
	req.Header.Set("X-User-ID", userID)
	if got := testHandler.resolveViewingTZ(req); got != "Asia/Tokyo" {
		t.Fatalf("invalid query param: expected Asia/Tokyo fallback, got %q", got)
	}

	// No ?tz= and no stored value → UTC.
	var bareUserID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email)
		 VALUES ('TZ Bare', 'tz-bare@multica.ai') RETURNING id`,
	).Scan(&bareUserID); err != nil {
		t.Fatalf("insert bare user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, bareUserID) })
	req = newRequest("GET", "/api/dashboard/usage/daily", nil)
	req.Header.Set("X-User-ID", bareUserID)
	if got := testHandler.resolveViewingTZ(req); got != "UTC" {
		t.Fatalf("no preference: expected UTC, got %q", got)
	}

	// A stored timezone that is itself an invalid IANA zone — e.g. a row
	// written before server-side validation existed — must fall through
	// to UTC. Without the LoadLocation guard on the stored value this
	// string would reach SQL `AT TIME ZONE` and 500 every dashboard read.
	var badTZUserID string
	if err := testPool.QueryRow(ctx,
		`INSERT INTO "user" (name, email, timezone)
		 VALUES ('TZ Bad', 'tz-bad@multica.ai', 'Bad/Zone') RETURNING id`,
	).Scan(&badTZUserID); err != nil {
		t.Fatalf("insert bad-tz user: %v", err)
	}
	t.Cleanup(func() { testPool.Exec(ctx, `DELETE FROM "user" WHERE id = $1`, badTZUserID) })
	req = newRequest("GET", "/api/dashboard/usage/daily", nil)
	req.Header.Set("X-User-ID", badTZUserID)
	if got := testHandler.resolveViewingTZ(req); got != "UTC" {
		t.Fatalf("invalid stored tz: expected UTC fallback, got %q", got)
	}

	// No X-User-ID header and no ?tz= — an unauthenticated caller has
	// neither signal, so the resolver must return UTC without attempting
	// (and panicking on) a GetUser lookup.
	req = newRequest("GET", "/api/dashboard/usage/daily", nil)
	if got := testHandler.resolveViewingTZ(req); got != "UTC" {
		t.Fatalf("unauthenticated caller: expected UTC, got %q", got)
	}
}

// TestRuntimeHeatmapEndpointsUseViewerTZ verifies that the hour-of-day
// heatmap endpoints (GetRuntimeUsageByHour and GetRuntimeTaskActivity) bucket
// in the viewer's tz supplied via ?tz= and return 200.
func TestRuntimeHeatmapEndpointsUseViewerTZ(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := handlerTestRuntimeID(t)

	cases := []struct {
		name   string
		path   string
		handle func(http.ResponseWriter, *http.Request)
	}{
		{"usage by-hour", "/api/runtimes/" + runtimeID + "/usage/by-hour?tz=Asia/Shanghai", testHandler.GetRuntimeUsageByHour},
		{"task activity", "/api/runtimes/" + runtimeID + "/activity?tz=Asia/Shanghai", testHandler.GetRuntimeTaskActivity},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := newRequest("GET", c.path, nil)
			req = withURLParam(req, "runtimeId", runtimeID)
			c.handle(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("%s: expected 200, got %d: %s", c.name, w.Code, w.Body.String())
			}
		})
	}
}

// Task #53: deriveRuntimeHealth previously trusted the raw agent_runtime.status
// column directly, which can read "online" for up to ~180s after the runtime
// actually went silent (sweeper lag) — the exact same bug #1664 already fixed
// for the agent list/detail display, and #50 fixed for the queued-task TTL.
// This was a real, user-visible inconsistency: the runtime detail page's
// health badge and the agent list's runtime_display_status could disagree
// about whether the same runtime was reachable, because one read the raw
// column and the other used runtimeConnectivity's freshness check.
//
// TestDeriveRuntimeHealthUsesHeartbeatFreshnessNotRawStatus is the regression
// test for that gap: a runtime whose status column still says "online" but
// whose heartbeat is stale must report health "offline", not "ok".
func TestDeriveRuntimeHealthUsesHeartbeatFreshnessNotRawStatus(t *testing.T) {
	now := time.Now()

	staleOnline := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Minute)), // well past the 150s/5min thresholds
		UpdatedAt:  pgtimestamptz(now.Add(-9 * time.Minute)),
	}
	if got := deriveRuntimeHealth(staleOnline, nil, nil, "idle", nil, now); got != "offline" {
		t.Fatalf("stale-online runtime (status column lying): health = %q, want %q (must key off heartbeat freshness, not the raw status column)", got, "offline")
	}

	freshOnline := db.AgentRuntime{
		Status:     "online",
		LastSeenAt: pgtimestamptz(now.Add(-5 * time.Second)),
		UpdatedAt:  pgtimestamptz(now.Add(-5 * time.Second)),
	}
	if got := deriveRuntimeHealth(freshOnline, nil, nil, "idle", nil, now); got != "ok" {
		t.Fatalf("fresh-online runtime: health = %q, want %q", got, "ok")
	}

	explicitOffline := db.AgentRuntime{
		Status:     "offline",
		LastSeenAt: pgtimestamptz(now.Add(-10 * time.Minute)),
		UpdatedAt:  pgtimestamptz(now.Add(-9 * time.Minute)),
	}
	if got := deriveRuntimeHealth(explicitOffline, nil, nil, "idle", nil, now); got != "offline" {
		t.Fatalf("explicitly-offline runtime: health = %q, want %q", got, "offline")
	}
}

// TestRuntimeShouldFetchLatestReleaseUsesHeartbeatFreshness is the same
// regression, for the sibling function that gates whether the server bothers
// checking for a newer CLI release at all.
func TestRuntimeShouldFetchLatestReleaseUsesHeartbeatFreshness(t *testing.T) {
	now := time.Now()
	version := "0.3.90"

	staleOnline := db.AgentRuntime{
		Status:      "online",
		RuntimeMode: "local",
		LastSeenAt:  pgtimestamptz(now.Add(-10 * time.Minute)),
		UpdatedAt:   pgtimestamptz(now.Add(-9 * time.Minute)),
	}
	if got := runtimeShouldFetchLatestRelease(staleOnline, nil, &version, nil, "idle", now); got {
		t.Fatalf("stale-online runtime: runtimeShouldFetchLatestRelease = true, want false (must not treat a stale heartbeat as reachable)")
	}

	freshOnline := db.AgentRuntime{
		Status:      "online",
		RuntimeMode: "local",
		LastSeenAt:  pgtimestamptz(now.Add(-5 * time.Second)),
		UpdatedAt:   pgtimestamptz(now.Add(-5 * time.Second)),
	}
	if got := runtimeShouldFetchLatestRelease(freshOnline, nil, &version, nil, "idle", now); !got {
		t.Fatalf("fresh-online local runtime: runtimeShouldFetchLatestRelease = false, want true")
	}
}
