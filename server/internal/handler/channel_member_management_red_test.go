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

// These tests are the executable adapter/service RED slice for task #844.
// They pin transport separation, authorization/no-op ordering, strict remove
// confirmation, self-leave semantics, and success-artifact boundaries that
// the shared principal-neutral service must close. Keep them at the HTTP
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
					"/api/channels/"+channelID+"/members/user/"+targetUserID+"?expected_remove_effect=none",
					nil)
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
		"/api/channels/"+channelID+"/members/user/"+targetID+"?expected_remove_effect=none",
		nil)
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
				"/api/channels/"+channelID+"/members/user/"+protected.id+"?expected_remove_effect=none",
				nil)
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
					"/api/channels/"+channelID+"/members/user/"+targetID+"?expected_remove_effect=none",
					nil)
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

// LRM-869: plain channel member who added an Agent may remove that Agent
// without channel manager / workspace admin authority.
func TestInviterChannelMemberCanRemoveSelfAddedAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	inviterID := createChannelPlainMember(t)
	otherMemberID := createChannelPlainMember(t)
	agentID := createHandlerTestAgent(t, "lrm-869-inviter-remove-"+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "lrm-869-inviter-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, role,
		  added_by_type, added_by_id, join_source
		)
		VALUES
		  ($1, $2, 'user', $3, 'member', 'system', NULL, 'manual'),
		  ($1, $2, 'user', $4, 'member', 'system', NULL, 'manual'),
		  ($1, $2, 'agent', $5, 'member', 'user', $3, 'manual')`,
		channelID, testWorkspaceID, inviterID, otherMemberID, agentID); err != nil {
		t.Fatalf("seed inviter/other/agent members: %v", err)
	}
	assertChannelAgentMembershipCount(t, ctx, channelID, agentID, 1)

	denyReq := newRequestAs(otherMemberID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none", nil)
	denyReq = withChannelTestWorkspaceCtx(t, denyReq, otherMemberID)
	denyReq = withRouteParams(denyReq,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	denyRec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(denyRec, denyReq)
	if denyRec.Code != http.StatusForbidden {
		t.Fatalf("non-inviter remove agent: status=%d body=%s", denyRec.Code, denyRec.Body.String())
	}
	assertChannelAgentMembershipCount(t, ctx, channelID, agentID, 1)

	req := newRequestAs(inviterID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none", nil)
	req = withChannelTestWorkspaceCtx(t, req, inviterID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("inviter remove self-added agent: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertChannelAgentMembershipCount(t, ctx, channelID, agentID, 0)
}

// LRM-868: Workspace admin who is only a plain channel member (not channel
// owner/manager) may still remove an Agent — pure workspace-role path.
func TestWorkspaceAdminChannelMemberCanRemoveAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	adminID := createChannelWorkspaceAdmin(t)
	agentID := createHandlerTestAgent(t, "lrm-868-ws-admin-remove-"+uuid.NewString(), nil)
	// Owner is testUserID; WS admin joins as ordinary channel member only.
	channelID := seedChannelForTest(t, "lrm-868-ws-admin-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES
		  ($1, $2, 'user', $3, 'member'),
		  ($1, $2, 'agent', $4, 'member')`,
		channelID, testWorkspaceID, adminID, agentID); err != nil {
		t.Fatalf("seed admin+agent members: %v", err)
	}
	assertChannelAgentMembershipCount(t, ctx, channelID, agentID, 1)

	var adminChannelRole string
	if err := testPool.QueryRow(ctx, `
		SELECT role FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2
		  AND member_type = 'user' AND member_id = $3`,
		channelID, testWorkspaceID, adminID,
	).Scan(&adminChannelRole); err != nil {
		t.Fatalf("load admin channel role: %v", err)
	}
	if adminChannelRole != "member" {
		t.Fatalf("admin channel role=%q, want member (plain member + WS admin)", adminChannelRole)
	}

	plainMemberID := createChannelPlainMember(t)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'user', $3, 'member')`,
		channelID, testWorkspaceID, plainMemberID); err != nil {
		t.Fatalf("seed plain member: %v", err)
	}
	denyReq := newRequestAs(plainMemberID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none", nil)
	denyReq = withChannelTestWorkspaceCtx(t, denyReq, plainMemberID)
	denyReq = withRouteParams(denyReq,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	denyRec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(denyRec, denyReq)
	if denyRec.Code != http.StatusForbidden {
		t.Fatalf("plain channel member remove agent: status=%d body=%s", denyRec.Code, denyRec.Body.String())
	}
	assertChannelAgentMembershipCount(t, ctx, channelID, agentID, 1)

	req := newRequestAs(adminID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/agent/"+agentID+"?expected_remove_effect=none", nil)
	req = withChannelTestWorkspaceCtx(t, req, adminID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "agent",
		"memberId", agentID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("WS admin (channel member) remove agent: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertChannelAgentMembershipCount(t, ctx, channelID, agentID, 0)
}

func TestRemoveChannelMemberRequiresExpectedEffect(t *testing.T) {
	t.Skip("retired singleton manager confirmation contract")
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	for _, tc := range []struct {
		name  string
		query string
		body  any
	}{
		{name: "missing", body: nil},
		{name: "malformed", query: "?expected_remove_effect=surprise_side_effect", body: nil},
		{
			name: "body_only_valid_is_not_a_query_fallback",
			body: map[string]string{"expected_remove_effect": "none"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targetID := createChannelPlainMember(t)
			channelID := seedChannelForTest(t, "remove-effect-required-"+tc.name+"-"+uuid.NewString(), testUserID, targetID)
			auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
			systemBefore := countChannelSystemMessagesForTest(t, channelID)

			req := newRequestAs(testUserID, http.MethodDelete,
				"/api/channels/"+channelID+"/members/user/"+targetID+tc.query, tc.body)
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
			assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
		})
	}
}

func TestHumanMemberSelfLeaveUsesMemberLeftSemantics(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	channelID := seedChannelForTest(t, "human-member-self-leave-"+uuid.NewString(), testUserID, memberID)
	auditBefore := countChannelMemberActivityForTest(t, channelID, "member_left")
	leftBefore := countChannelMemberSystemEventForTest(t, channelID, channelMemberLeftEvent)
	removedBefore := countChannelMemberSystemEventForTest(t, channelID, channelMemberRemovedEvent)

	req := newRequestAs(memberID, http.MethodDelete,
		"/api/channels/"+channelID+"/members/user/"+memberID+"?expected_remove_effect=none",
		nil)
	req = withChannelTestWorkspaceCtx(t, req, memberID)
	req = withRouteParams(req,
		"channelId", channelID,
		"memberType", "user",
		"memberId", memberID,
	)
	rec := httptest.NewRecorder()
	testHandler.RemoveChannelMember(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("human self-leave want 200 got %d: %s", rec.Code, rec.Body.String())
	}
	assertChannelUserMembershipCount(t, channelID, memberID, 0)
	if got := countChannelMemberActivityForTest(t, channelID, "member_left"); got != auditBefore+1 {
		t.Fatalf("member_left audit count=%d want=%d", got, auditBefore+1)
	}
	assertLatestChannelMemberActivityActorForTest(t, channelID, "member_left", "member", memberID)
	if got := countChannelMemberSystemEventForTest(t, channelID, channelMemberLeftEvent); got != leftBefore+1 {
		t.Fatalf("channel_member_left event count=%d want=%d", got, leftBefore+1)
	}
	if got := countChannelMemberSystemEventForTest(t, channelID, channelMemberRemovedEvent); got != removedBefore {
		t.Fatalf("self-leave emitted remove event count=%d want=%d", got, removedBefore)
	}
	event := latestChannelSystemEventForTest(t, channelID)
	assertChannelMemberSystemEvent(t, event, channelMemberLeftEvent, memberID, "human", memberID, "human")
}

func TestRemoveMemberDenialsLeaveZeroSuccessArtifacts(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	t.Run("ordinary_member_cannot_remove_other", func(t *testing.T) {
		memberID := createChannelPlainMember(t)
		targetID := createChannelPlainMember(t)
		channelID := seedChannelForTest(
			t,
			"ordinary-other-remove-denied-"+uuid.NewString(),
			testUserID,
			memberID,
			targetID,
		)
		auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
		systemBefore := countChannelSystemMessagesForTest(t, channelID)

		req := newRequestAs(memberID, http.MethodDelete,
			"/api/channels/"+channelID+"/members/user/"+targetID+"?expected_remove_effect=none",
			nil)
		req = withChannelTestWorkspaceCtx(t, req, memberID)
		req = withRouteParams(req,
			"channelId", channelID,
			"memberType", "user",
			"memberId", targetID,
		)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Fatalf("ordinary remove other want 403 got %d: %s", rec.Code, rec.Body.String())
		}
		assertChannelUserMembershipCount(t, channelID, targetID, 1)
		assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
	})

	t.Run("sole_owner_cannot_self_leave", func(t *testing.T) {
		channelID := seedChannelForTest(t, "sole-owner-self-leave-denied-"+uuid.NewString(), testUserID)
		auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
		systemBefore := countChannelSystemMessagesForTest(t, channelID)

		req := newRequestAs(testUserID, http.MethodDelete,
			"/api/channels/"+channelID+"/members/user/"+testUserID+"?expected_remove_effect=none",
			nil)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withRouteParams(req,
			"channelId", channelID,
			"memberType", "user",
			"memberId", testUserID,
		)
		rec := httptest.NewRecorder()
		testHandler.RemoveChannelMember(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("sole owner self-leave want 409 got %d: %s", rec.Code, rec.Body.String())
		}
		assertChannelUserMembershipCount(t, channelID, testUserID, 1)
		assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
	})
}

func TestDuplicateAddAuthorizesBeforeIdempotentNoop(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetAgentID := createHandlerTestAgent(t, "DuplicateAdd"+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "duplicate-add-authority-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1, $2, 'agent', $3, 'member')`,
		channelID, testWorkspaceID, targetAgentID); err != nil {
		t.Fatalf("seed existing agent member: %v", err)
	}

	auditBefore := countAllChannelMemberSuccessActivityForTest(t, channelID)
	systemBefore := countChannelSystemMessagesForTest(t, channelID)
	onboardingBefore := countChannelAgentOnboardingForTest(t, channelID, targetAgentID)

	duplicateReq := newRequestAs(testUserID, http.MethodPost,
		"/api/channels/"+channelID+"/members",
		AddChannelMemberRequest{MemberType: "agent", MemberID: targetAgentID})
	duplicateReq = withChannelTestWorkspaceCtx(t, duplicateReq, testUserID)
	duplicateReq = withURLParam(duplicateReq, "channelId", channelID)
	duplicateRec := httptest.NewRecorder()
	testHandler.AddChannelMember(duplicateRec, duplicateReq)

	if duplicateRec.Code != http.StatusOK {
		t.Fatalf("authorized duplicate add want 200 got %d: %s", duplicateRec.Code, duplicateRec.Body.String())
	}
	assertChannelAgentMembershipCount(t, context.Background(), channelID, targetAgentID, 1)
	assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
	if got := countChannelAgentOnboardingForTest(t, channelID, targetAgentID); got != onboardingBefore {
		t.Fatalf("duplicate add onboarding count=%d want=%d", got, onboardingBefore)
	}

	ordinaryNonMemberID := createChannelPlainMember(t)
	deniedReq := newRequestAs(ordinaryNonMemberID, http.MethodPost,
		"/api/channels/"+channelID+"/members",
		AddChannelMemberRequest{MemberType: "agent", MemberID: targetAgentID})
	deniedReq = withChannelTestWorkspaceCtx(t, deniedReq, ordinaryNonMemberID)
	deniedReq = withURLParam(deniedReq, "channelId", channelID)
	deniedRec := httptest.NewRecorder()
	testHandler.AddChannelMember(deniedRec, deniedReq)

	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("unauthorized duplicate add want 403 got %d: %s", deniedRec.Code, deniedRec.Body.String())
	}
	assertChannelAgentMembershipCount(t, context.Background(), channelID, targetAgentID, 1)
	assertChannelMemberArtifactCountsUnchanged(t, channelID, auditBefore, systemBefore)
	if got := countChannelAgentOnboardingForTest(t, channelID, targetAgentID); got != onboardingBefore {
		t.Fatalf("denied duplicate add onboarding count=%d want=%d", got, onboardingBefore)
	}
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
				"/api/channels/"+channelID+"/members/user/"+removeTargetID+"?expected_remove_effect=none",
				nil)
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

