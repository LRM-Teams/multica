package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCreateSkillDefaultsGrantLevelAgent(t *testing.T) {
	w := httptest.NewRecorder()
	req := newRequest(http.MethodPost, "/api/skills?workspace_id="+testWorkspaceID, CreateSkillRequest{
		Name:        "grant-default-" + t.Name(),
		Description: "default L1",
		Content:     "# skill",
	})
	testHandler.CreateSkill(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("CreateSkill: expected 201, got %d: %s", w.Code, w.Body.String())
	}

	var resp SkillWithFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM skill WHERE id = $1`, resp.ID)
	})
	if resp.GrantLevel != skillGrantLevelAgent {
		t.Fatalf("grant_level=%q, want agent", resp.GrantLevel)
	}
	if resp.ChannelID != nil {
		t.Fatalf("channel_id=%v, want nil", resp.ChannelID)
	}

	var dbLevel string
	if err := testPool.QueryRow(context.Background(),
		`SELECT grant_level FROM skill WHERE id = $1`, resp.ID,
	).Scan(&dbLevel); err != nil {
		t.Fatalf("db grant_level: %v", err)
	}
	if dbLevel != skillGrantLevelAgent {
		t.Fatalf("db grant_level=%q, want agent", dbLevel)
	}
}

func TestPromoteSkillChannelAndWorkspaceWithAuditAndCapabilityFilter(t *testing.T) {
	ctx := context.Background()
	skillID, _ := insertHandlerTestSkill(t, "promote-ladder", "# promote")

	channelID := insertSkillPromoteGroupChannel(t, "promote-ch-"+t.Name())
	// testUser is workspace owner; make them channel owner for L2.
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'owner')
		ON CONFLICT (channel_id, member_type, member_id) DO UPDATE SET role = 'owner'
	`, channelID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed channel owner: %v", err)
	}

	// Detail capabilities at L1: owner can promote to workspace; channel owner can promote to channel.
	detail := httptest.NewRecorder()
	detailReq := newRequest(http.MethodGet, "/api/skills/"+skillID, nil)
	detailReq = withURLParam(detailReq, "id", skillID)
	testHandler.GetSkill(detail, detailReq)
	if detail.Code != 200 {
		t.Fatalf("GetSkill: %d %s", detail.Code, detail.Body.String())
	}
	var before SkillWithFilesResponse
	if err := json.Unmarshal(detail.Body.Bytes(), &before); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if before.GrantLevel != skillGrantLevelAgent {
		t.Fatalf("initial grant_level=%q", before.GrantLevel)
	}
	if before.Capabilities == nil || !before.Capabilities.CanPromoteToChannel || !before.Capabilities.CanPromoteToWorkspace {
		t.Fatalf("expected both promote caps at L1 for owner+channel-owner, got %+v", before.Capabilities)
	}

	// Promote L1 → L2.
	promoteCh := httptest.NewRecorder()
	promoteChReq := newRequest(http.MethodPost, "/api/skills/"+skillID+"/promote", PromoteSkillRequest{
		ToLevel:   skillGrantLevelChannel,
		ChannelID: channelID,
	})
	promoteChReq = withURLParam(promoteChReq, "id", skillID)
	testHandler.PromoteSkill(promoteCh, promoteChReq)
	if promoteCh.Code != 200 {
		t.Fatalf("promote channel: %d %s", promoteCh.Code, promoteCh.Body.String())
	}
	var afterCh SkillWithFilesResponse
	if err := json.Unmarshal(promoteCh.Body.Bytes(), &afterCh); err != nil {
		t.Fatalf("decode promote channel: %v", err)
	}
	if afterCh.GrantLevel != skillGrantLevelChannel {
		t.Fatalf("after channel grant_level=%q", afterCh.GrantLevel)
	}
	if afterCh.ChannelID == nil || *afterCh.ChannelID != channelID {
		t.Fatalf("after channel channel_id=%v, want %s", afterCh.ChannelID, channelID)
	}
	if afterCh.Capabilities == nil || afterCh.Capabilities.CanPromoteToChannel || !afterCh.Capabilities.CanPromoteToWorkspace {
		t.Fatalf("at L2: can_channel should be false, can_workspace true; got %+v", afterCh.Capabilities)
	}

	// Ordinary member cannot promote to workspace (capability filter + enforce).
	memberID := insertSkillPromoteWorkspaceMember(t, "member")
	deny := httptest.NewRecorder()
	denyReq := newRequestAsUser(memberID, http.MethodPost, "/api/skills/"+skillID+"/promote", PromoteSkillRequest{
		ToLevel: skillGrantLevelWorkspace,
	})
	denyReq = withURLParam(denyReq, "id", skillID)
	testHandler.PromoteSkill(deny, denyReq)
	if deny.Code != http.StatusForbidden {
		t.Fatalf("member promote workspace: expected 403, got %d %s", deny.Code, deny.Body.String())
	}

	// Owner promotes L2 → L3.
	promoteWS := httptest.NewRecorder()
	promoteWSReq := newRequest(http.MethodPost, "/api/skills/"+skillID+"/promote", PromoteSkillRequest{
		ToLevel: skillGrantLevelWorkspace,
	})
	promoteWSReq = withURLParam(promoteWSReq, "id", skillID)
	testHandler.PromoteSkill(promoteWS, promoteWSReq)
	if promoteWS.Code != 200 {
		t.Fatalf("promote workspace: %d %s", promoteWS.Code, promoteWS.Body.String())
	}
	var afterWS SkillWithFilesResponse
	if err := json.Unmarshal(promoteWS.Body.Bytes(), &afterWS); err != nil {
		t.Fatalf("decode promote workspace: %v", err)
	}
	if afterWS.GrantLevel != skillGrantLevelWorkspace {
		t.Fatalf("after workspace grant_level=%q", afterWS.GrantLevel)
	}
	if afterWS.ChannelID != nil {
		t.Fatalf("workspace level should clear channel_id, got %v", afterWS.ChannelID)
	}
	if afterWS.Capabilities == nil || afterWS.Capabilities.CanPromoteToChannel || afterWS.Capabilities.CanPromoteToWorkspace {
		t.Fatalf("at L3 both caps false, got %+v", afterWS.Capabilities)
	}

	// Audit trail.
	list := httptest.NewRecorder()
	listReq := newRequest(http.MethodGet, "/api/skills/"+skillID+"/promotions", nil)
	listReq = withURLParam(listReq, "id", skillID)
	testHandler.ListSkillPromotions(list, listReq)
	if list.Code != 200 {
		t.Fatalf("list promotions: %d %s", list.Code, list.Body.String())
	}
	var promotions SkillPromotionsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &promotions); err != nil {
		t.Fatalf("decode promotions: %v", err)
	}
	if promotions.Total != 2 || len(promotions.Items) != 2 {
		t.Fatalf("promotions total/items=%d/%d, want 2", promotions.Total, len(promotions.Items))
	}
	// Newest first: workspace then channel.
	if promotions.Items[0].FromLevel != skillGrantLevelChannel || promotions.Items[0].ToLevel != skillGrantLevelWorkspace {
		t.Fatalf("first audit=%+v", promotions.Items[0])
	}
	if promotions.Items[1].FromLevel != skillGrantLevelAgent || promotions.Items[1].ToLevel != skillGrantLevelChannel {
		t.Fatalf("second audit=%+v", promotions.Items[1])
	}
	if promotions.Items[0].ActorType != "member" || promotions.Items[0].ActorID != testUserID {
		t.Fatalf("actor=%s/%s", promotions.Items[0].ActorType, promotions.Items[0].ActorID)
	}
}

