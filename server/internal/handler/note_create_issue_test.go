package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestCreateNotePageIssueCreatesIssueAndRef(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Source note "+uuid.NewString())
	if _, err := testPool.Exec(context.Background(), `
		UPDATE note_page SET content = $2 WHERE id = $1
	`, noteID, "Body that should become description context."); err != nil {
		t.Fatalf("set note content: %v", err)
	}

	desc := "Selected excerpt for the issue body."
	req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issues", map[string]any{
		"title":       "From note selection",
		"description": desc,
	}), "id", noteID)
	rec := httptest.NewRecorder()
	testHandler.CreateNotePageIssue(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateNotePageIssue: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp NoteCreateIssueResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Issue.ID == "" || resp.Issue.Title != "From note selection" || resp.Ref.ID != resp.Issue.ID || !resp.Ref.Accessible {
		t.Fatalf("response = %#v", resp)
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageIssueRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/issue-refs", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list refs: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Refs) != 1 || listed.Refs[0].ID != resp.Issue.ID {
		t.Fatalf("listed = %#v, want linked issue %s", listed.Refs, resp.Issue.ID)
	}

	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, resp.Issue.ID)
	})
}

func TestCreateNotePageIssueFallsBackToNoteTitle(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	title := "Fallback title note " + uuid.NewString()
	noteID := createNotePageForAITest(t, title)
	req := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issues", map[string]any{}), "id", noteID)
	rec := httptest.NewRecorder()
	testHandler.CreateNotePageIssue(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp NoteCreateIssueResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Issue.Title != title {
		t.Fatalf("issue title = %q, want note title %q", resp.Issue.Title, title)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, resp.Issue.ID)
	})
}

func TestCreateNotePageIssueRequiresNoteAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Private create-issue "+uuid.NewString())

	var outsiderID string
	email := "note-create-issue-outsider-" + uuid.NewString() + "@multica.test"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Note Create Issue Outsider', $1) RETURNING id
	`, email).Scan(&outsiderID); err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, outsiderID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, outsiderID); err != nil {
		t.Fatalf("add outsider: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.CreateNotePageIssue(rec, withURLParam(newRequestAs(outsiderID, http.MethodPost, "/api/notes/pages/"+noteID+"/issues", map[string]any{
		"title": "Should fail",
	}), "id", noteID))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("outsider create: expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}
