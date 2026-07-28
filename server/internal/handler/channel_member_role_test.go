package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

// TestCreateChannelCreatorIsOwnerAndListReturnsRole locks Beckham v2 §4
// data-model: channel_member.role is exposed on list, creator is owner.
func TestCreateChannelCreatorIsOwnerAndListReturnsRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	creator := testUserID
	req := newRequestAs(creator, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-panel-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, creator)
	created := httptest.NewRecorder()
	testHandler.CreateChannel(created, req)
	if created.Code != http.StatusCreated {
		t.Fatalf("CreateChannel = %d: %s", created.Code, created.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(created.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode channel: %v", err)
	}

	var ownerCount, agentOwnerCount int
	err := testPool.QueryRow(context.Background(), `
		SELECT
		  count(*) FILTER (WHERE role = 'owner' AND member_type = 'user'),
		  count(*) FILTER (WHERE role = 'owner' AND member_type = 'agent')
		FROM channel_member WHERE channel_id = $1`, parseUUID(ch.ID)).Scan(&ownerCount, &agentOwnerCount)
	if err != nil {
		t.Fatalf("count owners: %v", err)
	}
	if ownerCount != 1 || agentOwnerCount != 0 {
		t.Fatalf("owners user=%d agent=%d want user=1 agent=0", ownerCount, agentOwnerCount)
	}

	listReq := newRequestAs(creator, http.MethodGet, "/api/channels/"+ch.ID+"/members", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, creator)
	listReq = withURLParam(listReq, "channelId", ch.ID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannelMembers(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListChannelMembers = %d: %s", listRec.Code, listRec.Body.String())
	}
	var members []ChannelMemberResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &members); err != nil {
		t.Fatalf("decode members: %v", err)
	}
	if len(members) == 0 {
		t.Fatal("expected at least creator in members")
	}
	if members[0].MemberID != creator || members[0].Role != "owner" {
		t.Fatalf("first member = %+v want creator owner", members[0])
	}
}

// TestCreateChannelAlwaysWritesHumanOwner asserts concurrent creates never leave
// zero-owner groups (the former `_, _` swallow path).
func TestCreateChannelAlwaysWritesHumanOwner(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("role-atomic-%s-%d", t.Name(), i)
		req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": name})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		rec := httptest.NewRecorder()
		testHandler.CreateChannel(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", name, rec.Code, rec.Body.String())
		}
		var ch ChannelResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
			t.Fatalf("decode: %v", err)
		}
		var n int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM channel_member WHERE channel_id = $1 AND role = 'owner' AND member_type = 'user'`,
			parseUUID(ch.ID)).Scan(&n); err != nil || n != 1 {
			t.Fatalf("channel %s owners=%d err=%v", ch.ID, n, err)
		}
	}
}

// TestAgentCannotBeChannelOwnerCHECK documents 237 CHECK (owner ⇒ user).
func TestAgentCannotBeChannelOwnerCHECK(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-agent-owner-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var agentID pgtype.UUID
	err := testPool.QueryRow(context.Background(),
		`SELECT id FROM agent WHERE workspace_id = $1 LIMIT 1`, parseUUID(testWorkspaceID)).Scan(&agentID)
	if err != nil {
		t.Fatalf("agent fixture required for CHECK test: %v", err)
	}
	_, err = testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'member')
		ON CONFLICT DO NOTHING`, parseUUID(ch.ID), parseUUID(testWorkspaceID), agentID)
	if err != nil {
		t.Fatalf("insert agent member: %v", err)
	}
	_, err = testPool.Exec(context.Background(), `
		UPDATE channel_member SET role = 'owner'
		WHERE channel_id = $1 AND member_type = 'agent' AND member_id = $2`,
		parseUUID(ch.ID), agentID)
	if err == nil {
		t.Fatal("expected CHECK to reject agent owner")
	}
}

// TestRemoveSoleOwnerBlocked: ordinary group cannot lose its only human owner.
func TestRemoveSoleOwnerBlocked(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{
		"name": "role-sole-owner-" + t.Name(),
	})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	var ch ChannelResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ch); err != nil {
		t.Fatal(err)
	}
	// Self-leave as sole owner must fail.
	del := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+ch.ID+"/members/user/"+testUserID, nil)
	del = withChannelTestWorkspaceCtx(t, del, testUserID)
	del = withURLParam(del, "channelId", ch.ID)
	del = withURLParam(del, "memberType", "user")
	del = withURLParam(del, "memberId", testUserID)
	delRec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(delRec, del)
	if delRec.Code != http.StatusConflict {
		t.Fatalf("sole owner self-leave want 409, got %d: %s", delRec.Code, delRec.Body.String())
	}
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM channel_member WHERE channel_id = $1 AND role = 'owner'`,
		parseUUID(ch.ID)).Scan(&n); err != nil || n != 1 {
		t.Fatalf("owner still present: n=%d err=%v", n, err)
	}
}

// TestListChannelsMemberBriefIncludesRole asserts channel list avatar stack
// returns role (not omitempty-empty).
func TestListChannelsMemberBriefIncludesRole(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("handler test DB not configured")
	}
	name := "role-list-brief-" + t.Name()
	req := newRequestAs(testUserID, http.MethodPost, "/api/channels", map[string]any{"name": name})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	rec := httptest.NewRecorder()
	testHandler.CreateChannel(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	listReq := newRequestAs(testUserID, http.MethodGet, "/api/channels", nil)
	listReq = withChannelTestWorkspaceCtx(t, listReq, testUserID)
	listRec := httptest.NewRecorder()
	testHandler.ListChannels(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("ListChannels: %d %s", listRec.Code, listRec.Body.String())
	}
	var channels []ChannelResponse
	if err := json.Unmarshal(listRec.Body.Bytes(), &channels); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, ch := range channels {
		if ch.Name != name {
			continue
		}
		found = true
		if len(ch.Members) == 0 {
			t.Fatal("expected members on channel list brief")
		}
		if ch.Members[0].Role != "owner" {
			t.Fatalf("list brief role=%q want owner", ch.Members[0].Role)
		}
	}
	if !found {
		t.Fatalf("channel %q not in list", name)
	}
}
