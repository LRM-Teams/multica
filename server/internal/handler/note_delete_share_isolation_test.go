package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

func createNotePageViaAPI(t *testing.T, userID, title string, parentID *string) string {
	t.Helper()
	body := map[string]any{"title": title}
	if parentID != nil {
		body["parent_id"] = *parentID
	}
	req := newRequestAs(userID, http.MethodPost, "/api/notes/pages", body)
	rec := httptest.NewRecorder()
	testHandler.CreateNotePage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("CreateNotePage: expected 201, got %d: %s", rec.Code, rec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, page.ID) })
	return page.ID
}

func listNotePageIDs(t *testing.T, userID string) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.ListNotePages(rec, newRequestAs(userID, http.MethodGet, "/api/notes/pages", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ListNotePages: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Pages []NotePageResponse `json:"pages"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	ids := map[string]bool{}
	for _, page := range resp.Pages {
		ids[page.ID] = true
	}
	return ids
}

func listDeletedNotePageIDs(t *testing.T, userID string) map[string]bool {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.ListDeletedNotePages(rec, newRequestAs(userID, http.MethodGet, "/api/notes/pages/trash", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ListDeletedNotePages: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Pages []NotePageResponse `json:"pages"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode trash: %v", err)
	}
	ids := map[string]bool{}
	for _, page := range resp.Pages {
		ids[page.ID] = true
	}
	return ids
}

func deleteNotePageAs(t *testing.T, userID, noteID string) {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.DeleteNotePage(rec, withURLParam(newRequestAs(userID, http.MethodDelete, "/api/notes/pages/"+noteID, nil), "id", noteID))
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DeleteNotePage: expected 204, got %d: %s", rec.Code, rec.Body.String())
	}
}

func noteDeletedAt(t *testing.T, noteID string) bool {
	t.Helper()
	var deleted bool
	if err := testPool.QueryRow(context.Background(), `
SELECT deleted_at IS NOT NULL FROM note_page WHERE id = $1
`, noteID).Scan(&deleted); err != nil {
		t.Fatalf("deleted_at %s: %v", noteID, err)
	}
	return deleted
}

func TestOwnerDeleteDoesNotTrashCollaboratorChild(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	parentID := createNotePageViaAPI(t, testUserID, "Shared parent "+uuid.NewString(), nil)
	memberID := createWorkspaceMemberForNoteACL(t, "share-child-owner")
	shareNoteWithUser(t, parentID, memberID)
	childID := createNotePageViaAPI(t, memberID, "Collaborator child "+uuid.NewString(), &parentID)

	deleteNotePageAs(t, testUserID, parentID)

	if !noteDeletedAt(t, parentID) {
		t.Fatal("owner delete must trash the owned parent")
	}
	if noteDeletedAt(t, childID) {
		t.Fatal("owner delete must not trash another user's child")
	}

	ownerList := listNotePageIDs(t, testUserID)
	if ownerList[parentID] {
		t.Fatal("trashed parent must leave the owner's live list")
	}
	memberList := listNotePageIDs(t, memberID)
	if memberList[parentID] {
		t.Fatal("sharee must lose the trashed parent")
	}
	if !memberList[childID] {
		t.Fatal("sharee must still see the child they created")
	}
	if listDeletedNotePageIDs(t, memberID)[childID] {
		t.Fatal("sharee's child must not appear in their trash")
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, withURLParam(newRequestAs(memberID, http.MethodGet, "/api/notes/pages/"+childID, nil), "id", childID))
	if getRec.Code != http.StatusOK {
		t.Fatalf("sharee get own child: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
}

func TestShareeDeleteRemovesOwnShareOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageViaAPI(t, testUserID, "Shared note "+uuid.NewString(), nil)
	memberB := createWorkspaceMemberForNoteACL(t, "sharee-b")
	memberC := createWorkspaceMemberForNoteACL(t, "sharee-c")
	shareNoteWithUser(t, noteID, memberB)
	shareNoteWithUser(t, noteID, memberC)

	deleteNotePageAs(t, memberB, noteID)

	if noteDeletedAt(t, noteID) {
		t.Fatal("sharee delete must not trash the owner's note")
	}
	if listNotePageIDs(t, memberB)[noteID] {
		t.Fatal("sharee must no longer see the dismissed note")
	}
	if !listNotePageIDs(t, testUserID)[noteID] {
		t.Fatal("owner must still see the note")
	}
	if !listNotePageIDs(t, memberC)[noteID] {
		t.Fatal("other sharee must still see the note")
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, withURLParam(newRequestAs(memberB, http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID))
	if getRec.Code != http.StatusNotFound {
		t.Fatalf("dismissed sharee get: expected 404, got %d: %s", getRec.Code, getRec.Body.String())
	}

	var shareCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM note_page_share WHERE page_id = $1 AND user_id = $2
`, noteID, memberB).Scan(&shareCount); err != nil {
		t.Fatalf("count share: %v", err)
	}
	if shareCount != 0 {
		t.Fatal("sharee delete must drop their share row")
	}
}

func TestShareeDeleteHidesInheritedChild(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	parentID := createNotePageViaAPI(t, testUserID, "Share root "+uuid.NewString(), nil)
	childID := createNotePageViaAPI(t, testUserID, "Inherited child "+uuid.NewString(), &parentID)
	memberID := createWorkspaceMemberForNoteACL(t, "inherited-dismiss")
	shareNoteWithUser(t, parentID, memberID)

	deleteNotePageAs(t, memberID, childID)

	if noteDeletedAt(t, childID) || noteDeletedAt(t, parentID) {
		t.Fatal("sharee delete must not trash inherited pages")
	}
	memberList := listNotePageIDs(t, memberID)
	if !memberList[parentID] {
		t.Fatal("sharee must still see the shared parent")
	}
	if memberList[childID] {
		t.Fatal("sharee must not see the dismissed inherited child")
	}
	ownerList := listNotePageIDs(t, testUserID)
	if !ownerList[parentID] || !ownerList[childID] {
		t.Fatal("owner must still see both pages")
	}
}

func TestPermanentDeleteParentDoesNotDeleteCollaboratorChild(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	parentID := createNotePageViaAPI(t, testUserID, "Soon gone "+uuid.NewString(), nil)
	memberID := createWorkspaceMemberForNoteACL(t, "survive-permanent")
	shareNoteWithUser(t, parentID, memberID)
	childID := createNotePageViaAPI(t, memberID, "Keep me "+uuid.NewString(), &parentID)

	deleteNotePageAs(t, testUserID, parentID)
	permRec := httptest.NewRecorder()
	testHandler.PermanentlyDeleteNotePage(permRec, withURLParam(
		newRequest(http.MethodDelete, "/api/notes/pages/"+parentID+"/permanent", nil),
		"id", parentID,
	))
	if permRec.Code != http.StatusNoContent {
		t.Fatalf("PermanentlyDeleteNotePage: expected 204, got %d: %s", permRec.Code, permRec.Body.String())
	}

	var parent pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
SELECT parent_id FROM note_page WHERE id = $1
`, childID).Scan(&parent); err != nil {
		t.Fatalf("collaborator child vanished after permanent delete: %v", err)
	}
	if parent.Valid {
		t.Fatalf("orphaned child parent_id = %s, want NULL", uuidToString(parent))
	}
	if !listNotePageIDs(t, memberID)[childID] {
		t.Fatal("collaborator must still list their child after the parent is gone")
	}
}
