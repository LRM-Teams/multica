package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type systemGeneralHandlerFixture struct {
	workspaceID string
	channelID   string
	request     func(method, path string, body any, params ...string) *http.Request
}

func newSystemGeneralHandlerFixture(t *testing.T) systemGeneralHandlerFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	suffix := uuid.NewString()[:8]
	create := httptest.NewRecorder()
	testHandler.CreateWorkspace(create, newRequest(http.MethodPost, "/api/workspaces", map[string]string{
		"name": "System General " + suffix,
		"slug": "system-general-" + suffix,
	}))
	if create.Code != http.StatusCreated {
		t.Fatalf("CreateWorkspace = %d: %s", create.Code, create.Body.String())
	}
	var workspace WorkspaceResponse
	if err := json.Unmarshal(create.Body.Bytes(), &workspace); err != nil {
		t.Fatalf("decode workspace: %v", err)
	}
	t.Cleanup(func() {
		if _, err := testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspace.ID); err != nil {
			t.Errorf("cleanup workspace %s: %v", workspace.ID, err)
		}
	})

	var channelID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text
		FROM channel
		WHERE workspace_id = $1
		  AND name = 'general'
		  AND kind = 'group'
		  AND system_key = 'general'
		  AND archived_at IS NULL
		  AND project_id IS NULL
		  AND lark_chat_id IS NULL
	`, workspace.ID).Scan(&channelID); err != nil {
		t.Fatalf("load pristine general channel: %v", err)
	}

	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: util.MustParseUUID(workspace.ID),
	})
	if err != nil {
		t.Fatalf("load workspace owner: %v", err)
	}
	request := func(method, path string, body any, params ...string) *http.Request {
		req := newRequest(method, path, body)
		req.Header.Set("X-Workspace-ID", workspace.ID)
		req = req.WithContext(middleware.SetMemberContext(req.Context(), workspace.ID, memberRow))
		if len(params) > 0 {
			req = withRouteParams(req, params...)
		}
		return req
	}

	return systemGeneralHandlerFixture{
		workspaceID: workspace.ID,
		channelID:   channelID,
		request:     request,
	}
}

func TestCreateWorkspaceCreatesPristineSystemGeneral(t *testing.T) {
	fixture := newSystemGeneralHandlerFixture(t)

	var channelMembers, conversationMembers, messages int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2
	`, fixture.channelID, testUserID).Scan(&channelMembers); err != nil {
		t.Fatalf("count general owner membership: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM conversation_member
		WHERE conversation_id = $1
		  AND member_type = 'user'
		  AND member_id = $2
		  AND wake_state = 'active'
	`, fixture.channelID, testUserID).Scan(&conversationMembers); err != nil {
		t.Fatalf("count general conversation projection: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM channel_message WHERE channel_id = $1`, fixture.channelID).Scan(&messages); err != nil {
		t.Fatalf("count generated general messages: %v", err)
	}
	if channelMembers != 1 || conversationMembers != 1 || messages != 0 {
		t.Fatalf("general owner/channel projection/messages = %d/%d/%d, want 1/1/0", channelMembers, conversationMembers, messages)
	}

	list := httptest.NewRecorder()
	testHandler.ListChannels(list, fixture.request(http.MethodGet, "/api/channels", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("ListChannels = %d: %s", list.Code, list.Body.String())
	}
	var channels []ChannelResponse
	if err := json.Unmarshal(list.Body.Bytes(), &channels); err != nil {
		t.Fatalf("decode channels: %v", err)
	}
	found := false
	for _, channel := range channels {
		if channel.ID == fixture.channelID {
			found = channel.SystemKey != nil && *channel.SystemKey == "general"
		}
	}
	if !found {
		t.Fatalf("ListChannels did not expose system_key=general: %+v", channels)
	}
}

func TestSystemGeneralHandlersReturnStableProtectedConflict(t *testing.T) {
	fixture := newSystemGeneralHandlerFixture(t)
	name := "renamed"
	operations := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
		req     *http.Request
	}{
		{
			name:    "create reserved name",
			handler: testHandler.CreateChannel,
			req:     fixture.request(http.MethodPost, "/api/channels", map[string]any{"name": "general"}),
		},
		{
			name:    "update",
			handler: testHandler.UpdateChannel,
			req:     fixture.request(http.MethodPatch, "/api/channels/"+fixture.channelID, UpdateChannelRequest{Name: &name}, "channelId", fixture.channelID),
		},
		{
			name:    "delete",
			handler: testHandler.DeleteChannel,
			req:     fixture.request(http.MethodDelete, "/api/channels/"+fixture.channelID, nil, "channelId", fixture.channelID),
		},
		{
			name:    "archive",
			handler: testHandler.ArchiveChannel,
			req:     fixture.request(http.MethodPost, "/api/channels/"+fixture.channelID+"/archive", nil, "channelId", fixture.channelID),
		},
		{
			name:    "restore",
			handler: testHandler.RestoreChannel,
			req:     fixture.request(http.MethodPost, "/api/channels/"+fixture.channelID+"/restore", nil, "channelId", fixture.channelID),
		},
		{
			name:    "add member",
			handler: testHandler.AddChannelMember,
			req: fixture.request(http.MethodPost, "/api/channels/"+fixture.channelID+"/members", AddChannelMemberRequest{
				MemberType: "user",
				MemberID:   testUserID,
			}, "channelId", fixture.channelID),
		},
		{
			name:    "add members batch",
			handler: testHandler.AddChannelMembers,
			req: fixture.request(http.MethodPost, "/api/channels/"+fixture.channelID+"/members/batch", AddChannelMembersRequest{Members: []AddChannelMemberRequest{{
				MemberType: "user",
				MemberID:   testUserID,
			}}}, "channelId", fixture.channelID),
		},
		{
			name:    "remove member",
			handler: testHandler.RemoveChannelMember,
			req: fixture.request(http.MethodDelete, "/api/channels/"+fixture.channelID+"/members/user/"+testUserID+"?expected_remove_effect=none", nil,
				"channelId", fixture.channelID, "memberType", "user", "memberId", testUserID),
		},
		{
			name:    "set project",
			handler: testHandler.SetChannelProject,
			req:     fixture.request(http.MethodPut, "/api/channels/"+fixture.channelID+"/project", map[string]any{"project_id": nil}, "channelId", fixture.channelID),
		},
	}

	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			operation.handler(recorder, operation.req)
			if recorder.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s, want 409", recorder.Code, recorder.Body.String())
			}
			var response map[string]string
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if response["code"] != systemChannelProtectedCode {
				t.Fatalf("code=%q body=%v, want %q", response["code"], response, systemChannelProtectedCode)
			}
		})
	}

	var channelCount, memberCount, messageCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM channel WHERE id = $1 AND system_key = 'general'`, fixture.channelID).Scan(&channelCount); err != nil {
		t.Fatalf("count preserved channel: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM channel_member WHERE channel_id = $1`, fixture.channelID).Scan(&memberCount); err != nil {
		t.Fatalf("count preserved roster: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM channel_message WHERE channel_id = $1`, fixture.channelID).Scan(&messageCount); err != nil {
		t.Fatalf("count protected-operation messages: %v", err)
	}
	if channelCount != 1 || memberCount != 1 || messageCount != 0 {
		t.Fatalf("protected operations changed channel/roster/messages = %d/%d/%d", channelCount, memberCount, messageCount)
	}
}
