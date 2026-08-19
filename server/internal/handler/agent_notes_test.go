package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
