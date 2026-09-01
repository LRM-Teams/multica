package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
)

func withAgentTaskPrincipal(r *http.Request, agentID, workspaceID, ownerUserID, taskID string) *http.Request {
	p := middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		OwnerUserID: ownerUserID,
		ActorSource: "task_token",
		TaskID:      taskID,
	}
	r = r.WithContext(middleware.WithAgentPrincipal(r.Context(), p))
	r.Header.Set("X-User-ID", ownerUserID)
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Task-ID", taskID)
	r.Header.Set("X-Actor-Source", "task_token")
	r.Header.Set("X-Workspace-ID", workspaceID)
	return r
}

// Durable agent_credential has no task scope (Pi/local collectors often use this).
func withAgentCredentialPrincipal(r *http.Request, agentID, workspaceID, ownerUserID string) *http.Request {
	p := middleware.AgentPrincipal{
		AgentID:     agentID,
		WorkspaceID: workspaceID,
		OwnerUserID: ownerUserID,
		ActorSource: "agent_credential",
	}
	r = r.WithContext(middleware.WithAgentPrincipal(r.Context(), p))
	r.Header.Set("X-User-ID", ownerUserID)
	r.Header.Set("X-Agent-ID", agentID)
	r.Header.Set("X-Actor-Source", "agent_credential")
	r.Header.Set("X-Workspace-ID", workspaceID)
	return r
}

func TestGetAgentNotePageAllowsWorkerBriefPage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Agent Notes Allow "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Agent readable note "+uuid.NewString())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = $1 WHERE id = $2`, "secret brief body", noteID); err != nil {
		t.Fatalf("set content: %v", err)
	}

	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "use the note",
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob: %d %s", createRec.Code, createRec.Body.String())
	}
	var job NoteWorkerJobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.TaskID == nil {
		t.Fatal("expected task_id")
	}

	req := withURLParam(withAgentTaskPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+noteID, nil),
		agentID, testWorkspaceID, testUserID, *job.TaskID,
	), "id", noteID)
	rec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetAgentNotePage: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.ID != noteID || page.Content != "secret brief body" || page.CanManageShares {
		t.Fatalf("page = %#v", page)
	}
}

func TestGetAgentNotePageAllowsActiveWorkerJobWithoutTaskID(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Agent Notes Cred "+uuid.NewString()[:8], nil)
	noteID := createNotePageForAITest(t, "Credential-readable note "+uuid.NewString())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = $1 WHERE id = $2`, "credential brief body", noteID); err != nil {
		t.Fatalf("set content: %v", err)
	}

	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "use the note without task token",
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob: %d %s", createRec.Code, createRec.Body.String())
	}

	req := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+noteID, nil),
		agentID, testWorkspaceID, testUserID,
	), "id", noteID)
	rec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GetAgentNotePage with agent_credential: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if page.ID != noteID || page.Content != "credential brief body" {
		t.Fatalf("page = %#v", page)
	}
}

func TestGetAgentNotePageAllowsPeriodBriefDraftOutsideSubtree(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Residue Draft "+uuid.NewString()[:8], nil)
	rootID := createNotePageForAITest(t, "Bubble root "+uuid.NewString())
	draftID := createNotePageForAITest(t, "工作介绍 底稿 "+uuid.NewString())
	folderID := createNotePageForAITest(t, "工作介绍 "+uuid.NewString())
	sessionID := createHandlerTestChatSession(t, agentID)
	if _, err := testPool.Exec(context.Background(), `
UPDATE chat_session SET context_note_page_id = $1 WHERE id = $2`, rootID, sessionID); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_page SET content = 'draft brief body' WHERE id = $1`, draftID); err != nil {
		t.Fatalf("set draft body: %v", err)
	}
	insertPeriodBriefFixtureRun(t, rootID, folderID, agentID, draftID, "done", time.Now().UTC())
	if _, err := testPool.Exec(context.Background(), `
UPDATE note_period_brief_run SET chat_session_id = $1 WHERE draft_page_id = $2`, sessionID, draftID); err != nil {
		t.Fatalf("bind run: %v", err)
	}

	draftReq := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+draftID, nil),
		agentID, testWorkspaceID, testUserID,
	), "id", draftID)
	draftRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(draftRec, draftReq)
	if draftRec.Code != http.StatusOK {
		t.Fatalf("draft get: expected 200, got %d: %s", draftRec.Code, draftRec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(draftRec.Body).Decode(&page); err != nil {
		t.Fatalf("decode draft: %v", err)
	}
	if page.ID != draftID || page.Content != "draft brief body" {
		t.Fatalf("draft page = %#v", page)
	}

	outsider := createNotePageForAITest(t, "Unrelated "+uuid.NewString())
	denyReq := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+outsider, nil),
		agentID, testWorkspaceID, testUserID,
	), "id", outsider)
	denyRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(denyRec, denyReq)
	if denyRec.Code != http.StatusNotFound {
		t.Fatalf("unrelated page: expected 404, got %d: %s", denyRec.Code, denyRec.Body.String())
	}
}

func TestGetAgentNotePageRejectsUnauthorizedPage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Agent Notes Deny "+uuid.NewString()[:8], nil)
	briefNoteID := createNotePageForAITest(t, "Brief note "+uuid.NewString())
	otherNoteID := createNotePageForAITest(t, "Other private note "+uuid.NewString())

	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+briefNoteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "stay on brief",
	}), "id", briefNoteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob: %d %s", createRec.Code, createRec.Body.String())
	}
	var job NoteWorkerJobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}

	req := withURLParam(withAgentTaskPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+otherNoteID, nil),
		agentID, testWorkspaceID, testUserID, *job.TaskID,
	), "id", otherNoteID)
	rec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unauthorized page: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestGetAgentNotePageRejectsWhenCreatorLosesAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Agent Notes Share "+uuid.NewString()[:8], nil)
	ownerID := createWorkspaceMemberForNoteACL(t, "note-agent-owner")
	collaboratorID := testUserID // Worker creator

	var noteID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
		VALUES ($1, $2, $3, 'shared body', '00000000000000000001', $2, $2)
		RETURNING id
	`, testWorkspaceID, ownerID, "Shared for agent "+uuid.NewString()).Scan(&noteID); err != nil {
		t.Fatalf("create note: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, noteID) })
	shareNoteWithUser(t, noteID, collaboratorID)

	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "read shared note",
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob: %d %s", createRec.Code, createRec.Body.String())
	}
	var job NoteWorkerJobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}

	okReq := withURLParam(withAgentTaskPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+noteID, nil),
		agentID, testWorkspaceID, testUserID, *job.TaskID,
	), "id", noteID)
	okRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(okRec, okReq)
	if okRec.Code != http.StatusOK {
		t.Fatalf("shared access: expected 200, got %d: %s", okRec.Code, okRec.Body.String())
	}

	if _, err := testPool.Exec(context.Background(), `
DELETE FROM note_page_share WHERE page_id = $1 AND user_id = $2`, noteID, collaboratorID); err != nil {
		t.Fatalf("revoke share: %v", err)
	}

	denyReq := withURLParam(withAgentTaskPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+noteID, nil),
		agentID, testWorkspaceID, testUserID, *job.TaskID,
	), "id", noteID)
	denyRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(denyRec, denyReq)
	if denyRec.Code != http.StatusNotFound {
		t.Fatalf("revoked share: expected 404, got %d: %s", denyRec.Code, denyRec.Body.String())
	}
}

