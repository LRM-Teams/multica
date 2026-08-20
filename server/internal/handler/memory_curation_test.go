package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestParseUniqueAgentIDsRejectsInvalidUUID(t *testing.T) {
	w := httptest.NewRecorder()
	if _, ok := parseUniqueAgentIDsOrBadRequest(w, []string{"not-a-uuid"}); ok {
		t.Fatal("parseUniqueAgentIDsOrBadRequest ok = true, want false")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestAgentIDsBelongToWorkspaceRejectsForeignAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ('Memory Curation Foreign', 'memory-curation-foreign-' || substr(gen_random_uuid()::text, 1, 8))
		RETURNING id
	`).Scan(&otherWorkspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, device_info, owner_id)
		VALUES ($1, 'foreign memory runtime', 'local', 'legacy_local', 'foreign memory runtime', $2)
		RETURNING id
	`, otherWorkspaceID, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}

	var foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'foreign memory curation agent', 'local', $2, $3, 'composer-1.5')
		RETURNING id
	`, otherWorkspaceID, runtimeID, testUserID).Scan(&foreignAgentID); err != nil {
		t.Fatal(err)
	}

	ok, err := testHandler.agentIDsBelongToWorkspace(ctx, testWorkspaceID, []string{foreignAgentID})
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("agentIDsBelongToWorkspace ok = true, want false for foreign agent")
	}
}

func TestGetMemoryCurationRunRejectsInvalidRunID(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test database unavailable")
	}
	w := httptest.NewRecorder()
	req := withRouteParams(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory-curation/runs/not-a-uuid", nil), "id", testWorkspaceID, "runId", "not-a-uuid")
	testHandler.GetMemoryCurationRun(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestGetAgentMemoryCurationStatusRejectsInvalidAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test database unavailable")
	}
	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/agents/not-a-uuid/memory-curation/status", nil), "id", "not-a-uuid")
	testHandler.GetAgentMemoryCurationStatus(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestPublicMemoryCurationStatsProjectsSharedPromotionCounts(t *testing.T) {
	raw, err := json.Marshal(map[string]any{
		"agents_scanned":           3,
		"entries_promoted":         4,
		"shared_candidates_added":  2,
		"shared_candidates_synced": 1,
		"errors": []map[string]any{{
			"workspace_id": "workspace-1",
			"agent_id":     "agent-1",
			"stage":        "l3",
			"error":        "failed",
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	stats := publicMemoryCurationStats(raw)
	if stats.AgentsScanned != 3 || stats.EntriesPromoted != 4 {
		t.Fatalf("stats = %+v, want scanned=3 promoted=4", stats)
	}
	if stats.SharedCandidatesAdded != 2 || stats.SharedCandidatesSynced != 1 {
		t.Fatalf("stats = %+v, want shared added=2 synced=1", stats)
	}
	if stats.ErrorCount != 1 {
		t.Fatalf("error_count = %d, want 1", stats.ErrorCount)
	}
}

func TestPublicMemoryCurationStatsHandlesMalformedPayload(t *testing.T) {
	if got := publicMemoryCurationStats([]byte("not-json")); got != (memoryCurationRunStatsResponse{}) {
		t.Fatalf("stats = %+v, want zero value", got)
	}
}

func TestBuildMemoryCurationTimelineKeepsUnfinishedInvocationRunning(t *testing.T) {
	createdAt := time.Date(2026, 7, 19, 1, 0, 0, 0, time.UTC)
	startedAt := createdAt.Add(2 * time.Minute)
	items := buildMemoryCurationTimeline(memoryCurationRunResponse{
		Status:           "running",
		TriggerKind:      "scheduled",
		RuntimeName:      "runtime-89",
		CuratorAgentName: "xiao-lin",
		TargetAgentIDs:   []string{"agent-1"},
	}, []byte(`{}`), createdAt, &startedAt, nil)
	if len(items) != 3 {
		t.Fatalf("timeline length = %d, want 3: %+v", len(items), items)
	}
	if items[2].Key != "invoked_curator" || items[2].Status != "running" {
		t.Fatalf("invoked curator item = %+v, want running", items[2])
	}
	for _, item := range items {
		if item.Key == "validated_profile" || item.Key == "resolved_targets" {
			t.Fatalf("unfinished run should not synthesize completed step %+v", item)
		}
	}
}

func TestMemoryCurationClaimFailsExhaustedStaleRunAndClaimsNext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, owner_id, visibility, last_seen_at
		) VALUES ($1, 'memory-curator-stale-daemon', 'Memory Curator Stale Runtime', 'local', 'codex', 'online',
		          'memory curator stale test', jsonb_build_object('capabilities', jsonb_build_array($2::text)), $3, 'private', now())
		RETURNING id::text
	`, testWorkspaceID, protocol.DaemonCapabilityMemoryCuration, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	var staleRunID, nextRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to,
		  runtime_id, execution_owner, attempt, claimed_at, started_at
		) VALUES ($1, 'agent_self_review', 'scheduled', 'running', '2026-07-18', '2026-07-18',
		          $2, 'daemon', $3, now() - interval '20 minutes', now())
		RETURNING id::text
	`, testWorkspaceID, runtimeID, memoryCurationMaxAttempts).Scan(&staleRunID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to,
		  runtime_id, execution_owner
		) VALUES ($1, 'team_curation', 'scheduled', 'queued', '2026-07-18', '2026-07-18', $2, 'daemon')
		RETURNING id::text
	`, testWorkspaceID, runtimeID).Scan(&nextRunID); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = ANY($1::uuid[])`, []string{staleRunID, nextRunID})
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	}()

	rt, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := testHandler.claimPendingMemoryCurationRun(ctx, rt, "")
	if err != nil {
		t.Fatal(err)
	}
	if pending == nil || pending.ID != nextRunID {
		t.Fatalf("pending = %+v, want next run %s", pending, nextRunID)
	}
	var staleStatus, staleError string
	if err := testPool.QueryRow(ctx, `SELECT status, error FROM memory_curation_run WHERE id = $1`, staleRunID).Scan(&staleStatus, &staleError); err != nil {
		t.Fatal(err)
	}
	if staleStatus != "failed" || !strings.Contains(staleError, "max daemon claim attempts") {
		t.Fatalf("stale run status/error = %q/%q", staleStatus, staleError)
	}
}

func TestMemoryCurationActiveHeartbeatRefreshesBeforeExpirySweep(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, owner_id, visibility, last_seen_at
		) VALUES ($1, 'memory-curator-active-hb-daemon', 'Memory Curator Active HB Runtime', 'local', 'codex', 'online',
		          'memory curator active heartbeat', jsonb_build_object('capabilities', jsonb_build_array($2::text)), $3, 'private', now())
		RETURNING id::text
	`, testWorkspaceID, protocol.DaemonCapabilityMemoryCuration, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	var parentRunID, agentRunID, agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory_curator_active_hb', 'Memory Curator Active HB', 'local', $2, $3, 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to,
		  runtime_id, execution_owner, attempt, claimed_at, started_at
		) VALUES ($1, 'all', 'manual', 'running', '2026-07-18', '2026-07-18',
		          $2, 'daemon', $3, now() - interval '20 minutes', now() - interval '5 minutes')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, memoryCurationMaxAttempts).Scan(&parentRunID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_agent_run (
		  parent_run_id, workspace_id, agent_id, stage, status, runtime_id,
		  attempt, claimed_at, started_at
		) VALUES ($1, $2, $3, 'agent_self_review', 'running', $4,
		          $5, now() - interval '20 minutes', now() - interval '5 minutes')
		RETURNING id::text
	`, parentRunID, testWorkspaceID, agentID, runtimeID, memoryCurationMaxAttempts).Scan(&agentRunID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_agent_run WHERE id = $1`, agentRunID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = $1`, parentRunID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	rt, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatal(err)
	}
	pending, err := testHandler.claimPendingMemoryCurationRun(ctx, rt, agentRunID)
	if err != nil {
		t.Fatal(err)
	}
	if pending != nil {
		t.Fatalf("pending = %+v, want nil while active claim is refreshing", pending)
	}

	var parentStatus, agentStatus string
	var parentClaimedAge, agentClaimedAge float64
	if err := testPool.QueryRow(ctx, `
		SELECT r.status, extract(epoch from (now() - r.claimed_at)),
		       cr.status, extract(epoch from (now() - cr.claimed_at))
		  FROM memory_curation_run r
		  JOIN memory_curation_agent_run cr ON cr.parent_run_id = r.id
		 WHERE r.id = $1 AND cr.id = $2
	`, parentRunID, agentRunID).Scan(&parentStatus, &parentClaimedAge, &agentStatus, &agentClaimedAge); err != nil {
		t.Fatal(err)
	}
	if parentStatus != "running" || agentStatus != "running" {
		t.Fatalf("status parent/agent = %q/%q, want both running", parentStatus, agentStatus)
	}
	if parentClaimedAge > 5 || agentClaimedAge > 5 {
		t.Fatalf("claimed ages parent/agent = %.1f/%.1f, want refreshed near now", parentClaimedAge, agentClaimedAge)
	}
}

func TestWorkspaceMemoryCurationStatusSweepsExpiredRunningRuns(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, owner_id, visibility, last_seen_at
		) VALUES ($1, 'memory-curator-status-daemon', 'Memory Curator Status Runtime', 'local', 'codex', 'online',
		          'memory curator status sweep', jsonb_build_object('capabilities', jsonb_build_array($2::text)), $3, 'private', now())
		RETURNING id::text
	`, testWorkspaceID, protocol.DaemonCapabilityMemoryCuration, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	var staleRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (
		  workspace_id, stage, trigger_kind, status, date_from, date_to,
		  runtime_id, execution_owner, attempt, claimed_at, started_at
		) VALUES ($1, 'all', 'manual', 'running', '2026-07-20', '2026-07-20',
		          $2, 'daemon', $3, now() - interval '70 minutes', now() - interval '70 minutes')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, memoryCurationMaxAttempts).Scan(&staleRunID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = $1`, staleRunID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+testWorkspaceID+"/memory-curation/status", nil), "id", testWorkspaceID)
	testHandler.GetWorkspaceMemoryCurationStatus(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GetWorkspaceMemoryCurationStatus: status=%d body=%s", w.Code, w.Body.String())
	}

	var status, errText string
	if err := testPool.QueryRow(ctx, `SELECT status, error FROM memory_curation_run WHERE id = $1`, staleRunID).Scan(&status, &errText); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || !strings.Contains(errText, "exceeded server max runtime") {
		t.Fatalf("stale run status/error = %q/%q", status, errText)
	}
	var body struct {
		PendingRuns int `json:"pending_runs"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.PendingRuns != 0 {
		t.Fatalf("pending_runs = %d, want 0 after sweep", body.PendingRuns)
	}
}

