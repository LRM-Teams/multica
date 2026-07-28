package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// These tests are the first executable RED slice for task #844. They pin
// transport separation and the two authorization gaps that the shared
// principal-neutral decision function must close. Keep them at the HTTP
// boundary: a green result must prove both the decision and the mutation.

func TestMemberManagementHumanRoutesRejectAgentPrincipal(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, channelID, callerAgentID, targetUserID string) *httptest.ResponseRecorder
	}{
		{
			name: "single_add",
			run: func(t *testing.T, channelID, callerAgentID, targetUserID string) *httptest.ResponseRecorder {
				req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{
					MemberType: "user",
					MemberID:   targetUserID,
				})
				req = withAgentPrincipal(req, callerAgentID, testWorkspaceID, testUserID)
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withURLParam(req, "channelId", channelID)
				rec := httptest.NewRecorder()
				testHandler.AddChannelMember(rec, req)
				return rec
			},
		},
		{
			name: "batch_add",
			run: func(t *testing.T, channelID, callerAgentID, targetUserID string) *httptest.ResponseRecorder {
				req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/members/batch", AddChannelMembersRequest{
					Members: []AddChannelMemberRequest{{
						MemberType: "user",
						MemberID:   targetUserID,
					}},
				})
				req = withAgentPrincipal(req, callerAgentID, testWorkspaceID, testUserID)
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withURLParam(req, "channelId", channelID)
				rec := httptest.NewRecorder()
				testHandler.AddChannelMembers(rec, req)
				return rec
			},
		},
		{
			name: "remove",
			run: func(t *testing.T, channelID, callerAgentID, targetUserID string) *httptest.ResponseRecorder {
				req := newRequest(http.MethodDelete,
					"/api/channels/"+channelID+"/members/user/"+targetUserID,
					map[string]string{"expected_remove_effect": "none"})
				req = withAgentPrincipal(req, callerAgentID, testWorkspaceID, testUserID)
				req = withChannelTestWorkspaceCtx(t, req, testUserID)
				req = withRouteParams(req,
					"channelId", channelID,
					"memberType", "user",
					"memberId", targetUserID,
				)
				rec := httptest.NewRecorder()
				testHandler.RemoveChannelMember(rec, req)
				return rec
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetUserID := createChannelPlainMember(t)
			channelID := seedChannelForTest(t, "member-management-human-route-"+uuid.NewString(), testUserID)
			callerAgentID := createHandlerTestAgent(t, "HumanRouteGuard"+uuid.NewString()[:8], nil)
			if _, err := testPool.Exec(context.Background(), `
				INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
				VALUES ($1, $2, 'agent', $3, 'manager')`,
				channelID, testWorkspaceID, callerAgentID); err != nil {
				t.Fatalf("seed caller agent member: %v", err)
			}

			if tc.name == "remove" {
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
					VALUES ($1, $2, 'user', $3)`,
					channelID, testWorkspaceID, targetUserID); err != nil {
					t.Fatalf("seed remove target: %v", err)
				}
			}

			rec := tc.run(t, channelID, callerAgentID, targetUserID)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("%s want 403 got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "agent must use dedicated") {
				t.Fatalf("%s body=%q want dedicated route error", tc.name, rec.Body.String())
			}

			wantMembership := 0
			if tc.name == "remove" {
				wantMembership = 1
			}
			assertChannelUserMembershipCount(t, channelID, targetUserID, wantMembership)
		})
	}
}

func TestChannelManagerCanRemoveOrdinaryMember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	managerID := createChannelPlainMember(t)
	targetID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "manager-removes-member-"+uuid.NewString(), testUserID, managerID, targetID)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel_member
		SET role = 'manager'
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, managerID); err != nil {
		t.Fatalf("promote channel manager: %v", err)
	}

	req := newRequestAs(managerID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/user/"+targetID,
		map[string]string{"expected_remove_effect": "none"})
	req = withChannelTestWorkspaceCtx(t, req, managerID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "user",
		"memberId", targetID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("channel manager remove want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelUserMembershipCount(t, channelID, targetID, 0)
}

func TestNonMemberWorkspaceAdminCanManageOrdinaryMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, tc := range []struct {
		name string
		run  func(t *testing.T, channelID, adminID, targetID string) *httptest.ResponseRecorder
		want int
	}{
		{
			name: "add",
			run: func(t *testing.T, channelID, adminID, targetID string) *httptest.ResponseRecorder {
				req := newRequestAs(adminID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{
					MemberType: "user",
					MemberID:   targetID,
				})
				req = withChannelTestWorkspaceCtx(t, req, adminID)
				req = withURLParam(req, "channelId", channelID)
				rec := httptest.NewRecorder()
				testHandler.AddChannelMember(rec, req)
				return rec
			},
			want: 1,
		},
		{
			name: "remove",
			run: func(t *testing.T, channelID, adminID, targetID string) *httptest.ResponseRecorder {
				req := newRequestAs(adminID, http.MethodDelete,
					"/api/channels/"+channelID+"/members/user/"+targetID,
					map[string]string{"expected_remove_effect": "none"})
				req = withChannelTestWorkspaceCtx(t, req, adminID)
				req = withRouteParams(req,
					"channelId", channelID,
					"memberType", "user",
					"memberId", targetID,
				)
				rec := httptest.NewRecorder()
				testHandler.RemoveChannelMember(rec, req)
				return rec
			},
			want: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			adminID := createChannelWorkspaceAdmin(t)
			targetID := createChannelPlainMember(t)
			channelID := seedChannelForTest(t, "nonmember-admin-"+tc.name+"-"+uuid.NewString(), testUserID)
			if tc.name == "remove" {
				if _, err := testPool.Exec(context.Background(), `
					INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
					VALUES ($1, $2, 'user', $3)`,
					channelID, testWorkspaceID, targetID); err != nil {
					t.Fatalf("seed remove target: %v", err)
				}
			}
			assertChannelUserMembershipCount(t, channelID, adminID, 0)

			rec := tc.run(t, channelID, adminID, targetID)
			if rec.Code < 200 || rec.Code >= 300 {
				t.Fatalf("non-member admin %s want success got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
			assertChannelUserMembershipCount(t, channelID, adminID, 0)
			assertChannelUserMembershipCount(t, channelID, targetID, tc.want)
		})
	}
}

func assertChannelUserMembershipCount(t *testing.T, channelID, userID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, userID,
	).Scan(&got); err != nil {
		t.Fatalf("count channel user membership: %v", err)
	}
	if got != want {
		t.Fatalf("channel user membership channel=%s user=%s got=%d want=%d", channelID, userID, got, want)
	}
}
