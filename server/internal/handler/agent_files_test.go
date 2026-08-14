package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func createAgentFilesTestMember(t *testing.T, role string) string {
	t.Helper()
	userID := uuid.NewString()
	email := "agent-files-" + userID + "@example.com"
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO "user" (id, name, email) VALUES ($1, $2, $3)
	`, userID, "Agent Files "+role, email); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}

func TestListAgentFilesOwnerOrAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("MULTICA_DEV_AGENT_PROFILE_ACCESS", "false")
	agentID := createHandlerTestAgent(t, "agent-files-owner-or-admin", nil)

	for _, tc := range []struct {
		name   string
		userID string
		want   int
	}{
		{name: "owner", userID: testUserID, want: http.StatusOK},
		{name: "member", userID: createAgentFilesTestMember(t, "member"), want: http.StatusForbidden},
		{name: "admin", userID: createAgentFilesTestMember(t, "admin"), want: http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := withURLParam(newRequestAs(tc.userID, http.MethodGet, "/api/agents/"+agentID+"/files", nil), "id", agentID)
			w := httptest.NewRecorder()
			testHandler.ListAgentFiles(w, req)
			if w.Code != tc.want {
				t.Fatalf("expected %d, got %d: %s", tc.want, w.Code, w.Body.String())
			}
			if tc.want == http.StatusOK {
				var resp AgentFilesResponse
				if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if resp.AgentID != agentID {
					t.Fatalf("agent_id = %q, want %q", resp.AgentID, agentID)
				}
				if resp.Status == "" {
					t.Fatalf("expected status in response")
				}
			}
		})
	}
}

func TestListAgentFilesDevProfileAccessAllowsWorkspaceMembers(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("MULTICA_DEV_AGENT_PROFILE_ACCESS", "true")
	agentID := createHandlerTestAgent(t, "agent-files-dev-profile-access", nil)

	for _, tc := range []struct {
		name   string
		userID string
	}{
		{name: "member", userID: createAgentFilesTestMember(t, "member")},
		{name: "admin", userID: createAgentFilesTestMember(t, "admin")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := withURLParam(newRequestAs(tc.userID, http.MethodGet, "/api/agents/"+agentID+"/files", nil), "id", agentID)
			w := httptest.NewRecorder()
			testHandler.ListAgentFiles(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("expected dev member read 200, got %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func TestAgentFileContentRefusesSecretPaths(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("MULTICA_DEV_AGENT_PROFILE_ACCESS", "false")
	agentID := createHandlerTestAgent(t, "agent-files-secret-preview", nil)

	for _, path := range []string{".env", "api-token.json", "my-secret.md", "db-credentials.yaml", ".ssh/id_rsa"} {
		req := withURLParam(newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/files/content?path="+url.QueryEscape(path), nil), "id", agentID)
		w := httptest.NewRecorder()
		testHandler.GetAgentFileContent(w, req)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("path %q: expected 400, got %d: %s", path, w.Code, w.Body.String())
		}
	}
}

func TestAgentFileContentOwnerOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("MULTICA_DEV_AGENT_PROFILE_ACCESS", "false")
	agentID := createHandlerTestAgent(t, "agent-files-content-owner-only", nil)
	memberID := createAgentFilesTestMember(t, "member")

	req := withURLParam(newRequestAs(memberID, http.MethodGet, "/api/agents/"+agentID+"/files/content?path=memory/MEMORY.md", nil), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentFileContent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected non-owner read 403, got %d: %s", w.Code, w.Body.String())
	}

	req = withURLParam(newRequestAs(memberID, http.MethodPut, "/api/agents/"+agentID+"/files/content", map[string]any{
		"path":                  "memory/MEMORY.md",
		"content":               "updated",
		"expected_content_hash": "old",
	}), "id", agentID)
	w = httptest.NewRecorder()
	testHandler.UpdateAgentFileContent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected non-owner write 403, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAgentFileContentAllowsWorkspaceAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("MULTICA_DEV_AGENT_PROFILE_ACCESS", "false")
	agentID := createHandlerTestAgent(t, "agent-files-content-admin", nil)
	adminID := createAgentFilesTestMember(t, "admin")

	req := withURLParam(newRequestAs(adminID, http.MethodGet, "/api/agents/"+agentID+"/files/content?path=memory/MEMORY.md", nil), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentFileContent(w, req)
	if w.Code == http.StatusForbidden {
		t.Fatalf("expected admin read to pass auth, got 403: %s", w.Body.String())
	}

	req = withURLParam(newRequestAs(adminID, http.MethodPut, "/api/agents/"+agentID+"/files/content", map[string]any{
		"path":                  "memory/MEMORY.md",
		"content":               "updated",
		"expected_content_hash": "old",
	}), "id", agentID)
	w = httptest.NewRecorder()
	testHandler.UpdateAgentFileContent(w, req)
	if w.Code == http.StatusForbidden {
		t.Fatalf("expected admin write to pass auth, got 403: %s", w.Body.String())
	}
}

func TestAgentFileContentDevProfileAccessAllowsReadNotWrite(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	t.Setenv("MULTICA_DEV_AGENT_PROFILE_ACCESS", "true")
	agentID := createHandlerTestAgent(t, "agent-files-content-dev-profile-access", nil)
	memberID := createAgentFilesTestMember(t, "member")

	req := withURLParam(newRequestAs(memberID, http.MethodGet, "/api/agents/"+agentID+"/files/content?path=memory/MEMORY.md", nil), "id", agentID)
	w := httptest.NewRecorder()
	testHandler.GetAgentFileContent(w, req)
	if w.Code == http.StatusForbidden {
		t.Fatalf("expected dev member read to pass auth, got 403: %s", w.Body.String())
	}

	req = withURLParam(newRequestAs(memberID, http.MethodPut, "/api/agents/"+agentID+"/files/content", map[string]any{
		"path":                  "memory/MEMORY.md",
		"content":               "updated",
		"expected_content_hash": "old",
	}), "id", agentID)
	w = httptest.NewRecorder()
	testHandler.UpdateAgentFileContent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected dev member write 403, got %d: %s", w.Code, w.Body.String())
	}
}
