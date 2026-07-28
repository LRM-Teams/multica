package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
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
	peerManagerID := createChannelPlainMember(t)
	channelID := seedChannelForTest(
		t,
		"manager-removes-member-"+uuid.NewString(),
		testUserID,
		managerID,
		peerManagerID,
		targetID,
	)
	for _, id := range []string{managerID, peerManagerID} {
		if _, err := testPool.Exec(context.Background(), `
			UPDATE channel_member
			SET role = 'manager'
			WHERE channel_id = $1 AND workspace_id = $2
			  AND member_type = 'user' AND member_id = $3`,
			channelID, testWorkspaceID, id); err != nil {
			t.Fatalf("promote channel manager %s: %v", id, err)
		}
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

	for _, protected := range []struct {
		name string
		id   string
	}{
		{name: "peer_manager", id: peerManagerID},
		{name: "owner", id: testUserID},
	} {
		t.Run("cannot_remove_"+protected.name, func(t *testing.T) {
			req := newRequestAs(managerID, http.MethodDelete,
				"/api/channels/"+channelID+"/members/user/"+protected.id,
				map[string]string{"expected_remove_effect": "none"})
			req = withChannelTestWorkspaceCtx(t, req, managerID)
			req = withRouteParams(req,
				"channelId", channelID,
				"memberType", "user",
				"memberId", protected.id,
			)
			rec := httptest.NewRecorder()
			testHandler.RemoveChannelMember(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("channel manager remove %s want 403 got %d: %s", protected.name, rec.Code, rec.Body.String())
			}
			assertChannelUserMembershipCount(t, channelID, protected.id, 1)
		})
	}
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

func TestRemoveChannelMemberRequiresExpectedEffect(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, tc := range []struct {
		name string
		body any
	}{
		{name: "missing", body: nil},
		{name: "malformed", body: map[string]string{"expected_remove_effect": "surprise_side_effect"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetID := createChannelPlainMember(t)
			channelID := seedChannelForTest(t, "remove-effect-required-"+tc.name+"-"+uuid.NewString(), testUserID, targetID)
			systemBefore := countChannelSystemMessagesForTest(t, channelID)

			req := newRequestAs(testUserID, http.MethodDelete,
				"/api/channels/"+channelID+"/members/user/"+targetID, tc.body)
			req = withChannelTestWorkspaceCtx(t, req, testUserID)
			req = withRouteParams(req,
				"channelId", channelID,
				"memberType", "user",
				"memberId", targetID,
			)
			rec := httptest.NewRecorder()
			testHandler.RemoveChannelMember(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s expected_remove_effect want 400 got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
			assertChannelUserMembershipCount(t, channelID, targetID, 1)
			if got := countChannelSystemMessagesForTest(t, channelID); got != systemBefore {
				t.Errorf("%s system events got=%d want=%d", tc.name, got, systemBefore)
			}
		})
	}
}

func TestRemoveChannelMemberRejectsEffectMismatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("expected_none_but_target_is_bound", func(t *testing.T) {
		channelID := seedChannelForTest(t, "remove-effect-bound-mismatch-"+uuid.NewString(), testUserID)
		agentID := seedBoundGroupManagerAgent(t, channelID)
		systemBefore := countChannelSystemMessagesForTest(t, channelID)

		req := newRequestAs(testUserID, http.MethodDelete,
			"/api/channels/"+channelID+"/members/agent/"+agentID,
			map[string]string{"expected_remove_effect": "none"})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withRouteParams(req,
			"channelId", channelID,
			"memberType", "agent",
			"memberId", agentID,
		)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, req)

		assertRemoveEffectChangedResponse(t, rec)
		assertChannelAgentMembershipCount(t, context.Background(), channelID, agentID, 1)
		assertGroupManagerBinding(t, channelID, agentID)
		if got := countChannelSystemMessagesForTest(t, channelID); got != systemBefore {
			t.Errorf("bound mismatch system events got=%d want=%d", got, systemBefore)
		}
	})

	t.Run("expected_binding_clear_but_target_is_unbound", func(t *testing.T) {
		targetID := createChannelPlainMember(t)
		channelID := seedChannelForTest(t, "remove-effect-unbound-mismatch-"+uuid.NewString(), testUserID, targetID)
		systemBefore := countChannelSystemMessagesForTest(t, channelID)

		req := newRequestAs(testUserID, http.MethodDelete,
			"/api/channels/"+channelID+"/members/user/"+targetID,
			map[string]string{"expected_remove_effect": "clears_automation_binding"})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withRouteParams(req,
			"channelId", channelID,
			"memberType", "user",
			"memberId", targetID,
		)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, req)

		assertRemoveEffectChangedResponse(t, rec)
		assertChannelUserMembershipCount(t, channelID, targetID, 1)
		if got := countChannelSystemMessagesForTest(t, channelID); got != systemBefore {
			t.Errorf("unbound mismatch system events got=%d want=%d", got, systemBefore)
		}
	})
}

