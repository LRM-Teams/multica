package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListNotePagesIncludesOwnedNotesAcrossWorkspaces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	var otherWorkspaceID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', $3)
		RETURNING id
	`, "Notes Cross Workspace", "notes-cross-"+uuid.NewString(), "NCW").Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("create second workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, otherWorkspaceID)
	})

	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, otherWorkspaceID, testUserID); err != nil {
		t.Fatalf("add current user to second workspace: %v", err)
	}

	title := "Private cross-workspace note " + uuid.NewString()
	var noteID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO note_page (workspace_id, owner_user_id, title, content, sort_key, created_by, updated_by)
		VALUES ($1, $2, $3, 'private body', '00000000000000000001', $2, $2)
		RETURNING id
	`, testWorkspaceID, testUserID, title).Scan(&noteID); err != nil {
		t.Fatalf("create note: %v", err)
	}
	listReq := newRequest(http.MethodGet, "/api/notes/pages", nil)
	listReq.Header.Set("X-Workspace-ID", otherWorkspaceID)
	listRec := httptest.NewRecorder()
	testHandler.ListNotePages(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListNotePages: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}

	var listResp struct {
		Pages []NotePageResponse `json:"pages"`
	}
	if err := json.NewDecoder(listRec.Body).Decode(&listResp); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	var listed *NotePageResponse
	for i := range listResp.Pages {
		if listResp.Pages[i].ID == noteID {
			listed = &listResp.Pages[i]
			break
		}
	}
	if listed == nil {
		t.Fatalf("owned private note %s was not listed from workspace %s", noteID, otherWorkspaceID)
	}
	if listed.WorkspaceID != testWorkspaceID || listed.Title != title || !listed.CanManageShares {
		t.Fatalf("listed note = %#v, want workspace %s title %q manageable", *listed, testWorkspaceID, title)
	}

	getReq := withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID)
	getReq.Header.Set("X-Workspace-ID", otherWorkspaceID)
	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GetNotePage from second workspace: expected 200, got %d: %s", getRec.Code, getRec.Body.String())
	}
}
