package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestAgentChannelVisibility_CreateRequiresHomeChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name": "Channel Agent No Home " + uuid.NewString()[:8],
		"runtime_id":   testRuntimeID,
		"visibility":   "channel",
	})
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("create channel without home: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentChannelVisibility_CreateRejectsHomeOnPrivate(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	channelID := seedChannelForTest(t, "home-reject-"+uuid.NewString(), testUserID)

	req := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":    "Private With Home " + uuid.NewString()[:8],
		"runtime_id":      testRuntimeID,
		"visibility":      "private",
		"home_channel_id": channelID,
	})
	w := httptest.NewRecorder()
	testHandler.CreateAgent(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("private with home_channel_id: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAgentChannelVisibility_CreateListInviteMention(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	homeID := seedChannelForTest(t, "channel-vis-home-"+uuid.NewString(), testUserID)
	otherID := seedChannelForTest(t, "channel-vis-other-"+uuid.NewString(), testUserID)

	createReq := newRequest(http.MethodPost, "/api/agents", map[string]any{
		"display_name":    "Channel Only " + uuid.NewString()[:8],
		"runtime_id":      testRuntimeID,
		"visibility":      "channel",
		"home_channel_id": homeID,
	})
	createRec := httptest.NewRecorder()
	testHandler.CreateAgent(createRec, createReq)
	if createRec.Code != http.StatusCreated && createRec.Code != http.StatusOK {
		t.Fatalf("create channel agent: status=%d body=%s", createRec.Code, createRec.Body.String())
	}
	var created AgentResponse
	if err := json.Unmarshal(createRec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, created.ID)
	})
	if created.Visibility != "channel" {
		t.Fatalf("visibility=%q want channel", created.Visibility)
	}
	if created.HomeChannelID == nil || *created.HomeChannelID != homeID {
		t.Fatalf("home_channel_id=%v want %s", created.HomeChannelID, homeID)
	}

	var memberCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, homeID, created.ID).Scan(&memberCount); err != nil {
		t.Fatalf("count channel_member: %v", err)
	}
	if memberCount != 1 {
		t.Fatalf("channel agent membership count=%d want 1 (home bind must add member)", memberCount)
	}

	// Workspace directory (no channel_id): channel agent hidden.
	listRec := httptest.NewRecorder()
	testHandler.ListAgents(listRec, newRequest(http.MethodGet, "/api/agents", nil))
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListAgents: status=%d body=%s", listRec.Code, listRec.Body.String())
	}
	var listed []AgentResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	for _, a := range listed {
		if a.ID == created.ID {
			t.Fatal("channel agent appeared in workspace ListAgents without channel_id")
		}
	}

	// Home channel context: visible.
	homeListRec := httptest.NewRecorder()
	testHandler.ListAgents(homeListRec, newRequest(http.MethodGet, "/api/agents?channel_id="+homeID, nil))
	if homeListRec.Code != http.StatusOK {
		t.Fatalf("ListAgents home: status=%d body=%s", homeListRec.Code, homeListRec.Body.String())
	}
	var homeListed []AgentResponse
	if err := json.Unmarshal(homeListRec.Body.Bytes(), &homeListed); err != nil {
		t.Fatalf("decode home list: %v", err)
	}
	found := false
	for _, a := range homeListed {
		if a.ID == created.ID {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("channel agent missing from ListAgents?channel_id=home")
	}

	// Other channel context: hidden.
	otherListRec := httptest.NewRecorder()
	testHandler.ListAgents(otherListRec, newRequest(http.MethodGet, "/api/agents?channel_id="+otherID, nil))
	if otherListRec.Code != http.StatusOK {
		t.Fatalf("ListAgents other: status=%d body=%s", otherListRec.Code, otherListRec.Body.String())
	}
	var otherListed []AgentResponse
	if err := json.Unmarshal(otherListRec.Body.Bytes(), &otherListed); err != nil {
		t.Fatalf("decode other list: %v", err)
	}
	for _, a := range otherListed {
		if a.ID == created.ID {
			t.Fatal("channel agent appeared in ListAgents for non-home channel")
		}
	}

	// Invite into non-home channel rejected; membership not created.
	inviteRec := httptest.NewRecorder()
	inviteReq := newRequest(http.MethodPost, "/api/channels/"+otherID+"/members", AddChannelMemberRequest{
		MemberType: "agent",
		MemberID:   created.ID,
	})
	inviteReq = withChannelTestWorkspaceCtx(t, inviteReq, testUserID)
	inviteReq = withURLParam(inviteReq, "channelId", otherID)
	testHandler.AddChannelMember(inviteRec, inviteReq)
	if inviteRec.Code != http.StatusBadRequest {
		t.Fatalf("invite to non-home: status=%d body=%s", inviteRec.Code, inviteRec.Body.String())
	}
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, otherID, created.ID).Scan(&count); err != nil {
		t.Fatalf("count other membership: %v", err)
	}
	if count != 0 {
		t.Fatalf("non-home invite created membership count=%d", count)
	}

	// Invite into home channel allowed.
	homeInviteRec := httptest.NewRecorder()
	homeInviteReq := newRequest(http.MethodPost, "/api/channels/"+homeID+"/members", AddChannelMemberRequest{
		MemberType: "agent",
		MemberID:   created.ID,
	})
	homeInviteReq = withChannelTestWorkspaceCtx(t, homeInviteReq, testUserID)
	homeInviteReq = withURLParam(homeInviteReq, "channelId", homeID)
	testHandler.AddChannelMember(homeInviteRec, homeInviteReq)
	if homeInviteRec.Code != http.StatusOK && homeInviteRec.Code != http.StatusCreated {
		t.Fatalf("invite to home: status=%d body=%s", homeInviteRec.Code, homeInviteRec.Body.String())
	}

	// Seed a stale membership in the other channel (存量) — must NOT auto-kick,
	// but must not appear in @mention candidates there.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, join_source)
		VALUES ($1, $2, 'agent', $3, 'system')
		ON CONFLICT DO NOTHING
	`, otherID, testWorkspaceID, created.ID); err != nil {
		t.Fatalf("seed stale other membership: %v", err)
	}
	var staleCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2
	`, otherID, created.ID).Scan(&staleCount); err != nil {
		t.Fatalf("count stale membership: %v", err)
	}
	if staleCount != 1 {
		t.Fatalf("stale membership not retained; count=%d", staleCount)
	}

	homeCandidates := testHandler.channelMentionCandidates(context.Background(), testWorkspaceID, homeID)
	if _, ok := homeCandidates[normalizeMentionCandidateLabel(created.Name)]; !ok {
		t.Fatalf("home @mention candidates missing agent handle %q; keys=%v", created.Name, candidateKeys(homeCandidates))
	}
	otherCandidates := testHandler.channelMentionCandidates(context.Background(), testWorkspaceID, otherID)
	if _, ok := otherCandidates[normalizeMentionCandidateLabel(created.Name)]; ok {
		t.Fatal("non-home @mention candidates still include channel-visibility agent")
	}
}

