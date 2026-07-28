package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
)

// Source-level hard controls for #801 Barry residual after ①② test PASS.

func TestAgentDirectoryIsNarrowDTO(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	var agentID, ownerID string
	err := testPool.QueryRow(context.Background(),
		`SELECT id::text, COALESCE(owner_id::text, '') FROM agent WHERE workspace_id = $1 LIMIT 1`,
		parseUUID(testWorkspaceID)).Scan(&agentID, &ownerID)
	if err != nil || agentID == "" {
		t.Skip("no agent fixture")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agent/agents", nil)
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: testWorkspaceID,
		OwnerUserID: ownerID,
		ActorSource: "agent_credential",
	}))
	rec := httptest.NewRecorder()
	testHandler.ListAgentDirectoryAgents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("directory status=%d body=%s", rec.Code, rec.Body.String())
	}
	var items []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode: %v body=%s", err, rec.Body.String())
	}
	for _, it := range items {
		for _, forbidden := range []string{
			"instructions", "runtime_config", "custom_args", "skills",
			"mcp_config", "custom_env", "owner_id", "runtime_id",
		} {
			if _, ok := it[forbidden]; ok {
				t.Fatalf("directory item leaked field %q: %#v", forbidden, it)
			}
		}
		if _, ok := it["id"]; !ok {
			t.Fatalf("directory item missing id: %#v", it)
		}
		if _, ok := it["name"]; !ok {
			t.Fatalf("directory item missing name: %#v", it)
		}
	}
}

func TestAgentUnboundUploadRejected(t *testing.T) {
	if testHandler == nil {
		t.Skip("handler test DB not configured")
	}
	if testHandler.Storage == nil {
		t.Skip("storage not configured on testHandler")
	}
	var agentID, ownerID string
	if testPool != nil {
		_ = testPool.QueryRow(context.Background(),
			`SELECT id::text, COALESCE(owner_id::text, '') FROM agent WHERE workspace_id = $1 LIMIT 1`,
			parseUUID(testWorkspaceID)).Scan(&agentID, &ownerID)
	}
	if agentID == "" {
		t.Skip("no agent fixture")
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("hello"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/agent/attachments", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: testWorkspaceID,
		OwnerUserID: ownerID,
		ActorSource: "agent_credential",
	}))
	rec := httptest.NewRecorder()
	testHandler.UploadAgentAttachment(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unbound upload want 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "issue_id") && !strings.Contains(rec.Body.String(), "channel_id") {
		t.Fatalf("expected provenance error, body=%s", rec.Body.String())
	}
}

func TestRejectAgentOnHumanAPIMiddleware(t *testing.T) {
	h := middleware.RejectAgentOnHumanAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/projects", nil)
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), middleware.AgentPrincipal{
		AgentID:     "00000000-0000-0000-0000-000000000001",
		WorkspaceID: "00000000-0000-0000-0000-000000000002",
		ActorSource: "agent_credential",
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("human path want 403, got %d", rec.Code)
	}

	passed := false
	h2 := middleware.RejectAgentOnHumanAPI(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		passed = true
		w.WriteHeader(http.StatusNoContent)
	}))
	req2 := httptest.NewRequest(http.MethodGet, "/api/agent/workspace", nil)
	req2 = req2.WithContext(middleware.WithAgentPrincipal(req2.Context(), middleware.AgentPrincipal{
		AgentID:     "00000000-0000-0000-0000-000000000001",
		WorkspaceID: "00000000-0000-0000-0000-000000000002",
		ActorSource: "agent_credential",
	}))
	rec2 := httptest.NewRecorder()
	h2.ServeHTTP(rec2, req2)
	if !passed || rec2.Code != http.StatusNoContent {
		t.Fatalf("agent path should pass middleware, code=%d passed=%v", rec2.Code, passed)
	}
}