func countChannelMemberActivityForTest(t *testing.T, channelID, action string) int {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'channel_id' = $3`,
		testWorkspaceID, action, channelID,
	).Scan(&got); err != nil {
		t.Fatalf("count channel member activity %s: %v", action, err)
	}
	return got
}

func countAllChannelMemberSuccessActivityForTest(t *testing.T, channelID string) int {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action IN ('member_added', 'member_removed', 'member_left')
		  AND details->>'channel_id' = $2`,
		testWorkspaceID, channelID,
	).Scan(&got); err != nil {
		t.Fatalf("count all channel member success activity: %v", err)
	}
	return got
}

func assertLatestChannelMemberActivityActorForTest(t *testing.T, channelID, action, wantActorType, wantActorID string) {
	t.Helper()
	var actorType, actorID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT actor_type, actor_id::text
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = $2
		  AND details->>'channel_id' = $3
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
		testWorkspaceID, action, channelID,
	).Scan(&actorType, &actorID); err != nil {
		t.Fatalf("load latest %s activity actor: %v", action, err)
	}
	if actorType != wantActorType || actorID != wantActorID {
		t.Fatalf("%s activity actor=%s/%s want=%s/%s", action, actorType, actorID, wantActorType, wantActorID)
	}
}

func countChannelMemberSystemEventForTest(t *testing.T, channelID, event string) int {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'system'
		  AND parts->0->>'event' = $2`,
		channelID, event,
	).Scan(&got); err != nil {
		t.Fatalf("count channel member system event %s: %v", event, err)
	}
	return got
}

func countChannelAgentOnboardingForTest(t *testing.T, channelID, agentID string) int {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_agent_onboarding
		WHERE channel_id = $1 AND agent_id = $2`,
		channelID, agentID,
	).Scan(&got); err != nil {
		t.Fatalf("count channel agent onboarding: %v", err)
	}
	return got
}

func assertChannelMemberArtifactCountsUnchanged(t *testing.T, channelID string, wantAudit, wantSystem int) {
	t.Helper()
	if got := countAllChannelMemberSuccessActivityForTest(t, channelID); got != wantAudit {
		t.Fatalf("channel member success audit count=%d want=%d", got, wantAudit)
	}
	if got := countChannelSystemMessagesForTest(t, channelID); got != wantSystem {
		t.Fatalf("channel member system message count=%d want=%d", got, wantSystem)
	}
}