func TestGetAgentNotePageAllowsNoteChatSessionSubtree(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Note Chat Bubble "+uuid.NewString()[:8], nil)
	rootID := createNotePageForAITest(t, "Bubble root "+uuid.NewString())
	var childID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, 'child body', '00000000000000000002', $3, $3)
RETURNING id`, testWorkspaceID, rootID, testUserID, "Bubble child "+uuid.NewString()).Scan(&childID); err != nil {
		t.Fatalf("create child: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, childID) })

	createRec := httptest.NewRecorder()
	createReq := newRequest(http.MethodPost, "/api/chat/sessions", map[string]any{
		"agent_id":             agentID,
		"title":                "note bubble",
		"context_note_page_id": rootID,
	})
	createReq = withChatTestWorkspaceCtx(t, createReq)
	testHandler.CreateChatSession(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateChatSession: %d %s", createRec.Code, createRec.Body.String())
	}
	var session ChatSessionResponse
	if err := json.NewDecoder(createRec.Body).Decode(&session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if session.ContextNotePageID != rootID {
		t.Fatalf("context_note_page_id = %q, want %s", session.ContextNotePageID, rootID)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id = $1`, session.ID)
	})

	childReq := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+childID, nil),
		agentID, testWorkspaceID, testUserID,
	), "id", childID)
	childRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(childRec, childReq)
	if childRec.Code != http.StatusOK {
		t.Fatalf("child get: expected 200, got %d: %s", childRec.Code, childRec.Body.String())
	}

	treeReq := withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+rootID+"/tree", nil),
		agentID, testWorkspaceID, testUserID,
	), "id", rootID)
	treeRec := httptest.NewRecorder()
	testHandler.ListAgentNoteTree(treeRec, treeReq)
	if treeRec.Code != http.StatusOK {
		t.Fatalf("tree: expected 200, got %d: %s", treeRec.Code, treeRec.Body.String())
	}
	var tree struct {
		Pages []struct {
			ID    string `json:"id"`
			Depth int    `json:"depth"`
		} `json:"pages"`
	}
	if err := json.NewDecoder(treeRec.Body).Decode(&tree); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	if len(tree.Pages) < 2 {
		t.Fatalf("tree pages = %#v, want root+child", tree.Pages)
	}
}

func TestGetAgentNotePageAllowsWorkerSubtreeDescendant(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Worker Subtree "+uuid.NewString()[:8], nil)
	rootID := createNotePageForAITest(t, "Worker root "+uuid.NewString())
	var childID string
	if err := testPool.QueryRow(context.Background(), `
INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
VALUES ($1, $2, $3, $4, 'worker child', '00000000000000000002', $3, $3)
RETURNING id`, testWorkspaceID, rootID, testUserID, "Worker child "+uuid.NewString()).Scan(&childID); err != nil {
		t.Fatalf("create child: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, childID) })

	createRec := httptest.NewRecorder()
	testHandler.CreateNoteWorkerJob(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+rootID+"/worker-jobs", map[string]any{
		"agent_id":    agentID,
		"instruction": "read subtree",
	}), "id", rootID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNoteWorkerJob: %d %s", createRec.Code, createRec.Body.String())
	}
	var job NoteWorkerJobResponse
	if err := json.NewDecoder(createRec.Body).Decode(&job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.TaskID == nil {
		t.Fatal("expected task_id")
	}

	req := withURLParam(withAgentTaskPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+childID, nil),
		agentID, testWorkspaceID, testUserID, *job.TaskID,
	), "id", childID)
	rec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("descendant get: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
