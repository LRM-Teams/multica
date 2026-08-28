package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestMoveOwnNoteUnderSharedParentSharesWithOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	parentID := createNotePageViaAPI(t, testUserID, "Shared parent "+uuid.NewString(), nil)
	memberB := createWorkspaceMemberForNoteACL(t, "move-under-share")
	memberC := createWorkspaceMemberForNoteACL(t, "extra-share")
	shareNoteWithUser(t, parentID, memberB)
	ownID := createNotePageViaAPI(t, memberB, "B private "+uuid.NewString(), nil)

	moveRec := httptest.NewRecorder()
	testHandler.MoveNotePage(moveRec, withURLParam(newRequestAs(memberB, http.MethodPatch, "/api/notes/pages/"+ownID+"/move", map[string]any{
		"parent_id": parentID,
		"sort_key":  "00000000000000000050",
	}), "id", ownID))
	if moveRec.Code != http.StatusOK {
		t.Fatalf("MoveNotePage: expected 200, got %d: %s", moveRec.Code, moveRec.Body.String())
	}
	var moved NotePageResponse
	if err := json.NewDecoder(moveRec.Body).Decode(&moved); err != nil {
		t.Fatalf("decode move: %v", err)
	}
	if moved.ParentID == nil || *moved.ParentID != parentID {
		t.Fatalf("moved parent = %#v, want %s", moved.ParentID, parentID)
	}
	foundOwnerShare := false
	for _, id := range moved.ShareUserIDs {
		if id == testUserID {
			foundOwnerShare = true
			break
		}
	}
	if !foundOwnerShare {
		t.Fatalf("move under shared parent must share with the parent owner, shares=%#v", moved.ShareUserIDs)
	}

	ownerList := listNotePageIDs(t, testUserID)
	if !ownerList[ownID] {
		t.Fatal("parent owner must see the moved child")
	}

	shareRec := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(shareRec, withURLParam(newRequestAs(memberB, http.MethodPut, "/api/notes/pages/"+ownID+"/shares", map[string]any{
		"user_ids":    []string{testUserID, memberC},
		"agent_ids":   []string{},
		"channel_ids": []string{},
	}), "id", ownID))
	if shareRec.Code != http.StatusOK {
		t.Fatalf("extra share: expected 200, got %d: %s", shareRec.Code, shareRec.Body.String())
	}
	if !listNotePageIDs(t, memberC)[ownID] {
		t.Fatal("owner of the moved note must still be able to share it with more people")
	}
}

func TestMoveSharedNoteIsStillForbidden(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	parentID := createNotePageViaAPI(t, testUserID, "Owned parent "+uuid.NewString(), nil)
	sharedID := createNotePageViaAPI(t, testUserID, "Shared root "+uuid.NewString(), nil)
	memberB := createWorkspaceMemberForNoteACL(t, "cannot-move-shared")
	shareNoteWithUser(t, sharedID, memberB)

	rec := httptest.NewRecorder()
	testHandler.MoveNotePage(rec, withURLParam(newRequestAs(memberB, http.MethodPatch, "/api/notes/pages/"+sharedID+"/move", map[string]any{
		"parent_id": parentID,
		"sort_key":  "00000000000000000050",
	}), "id", sharedID))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("moving a shared note: expected 403, got %d: %s", rec.Code, rec.Body.String())
	}

	var parent any
	if err := testPool.QueryRow(context.Background(), `SELECT parent_id FROM note_page WHERE id = $1`, sharedID).Scan(&parent); err != nil {
		t.Fatalf("load shared note: %v", err)
	}
	if parent != nil {
		t.Fatalf("shared note parent_id = %#v, want NULL", parent)
	}
}