func TestPromoteSkillChannelRequiresOwnerOrManager(t *testing.T) {
	ctx := context.Background()
	skillID, _ := insertHandlerTestSkill(t, "promote-forbidden", "# x")

	// Channel owned by a different human; insertSkillPromoteGroupChannel seeds
	// created_by=testUser as owner via triggers, so build the channel under otherOwner.
	otherOwner := insertSkillPromoteWorkspaceMember(t, "owner")
	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, kind, created_by)
		VALUES ($1, $2, 'group', $3)
		RETURNING id::text
	`, testWorkspaceID, "promote-forbid-"+t.Name(), otherOwner).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(ctx, `DELETE FROM channel WHERE id = $1`, channelID)
	})

	// Ordinary workspace member who is only a channel member (not owner/manager).
	memberID := insertSkillPromoteWorkspaceMember(t, "member")
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')
	`, channelID, testWorkspaceID, memberID); err != nil {
		t.Fatalf("seed channel member: %v", err)
	}

	w := httptest.NewRecorder()
	req := newRequestAsUser(memberID, http.MethodPost, "/api/skills/"+skillID+"/promote", PromoteSkillRequest{
		ToLevel:   skillGrantLevelChannel,
		ChannelID: channelID,
	})
	req = withURLParam(req, "id", skillID)
	testHandler.PromoteSkill(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d %s", w.Code, w.Body.String())
	}

	// Group manager agent may promote.
	agentID := createHandlerTestAgent(t, "Skill Promote Manager", nil)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager')
		ON CONFLICT (channel_id, member_type, member_id) DO UPDATE SET role = 'manager'
	`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed manager: %v", err)
	}

	taskID := createHandlerTestTaskForAgent(t, agentID)
	okRec := httptest.NewRecorder()
	okReq := newRequest(http.MethodPost, "/api/skills/"+skillID+"/promote", PromoteSkillRequest{
		ToLevel:   skillGrantLevelChannel,
		ChannelID: channelID,
	})
	okReq.Header.Set("X-Agent-ID", agentID)
	okReq.Header.Set("X-Task-ID", taskID)
	okReq = withURLParam(okReq, "id", skillID)
	testHandler.PromoteSkill(okRec, okReq)
	if okRec.Code != 200 {
		t.Fatalf("manager promote: %d %s", okRec.Code, okRec.Body.String())
	}
	var resp SkillWithFilesResponse
	if err := json.Unmarshal(okRec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.GrantLevel != skillGrantLevelChannel {
		t.Fatalf("grant_level=%q", resp.GrantLevel)
	}

	list := httptest.NewRecorder()
	listReq := newRequest(http.MethodGet, "/api/skills/"+skillID+"/promotions", nil)
	listReq = withURLParam(listReq, "id", skillID)
	testHandler.ListSkillPromotions(list, listReq)
	var promotions SkillPromotionsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &promotions); err != nil {
		t.Fatalf("decode promotions: %v", err)
	}
	if len(promotions.Items) != 1 || promotions.Items[0].ActorType != "agent" || promotions.Items[0].ActorID != agentID {
		t.Fatalf("audit=%+v", promotions.Items)
	}
}

func TestGetSkillCapabilityFilterHidesPromoteForOrdinaryMember(t *testing.T) {
	skillID, _ := insertHandlerTestSkill(t, "caps-filter", "# x")
	memberID := insertSkillPromoteWorkspaceMember(t, "member")

	w := httptest.NewRecorder()
	req := newRequestAsUser(memberID, http.MethodGet, "/api/skills/"+skillID, nil)
	req = withURLParam(req, "id", skillID)
	testHandler.GetSkill(w, req)
	if w.Code != 200 {
		t.Fatalf("GetSkill: %d %s", w.Code, w.Body.String())
	}
	var resp SkillWithFilesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Capabilities == nil {
		t.Fatal("expected capabilities")
	}
	if resp.Capabilities.CanPromoteToChannel || resp.Capabilities.CanPromoteToWorkspace {
		t.Fatalf("ordinary member should have no promote caps, got %+v", resp.Capabilities)
	}
}

func insertSkillPromoteGroupChannel(t *testing.T, name string) string {
	t.Helper()
	var id string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, kind, created_by)
		VALUES ($1, $2, 'group', $3)
		RETURNING id::text
	`, testWorkspaceID, name, testUserID).Scan(&id); err != nil {
		t.Fatalf("insert group channel: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, id)
	})
	return id
}

func insertSkillPromoteWorkspaceMember(t *testing.T, role string) string {
	t.Helper()
	var userID string
	suffix := randomID()
	email := t.Name() + "-" + role + "-" + suffix + "@example.com"
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id::text
	`, "Skill Promote "+role+" "+suffix, email).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, $3)
	`, testWorkspaceID, userID, role); err != nil {
		t.Fatalf("insert member: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM member WHERE user_id = $1`, userID)
		testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	return userID
}
