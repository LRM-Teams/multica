package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createWorkspaceMemberForNoteACL(t *testing.T, namePrefix string) string {
	t.Helper()
	ctx := context.Background()
	var userID string
	name := namePrefix + "-" + uuid.NewString()
	email := name + "@multica.test"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ($1, $2) RETURNING id
	`, name, email).Scan(&userID); err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, userID); err != nil {
		t.Fatalf("add member: %v", err)
	}
	return userID
}

func shareNoteWithUser(t *testing.T, noteID, userID string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO note_page_share (page_id, user_id, created_by)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, noteID, userID, testUserID); err != nil {
		t.Fatalf("share note: %v", err)
	}
}

func createPendingWritebackForACL(t *testing.T, noteID string) NoteWritebackResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.CreateNotePageWriteback(rec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/writebacks", map[string]any{
		"action":  "append",
		"content": "ACL probe content",
		"evidence": []any{
			map[string]any{"type": "issue", "id": uuid.NewString(), "label": "MUL-ACL"},
		},
	}), "id", noteID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create writeback: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var created NoteWritebackResponse
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode writeback: %v", err)
	}
	return created
}

func TestNoteWritebackRequiresNoteAccessForOutsider(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Private writeback ACL "+uuid.NewString())
	outsiderID := createWorkspaceMemberForNoteACL(t, "note-wb-outsider")
	created := createPendingWritebackForACL(t, noteID)

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageWritebacks(listRec, withURLParam(newRequestAs(outsiderID, http.MethodGet, "/api/notes/pages/"+noteID+"/writebacks", nil), "id", noteID))
	if listRec.Code != http.StatusNotFound {
		t.Fatalf("outsider list: expected 404, got %d: %s", listRec.Code, listRec.Body.String())
	}

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageWriteback(createRec, withURLParam(newRequestAs(outsiderID, http.MethodPost, "/api/notes/pages/"+noteID+"/writebacks", map[string]any{
		"action":  "append",
		"content": "should fail",
		"evidence": []any{
			map[string]any{"type": "issue", "id": uuid.NewString()},
		},
	}), "id", noteID))
	if createRec.Code != http.StatusNotFound {
		t.Fatalf("outsider create: expected 404, got %d: %s", createRec.Code, createRec.Body.String())
	}

	acceptRec := httptest.NewRecorder()
	testHandler.AcceptNotePageWriteback(acceptRec, withURLParam(newRequestAs(outsiderID, http.MethodPost, "/api/notes/writebacks/"+created.ID+"/accept", nil), "writebackId", created.ID))
	if acceptRec.Code != http.StatusNotFound {
		t.Fatalf("outsider accept: expected 404, got %d: %s", acceptRec.Code, acceptRec.Body.String())
	}

	rejectRec := httptest.NewRecorder()
	testHandler.RejectNotePageWriteback(rejectRec, withURLParam(newRequestAs(outsiderID, http.MethodPost, "/api/notes/writebacks/"+created.ID+"/reject", nil), "writebackId", created.ID))
	if rejectRec.Code != http.StatusNotFound {
		t.Fatalf("outsider reject: expected 404, got %d: %s", rejectRec.Code, rejectRec.Body.String())
	}
}

func TestNoteWritebackAllowsSharedCollaborator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Shared writeback ACL "+uuid.NewString())
	if _, err := testPool.Exec(ctx, `UPDATE note_page SET content = $2 WHERE id = $1`, noteID, "Shared original"); err != nil {
		t.Fatalf("set content: %v", err)
	}
	collaboratorID := createWorkspaceMemberForNoteACL(t, "note-wb-sharee")
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `UPDATE note_page SET updated_by = $2 WHERE id = $1`, noteID, testUserID)
	})
	shareNoteWithUser(t, noteID, collaboratorID)
	created := createPendingWritebackForACL(t, noteID)

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageWritebacks(listRec, withURLParam(newRequestAs(collaboratorID, http.MethodGet, "/api/notes/pages/"+noteID+"/writebacks?status=pending", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("sharee list: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed NoteWritebackListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Writebacks) != 1 || listed.Writebacks[0].ID != created.ID {
		t.Fatalf("listed = %#v", listed.Writebacks)
	}

	acceptRec := httptest.NewRecorder()
	testHandler.AcceptNotePageWriteback(acceptRec, withURLParam(newRequestAs(collaboratorID, http.MethodPost, "/api/notes/writebacks/"+created.ID+"/accept", nil), "writebackId", created.ID))
	if acceptRec.Code != http.StatusOK {
		t.Fatalf("sharee accept: expected 200, got %d: %s", acceptRec.Code, acceptRec.Body.String())
	}
	var accepted NoteWritebackResponse
	if err := json.NewDecoder(acceptRec.Body).Decode(&accepted); err != nil {
		t.Fatalf("decode accept: %v", err)
	}
	if accepted.Status != "applied" || accepted.ResolvedBy == nil || *accepted.ResolvedBy != collaboratorID {
		t.Fatalf("accepted = %#v", accepted)
	}

	var content, updatedBy string
	if err := testPool.QueryRow(ctx, `SELECT content, updated_by::text FROM note_page WHERE id = $1`, noteID).Scan(&content, &updatedBy); err != nil {
		t.Fatalf("load note: %v", err)
	}
	if content != "Shared original\n\nACL probe content" {
		t.Fatalf("content = %q", content)
	}
	if updatedBy != collaboratorID {
		t.Fatalf("updated_by = %q, want collaborator %q", updatedBy, collaboratorID)
	}
}

func TestNoteWritebackRecordsMemberActor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Actor writeback ACL "+uuid.NewString())
	created := createPendingWritebackForACL(t, noteID)
	if created.CreatedByType != "member" || created.CreatedByID != testUserID {
		t.Fatalf("actor = type=%q id=%q", created.CreatedByType, created.CreatedByID)
	}
}

func TestNotePageIssueRefDeleteRequiresNoteAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Delete ref ACL "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Delete ref issue "+uuid.NewString())
	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create ref: %d %s", createRec.Code, createRec.Body.String())
	}

	outsiderID := createWorkspaceMemberForNoteACL(t, "note-ref-delete-outsider")
	deleteRec := httptest.NewRecorder()
	testHandler.DeleteNotePageIssueRef(deleteRec, withRouteParams(
		newRequestAs(outsiderID, http.MethodDelete, "/api/notes/pages/"+noteID+"/issue-refs/"+issueID, nil),
		"id", noteID, "issueId", issueID,
	))
	if deleteRec.Code != http.StatusNotFound {
		t.Fatalf("outsider delete: expected 404, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestSharedCollaboratorCanCreateIssueFromNote(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Shared create-issue ACL "+uuid.NewString())
	collaboratorID := createWorkspaceMemberForNoteACL(t, "note-create-sharee")
	shareNoteWithUser(t, noteID, collaboratorID)

	rec := httptest.NewRecorder()
	testHandler.CreateNotePageIssue(rec, withURLParam(newRequestAs(collaboratorID, http.MethodPost, "/api/notes/pages/"+noteID+"/issues", map[string]any{
		"title": "From shared collaborator",
	}), "id", noteID))
	if rec.Code != http.StatusCreated {
		t.Fatalf("sharee create issue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp NoteCreateIssueResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, resp.Issue.ID)
	})
	if resp.Issue.CreatorID != collaboratorID || resp.Issue.CreatorType != "member" {
		t.Fatalf("issue creator = %#v", resp.Issue)
	}
}
