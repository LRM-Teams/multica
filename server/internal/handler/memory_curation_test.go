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
		INSERT INTO agent (workspace_id, name, runtime_mode, runtime_id, owner_id)
		VALUES ($1, 'foreign memory curation agent', 'local', $2, $3)
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
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id, instructions)
		VALUES ($1, 'memory_curator_test', 'Memory Curator Test', 'local', $2, $3, 'Keep durable facts concise.')
		RETURNING id::text
	`, testWorkspaceID, runtimeID, testUserID).Scan(&curatorAgentID); err != nil {
		t.Fatal(err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_id, owner_id)
		VALUES ($1, 'memory_target_test', 'Memory Target Test', 'local', $2, $3)
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
		INSERT INTO agent_task_queue (agent_id, issue_id, runtime_id, status, created_at)
		VALUES ($1, $2, $3, 'completed', now())
	`, targetAgentID, issueID, runtimeID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE issue_id = $1`, issueID)
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
	if heartbeat.Pending == nil || heartbeat.Pending.ID != created.ID || heartbeat.Pending.ClaimToken == "" {
		t.Fatalf("pending = %+v", heartbeat.Pending)
	}
	if heartbeat.Pending.Mode != "auto_safe" || heartbeat.Pending.ConfidenceThreshold != 0.9 {
		t.Fatalf("pending policy = %+v", heartbeat.Pending)
	}

	w = httptest.NewRecorder()
	staleResultReq := withURLParams(newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/memory-curation/"+created.ID+"/result",
		map[string]any{"status": "succeeded", "claim_token": "00000000-0000-0000-0000-000000000001", "result": map[string]any{}},
		testWorkspaceID, "memory-curator-test-daemon"), "runtimeId", runtimeID, "runId", created.ID)
	testHandler.ReportMemoryCurationRunResult(w, staleResultReq)
	if w.Code != http.StatusConflict {
		t.Fatalf("stale ReportMemoryCurationRunResult: status=%d body=%s", w.Code, w.Body.String())
	}

	w = httptest.NewRecorder()
	resultReq := withURLParams(newDaemonTokenRequest(http.MethodPost,
		"/api/daemon/runtimes/"+runtimeID+"/memory-curation/"+created.ID+"/result",
		map[string]any{"status": "succeeded", "claim_token": heartbeat.Pending.ClaimToken, "result": map[string]any{"agents_scanned": 1}},
		testWorkspaceID, "memory-curator-test-daemon"), "runtimeId", runtimeID, "runId", created.ID)
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
