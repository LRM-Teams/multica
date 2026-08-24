package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestUpdateNotePageSharesAcceptsAgentAndChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	noteID := createNotePageForAITest(t, "Virtual share "+uuid.NewString())
	agentID := createHandlerTestAgent(t, "Share Agent "+uuid.NewString()[:8], nil)
	channelID := createGroupChannelForNoteRefTest(t, "share-ch-"+uuid.NewString()[:8])
	memberID := createWorkspaceMemberForNoteACL(t, "share-human")
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')
		ON CONFLICT DO NOTHING
	`, channelID, testWorkspaceID, memberID); err != nil {
		t.Fatalf("add human channel member: %v", err)
	}

	rec := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(rec, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"user_ids":    []string{},
		"agent_ids":   []string{agentID},
		"channel_ids": []string{channelID},
	}), "id", noteID))
	if rec.Code != http.StatusOK {
		t.Fatalf("update shares: %d %s", rec.Code, rec.Body.String())
	}
	var page NotePageResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.ShareAgentIDs) != 1 || page.ShareAgentIDs[0] != agentID {
		t.Fatalf("share_agent_ids = %#v", page.ShareAgentIDs)
	}
	if len(page.ShareChannelIDs) != 1 || page.ShareChannelIDs[0] != channelID {
		t.Fatalf("share_channel_ids = %#v", page.ShareChannelIDs)
	}

	getRec := httptest.NewRecorder()
	testHandler.GetNotePage(getRec, withURLParam(newRequestAs(memberID, http.MethodGet, "/api/notes/pages/"+noteID, nil), "id", noteID))
	if getRec.Code != http.StatusOK {
		t.Fatalf("channel member should read shared note: %d %s", getRec.Code, getRec.Body.String())
	}

	var childID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
		VALUES ($1, $2, $3, $4, 'child body', '00000000000000000002', $3, $3)
		RETURNING id
	`, testWorkspaceID, noteID, testUserID, "Channel share child").Scan(&childID); err != nil {
		t.Fatalf("create child: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, childID) })
	childRec := httptest.NewRecorder()
	testHandler.GetNotePage(childRec, withURLParam(newRequestAs(memberID, http.MethodGet, "/api/notes/pages/"+childID, nil), "id", childID))
	if childRec.Code != http.StatusOK {
		t.Fatalf("channel member should inherit child read: %d %s", childRec.Code, childRec.Body.String())
	}

	var cardCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM channel_message
WHERE channel_id = $1 AND parts::text LIKE '%note_brief%'
`, channelID).Scan(&cardCount); err != nil {
		t.Fatalf("count channel cards: %v", err)
	}
	if cardCount < 1 {
		t.Fatal("expected a note_brief card in the shared channel")
	}

	again := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(again, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+noteID+"/shares", map[string]any{
		"user_ids":    []string{},
		"agent_ids":   []string{agentID},
		"channel_ids": []string{channelID},
	}), "id", noteID))
	if again.Code != http.StatusOK {
		t.Fatalf("re-save shares: %d %s", again.Code, again.Body.String())
	}
	var againCount int
	if err := testPool.QueryRow(context.Background(), `
SELECT count(*) FROM channel_message
WHERE channel_id = $1 AND parts::text LIKE '%note_brief%'
`, channelID).Scan(&againCount); err != nil {
		t.Fatalf("count channel cards after re-save: %v", err)
	}
	if againCount != cardCount {
		t.Fatalf("re-saving the same channel must not post another card: before=%d after=%d", cardCount, againCount)
	}
}

func TestAgentNoteShareAllowsCurrentPageOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	parentID := createNotePageForAITest(t, "Share parent "+uuid.NewString())
	var childID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO note_page (workspace_id, parent_id, owner_user_id, title, content, sort_key, created_by, updated_by)
		VALUES ($1, $2, $3, $4, 'child body', '00000000000000000002', $3, $3)
		RETURNING id
	`, testWorkspaceID, parentID, testUserID, "Share child").Scan(&childID); err != nil {
		t.Fatalf("create child: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM note_page WHERE id = $1`, childID) })

	agentID := createHandlerTestAgent(t, "Page-only Agent "+uuid.NewString()[:8], nil)
	rec := httptest.NewRecorder()
	testHandler.UpdateNotePageShares(rec, withURLParam(newRequest(http.MethodPut, "/api/notes/pages/"+parentID+"/shares", map[string]any{
		"agent_ids": []string{agentID},
	}), "id", parentID))
	if rec.Code != http.StatusOK {
		t.Fatalf("share to agent: %d %s", rec.Code, rec.Body.String())
	}

	parentRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(parentRec, withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+parentID, nil),
		agentID, testWorkspaceID, testUserID,
	), "id", parentID))
	if parentRec.Code != http.StatusOK {
		t.Fatalf("shared agent should read current page: %d %s", parentRec.Code, parentRec.Body.String())
	}

	childRec := httptest.NewRecorder()
	testHandler.GetAgentNotePage(childRec, withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+childID, nil),
		agentID, testWorkspaceID, testUserID,
	), "id", childID))
	if childRec.Code != http.StatusNotFound {
		t.Fatalf("shared agent must not read child: %d %s", childRec.Code, childRec.Body.String())
	}

	treeRec := httptest.NewRecorder()
	testHandler.ListAgentNoteTree(treeRec, withURLParam(withAgentCredentialPrincipal(
		newRequest(http.MethodGet, "/api/agent/notes/pages/"+parentID+"/tree", nil),
		agentID, testWorkspaceID, testUserID,
	), "id", parentID))
	if treeRec.Code != http.StatusOK {
		t.Fatalf("tree: %d %s", treeRec.Code, treeRec.Body.String())
	}
	var tree struct {
		Pages []agentNoteTreeNode `json:"pages"`
	}
	if err := json.NewDecoder(treeRec.Body).Decode(&tree); err != nil {
		t.Fatalf("decode tree: %v", err)
	}
	if len(tree.Pages) != 1 || tree.Pages[0].ID != parentID {
		t.Fatalf("share-only tree should be the current page, got %#v", tree.Pages)
	}
}
