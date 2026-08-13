package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createIssueForNoteRefTest(t *testing.T, workspaceID, title string) (issueID string, number int32) {
	t.Helper()
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO issue (workspace_id, title, status, priority, creator_id, creator_type, number, position)
		VALUES ($1, $2, 'todo', 'none', $3, 'member',
		  (SELECT COALESCE(MAX(number), 0) + 1 FROM issue WHERE workspace_id = $1),
		  0)
		RETURNING id, number
	`, workspaceID, title, testUserID).Scan(&issueID, &number); err != nil {
		t.Fatalf("create issue: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM issue WHERE id = $1`, issueID)
	})
	return issueID, number
}

func TestNotePageIssueRefCreateListDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Issue ref note "+uuid.NewString())
	issueID, number := createIssueForNoteRefTest(t, testWorkspaceID, "Linked issue "+uuid.NewString())

	createReq := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID)
	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNotePageIssueRef: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created NotePageIssueRefResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Type != "issue" || created.ID != issueID || created.PageID != noteID || created.IssueID != issueID || !created.Accessible || created.Label == nil || *created.Label == "" || created.Number == nil || *created.Number != number || created.Identifier == "" || created.Title == "" {
		t.Fatalf("created ref = %#v", created)
	}

	listReq := withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/issue-refs", nil), "id", noteID)
	listRec := httptest.NewRecorder()
	testHandler.ListNotePageIssueRefs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListNotePageIssueRefs: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Refs) != 1 || listed.Refs[0].ID != issueID || !listed.Refs[0].Accessible {
		t.Fatalf("listed refs = %#v, want one accessible ref for %s", listed.Refs, issueID)
	}

	// Idempotent re-create returns 201 with the same link.
	createAgainRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createAgainRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if createAgainRec.Code != http.StatusCreated {
		t.Fatalf("idempotent create: expected 201, got %d: %s", createAgainRec.Code, createAgainRec.Body.String())
	}

	deleteReq := withRouteParams(newRequest(http.MethodDelete, "/api/notes/pages/"+noteID+"/issue-refs/"+issueID, nil), "id", noteID, "issueId", issueID)
	deleteRec := httptest.NewRecorder()
	testHandler.DeleteNotePageIssueRef(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("DeleteNotePageIssueRef: expected 204, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}

	listAfterRec := httptest.NewRecorder()
	testHandler.ListNotePageIssueRefs(listAfterRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/issue-refs", nil), "id", noteID))
	if listAfterRec.Code != http.StatusOK {
		t.Fatalf("list after delete: expected 200, got %d: %s", listAfterRec.Code, listAfterRec.Body.String())
	}
	var listedAfter NotePageIssueRefListResponse
	if err := json.NewDecoder(listAfterRec.Body).Decode(&listedAfter); err != nil {
		t.Fatalf("decode list after delete: %v", err)
	}
	if len(listedAfter.Refs) != 0 {
		t.Fatalf("listed after delete = %#v, want empty", listedAfter.Refs)
	}
}

func TestNotePageIssueRefRejectsForeignWorkspaceIssue(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', $3)
		RETURNING id
	`, "Note Ref Foreign WS", "note-ref-foreign-"+uuid.NewString(), "NRF").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	foreignIssueID, _ := createIssueForNoteRefTest(t, otherWorkspaceID, "Foreign issue "+uuid.NewString())
	noteID := createNotePageForAITest(t, "Local note "+uuid.NewString())

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": foreignIssueID,
	}), "id", noteID))
	if createRec.Code != http.StatusNotFound {
		t.Fatalf("create foreign issue ref: expected 404, got %d: %s", createRec.Code, createRec.Body.String())
	}
}

func TestNotePageIssueRefListMarksInaccessibleWithoutLeaking(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Mark inaccessible "+uuid.NewString())
	localIssueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Visible issue "+uuid.NewString())

	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', $3)
		RETURNING id
	`, "Note Ref Leak WS", "note-ref-leak-"+uuid.NewString(), "NRL").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create leak workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})
	foreignIssueID, _ := createIssueForNoteRefTest(t, otherWorkspaceID, "Should not leak title "+uuid.NewString())

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": localIssueID,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create local ref: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	// Inconsistent row: issue belongs to another workspace.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO note_page_issue_ref (page_id, issue_id, workspace_id, created_by)
		VALUES ($1, $2, $3, $4)
	`, noteID, foreignIssueID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed inconsistent foreign ref row: %v", err)
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageIssueRefs(listRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/issue-refs", nil), "id", noteID))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Refs) != 2 {
		t.Fatalf("listed = %#v, want accessible + inaccessible", listed.Refs)
	}

	byID := map[string]NotePageIssueRefResponse{}
	for _, ref := range listed.Refs {
		byID[ref.ID] = ref
	}
	local := byID[localIssueID]
	if !local.Accessible || local.Label == nil || local.Title == "" {
		t.Fatalf("local ref = %#v, want accessible with label/title", local)
	}
	foreign := byID[foreignIssueID]
	if foreign.Accessible || foreign.Label != nil || foreign.Title != "" || foreign.Identifier != "" {
		t.Fatalf("foreign ref = %#v, want accessible=false with no leaked fields", foreign)
	}
}

func TestGetNotePageIncludesStructuredRefs(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Detail refs "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Detail linked "+uuid.NewString())

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID))
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNotePage: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(getRec.Body).Decode(&page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Refs) != 1 || page.Refs[0].ID != issueID || !page.Refs[0].Accessible || page.Refs[0].Label == nil {
		t.Fatalf("page.refs = %#v, want one accessible structured ref", page.Refs)
	}
}

func TestNotePageIssueRefRequiresNoteAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	noteID := createNotePageForAITest(t, "Private for outsider "+uuid.NewString())
	issueID, _ := createIssueForNoteRefTest(t, testWorkspaceID, "Issue for outsider "+uuid.NewString())

	var outsiderID string
	email := "note-ref-outsider-" + uuid.NewString() + "@multica.test"
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, email) VALUES ('Note Ref Outsider', $1) RETURNING id
	`, email).Scan(&outsiderID); err != nil {
		t.Fatalf("create outsider: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, outsiderID)
	})
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role) VALUES ($1, $2, 'member')
	`, testWorkspaceID, outsiderID); err != nil {
		t.Fatalf("add outsider member: %v", err)
	}

	listRec := httptest.NewRecorder()
	testHandler.ListNotePageIssueRefs(listRec, withURLParam(newRequestAs(outsiderID, http.MethodGet, "/api/notes/pages/"+noteID+"/issue-refs", nil), "id", noteID))
	if listRec.Code != http.StatusNotFound {
		t.Fatalf("outsider list: expected 404, got %d: %s", listRec.Code, listRec.Body.String())
	}

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageIssueRef(createRec, withURLParam(newRequestAs(outsiderID, http.MethodPost, "/api/notes/pages/"+noteID+"/issue-refs", map[string]any{
		"issue_id": issueID,
	}), "id", noteID))
	if createRec.Code != http.StatusNotFound {
		t.Fatalf("outsider create: expected 404, got %d: %s", createRec.Code, createRec.Body.String())
	}
}
