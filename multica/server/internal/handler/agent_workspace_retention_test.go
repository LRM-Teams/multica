package handler

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// archiveHandlerTestAgentAt backdates an agent's archived_at directly via SQL
// — ArchiveAgent always stamps now(), and these tests need to simulate an
// archival that happened N days ago.
func archiveHandlerTestAgentAt(t *testing.T, agentID string, archivedAt time.Time) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET archived_at = $2, archived_by = NULL WHERE id = $1`,
		agentID, archivedAt,
	); err != nil {
		t.Fatalf("backdate archived_at: %v", err)
	}
}

func checkAgentWorkspaceRetention(t *testing.T, agentIDs []string) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/workspaces/"+testWorkspaceID+"/agent-workspace-retention/check",
		map[string]any{"agent_ids": agentIDs}, testWorkspaceID, "test-daemon-mdt")
	req = withURLParam(req, "workspaceId", testWorkspaceID)

	testHandler.CheckAgentWorkspaceRetention(w, req)
	if w.Code != 200 {
		t.Fatalf("CheckAgentWorkspaceRetention: expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func eligibleIDs(t *testing.T, resp map[string]any) []string {
	t.Helper()
	raw, ok := resp["eligible_agent_ids"].([]any)
	if !ok {
		t.Fatalf("eligible_agent_ids missing or wrong type in %+v", resp)
	}
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i] = v.(string)
	}
	return out
}

func containsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// TestCheckAgentWorkspaceRetention_PastRetentionIsEligible is the positive
// direction of task #96's dual-direction requirement: an agent archived
// safely past the default 30-day window must come back as eligible so the
// daemon's retention job actually reclaims dead workspaces.
func TestCheckAgentWorkspaceRetention_PastRetentionIsEligible(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Retention Past Due", nil)
	archiveHandlerTestAgentAt(t, agentID, time.Now().AddDate(0, 0, -31))

	resp := checkAgentWorkspaceRetention(t, []string{agentID})
	if !containsID(eligibleIDs(t, resp), agentID) {
		t.Fatalf("agent archived 31 days ago must be eligible, got %+v", resp)
	}
}

// TestCheckAgentWorkspaceRetention_WithinRetentionIsNotEligible is the
// requirement Parker flagged as the more important direction: an agent
// archived recently must NOT come back as eligible. Only testing the
// positive direction could ship a job that reclaims live-ish data.
func TestCheckAgentWorkspaceRetention_WithinRetentionIsNotEligible(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Retention Within Window", nil)
	archiveHandlerTestAgentAt(t, agentID, time.Now().AddDate(0, 0, -5))

	resp := checkAgentWorkspaceRetention(t, []string{agentID})
	if containsID(eligibleIDs(t, resp), agentID) {
		t.Fatalf("agent archived only 5 days ago must not be eligible, got %+v", resp)
	}
}

// TestCheckAgentWorkspaceRetention_NeverArchivedIsNotEligible covers a live
// agent whose directory a daemon might report by mistake (e.g. a stale scan)
// — it must never be treated as reclaimable.
func TestCheckAgentWorkspaceRetention_NeverArchivedIsNotEligible(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Retention Never Archived", nil)

	resp := checkAgentWorkspaceRetention(t, []string{agentID})
	if containsID(eligibleIDs(t, resp), agentID) {
		t.Fatalf("a live (never-archived) agent must not be eligible, got %+v", resp)
	}
}

// TestCheckAgentWorkspaceRetention_RestoredAgentIsNotEligible is the
// regression guard for the "RestoreAgent clears archived_at, so a restored
// agent falls out of the set on its own" invariant the query's doc comment
// claims. If this ever regressed, a restored agent's workspace could be
// silently destroyed by the retention job despite the user un-deleting it.
func TestCheckAgentWorkspaceRetention_RestoredAgentIsNotEligible(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "Retention Restored", nil)
	archiveHandlerTestAgentAt(t, agentID, time.Now().AddDate(0, 0, -60))

	// Confirm-broken: before restoring, this agent is eligible.
	resp := checkAgentWorkspaceRetention(t, []string{agentID})
	if !containsID(eligibleIDs(t, resp), agentID) {
		t.Fatalf("setup check: agent archived 60 days ago should be eligible before restore, got %+v", resp)
	}

	if _, err := testPool.Exec(context.Background(),
		`UPDATE agent SET archived_at = NULL, archived_by = NULL WHERE id = $1`, agentID,
	); err != nil {
		t.Fatalf("restore agent: %v", err)
	}

	resp = checkAgentWorkspaceRetention(t, []string{agentID})
	if containsID(eligibleIDs(t, resp), agentID) {
		t.Fatalf("a restored agent must not be eligible, got %+v", resp)
	}
}

// TestCheckAgentWorkspaceRetention_EmptyAgentIDsReturnsEmpty guards the
// no-op path a daemon hits when it has no .multica/agents/* directories at
// all — must not error and must not accidentally return every archived
// agent in the workspace.
func TestCheckAgentWorkspaceRetention_EmptyAgentIDsReturnsEmpty(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	resp := checkAgentWorkspaceRetention(t, []string{})
	if ids := eligibleIDs(t, resp); len(ids) != 0 {
		t.Fatalf("expected empty eligible_agent_ids for empty request, got %+v", ids)
	}
}

// TestCheckAgentWorkspaceRetention_WorkspaceMismatch confirms the standard
// daemon-token workspace-ownership check applies here like every other
// daemon-facing endpoint.
func TestCheckAgentWorkspaceRetention_WorkspaceMismatch(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/workspaces/"+testWorkspaceID+"/agent-workspace-retention/check",
		map[string]any{"agent_ids": []string{}}, "00000000-0000-0000-0000-000000000000", "test-daemon-mdt")
	req = withURLParam(req, "workspaceId", testWorkspaceID)

	testHandler.CheckAgentWorkspaceRetention(w, req)
	if w.Code != 404 {
		t.Fatalf("expected 404 for workspace-mismatched daemon token, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCheckAgentWorkspaceRetention_RejectsMalformedAgentID guards against a
// malformed ID silently dropping out of the batch instead of failing loud.
func TestCheckAgentWorkspaceRetention_RejectsMalformedAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	w := httptest.NewRecorder()
	req := newDaemonTokenRequest("POST", "/api/daemon/workspaces/"+testWorkspaceID+"/agent-workspace-retention/check",
		map[string]any{"agent_ids": []string{"not-a-uuid"}}, testWorkspaceID, "test-daemon-mdt")
	req = withURLParam(req, "workspaceId", testWorkspaceID)

	testHandler.CheckAgentWorkspaceRetention(w, req)
	if w.Code != 400 {
		t.Fatalf("expected 400 for malformed agent_id, got %d: %s", w.Code, w.Body.String())
	}
}

// TestCheckAgentWorkspaceRetention_OtherWorkspaceAgentExcluded proves the
// query's workspace_id scope: an archived agent belonging to a different
// workspace must never be reported eligible just because its ID appears in
// this daemon's batch (e.g. a copy-paste bug on the daemon side must not
// leak cross-workspace archival state or, worse, get a foreign agent's
// directory deleted).
func TestCheckAgentWorkspaceRetention_OtherWorkspaceAgentExcluded(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	var foreignWorkspaceID, foreignUserID, foreignRuntimeID, foreignAgentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Retention Foreign Owner', 'retention-foreign-owner@example.test')
		RETURNING id::text
	`).Scan(&foreignUserID); err != nil {
		t.Fatalf("create foreign user: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Retention Other Workspace', 'retention-other-workspace', '', 'ROW')
		RETURNING id::text
	`).Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, foreignUserID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'owner')
	`, foreignWorkspaceID, foreignUserID); err != nil {
		t.Fatalf("add foreign member: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, owner_id, last_seen_at)
		VALUES ($1, 'Retention Foreign Runtime', 'cloud', 'claude', 'online', 'test', $2, now())
		RETURNING id::text
	`, foreignWorkspaceID, foreignUserID).Scan(&foreignRuntimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config, model)
		VALUES ($1, 'retention_foreign_agent', 'Retention Foreign Agent', '', 'cloud', '{}'::jsonb, $2, 1, $3, '', '{}'::jsonb, '[]'::jsonb, '{}'::jsonb, 'composer-1.5')
		RETURNING id::text
	`, foreignWorkspaceID, foreignRuntimeID, foreignUserID).Scan(&foreignAgentID); err != nil {
		t.Fatalf("create foreign agent: %v", err)
	}
	archiveHandlerTestAgentAt(t, foreignAgentID, time.Now().AddDate(0, 0, -60))

	resp := checkAgentWorkspaceRetention(t, []string{foreignAgentID})
	if containsID(eligibleIDs(t, resp), foreignAgentID) {
		t.Fatalf("an archived agent from a different workspace must not be eligible, got %+v", resp)
	}
}