func TestMemoryCuratorProfileQueuesAndCompletesDaemonRun(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider, status,
		  device_info, metadata, owner_id, visibility, last_seen_at
		) VALUES ($1, 'memory-curator-test-daemon', 'Memory Curator Test Runtime', 'local', 'codex', 'online',
		          'memory curator test', jsonb_build_object('capabilities', jsonb_build_array($2::text)), $3, 'private', now())
		RETURNING id::text
	`, testWorkspaceID, protocol.DaemonCapabilityMemoryCuration, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	var curatorAgentID, targetAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, instructions, model)
		VALUES ($1, 'memory_curator_test', 'Memory Curator Test', 'local', $2, $3, 'Keep durable facts concise.', 'composer-1.5')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, testUserID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, model)
		VALUES ($1, 'memory_target_test', 'Memory Target Test', 'local', $2, $3, 'composer-1.5')
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
		"schedule_hour": 1, "catch_up_enabled": true, "confidence_threshold": 0.9,
	}), "id", testWorkspaceID)
	testHandler.UpdateMemoryCuratorProfile(w, profileReq)
	if w.Code != http.StatusOK {
		t.Fatalf("UpdateMemoryCuratorProfile: status=%d body=%s", w.Code, w.Body.String())
	}
	var profile memoryCuratorProfileResponse
	if err := json.NewDecoder(w.Body).Decode(&profile); err != nil {
		t.Fatal(err)
	}
	if profile.RuntimeID != runtimeID || profile.CuratorAgentID != curatorAgentID || len(profile.TargetAgentIDs) != 1 || profile.TargetAgentIDs[0] != targetAgentID {
		t.Fatalf("profile = %+v", profile)
	}

	// Allocate a unique per-workspace issue number, mirroring the production
	// IncrementIssueCounter path, so this issue never collides on
	// uq_issue_workspace_number with the many other raw test inserts across
	// the handler suite that default `number` to 0. Also clean up the task
	// and issue so they do not leak into the shared test workspace.
	var issueID string
	if err := testPool.QueryRow(ctx, `
		WITH bumped AS (
			UPDATE workspace SET issue_counter = issue_counter + 1
			WHERE id = $1 RETURNING issue_counter
		)
		INSERT INTO issue (workspace_id, title, status, priority, creator_type, creator_id, number)
		SELECT $1, 'memory curation active target', 'todo', 'none', 'member', $2, bumped.issue_counter
		FROM bumped
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&issueID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_inbox_event (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'acked', now())
	`, targetAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE issue_id = $1`, issueID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})

	w = httptest.NewRecorder()
	runReq := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+testWorkspaceID+"/memory-curation/runs", map[string]any{
		"agent_ids": []string{targetAgentID}, "stage": "agent_self_review", "until": time.Now().UTC().Format("2006-01-02"), "dry_run": true,
	}), "id", testWorkspaceID)
	testHandler.StartMemoryCurationRun(w, runReq)
	if w.Code != http.StatusAccepted {
		t.Fatalf("StartMemoryCurationRun: status=%d body=%s", w.Code, w.Body.String())
	}
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Status != "queued" || created.ID == "" {
		t.Fatalf("created run = %+v", created)
	}
	var childCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM memory_curation_agent_run WHERE parent_run_id = $1 AND agent_id = $2 AND status = 'queued'`, created.ID, targetAgentID).Scan(&childCount); err != nil {
		t.Fatal(err)
	}
	if childCount != 1 {
		t.Fatalf("child run count = %d, want 1", childCount)
	}

	w = httptest.NewRecorder()
	heartbeatReq := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{
		"runtime_id": runtimeID, "supports_memory_curation": true,
	}, testWorkspaceID, "memory-curator-test-daemon")
	testHandler.DaemonHeartbeat(w, heartbeatReq)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonHeartbeat: status=%d body=%s", w.Code, w.Body.String())
	}
	var heartbeat struct {
		Pending *protocol.DaemonHeartbeatPendingMemoryCuration `json:"pending_memory_curation"`
	}
	if err := json.NewDecoder(w.Body).Decode(&heartbeat); err != nil {
		t.Fatal(err)
	}
	if heartbeat.Pending == nil || heartbeat.Pending.ParentRunID != created.ID || heartbeat.Pending.AgentRunID == "" || heartbeat.Pending.ID != heartbeat.Pending.AgentRunID || heartbeat.Pending.ClaimToken == "" {
		t.Fatalf("pending = %+v", heartbeat.Pending)
	}
	if len(heartbeat.Pending.AgentIDs) != 1 || heartbeat.Pending.AgentIDs[0] != targetAgentID || heartbeat.Pending.CuratorAgentID != targetAgentID {
		t.Fatalf("pending target identity = %+v", heartbeat.Pending)
	}
	if heartbeat.Pending.Mode != "auto_safe" || heartbeat.Pending.ConfidenceThreshold != 0.9 {
		t.Fatalf("pending policy = %+v", heartbeat.Pending)
	}

	w = httptest.NewRecorder()
	staleResultReq := withURLParams(newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/memory-curation/"+heartbeat.Pending.ID+"/result",
		map[string]any{"status": "succeeded", "claim_token": "00000000-0000-0000-0000-000000000001", "result": map[string]any{}},
		testWorkspaceID, "memory-curator-test-daemon"), "runtimeId", runtimeID, "runId", heartbeat.Pending.ID)
	testHandler.ReportMemoryCurationRunResult(w, staleResultReq)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale ReportMemoryCurationRunResult: status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	resultReq := withURLParams(newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/memory-curation/"+heartbeat.Pending.ID+"/result",
		map[string]any{"status": "succeeded", "claim_token": heartbeat.Pending.ClaimToken, "result": map[string]any{
			"agents_scanned": 1,
			"agent_results": []map[string]any{{
				"agent_id":       targetAgentID,
				"curator_output": `{"candidates":[{"type":"memory","scope":"agent","title":"dry run only","content":"must not persist","confidence":0.9}]}`,
			}},
		}},
		testWorkspaceID, "memory-curator-test-daemon"), "runtimeId", runtimeID, "runId", heartbeat.Pending.ID)
	testHandler.ReportMemoryCurationRunResult(w, resultReq)
	if w.Code != http.StatusOK {
		t.Fatalf("ReportMemoryCurationRunResult: status=%d body=%s", w.Code, w.Body.String())
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM memory_curation_run WHERE id = $1`, created.ID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" {
		t.Fatalf("run status = %q, want succeeded", status)
	}
	var dryRunCandidates int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_memory_curation_candidate WHERE run_id = $1`, created.ID).Scan(&dryRunCandidates); err != nil {
		t.Fatal(err)
	}
	if dryRunCandidates != 0 {
		t.Fatalf("dry-run persisted %d candidates, want 0", dryRunCandidates)
	}
	var childStatus, childOutput string
	if err := testPool.QueryRow(ctx, `SELECT status, output->>'curator_output' FROM memory_curation_agent_run WHERE parent_run_id = $1 AND agent_id = $2`, created.ID, targetAgentID).Scan(&childStatus, &childOutput); err != nil {
		t.Fatal(err)
	}
	if childStatus != "succeeded" || !strings.Contains(childOutput, "dry run only") {
		t.Fatalf("child run = status %q output %q, want succeeded with dry-run output", childStatus, childOutput)
	}
}

func TestTeamCurationPersistsKnowledgeAndAppliesDecisionAtomically(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	suffix := randomID()
	const applicabilityProjectID = "22222222-2222-2222-2222-222222222222"
	var agentID, runID, candidateID, privateCandidateID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id,name,runtime_mode,runtime_id,owner_id, model)
		VALUES ($1,$2,'local',$3,$4, 'composer-1.5') RETURNING id::text
	`, testWorkspaceID, "curation-lifecycle-"+suffix, testRuntimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (workspace_id,stage,trigger_kind,status,curator_agent_id)
		VALUES ($1,'team_curation','manual','running',$2) RETURNING id::text
	`, testWorkspaceID, agentID).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_memory_curation_candidate
		(workspace_id,source_agent_id,run_id,candidate_type,scope,title,content,metadata)
		VALUES ($1,$2,$3,'team_memory','team','Team release rule','Run release tests before publishing.','{"shareable":true}'::jsonb)
		RETURNING id::text
	`, testWorkspaceID, agentID, runID).Scan(&candidateID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_memory_curation_candidate
		(workspace_id,source_agent_id,run_id,candidate_type,scope,title,content,metadata)
		VALUES ($1,$2,$3,'user_preference','user','Private preference','Never share this.','{"shareable":true}'::jsonb)
		RETURNING id::text
	`, testWorkspaceID, agentID, runID).Scan(&privateCandidateID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM team_knowledge_item WHERE workspace_id=$1 AND title='Release verification'`, testWorkspaceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM team_knowledge_item WHERE workspace_id=$1 AND title='Private leak'`, testWorkspaceID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_memory_curation_candidate WHERE id=$1`, candidateID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_memory_curation_candidate WHERE id=$1`, privateCandidateID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id=$1`, runID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id=$1`, agentID)
	})

	bundles := testHandler.memoryCurationEvidenceBundles(ctx, testWorkspaceID, []string{agentID}, "2026-07-16", "2026-07-17", true)
	foundEvidence := false
	for _, bundle := range bundles {
		for _, item := range bundle.Items {
			if item.Kind == "curation_candidate" && item.ID == candidateID {
				foundEvidence = true
			}
			if item.ID == privateCandidateID {
				t.Fatal("user-private candidate was delivered to team curator evidence")
			}
		}
	}
	if !foundEvidence {
		t.Fatal("pending candidate was not delivered to team curator evidence")
	}

	output, _ := json.Marshal(map[string]any{
		"team_knowledge": []map[string]any{{
			"kind": "policy", "title": "Release verification",
			"content": "Run release tests before publishing.", "source_candidate_ids": []string{candidateID},
			"applies": map[string]any{"project_ids": []string{applicabilityProjectID}, "task_types": []string{"issue"}, "expires_at": "2099-01-01T00:00:00Z"},
		}, {
			"kind": "memory", "title": "Private leak", "content": "Never share this.",
			"source_candidate_ids": []string{privateCandidateID},
		}},
		"decisions": []map[string]any{
			{"candidate_id": candidateID, "status": "promoted", "reason": "workspace policy"},
			{"candidate_id": privateCandidateID, "status": "promoted", "reason": "must be rejected by server"},
		},
	})
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := testHandler.persistTeamCurationOutput(ctx, tx, runID, testWorkspaceID, string(output)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var candidateStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_memory_curation_candidate WHERE id=$1`, candidateID).Scan(&candidateStatus); err != nil {
		t.Fatal(err)
	}
	if candidateStatus != "promoted" {
		t.Fatalf("candidate status = %q, want promoted", candidateStatus)
	}
	var privateStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_memory_curation_candidate WHERE id=$1`, privateCandidateID).Scan(&privateStatus); err != nil {
		t.Fatal(err)
	}
	if privateStatus != "pending" {
		t.Fatalf("private candidate status = %q, want pending", privateStatus)
	}
	var privateKnowledgeCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM team_knowledge_item WHERE workspace_id=$1 AND title='Private leak'`, testWorkspaceID).Scan(&privateKnowledgeCount); err != nil {
		t.Fatal(err)
	}
	if privateKnowledgeCount != 0 {
		t.Fatalf("private team knowledge count = %d, want 0", privateKnowledgeCount)
	}
	var knowledgeCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM team_knowledge_item WHERE workspace_id=$1 AND title='Release verification' AND $2::uuid = ANY(source_candidate_ids)`, testWorkspaceID, candidateID).Scan(&knowledgeCount); err != nil {
		t.Fatal(err)
	}
	if knowledgeCount != 1 {
		t.Fatalf("team knowledge count = %d, want 1", knowledgeCount)
	}
	var appliesProject, appliesTask, appliesExpiry string
	if err := testPool.QueryRow(ctx, `
		SELECT metadata->'applies'->'project_ids'->>0,
		       metadata->'applies'->'task_types'->>0,
		       metadata->'applies'->>'expires_at'
		  FROM team_knowledge_item
		 WHERE workspace_id=$1 AND title='Release verification'
	`, testWorkspaceID).Scan(&appliesProject, &appliesTask, &appliesExpiry); err != nil {
		t.Fatal(err)
	}
	if appliesProject != applicabilityProjectID || appliesTask != "issue" || appliesExpiry != "2099-01-01T00:00:00Z" {
		t.Fatalf("team knowledge applies = %q/%q/%q", appliesProject, appliesTask, appliesExpiry)
	}

	rollbackTitle := "Rollback verification " + suffix
	// Non-UUID decision ids (file-slug style) and unknown candidates must not
	// fail the whole team curation persist — otherwise runs stay stuck running.
	slugOutput, _ := json.Marshal(map[string]any{
		"team_knowledge": []map[string]any{{
			"kind": "policy", "title": rollbackTitle, "content": "Slug ids should not abort persist.",
			"source_candidate_ids": []string{"1864763b:2026-07-08:slack-mobile-message-ux", candidateID},
		}},
		"decisions": []map[string]any{
			{"candidate_id": "not-a-uuid", "status": "promoted"},
			{"candidate_id": "00000000-0000-4000-8000-000000000099", "status": "promoted", "reason": "missing"},
		},
	})
	tx, err = testPool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := testHandler.persistTeamCurationOutput(ctx, tx, runID, testWorkspaceID, string(slugOutput)); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("slug decision should be skipped, got %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var slugKnowledgeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM team_knowledge_item
		 WHERE workspace_id=$1 AND title=$2
		   AND $3::uuid = ANY(source_candidate_ids)
		   AND metadata ? 'source_candidate_refs'
	`, testWorkspaceID, rollbackTitle, candidateID).Scan(&slugKnowledgeCount); err != nil {
		t.Fatal(err)
	}
	if slugKnowledgeCount != 1 {
		t.Fatalf("slug-tolerant team knowledge count = %d, want 1", slugKnowledgeCount)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM team_knowledge_item WHERE workspace_id=$1 AND title=$2`, testWorkspaceID, rollbackTitle)
	})
}

func TestDeleteRuntimeFailsIncompleteMemoryCurationRuns(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	ctx := context.Background()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, name, runtime_mode, provider, status, device_info, owner_id
		) VALUES ($1, 'Curator Cleanup Runtime', 'local', 'pi', 'online', 'cleanup test', $2)
		RETURNING id::text
	`, testWorkspaceID, testUserID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	var queuedRunID, doneRunID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (workspace_id, stage, trigger_kind, status, runtime_id)
		VALUES ($1, 'l3_promote', 'manual', 'queued', $2)
		RETURNING id::text
	`, testWorkspaceID, runtimeID).Scan(&queuedRunID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO memory_curation_run (workspace_id, stage, trigger_kind, status, runtime_id)
		VALUES ($1, 'l1_daily', 'manual', 'succeeded', $2)
		RETURNING id::text
	`, testWorkspaceID, runtimeID).Scan(&doneRunID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM memory_curation_run WHERE id = ANY($1::uuid[])`, []string{queuedRunID, doneRunID})
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	w := httptest.NewRecorder()
	req := withURLParam(newRequest(http.MethodDelete, "/api/workspaces/"+testWorkspaceID+"/runtimes/"+runtimeID, nil), "runtimeId", runtimeID)
	testHandler.DeleteAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DeleteAgentRuntime: status=%d body=%s", w.Code, w.Body.String())
	}

	var queuedStatus, doneStatus string
	if err := testPool.QueryRow(ctx, `SELECT status FROM memory_curation_run WHERE id = $1`, queuedRunID).Scan(&queuedStatus); err != nil {
		t.Fatal(err)
	}
	if queuedStatus != "failed" {
		t.Fatalf("queued run status = %q, want failed", queuedStatus)
	}
	if err := testPool.QueryRow(ctx, `SELECT status FROM memory_curation_run WHERE id = $1`, doneRunID).Scan(&doneStatus); err != nil {
		t.Fatal(err)
	}
	if doneStatus != "succeeded" {
		t.Fatalf("completed run status = %q, want succeeded (should be unchanged)", doneStatus)
	}
}

// newGraphCurationWorkspaceFixture builds a dedicated workspace where
// testUserID is owner and the graph_memory_profile row says memory_type
// 'graph'. Spec §10: legacy-only curation endpoints answer with the stable
// not-applicable response instead of queueing legacy runs.
func newGraphCurationWorkspaceFixture(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug)
		VALUES ($1, $2)
		RETURNING id::text
	`, "Graph Curation Not Applicable", "graph-curation-na-"+uuid.NewString()[:8]).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})
	if _, err := testPool.Exec(ctx, `INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')`, workspaceID, testUserID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx,
		`INSERT INTO graph_memory_profile (workspace_id, memory_type) VALUES ($1, 'graph')`, workspaceID); err != nil {
		t.Fatal(err)
	}
	return workspaceID
}

