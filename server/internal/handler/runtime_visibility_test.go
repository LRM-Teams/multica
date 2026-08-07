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

// runtimeVisibilityRetirementFixture creates a runtime that still carries the
// legacy private storage value plus two ordinary members. The column remains
// during the compatibility window, but no API or authorization path may use it.
func runtimeVisibilityRetirementFixture(t *testing.T) (runtimeID, runtimeOwnerID, plainMemberID string) {
	t.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()
	runtimeOwnerID = createWorkspaceMemberUser(t, "Runtime Owner "+suffix, "runtime-owner-"+suffix+"@multica.test")
	plainMemberID = createWorkspaceMemberUser(t, "Plain Runtime Member "+suffix, "runtime-member-"+suffix+"@multica.test")

	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, owner_id, visibility, last_seen_at
		)
		VALUES ($1, NULL, 'Legacy Private Runtime', 'cloud', 'visibility_retirement', 'online',
		        'visibility retirement', '{}'::jsonb, $2, 'private', now())
		RETURNING id
	`, testWorkspaceID, runtimeOwnerID).Scan(&runtimeID); err != nil {
		t.Fatalf("create legacy private runtime: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})
	return runtimeID, runtimeOwnerID, plainMemberID
}

func foreignRuntimeForVisibilityRetirement(t *testing.T) string {
	t.Helper()
	ctx := context.Background()
	raw := strings.ReplaceAll(uuid.NewString(), "-", "")
	slug := "runtime-list-isolation-" + raw[:10]
	prefix := "R" + strings.ToUpper(raw[:6])

	var workspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Runtime List Isolation', $1, 'LRM-1421 cross-workspace regression', $2)
		RETURNING id
	`, slug, prefix).Scan(&workspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
	})

	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, visibility, last_seen_at
		)
		VALUES ($1, NULL, 'Foreign Legacy Private Runtime', 'cloud', 'visibility_retirement',
		        'online', 'foreign runtime', '{}'::jsonb, 'private', now())
		RETURNING id
	`, workspaceID).Scan(&runtimeID); err != nil {
		t.Fatalf("create foreign runtime: %v", err)
	}
	return runtimeID
}

func TestListAgentRuntimes_ReturnsAllWorkspaceRuntimesWithoutVisibility(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	workspaceRuntimeID, _, plainMemberID := runtimeVisibilityRetirementFixture(t)
	foreignRuntimeID := foreignRuntimeForVisibilityRetirement(t)

	w := httptest.NewRecorder()
	testHandler.ListAgentRuntimes(w, newRequestAs(plainMemberID, http.MethodGet, "/api/runtimes", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("ListAgentRuntimes: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var rows []map[string]json.RawMessage
	if err := json.NewDecoder(w.Body).Decode(&rows); err != nil {
		t.Fatalf("decode runtimes: %v", err)
	}
	foundWorkspaceRuntime := false
	for _, row := range rows {
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil {
			t.Fatalf("decode runtime id: %v", err)
		}
		if _, present := row["visibility"]; present {
			t.Fatalf("retired visibility field leaked in runtime %s", id)
		}
		if id == workspaceRuntimeID {
			foundWorkspaceRuntime = true
		}
		if id == foreignRuntimeID {
			t.Fatalf("foreign workspace runtime %s leaked into response", id)
		}
	}
	if !foundWorkspaceRuntime {
		t.Fatalf("same-workspace runtime %s was filtered by legacy visibility", workspaceRuntimeID)
	}
}

func TestCreateAgent_AllowsAnyWorkspaceRuntime(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	runtimeID, _, plainMemberID := runtimeVisibilityRetirementFixture(t)
	// LRM-2343: agent creation is gated behind the unified `manageAgents`
	// capability (workspace owner/admin). Promote the actor to admin so the
	// test keeps exercising the original runtime-visibility intent: creating an
	// agent on a teammate runtime is never blocked by the retired `visibility`
	// column. A plain member is covered by TestCreateAgent_RequiresManageAgents.
	if _, err := testPool.Exec(context.Background(),
		`UPDATE member SET role = 'admin' WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID, plainMemberID); err != nil {
		t.Fatalf("promote create actor to owner: %v", err)
	}
	name := "visibility-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE workspace_id = $1 AND name = $2`, testWorkspaceID, name)
	})

	body := map[string]any{
		"name":                 name,
		"display_name":         name,
		"description":          "",
		"runtime_id":           runtimeID,
		"model":                "composer-1.5",
		"visibility":           "private",
		"max_concurrent_tasks": 1,
	}
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, newRequestAs(plainMemberID, http.MethodPost, "/api/agents", body))
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateAgent on teammate runtime: expected 201, got %d: %s", w.Code, w.Body.String())
	}
}

func TestUpdateAgentRuntime_IgnoresRetiredVisibilityAndOmitsItFromResponse(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	runtimeID, runtimeOwnerID, _ := runtimeVisibilityRetirementFixture(t)
	w := httptest.NewRecorder()
	req := newRequestAs(runtimeOwnerID, http.MethodPatch, "/api/runtimes/"+runtimeID, map[string]any{
		"visibility": "public",
	})
	req = withURLParam(req, "runtimeId", runtimeID)
	testHandler.UpdateAgentRuntime(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH retired visibility: expected 200 compatibility no-op, got %d: %s", w.Code, w.Body.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if _, present := raw["visibility"]; present {
		t.Fatalf("response unexpectedly contains retired visibility: %s", w.Body.String())
	}

	var stored string
	if err := testPool.QueryRow(context.Background(), `SELECT visibility FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&stored); err != nil {
		t.Fatalf("read compatibility column: %v", err)
	}
	if stored != "private" {
		t.Fatalf("retired visibility patch mutated compatibility column: got %q", stored)
	}
}
