package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentChannelMemberAddRequiresManagementAuthority(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(channelID, targetAgentID, callerAgentID string) *httptest.ResponseRecorder
	}{
		{
			name: "single",
			call: func(channelID, targetAgentID, callerAgentID string) *httptest.ResponseRecorder {
				req := newRequest(http.MethodPost, "/api/agent/channels/"+channelID+"/members", AddChannelMemberRequest{
					MemberType: "agent",
					MemberID:   targetAgentID,
				})
				req = withAgentPrincipal(req, callerAgentID, testWorkspaceID, testUserID)
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withURLParam(req, "channelId", channelID)
				rec := httptest.NewRecorder()
				testHandler.AddAgentChannelMember(rec, req)
				return rec
			},
		},
		{
			name: "batch",
			call: func(channelID, targetAgentID, callerAgentID string) *httptest.ResponseRecorder {
				req := newRequest(http.MethodPost, "/api/agent/channels/"+channelID+"/members/batch", AddChannelMembersRequest{
					Members: []AddChannelMemberRequest{{
						MemberType: "agent",
						MemberID:   targetAgentID,
					}},
				})
				req = withAgentPrincipal(req, callerAgentID, testWorkspaceID, testUserID)
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withURLParam(req, "channelId", channelID)
				rec := httptest.NewRecorder()
				testHandler.AddAgentChannelMembers(rec, req)
				return rec
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callerAgentID := createHandlerTestAgent(t, "removed-agent-caller-"+uuid.NewString(), nil)
			targetAgentID := createHandlerTestAgent(t, "target-agent-"+uuid.NewString(), nil)
			channelID := seedChannelForTest(t, "agent-member-guard-"+uuid.NewString(), testUserID)

			rec := tc.call(channelID, targetAgentID, callerAgentID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("ordinary non-member agent add: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), MemberManagementCodeForbidden) {
				t.Fatalf("ordinary non-member agent add: unexpected body=%s", rec.Body.String())
			}
			assertChannelAgentMembershipCount(t, ctx, channelID, targetAgentID, 0)

			if _, err := testPool.Exec(ctx, `
				INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
				VALUES ($1, $2, 'agent', $3, 'manager')
ON CONFLICT DO NOTHING`,
				channelID, testWorkspaceID, callerAgentID,
			); err != nil {
				t.Fatalf("grant caller agent manager role: %v", err)
			}
			rec = tc.call(channelID, targetAgentID, callerAgentID)
			if rec.Code != http.StatusCreated {
				t.Fatalf("agent manager add target: status=%d body=%s", rec.Code, rec.Body.String())
			}
			assertChannelAgentMembershipCount(t, ctx, channelID, targetAgentID, 1)
		})
	}
}

func assertChannelAgentMembershipCount(t *testing.T, ctx context.Context, channelID, agentID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = 'agent'
		  AND member_id = $3`,
		channelID, testWorkspaceID, agentID,
	).Scan(&got); err != nil {
		t.Fatalf("count channel agent membership: %v", err)
	}
	if got != want {
		t.Fatalf("channel agent membership count=%d, want %d", got, want)
	}
}
