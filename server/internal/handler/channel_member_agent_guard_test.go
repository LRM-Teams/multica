package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestChannelMemberAddRequiresAgentCallerMembership(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	for _, tc := range []struct {
		name string
		call func(channelID, targetAgentID, callerAgentID, taskID string) *httptest.ResponseRecorder
	}{
		{
			name: "single",
			call: func(channelID, targetAgentID, callerAgentID, taskID string) *httptest.ResponseRecorder {
				req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{
					MemberType: "agent",
					MemberID:   targetAgentID,
				})
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withURLParam(req, "channelId", channelID)
				req.Header.Set("X-Actor-Source", "task_token")
				req.Header.Set("X-Agent-ID", callerAgentID)
				req.Header.Set("X-Task-ID", taskID)
				rec := httptest.NewRecorder()
				testHandler.AddChannelMember(rec, req)
				return rec
			},
		},
		{
			name: "batch",
			call: func(channelID, targetAgentID, callerAgentID, taskID string) *httptest.ResponseRecorder {
				req := newRequestAs(testUserID, http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{
					Members: []AddChannelMemberRequest{{
						MemberType: "agent",
						MemberID:   targetAgentID,
					}},
				})
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withURLParam(req, "channelId", channelID)
				req.Header.Set("X-Actor-Source", "task_token")
				req.Header.Set("X-Agent-ID", callerAgentID)
				req.Header.Set("X-Task-ID", taskID)
				rec := httptest.NewRecorder()
				testHandler.AddChannelMembers(rec, req)
				return rec
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callerAgentID := createHandlerTestAgent(t, "removed-agent-caller-"+uuid.NewString(), nil)
			targetAgentID := createHandlerTestAgent(t, "target-agent-"+uuid.NewString(), nil)
			taskID := createHandlerTestTaskForAgent(t, callerAgentID)
			channelID := seedChannelForTest(t, "agent-member-guard-"+uuid.NewString(), testUserID)

			// The credential owner remains a channel user, but the bound agent
			// is not a member (the exact shape after a human removes Wendy).
			rec := tc.call(channelID, callerAgentID, callerAgentID, taskID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("removed agent re-add: status=%d body=%s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "agent caller is not a channel member") {
				t.Fatalf("removed agent re-add: unexpected body=%s", rec.Body.String())
			}
			assertChannelAgentMembershipCount(t, ctx, channelID, callerAgentID, 0)

			// Once a human explicitly invites the caller again, its existing
			// delegated add-member workflow remains available.
			if _, err := testPool.Exec(ctx, `
				INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
				VALUES ($1, $2, 'agent', $3)`,
				channelID, testWorkspaceID, callerAgentID,
			); err != nil {
				t.Fatalf("restore caller agent membership: %v", err)
			}
			rec = tc.call(channelID, targetAgentID, callerAgentID, taskID)
			if rec.Code != http.StatusCreated {
				t.Fatalf("active agent add target: status=%d body=%s", rec.Code, rec.Body.String())
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
