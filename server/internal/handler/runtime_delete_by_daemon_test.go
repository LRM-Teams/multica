package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func createDaemonFixtureRuntime(t *testing.T, ctx context.Context, daemonID, provider, name string) string {
	t.Helper()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', $4, 'offline', $5, '{}'::jsonb, $6, now())
		RETURNING id
	`, testWorkspaceID, daemonID, name, provider, name+" device", testUserID).Scan(&runtimeID); err != nil {
		t.Fatalf("insert daemon fixture runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE runtime_id = $1`, runtimeID)
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID
}

func TestDeleteAgentRuntimesByDaemon_EmptyBindSucceeds(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "lrm-438-empty-" + t.Name()

	rt1 := createDaemonFixtureRuntime(t, ctx, daemonID, "claude", "Empty Claude")
	rt2 := createDaemonFixtureRuntime(t, ctx, daemonID, "codex", "Empty Codex")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/delete-by-daemon", map[string]any{
		"daemon_id": daemonID,
	})
	testHandler.DeleteAgentRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var body struct {
		Status            string   `json:"status"`
		DeletedRuntimeIDs []string `json:"deleted_runtime_ids"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status = %q", body.Status)
	}
	if len(body.DeletedRuntimeIDs) != 2 {
		t.Fatalf("deleted_runtime_ids = %v, want 2", body.DeletedRuntimeIDs)
	}

	var remaining int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime WHERE id = ANY($1::uuid[])
	`, []string{rt1, rt2}).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected both runtimes deleted, remaining=%d", remaining)
	}
}

func TestDeleteAgentRuntimesByDaemon_ActiveAgentsConflict(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "lrm-438-agents-" + t.Name()

	rt1 := createDaemonFixtureRuntime(t, ctx, daemonID, "claude", "Blocked Claude")
	_ = createDaemonFixtureRuntime(t, ctx, daemonID, "codex", "Blocked Codex")
	agentID := createCascadeFixtureAgent(t, ctx, rt1, "Blocked Agent")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/delete-by-daemon", map[string]any{
		"daemon_id": daemonID,
	})
	testHandler.DeleteAgentRuntimesByDaemon(w, req)
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
	if body.Code != "runtime_has_active_agents" {
		t.Fatalf("code = %q", body.Code)
	}
	if len(body.ActiveAgents) != 1 || body.ActiveAgents[0].ID != agentID {
		t.Fatalf("active_agents = %+v, want %s", body.ActiveAgents, agentID)
	}

	var remaining int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime WHERE LOWER(daemon_id) = LOWER($1)
	`, daemonID).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 2 {
		t.Fatalf("expected both runtimes to survive 409, remaining=%d", remaining)
	}
}

func TestArchiveAgentsAndDeleteRuntimesByDaemon_HappyPath(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	daemonID := "lrm-438-cascade-" + t.Name()

	rt1 := createDaemonFixtureRuntime(t, ctx, daemonID, "claude", "Cascade Claude")
	rt2 := createDaemonFixtureRuntime(t, ctx, daemonID, "codex", "Cascade Codex")
	agentID := createCascadeFixtureAgent(t, ctx, rt1, "Cascade Daemon Agent")

	w := httptest.NewRecorder()
	req := newRequest("POST", "/api/runtimes/archive-agents-and-delete-by-daemon", map[string]any{
		"daemon_id":                 daemonID,
		"expected_active_agent_ids": []string{agentID},
	})
	testHandler.ArchiveAgentsAndDeleteRuntimesByDaemon(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var remaining int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_runtime WHERE id = ANY($1::uuid[])
	`, []string{rt1, rt2}).Scan(&remaining); err != nil {
		t.Fatalf("count remaining: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expected both runtimes deleted, remaining=%d", remaining)
	}
	var agentRows int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent WHERE id = $1`, agentID).Scan(&agentRows); err != nil {
		t.Fatalf("count agent: %v", err)
	}
	if agentRows != 0 {
		t.Fatalf("expected agent hard-deleted, found %d", agentRows)
	}
}
