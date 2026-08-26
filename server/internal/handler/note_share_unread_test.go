package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func listNotePagesAsUser(t *testing.T, userID string) []NotePageResponse {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.ListNotePages(rec, newRequestAs(userID, http.MethodGet, "/api/notes/pages", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("ListNotePages: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Pages []NotePageResponse `json:"pages"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	return body.Pages
}

func noteShareUnreadCountAsUser(t *testing.T, userID string) int {
	t.Helper()
	rec := httptest.NewRecorder()
	testHandler.CountNoteShareUnread(rec, newRequestAs(userID, http.MethodGet, "/api/notes/share-unread-count", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("CountNoteShareUnread: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode count: %v", err)
	}
	return body.Count
}

func findNotePage(pages []NotePageResponse, id string) NotePageResponse {
	for _, page := range pages {
		if page.ID == id {
			return page
		}
	}
	return NotePageResponse{}
}

func TestDirectNoteShareMarksRecipientUnreadUntilOpened(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Unread share "+uuid.NewString())
	memberID := createWorkspaceMemberForNoteACL(t, "share-unread")

	shareRec := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(shareRec, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"user_ids": []string{memberID},
	}), "id", noteID))
	if shareRec.Code != http.StatusOK {
		t.Fatalf("share: %d %s", shareRec.Code, shareRec.Body.String())
	}

	if findNotePage(listNotePagesAsUser(t, testUserID), noteID).ShareUnread {
		t.Fatal("owner must not see share_unread on their own page")
	}
	if noteShareUnreadCountAsUser(t, testUserID) != 0 {
		t.Fatal("owner unread count must stay 0")
	}

	listed := findNotePage(listNotePagesAsUser(t, memberID), noteID)
	if !listed.ShareUnread {
		t.Fatal("new direct share must be unread for the recipient")
	}
	if noteShareUnreadCountAsUser(t, memberID) != 1 {
		t.Fatalf("recipient unread count = %d, want 1", noteShareUnreadCountAsUser(t, memberID))
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, withURLParam(newRequestAs(memberID, http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID))
	if getRec.Code != http.StatusOK {
		t.Fatalf("open shared note: %d %s", getRec.Code, getRec.Body.String())
	}
	var opened NotePageResponse
	if err := json.NewDecoder(getRec.Body).Decode(&opened); err != nil {
		t.Fatalf("decode opened: %v", err)
	}
	if opened.ShareUnread {
		t.Fatal("opening the note must clear share_unread on the detail")
	}
	if findNotePage(listNotePagesAsUser(t, memberID), noteID).ShareUnread {
		t.Fatal("opening the note must clear share_unread on the list")
	}
	if noteShareUnreadCountAsUser(t, memberID) != 0 {
		t.Fatal("opening the note must clear the unread count")
	}

	again := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(again, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"user_ids": []string{memberID},
	}), "id", noteID))
	if again.Code != http.StatusOK {
		t.Fatalf("re-save shares: %d %s", again.Code, again.Body.String())
	}
	if findNotePage(listNotePagesAsUser(t, memberID), noteID).ShareUnread {
		t.Fatal("re-saving the same member must not revive unread")
	}

	unshare := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(unshare, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"user_ids": []string{},
	}), "id", noteID))
	if unshare.Code != http.StatusOK {
		t.Fatalf("unshare: %d %s", unshare.Code, unshare.Body.String())
	}
	reshare := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(reshare, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"user_ids": []string{memberID},
	}), "id", noteID))
	if reshare.Code != http.StatusOK {
		t.Fatalf("reshare: %d %s", reshare.Code, reshare.Body.String())
	}
	if !findNotePage(listNotePagesAsUser(t, memberID), noteID).ShareUnread {
		t.Fatal("unshare then share again must mark the note unread")
	}
}

func TestChannelNoteShareDoesNotMarkShareUnread(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Channel unread "+uuid.NewString())
	channelID := createGroupChannelForNoteRefTest(t, "unread-ch-"+uuid.NewString()[:8])
	memberID := createWorkspaceMemberForNoteACL(t, "channel-unread")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')
		ON CONFLICT DO NOTHING
	`, channelID, testWorkspaceID, memberID); err != nil {
		t.Fatalf("add channel member: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(rec, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"channel_ids": []string{channelID},
	}), "id", noteID))
	if rec.Code != http.StatusOK {
		t.Fatalf("channel share: %d %s", rec.Code, rec.Body.String())
	}

	if findNotePage(listNotePagesAsUser(t, memberID), noteID).ShareUnread {
		t.Fatal("channel share must not set share_unread")
	}
	if noteShareUnreadCountAsUser(t, memberID) != 0 {
		t.Fatal("channel share must not increment unread count")
	}
}
