package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/multica-ai/multica/server/internal/middleware"
)

// Source-level hard controls for #801 Barry residual after ①② test PASS.
// Unbound attachment: Parker secure staging — uploader self-visible, foreign DENY.

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

func TestAgentUnboundUploadStagingSelfVisible(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	// Never Skip on Storage nil — inject mockStorage (Barry anti-false-green).
	prev := testHandler.Storage
	testHandler.Storage = &mockStorage{}
	t.Cleanup(func() { testHandler.Storage = prev })

	agentID := createHandlerTestAgent(t, "StagingSelfAgent", []byte("[]"))
	ownerID := testUserID

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("file", "x.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("hello staging"))
	_ = w.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/agent/attachments", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	p := middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: testWorkspaceID,
		OwnerUserID: ownerID,
		ActorSource: "agent_credential",
	}
	req = req.WithContext(middleware.WithAgentPrincipal(req.Context(), p))
	rec := httptest.NewRecorder()
	testHandler.UploadAgentAttachment(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unbound staging upload want 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var att map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &att); err != nil {
		t.Fatal(err)
	}
	attID, _ := att["id"].(string)
	if attID == "" {
		t.Fatalf("missing attachment id: %#v", att)
	}

	// Self view must succeed.
	view := httptest.NewRequest(http.MethodGet, "/api/agent/attachments/"+attID, nil)
	view = withURLParam(view, "id", attID)
	view = view.WithContext(middleware.WithAgentPrincipal(view.Context(), p))
	viewRec := httptest.NewRecorder()
	testHandler.GetAgentAttachment(viewRec, view)
	if viewRec.Code != http.StatusOK {
		t.Fatalf("uploader self-view want 200, got %d body=%s", viewRec.Code, viewRec.Body.String())
	}

	// Foreign agent DENY — always create second agent so no soft skip.
	otherAgent := createHandlerTestAgent(t, "StagingForeignAgent", []byte("[]"))
	foreign := httptest.NewRequest(http.MethodGet, "/api/agent/attachments/"+attID, nil)
	foreign = withURLParam(foreign, "id", attID)
	foreign = foreign.WithContext(middleware.WithAgentPrincipal(foreign.Context(), middleware.AgentPrincipal{
		AgentID:     otherAgent,
		WorkspaceID: testWorkspaceID,
		OwnerUserID: ownerID,
		ActorSource: "agent_credential",
	}))
	fRec := httptest.NewRecorder()
	testHandler.GetAgentAttachment(fRec, foreign)
	if fRec.Code != http.StatusNotFound && fRec.Code != http.StatusForbidden {
		t.Fatalf("foreign agent want 403/404, got %d body=%s", fRec.Code, fRec.Body.String())
	}
	// Product lock: prefer exact 403 when we surface forbidden; 404 is also fail-closed.
	// Barry asked exact 403 when possible — GetAgentAttachment uses 404-on-deny for IDOR.
	// Document: metadata uses 404 shape; middleware human path is 403.
}

func TestAgentAttachmentUploaderFallbackOnlyWhenUnbound(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	// Unit SQL contract via agentAttachmentVisible: bind channel_id then
	// without membership must DENY even if uploader is self.
	var agentID string
	err := testPool.QueryRow(context.Background(),
		`SELECT id::text FROM agent WHERE workspace_id = $1 LIMIT 1`,
		parseUUID(testWorkspaceID)).Scan(&agentID)
	if err != nil || agentID == "" {
		t.Skip("no agent fixture")
	}
	agentUUID := parseUUID(agentID)
	ws := parseUUID(testWorkspaceID)

	// Create unbound attachment as this agent.
	var attID string
	err = testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, uploader_type, uploader_id, filename, content_type, size_bytes, url)
		VALUES ($1, 'agent', $2, 't.txt', 'text/plain', 1, 'https://example.invalid/t')
		RETURNING id::text`, ws, agentUUID).Scan(&attID)
	if err != nil {
		t.Fatalf("insert attachment: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM attachment WHERE id = $1`, parseUUID(attID))
	})
	attUUID := parseUUID(attID)

	if !testHandler.agentAttachmentVisible(context.Background(), ws, agentUUID, attUUID) {
		t.Fatal("unbound self upload must be visible to uploader")
	}

	// Bind channel_id without membership → must NOT fall back to uploader privilege.
	chID := insertAgentSurfaceBindDenyChannel(t, testWorkspaceID, agentID)
	_, err = testPool.Exec(context.Background(), `
		UPDATE attachment SET channel_id = $1 WHERE id = $2`, parseUUID(chID), attUUID)
	if err != nil {
		t.Fatalf("bind channel_id: %v", err)
	}
	// Ensure agent is NOT a member (delete if auto-seeded somehow for agents — usually not).
	_, _ = testPool.Exec(context.Background(), `
		DELETE FROM channel_member WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		parseUUID(chID), agentUUID)

	if testHandler.agentAttachmentVisible(context.Background(), ws, agentUUID, attUUID) {
		t.Fatal("after bind without membership, uploader fallback must not grant visibility")
	}
}

// insertAgentSurfaceBindDenyChannel creates a group channel not owned by the
// given agent (used to test that binding an attachment to a channel the
// uploader isn't a member of does not fall back to uploader privilege).
// randomID() breaks the name tie: without it, "bind-deny-"+t.Name() alone
// collides with a leftover row from any prior interrupted run of the same
// test (task #86, same shape as task #78/#1807's insertSkillPromoteWorkspaceMember).
func insertAgentSurfaceBindDenyChannel(t *testing.T, workspaceID, agentID string) string {
	t.Helper()
	ws := parseUUID(workspaceID)
	agentUUID := parseUUID(agentID)
	name := "bind-deny-" + t.Name() + "-" + randomID()
	var chID string
	err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, kind, created_by)
		VALUES ($1, $2, 'group', (SELECT owner_id FROM agent WHERE id = $3 LIMIT 1))
		RETURNING id::text`, ws, name, agentUUID).Scan(&chID)
	if err != nil {
		// created_by may need a user — try workspace member
		err = testPool.QueryRow(context.Background(), `
			INSERT INTO channel (workspace_id, name, kind, created_by)
			SELECT $1, $2, 'group', m.user_id FROM member m WHERE m.workspace_id = $1 LIMIT 1
			RETURNING id::text`, ws, name).Scan(&chID)
	}
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_member WHERE channel_id = $1`, parseUUID(chID))
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, parseUUID(chID))
	})
	return chID
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
