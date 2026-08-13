package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

func TestListChannelMentionCandidatesKeepsInChannelAndPagesOutsiders(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	memberID := testUserID
	otherMemberID := createChannelWorkspaceMemberWithRole(t, "member")
	inChannelAgentID := seedMentionTestAgent(t, memberID, "li-wei-"+uuid.NewString()[:8], "里维")
	channelID := seedChannelForTest(t, "mention-candidates-"+uuid.NewString(), memberID, otherMemberID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, inChannelAgentID); err != nil {
		t.Fatalf("add in-channel agent: %v", err)
	}

	for i := 0; i < 5; i++ {
		seedMentionTestAgent(t, memberID, fmt.Sprintf("ascii-agent-%d-%s", i, uuid.NewString()[:8]), fmt.Sprintf("Agent %02d", i))
	}

	req := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/mention-candidates?limit=2", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMentionCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}

	var page ChannelMentionCandidatesResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !channelMentionCandidatesContain(page.InChannel, "agent", inChannelAgentID) {
		t.Fatalf("in-channel agent missing: %+v", page.InChannel)
	}
	if channelMentionCandidatesContain(page.InChannel, "member", memberID) {
		t.Fatalf("viewer should not be a group @ candidate: %+v", page.InChannel)
	}
	if !channelMentionCandidatesContain(page.InChannel, "member", otherMemberID) {
		t.Fatalf("other in-channel member missing: %+v", page.InChannel)
	}
	if channelMentionCandidatesContain(page.NotInChannel, "agent", inChannelAgentID) {
		t.Fatalf("in-channel agent leaked into not_in_channel")
	}
	if channelMentionCandidatesContain(page.NotInChannel, "member", memberID) {
		t.Fatalf("in-channel member leaked into not_in_channel")
	}
	if len(page.NotInChannel) != 2 {
		t.Fatalf("first page size = %d, want 2: %+v", len(page.NotInChannel), page.NotInChannel)
	}
	if !page.HasMore || page.NextOffset == nil || *page.NextOffset != 2 {
		t.Fatalf("has_more/next_offset = %v %+v, want true / 2", page.HasMore, page.NextOffset)
	}

	req2 := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/mention-candidates?limit=2&offset=2", nil)
	req2 = withChannelTestWorkspaceCtx(t, req2, memberID)
	req2 = withURLParam(req2, "channelId", channelID)
	rec2 := httptest.NewRecorder()
	testHandler.ListChannelMentionCandidates(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("page 2 status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	var page2 ChannelMentionCandidatesResponse
	if err := json.NewDecoder(rec2.Body).Decode(&page2); err != nil {
		t.Fatalf("decode page 2: %v", err)
	}
	if !channelMentionCandidatesContain(page2.InChannel, "agent", inChannelAgentID) {
		t.Fatalf("page 2 dropped in-channel agent")
	}
	if len(page2.NotInChannel) == 0 {
		t.Fatalf("page 2 outsiders empty")
	}
	if page.NotInChannel[0].ID == page2.NotInChannel[0].ID {
		t.Fatalf("page 2 repeated page 1 outsider %s", page.NotInChannel[0].ID)
	}
}

func TestListChannelMentionCandidatesSearchFindsInChannelCJKName(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := testUserID
	agentID := seedMentionTestAgent(t, memberID, "li-wei-"+uuid.NewString()[:8], "里维")
	channelID := seedChannelForTest(t, "mention-search-"+uuid.NewString(), memberID)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add agent: %v", err)
	}

	req := newRequestAs(memberID, http.MethodGet, "/api/channels/"+channelID+"/mention-candidates?q=%E9%87%8C%E7%BB%B4", nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withURLParam(req, "channelId", channelID)
	rec := httptest.NewRecorder()
	testHandler.ListChannelMentionCandidates(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var page ChannelMentionCandidatesResponse
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !channelMentionCandidatesContain(page.InChannel, "agent", agentID) {
		t.Fatalf("q=里维 missed in-channel agent: %+v", page.InChannel)
	}
}

func seedMentionTestAgent(t *testing.T, ownerID, name, displayName string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, model
		) VALUES ($1, $2, $3, '', 'cloud', '{}'::jsonb, $4, 1, $5, '', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id`,
		testWorkspaceID, name, displayName, handlerTestRuntimeID(t), ownerID,
	).Scan(&agentID); err != nil {
		t.Fatalf("seed mention test agent: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})
	return agentID
}

func TestDropViewerMentionCandidate(t *testing.T) {
	rows := []ChannelMentionCandidate{
		{Type: "member", ID: "self"},
		{Type: "member", ID: "other"},
		{Type: "agent", ID: "self"},
	}
	got := dropViewerMentionCandidate(rows, "self")
	if channelMentionCandidatesContain(got, "member", "self") {
		t.Fatalf("viewer member still present: %+v", got)
	}
	if !channelMentionCandidatesContain(got, "member", "other") {
		t.Fatalf("other member dropped: %+v", got)
	}
	if !channelMentionCandidatesContain(got, "agent", "self") {
		t.Fatalf("agent sharing viewer id dropped: %+v", got)
	}
}

func channelMentionCandidatesContain(rows []ChannelMentionCandidate, typ, id string) bool {
	for _, row := range rows {
		if row.Type == typ && row.ID == id {
			return true
		}
	}
	return false
}