func TestRemoveBoundAgentClearsAutomationBinding(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	channelID := seedChannelForTest(t, "remove-bound-agent-"+uuid.NewString(), testUserID)
	agentID := seedBoundGroupManagerAgent(t, channelID)

	req := newRequestAs(testUserID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID,
		map[string]string{"expected_remove_effect": "clears_automation_binding"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("remove bound agent want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelAgentMembershipCount(t, context.Background(), channelID, agentID, 0)
	assertGroupManagerBinding(t, channelID, "")
}

func TestNonMemberWorkspaceAdminStillCannotReadPrivateChannelContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	adminID := createChannelWorkspaceAdmin(t)
	channelID := seedChannelForTest(t, "nonmember-admin-content-deny-"+uuid.NewString(), testUserID)
	assertChannelUserMembershipCount(t, channelID, adminID, 0)

	root, err := testHandler.insertChannelMessage(
		context.Background(),
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		"private content",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		strPtr("admin-content-deny-"+uuid.NewString()),
		0,
	)
	if err != nil {
		t.Fatalf("seed private channel root: %v", err)
	}

	for _, tc := range []struct {
		name    string
		path    string
		params  []string
		handler http.HandlerFunc
	}{
		{
			name:    "messages",
			path:    "/api/channels/" + channelID + "/messages",
			params:  []string{"channelId", channelID},
			handler: testHandler.ListChannelMessages,
		},
		{
			name:    "thread",
			path:    "/api/channels/" + channelID + "/messages/" + root.ID + "/thread",
			params:  []string{"channelId", channelID, "messageId", root.ID},
			handler: testHandler.ListChannelMessageThread,
		},
		{
			name:    "attachments",
			path:    "/api/channels/" + channelID + "/attachments",
			params:  []string{"channelId", channelID},
			handler: testHandler.ListChannelAttachments,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := newRequestAs(adminID, http.MethodGet, tc.path, nil)
			req = withChannelTestWorkspaceCtx(t, req, adminID)
			req = withURLParams(req, tc.params...)
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("non-member admin %s content read want 403 got %d: %s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestArchivedChannelDeniesHumanMemberMutationsForEveryAuthority(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, role := range []string{"member", "manager", "owner", "workspace_admin"} {
		t.Run(role, func(t *testing.T) {
			actorID := testUserID
			channelMembers := []string{testUserID}
			switch role {
			case "member", "manager":
				actorID = createChannelPlainMember(t)
				channelMembers = append(channelMembers, actorID)
			case "workspace_admin":
				actorID = createChannelWorkspaceAdmin(t)
			}

			removeTargetID := createChannelPlainMember(t)
			addTargetID := createChannelPlainMember(t)
			channelMembers = append(channelMembers, removeTargetID)
			channelID := seedChannelForTest(t, "archived-member-management-"+role+"-"+uuid.NewString(), channelMembers...)
			if role == "manager" {
				if _, err := testPool.Exec(context.Background(), `
					UPDATE channel_member SET role = 'manager'
					WHERE channel_id = $1 AND workspace_id = $2
					  AND member_type = 'user' AND member_id = $3`,
					channelID, testWorkspaceID, actorID); err != nil {
					t.Fatalf("promote manager: %v", err)
				}
			}
			if _, err := testPool.Exec(context.Background(), `
				UPDATE channel SET archived_at = now(), archived_by = $2 WHERE id = $1`,
				channelID, testUserID); err != nil {
				t.Fatalf("archive channel: %v", err)
			}

			addReq := newRequestAs(actorID, http.MethodPost, "/api/channels/"+channelID+"/members", AddChannelMemberRequest{
				MemberType: "user",
				MemberID:   addTargetID,
			})
			addReq = withChannelTestWorkspaceCtx(t, addReq, actorID)
			addReq = withURLParam(addReq, "channelId", channelID)
			addRec := httptest.NewRecorder()
			testHandler.AddChannelMember(addRec, addReq)
			if addRec.Code >= 200 && addRec.Code < 300 {
				t.Errorf("archived %s add unexpectedly succeeded: %d %s", role, addRec.Code, addRec.Body.String())
			}

			removeReq := newRequestAs(actorID, http.MethodDelete,
				"/api/channels/"+channelID+"/members/user/"+removeTargetID,
				map[string]string{"expected_remove_effect": "none"})
			removeReq = withChannelTestWorkspaceCtx(t, removeReq, actorID)
			removeReq = withRouteParams(removeReq,
				"channelId", channelID,
				"memberType", "user",
				"memberId", removeTargetID,
			)
			removeRec := httptest.NewRecorder()
			testHandler.RemoveChannelMember(removeRec, removeReq)
			if removeRec.Code >= 200 && removeRec.Code < 300 {
				t.Errorf("archived %s remove unexpectedly succeeded: %d %s", role, removeRec.Code, removeRec.Body.String())
			}

			assertChannelUserMembershipCount(t, channelID, addTargetID, 0)
			assertChannelUserMembershipCount(t, channelID, removeTargetID, 1)
		})
	}
}

func seedBoundGroupManagerAgent(t *testing.T, channelID string) string {
	t.Helper()
	agentID := createHandlerTestAgent(t, "BoundManager"+uuid.NewString()[:8], nil)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent SET managed_role = 'group_manager' WHERE id = $1`,
		agentID); err != nil {
		t.Fatalf("mark group manager agent: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'manager')`,
		channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed bound agent member: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel SET group_manager_agent_id = $2 WHERE id = $1`,
		channelID, agentID); err != nil {
		t.Fatalf("bind group manager agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			UPDATE channel SET group_manager_agent_id = NULL WHERE id = $1`,
			channelID)
	})
	return agentID
}

func assertRemoveEffectChangedResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusConflict {
		t.Errorf("effect mismatch want 409 got %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "remove_effect_changed") {
		t.Errorf("effect mismatch body=%q want remove_effect_changed", rec.Body.String())
	}
}

func assertGroupManagerBinding(t *testing.T, channelID, wantAgentID string) {
	t.Helper()
	var got pgtype.UUID
	if err := testPool.QueryRow(context.Background(), `
		SELECT group_manager_agent_id FROM channel WHERE id = $1`,
		channelID,
	).Scan(&got); err != nil {
		t.Fatalf("load group manager binding: %v", err)
	}
	if wantAgentID == "" {
		if got.Valid {
			t.Fatalf("group manager binding got=%s want NULL", uuidToString(got))
		}
		return
	}
	if !got.Valid || uuidToString(got) != wantAgentID {
		t.Fatalf("group manager binding got=%s valid=%t want=%s", uuidToString(got), got.Valid, wantAgentID)
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
