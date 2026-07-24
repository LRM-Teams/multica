package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestDeleteRuntimesByDaemon_SucceedsWithHistoricalInboxRuntimeSnapshot(t *testing.T) {
	// LRM-437/438: migration 183 left agent_inbox_event.runtime_id without
	// ON DELETE, so a historical inbox snapshot blocked Computer bulk delete
	// with generic "failed to delete runtimes" (Frank IMG_3127).
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-fk-" + uuid.NewString()

	victim := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	// Agent lives on a different machine so the active-agent gate does not fire;
	// only the inbox row's immutable runtime_id snapshot points at victim.
	otherDaemon := "other-inbox-fk-" + uuid.NewString()
	otherRT := createBulkDaemonRuntime(t, ctx, otherDaemon, "claude", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, otherRT, "Inbox Snapshot Agent")

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, reason, requires_wake, status, priority, runtime_id
		)
		VALUES ($1, $2, 'ambient', false, 'acked', 0, $3)
		RETURNING id
	`, testWorkspaceID, agentID, victim).Scan(&eventID); err != nil {
		t.Fatalf("insert inbox event with runtime snapshot: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 with inbox runtime snapshot, got %d: %s", w.Code, w.Body.String())
	}
	assertRuntimeGone(t, ctx, victim)
	assertRuntimeExists(t, ctx, otherRT)

	var stillPinned int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1 AND runtime_id IS NOT NULL
	`, eventID).Scan(&stillPinned); err != nil {
		t.Fatalf("count inbox runtime snapshot: %v", err)
	}
	if stillPinned != 0 {
		t.Fatalf("expected inbox runtime_id nulled on runtime delete, still pinned=%d", stillPinned)
	}
}

func TestDeleteRuntimesByDaemon_HappyPathDeletesAllOfflineProviders(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-daemon-" + uuid.NewString()

	rtClaude := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	rtCodex := createBulkDaemonRuntime(t, ctx, daemonID, "codex", "offline")
	// Unrelated machine must survive.
	other := createBulkDaemonRuntime(t, ctx, "other-daemon-"+uuid.NewString(), "claude", "offline")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Status            string   `json:"status"`
		DaemonID          string   `json:"daemon_id"`
		DeletedCount      int      `json:"deleted_count"`
		DeletedRuntimeIDs []string `json:"deleted_runtime_ids"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" || body.DaemonID != daemonID || body.DeletedCount != 2 {
		t.Fatalf("unexpected body: %+v", body)
	}
	got := map[string]bool{}
	for _, id := range body.DeletedRuntimeIDs {
		got[id] = true
	}
	if !got[rtClaude] || !got[rtCodex] {
		t.Fatalf("deleted ids missing expected runtimes: %+v", body.DeletedRuntimeIDs)
	}

	assertRuntimeGone(t, ctx, rtClaude)
	assertRuntimeGone(t, ctx, rtCodex)
	assertRuntimeExists(t, ctx, other)
}

func TestDeleteRuntimesByDaemon_RefusesOnline(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-online-" + uuid.NewString()

	onlineID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "online")
	offlineID := createBulkDaemonRuntime(t, ctx, daemonID, "codex", "offline")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "computer_has_online_runtimes" {
		t.Fatalf("expected computer_has_online_runtimes, got %q", body.Code)
	}

	assertRuntimeExists(t, ctx, onlineID)
	assertRuntimeExists(t, ctx, offlineID)
}

func TestDeleteRuntimesByDaemon_RefusesActiveAgents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-agents-" + uuid.NewString()

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Block Agent")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code         string          `json:"code"`
		ActiveAgents []AgentResponse `json:"active_agents"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "computer_has_active_agents" {
		t.Fatalf("expected computer_has_active_agents, got %q", body.Code)
	}
	if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].ID != agentID {
		t.Fatalf("expected active agent %s, got %+v", agentID, body.ActiveAgents)
	}
	assertRuntimeExists(t, ctx, rtID)
}

func TestDeleteRuntimesByDaemon_RefusesActiveTasks(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-tasks-" + uuid.NewString()

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	// Active task without a live agent binding still blocks computer delete
	// (LRM-438 / LRM-238): refuse with an explicit reason.
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Task Agent")
	// Archive the agent so the active-agents guard does not fire first —
	// we specifically want the active-tasks branch.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, agentID, testUserID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	issueID := createBulkFixtureIssue(t, ctx)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, issue_id, status, priority)
		VALUES ($1, $2, $3, 'queued', 0)
	`, agentID, rtID, issueID); err != nil {
		t.Fatalf("insert active task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE runtime_id = $1`, rtID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Code            string `json:"code"`
		ActiveTaskCount int64  `json:"active_task_count"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Code != "computer_has_active_tasks" || body.ActiveTaskCount < 1 {
		t.Fatalf("expected computer_has_active_tasks with count>=1, got %+v", body)
	}
	assertRuntimeExists(t, ctx, rtID)
}

func TestDeleteRuntimesByDaemon_RuntimeModeFilter(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-mode-" + uuid.NewString()

	localID := createBulkDaemonRuntimeWithMode(t, ctx, daemonID, "claude", "offline", "local")
	cloudID := createBulkDaemonRuntimeWithMode(t, ctx, daemonID, "codex", "offline", "cloud")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID+"?runtime_mode=local", nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, localID)
	assertRuntimeExists(t, ctx, cloudID)
}

func createBulkDaemonRuntime(t *testing.T, ctx context.Context, daemonID, provider, status string) string {
	t.Helper()
	return createBulkDaemonRuntimeWithMode(t, ctx, daemonID, provider, status, "local")
}

func createBulkDaemonRuntimeWithMode(t *testing.T, ctx context.Context, daemonID, provider, status, mode string) string {
	t.Helper()
	var runtimeID string
	name := "Bulk " + provider + " " + uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, '{}'::jsonb, $8, now() - interval '1 hour')
		RETURNING id
	`, testWorkspaceID, daemonID, name, mode, provider, status, name+" device", testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert bulk daemon runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE runtime_id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}

func createBulkFixtureIssue(t *testing.T, ctx context.Context) string {
	t.Helper()
	var issueID string
	var number int
	if err := testPool.QueryRow(ctx, `SELECT COALESCE(max(number), 0) + 1 FROM issue WHERE workspace_id = $1`, testWorkspaceID).Scan(&number); err != nil {
		t.Fatalf("next issue number: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO issue (workspace_id, title, status, priority, number, creator_id, creator_type)
		VALUES ($1, $2, 'todo', 'none', $3, $4, 'member')
		RETURNING id
	`, testWorkspaceID, "bulk-delete-task-"+uuid.NewString()[:8], number, testUserID).Scan(&issueID); err != nil {
		t.Fatalf("insert bulk fixture issue: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID
}

func assertRuntimeGone(t *testing.T, ctx context.Context, runtimeID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&n); err != nil {
		t.Fatalf("count runtime: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected runtime %s deleted, count=%d", runtimeID, n)
	}
}

func assertRuntimeExists(t *testing.T, ctx context.Context, runtimeID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&n); err != nil {
		t.Fatalf("count runtime: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected runtime %s to exist, count=%d", runtimeID, n)
	}
}
