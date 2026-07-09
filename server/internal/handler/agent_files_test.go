package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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

func TestListAgentFilesOwnerOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "agent-files-owner-only", nil)

	for _, tc := range []struct {
		name   string
		userID string
		want   int
	}{
		{name: "owner", userID: testUserID, want: http.StatusOK},
		{name: "member", userID: createAgentFilesTestMember(t, "member"), want: http.StatusForbidden},
		{name: "admin", userID: createAgentFilesTestMember(t, "admin"), want: http.StatusForbidden},
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

func TestAgentFileContentOwnerOnly(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
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