func assertLegacyCurationNotApplicable(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409; body %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "legacy_curation_not_applicable") {
		t.Fatalf("body = %s, want stable not-applicable code", rec.Body.String())
	}
}

func TestStartMemoryCurationRunNotApplicableForGraphWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	workspaceID := newGraphCurationWorkspaceFixture(t)
	req := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/memory-curation/runs", map[string]any{
		"stage": "all", "all_agents": true,
	}), "id", workspaceID)
	rec := httptest.NewRecorder()
	testHandler.StartMemoryCurationRun(rec, req)
	assertLegacyCurationNotApplicable(t, rec)
}

func TestStartMemoryCurationBackfillNotApplicableForGraphWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	workspaceID := newGraphCurationWorkspaceFixture(t)
	req := withURLParam(newRequest(http.MethodPost, "/api/workspaces/"+workspaceID+"/memory-curation/backfill", map[string]any{
		"since": "2026-07-01", "until": "2026-07-02",
	}), "id", workspaceID)
	rec := httptest.NewRecorder()
	testHandler.StartMemoryCurationBackfill(rec, req)
	assertLegacyCurationNotApplicable(t, rec)
}

func TestPreviewMemoryCurationBackfillNotApplicableForGraphWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test database unavailable")
	}
	workspaceID := newGraphCurationWorkspaceFixture(t)
	req := withURLParam(newRequest(http.MethodGet, "/api/workspaces/"+workspaceID+"/memory-curation/backfill/preview?since=2026-07-01&until=2026-07-02", nil),
		"id", workspaceID)
	rec := httptest.NewRecorder()
	testHandler.PreviewMemoryCurationBackfill(rec, req)
	assertLegacyCurationNotApplicable(t, rec)
}
