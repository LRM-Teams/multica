package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
	if _, _, err := testHandler.issueDaemonRegisterToken(
		ctx,
		parseUUID(testWorkspaceID),
		strings.ToUpper(daemonID),
	); err != nil {
		t.Fatalf("issue daemon token: %v", err)
	}

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
	assertDaemonTombstoned(t, ctx, daemonID)

	var tokenCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_token
		WHERE workspace_id = $1 AND lower(daemon_id) = lower($2)
	`, testWorkspaceID, daemonID).Scan(&tokenCount); err != nil {
		t.Fatalf("count daemon tokens: %v", err)
	}
	if tokenCount != 0 {
		t.Fatalf("expected all daemon tokens revoked, count=%d", tokenCount)
	}
}

func TestDeleteRuntimesByDaemon_DeletesOnlineComputer(t *testing.T) {
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
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, onlineID)
	assertRuntimeGone(t, ctx, offlineID)
	assertDaemonTombstoned(t, ctx, daemonID)
}

func TestDeleteRuntimesByDaemon_BlocksUntilExactAgentPlanIsRemoved(t *testing.T) {
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

	stalePlan := httptest.NewRecorder()
	stalePlanReq := newRequest("POST", "/api/runtimes/by-daemon/"+daemonID+"/remove-agents", map[string]any{
		"expected_active_agent_ids": []string{},
	})
	stalePlanReq = withURLParam(stalePlanReq, "daemonId", daemonID)
	testHandler.RemoveAgentsByDaemon(stalePlan, stalePlanReq)
	if stalePlan.Code != http.StatusConflict {
		t.Fatalf("expected stale agent plan 409, got %d: %s", stalePlan.Code, stalePlan.Body.String())
	}
	var staleBody struct {
		Code         string          `json:"code"`
		ActiveAgents []AgentResponse `json:"active_agents"`
	}
	if err := json.NewDecoder(stalePlan.Body).Decode(&staleBody); err != nil {
		t.Fatalf("decode stale plan: %v", err)
	}
	if staleBody.Code != "computer_agent_plan_changed" ||
		len(staleBody.ActiveAgents) != 1 ||
		staleBody.ActiveAgents[0].ID != agentID {
		t.Fatalf("unexpected stale plan body: %+v", staleBody)
	}

	remove := httptest.NewRecorder()
	removeReq := newRequest("POST", "/api/runtimes/by-daemon/"+daemonID+"/remove-agents", map[string]any{
		"expected_active_agent_ids": []string{agentID},
	})
	removeReq = withURLParam(removeReq, "daemonId", daemonID)
	testHandler.RemoveAgentsByDaemon(remove, removeReq)
	if remove.Code != http.StatusOK {
		t.Fatalf("expected remove agents 200, got %d: %s", remove.Code, remove.Body.String())
	}
	assertRuntimeExists(t, ctx, rtID)

	var archived bool
	if err := testPool.QueryRow(ctx, `
		SELECT archived_at IS NOT NULL
		FROM agent WHERE id = $1
	`, agentID).Scan(&archived); err != nil {
		t.Fatalf("read removed agent: %v", err)
	}
	if !archived {
		t.Fatal("expected agent archived before computer delete")
	}

	confirm := httptest.NewRecorder()
	confirmReq := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	confirmReq = withURLParam(confirmReq, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(confirm, confirmReq)
	if confirm.Code != http.StatusOK {
		t.Fatalf("expected empty computer delete 200, got %d: %s", confirm.Code, confirm.Body.String())
	}
	assertRuntimeGone(t, ctx, rtID)

	var agentCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE id = $1`, agentID).Scan(&agentCount); err != nil {
		t.Fatalf("count deleted agent: %v", err)
	}
	if agentCount != 0 {
		t.Fatalf("expected removed agent hard-deleted with computer, count=%d", agentCount)
	}
}

