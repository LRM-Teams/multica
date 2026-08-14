package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func createGroupChannelForNoteRefTest(t *testing.T, name string) string {
	t.Helper()
	channelID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel (id, workspace_id, name, kind, created_by)
		VALUES ($1, $2, $3, 'group', $4)
	`, channelID, testWorkspaceID, name, testUserID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'owner')
		ON CONFLICT DO NOTHING
	`, channelID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("add channel member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM note_page_channel_ref WHERE channel_id = $1`, channelID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_member WHERE channel_id = $1`, channelID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})
	return channelID
}

func TestNotePageChannelRefCreateListDelete(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Channel ref note "+uuid.NewString())
	channelID := createGroupChannelForNoteRefTest(t, "collab-"+uuid.NewString()[:8])

	createReq := withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/channel-refs", map[string]any{
		"channel_id": channelID,
		"kind":       "worker",
	}), "id", noteID)
	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageChannelRef(createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("CreateNotePageChannelRef: expected 201, got %d: %s", createRec.Code, createRec.Body.String())
	}
	var created NotePageIssueRefResponse
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Type != "channel" || created.ID != channelID || !created.Accessible || created.Label == nil || *created.Label == "" || created.Identifier != "worker" {
		t.Fatalf("created ref = %#v", created)
	}

	listReq := withURLParam(newRequest(http.MethodGet, "/api/notes/pages/"+noteID+"/channel-refs", nil), "id", noteID)
	listRec := httptest.NewRecorder()
	testHandler.ListNotePageChannelRefs(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListNotePageChannelRefs: expected 200, got %d: %s", listRec.Code, listRec.Body.String())
	}
	var listed NotePageIssueRefListResponse
	if err := json.NewDecoder(listRec.Body).Decode(&listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Refs) != 1 || listed.Refs[0].ID != channelID || !listed.Refs[0].Accessible {
		t.Fatalf("listed refs = %#v", listed.Refs)
	}

	createAgainRec := httptest.NewRecorder()
	testHandler.CreateNotePageChannelRef(createAgainRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/channel-refs", map[string]any{
		"channel_id": channelID,
	}), "id", noteID))
	if createAgainRec.Code != http.StatusCreated {
		t.Fatalf("idempotent create: expected 201, got %d: %s", createAgainRec.Code, createAgainRec.Body.String())
	}

	deleteReq := withRouteParams(newRequest(http.MethodDelete, "/api/notes/pages/"+noteID+"/channel-refs/"+channelID, nil), "id", noteID, "channelId", channelID)
	deleteRec := httptest.NewRecorder()
	testHandler.DeleteNotePageChannelRef(deleteRec, deleteReq)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("DeleteNotePageChannelRef: expected 204, got %d: %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestNotePageChannelRefRejectsDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "DM ref note "+uuid.NewString())
	channelID := uuid.NewString()
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel (id, workspace_id, name, kind, created_by)
		VALUES ($1, $2, $3, 'dm', $4)
	`, channelID, testWorkspaceID, "dm-"+uuid.NewString()[:8], testUserID); err != nil {
		t.Fatalf("create dm: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'owner')
		ON CONFLICT DO NOTHING
	`, channelID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("add dm member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel_member WHERE channel_id = $1`, channelID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	createRec := httptest.NewRecorder()
	testHandler.CreateNotePageChannelRef(createRec, withURLParam(newRequest(http.MethodPost, "/api/notes/pages/"+noteID+"/channel-refs", map[string]any{
		"channel_id": channelID,
	}), "id", noteID))
	if createRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for dm channel, got %d: %s", createRec.Code, createRec.Body.String())
	}
}
