package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListIssueNoteRefsReturnsAccessibleLinkedNotes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Issue reverse note "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Reverse discover "+uuid.NewString())

	linkRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(linkRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if linkRec.Code != http.StatusCreated {
		t.Fatalf("link: expected 201, got %d: %s", linkRec.Code, linkRec.Body.String())
	}

	listRec := httptest.NewRecorder()
	testHandler.ListIssueNoteRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/issues/"+issueID+"/note-refs", nil), "id", issueID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list note-refs: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed IssueNoteRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Notes) != 1 || listed.Notes[0].ID != noteID {
		t.Fatalf("notes = %#v, want [{id:%s}]", listed.Notes, noteID)
	}
	if listed.Notes[0].Title == "" || listed.Notes[0].CreatedAt == "" {
		t.Fatalf("expected title+created_at, got %#v", listed.Notes[0])
	}
}

func TestListIssueNoteRefsOmitsInaccessiblePrivateNotes(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Private reverse note "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Private reverse issue "+uuid.NewString())

	linkRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(linkRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if linkRec.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", linkRec.Code, linkRec.Body.String())
	}

	outsiderID := createWorkspaceMemberForNoteACL(t, "note-ref-outsider")
	listRec := httptest.NewRecorder()
	testHandler.ListIssueNoteRefs(listRec, withURLParam(newRequestAs(outsiderID, http.MethodGet, "/api/issues/"+issueID+"/note-refs", nil), "id", issueID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed IssueNoteRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Notes) != 0 {
		t.Fatalf("expected omitted private note, got %#v", listed.Notes)
	}
}

func TestListIssueNoteRefsAllowsSharedCollaborator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Shared reverse note "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Shared reverse issue "+uuid.NewString())

	linkRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(linkRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if linkRec.Code != http.StatusCreated {
		t.Fatalf("link: %d %s", linkRec.Code, linkRec.Body.String())
	}

	collaboratorID := createWorkspaceMemberForNoteACL(t, "note-ref-collab")
	shareNoteWithUser(t, noteID, collaboratorID)

	listRec := httptest.NewRecorder()
	testHandler.ListIssueNoteRefs(listRec, withURLParam(newRequestAs(collaboratorID, http.MethodGet, "/api/issues/"+issueID+"/note-refs", nil), "id", issueID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", listRec.Code, listRec.Body.String())
	}
	var listed IssueNoteRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(listed.Notes) != 1 || listed.Notes[0].ID != noteID {
		t.Fatalf("notes = %#v, want shared note %s", listed.Notes, noteID)
	}
}

func TestListIssueNoteRefsRequiresIssueAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	missingID := uuid.NewString()
	listRec := httptest.NewRecorder()
	testHandler.ListIssueNoteRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/issues/"+missingID+"/note-refs", nil), "id", missingID))
	if listRec.Code != http.StatusNotFound && listRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 404/400 for missing issue, got %d: %s", listRec.Code, listRec.Body.String())
	}
}