func TestDeleteRuntimesByDaemon_CancelsArchivedAgentTaskWithoutRuntimeSnapshot(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-tasks-" + uuid.NewString()

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	// Active work on an already-removed agent is cancelled atomically with the
	// empty-computer cleanup.
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Task Agent")
	// Archive the agent so the active-agents guard does not fire. The task has
	// no runtime snapshot, so only the archived-agent id can match it.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, agentID, testUserID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	issueID := createBulkFixtureIssue(t, ctx)
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, issue_id, reason, status, priority
		)
		VALUES ($1, $2, NULL, $3, 'issue', 'pending', 0)
		RETURNING id
	`, testWorkspaceID, agentID, issueID).Scan(&eventID); err != nil {
		t.Fatalf("insert active task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		TasksCancelled int `json:"tasks_cancelled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TasksCancelled != 1 {
		t.Fatalf("expected one cancelled task, got %+v", body)
	}
	assertRuntimeGone(t, ctx, rtID)

	var eventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1
	`, eventID).Scan(&eventCount); err != nil {
		t.Fatalf("count cancelled task: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("expected removed agent task history deleted, count=%d", eventCount)
	}
}

func TestDeleteRuntimesByDaemon_CancelsActiveInboxWork(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-active-" + uuid.NewString()

	rtID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, rtID, "Bulk Inbox Active Agent")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET archived_at = now(), archived_by = $2 WHERE id = $1
	`, agentID, testUserID); err != nil {
		t.Fatalf("archive agent: %v", err)
	}
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, runtime_id, reason, status, priority)
		VALUES ($1, $2, $3, 'mention', 'pending', 0)
		RETURNING id
	`, testWorkspaceID, agentID, rtID).Scan(&eventID); err != nil {
		t.Fatalf("insert active inbox event: %v", err)
	}
	defer testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		TasksCancelled int `json:"tasks_cancelled"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.TasksCancelled != 1 {
		t.Fatalf("expected one cancelled inbox event, got %+v", body)
	}
	assertRuntimeGone(t, ctx, rtID)
	var eventCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1
	`, eventID).Scan(&eventCount); err != nil {
		t.Fatalf("count cancelled inbox event: %v", err)
	}
	if eventCount != 0 {
		t.Fatalf("expected removed agent inbox history deleted, count=%d", eventCount)
	}
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

func TestDeleteRuntimesByDaemon_DetachesTerminalInboxEvents(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-terminal-" + uuid.NewString()

	targetRuntimeID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	otherRuntimeID := createBulkDaemonRuntime(t, ctx, "other-inbox-"+uuid.NewString(), "codex", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, otherRuntimeID, "Bulk Inbox Agent")
	eventID := createBulkInboxEvent(t, ctx, targetRuntimeID, agentID, "acked")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	assertRuntimeGone(t, ctx, targetRuntimeID)
	assertInboxEventRuntimeCleared(t, ctx, eventID)
	assertRuntimeExists(t, ctx, otherRuntimeID)
}

func TestDeleteRuntimesByDaemon_CancelsActiveInboxEventOnOtherAgent(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "bulk-inbox-active-" + uuid.NewString()

	targetRuntimeID := createBulkDaemonRuntime(t, ctx, daemonID, "claude", "offline")
	otherRuntimeID := createBulkDaemonRuntime(t, ctx, "other-inbox-"+uuid.NewString(), "codex", "offline")
	agentID := createCascadeFixtureAgent(t, ctx, otherRuntimeID, "Bulk Active Inbox Agent")
	createBulkInboxEvent(t, ctx, targetRuntimeID, agentID, "pending")

	w := httptest.NewRecorder()
	req := newRequest("DELETE", "/api/runtimes/by-daemon/"+daemonID, nil)
	req = withURLParam(req, "daemonId", daemonID)
	testHandler.DeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	assertRuntimeGone(t, ctx, targetRuntimeID)
	assertRuntimeExists(t, ctx, otherRuntimeID)
}

func TestDaemonRegister_RejectsPermanentlyRemovedComputer(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "removed-daemon-" + uuid.NewString()
	if _, err := testPool.Exec(ctx, `
		INSERT INTO daemon_registration_tombstone (workspace_id, daemon_id, removed_by)
		VALUES ($1, lower($2), $3)
	`, testWorkspaceID, daemonID, testUserID); err != nil {
		t.Fatalf("insert daemon tombstone: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `
			DELETE FROM daemon_registration_tombstone
			WHERE workspace_id = $1 AND daemon_id = lower($2)
		`, testWorkspaceID, daemonID)
	})

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/daemon/register", map[string]any{
		"workspace_id": testWorkspaceID,
		"daemon_id":    strings.ToUpper(daemonID),
		"device_name":  "removed computer",
		"runtimes": []map[string]any{
			{"name": "claude", "type": "claude", "status": "online"},
		},
	})
	testHandler.DaemonRegister(w, req)
	if w.Code != http.StatusGone {
		t.Fatalf("expected 410, got %d: %s", w.Code, w.Body.String())
	}
	var body struct {
		Code string `json:"code"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body.Code != "daemon_removed" {
		t.Fatalf("expected daemon_removed, got %q", body.Code)
	}

	var runtimeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime
		WHERE workspace_id = $1 AND lower(daemon_id) = lower($2)
	`, testWorkspaceID, daemonID).Scan(&runtimeCount); err != nil {
		t.Fatalf("count recreated runtimes: %v", err)
	}
	if runtimeCount != 0 {
		t.Fatalf("expected no recreated runtime, count=%d", runtimeCount)
	}
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

func createBulkInboxEvent(t *testing.T, ctx context.Context, runtimeID, agentID, status string) string {
	t.Helper()
	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (workspace_id, agent_id, runtime_id, reason, status, priority)
		VALUES ($1, $2, $3, 'mention', $4, 0)
		RETURNING id
	`, testWorkspaceID, agentID, runtimeID, status).Scan(&eventID); err != nil {
		t.Fatalf("insert bulk inbox event: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})
	return eventID
}

func assertInboxEventRuntimeCleared(t *testing.T, ctx context.Context, eventID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE id = $1 AND runtime_id IS NULL
	`, eventID).Scan(&n); err != nil {
		t.Fatalf("count inbox event: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected inbox event %s runtime_id cleared, count=%d", eventID, n)
	}
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

func assertDaemonTombstoned(t *testing.T, ctx context.Context, daemonID string) {
	t.Helper()
	var n int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM daemon_registration_tombstone
		WHERE workspace_id = $1 AND daemon_id = lower($2)
	`, testWorkspaceID, daemonID).Scan(&n); err != nil {
		t.Fatalf("count daemon tombstone: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected daemon %s tombstoned, count=%d", daemonID, n)
	}
}