func candidateKeys(m map[string]channelMentionCandidate) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestAgentChannelVisibility_UpdateIllegalCombo(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	agentID := createHandlerTestAgent(t, "channel-update-"+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "channel-update-home-"+uuid.NewString(), testUserID)

	// private → channel without home_channel_id
	rec := httptest.NewRecorder()
	req := newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"visibility": "channel",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("update to channel without home: status=%d body=%s", rec.Code, rec.Body.String())
	}

	// private → channel with home
	rec = httptest.NewRecorder()
	req = newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"visibility":      "channel",
		"home_channel_id": channelID,
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update to channel with home: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var updated AgentResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode update: %v", err)
	}
	if updated.Visibility != "channel" || updated.HomeChannelID == nil || *updated.HomeChannelID != channelID {
		t.Fatalf("updated visibility/home = %s/%v", updated.Visibility, updated.HomeChannelID)
	}

	// channel → workspace clears home
	rec = httptest.NewRecorder()
	req = newRequest(http.MethodPut, "/api/agents/"+agentID, map[string]any{
		"visibility": "workspace",
	})
	req = withURLParam(req, "id", agentID)
	testHandler.UpdateAgent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update to workspace: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &updated); err != nil {
		t.Fatalf("decode workspace update: %v", err)
	}
	if updated.Visibility != "workspace" {
		t.Fatalf("visibility=%q want workspace", updated.Visibility)
	}
	if updated.HomeChannelID != nil {
		t.Fatalf("home_channel_id should be cleared, got %v", updated.HomeChannelID)
	}
}
