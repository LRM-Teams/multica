package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

const (
	humanMemberManagementCapabilitiesHandler = "GetChannelMemberManagementCapabilities"
	agentMemberManagementCapabilitiesHandler = "GetAgentChannelMemberManagementCapabilities"
)

type memberManagementCapabilitiesResponse struct {
	ChannelID        string                             `json:"channel_id"`
	Name             string                             `json:"name"`
	Kind             string                             `json:"kind"`
	Archived         bool                               `json:"archived"`
	CanAddMembers    bool                               `json:"can_add_members"`
	CanRemoveMembers bool                               `json:"can_remove_members"`
	CanLeave         bool                               `json:"can_leave"`
	Targets          []memberManagementCapabilityTarget `json:"targets"`
}

type memberManagementCapabilityTarget struct {
	MemberType           string  `json:"member_type"`
	MemberID             string  `json:"member_id"`
	DisplayName          string  `json:"display_name"`
	AvatarURL            *string `json:"avatar_url"`
	Role                 string  `json:"role"`
	CanRemove            bool    `json:"can_remove"`
	CanPromoteToManager  bool    `json:"can_promote_to_manager"`
	CanDemoteToMember    bool    `json:"can_demote_to_member"`
	CanTransferOwnership bool    `json:"can_transfer_ownership"`
}

type memberManagementCapabilitiesFixture struct {
	channelID      string
	ownerID        string
	memberID       string
	managerID      string
	agentMemberID  string
	agentManagerID string
	channelName    string
}

func newMemberManagementCapabilitiesFixture(t *testing.T) memberManagementCapabilitiesFixture {
	t.Helper()
	return newMemberManagementCapabilitiesFixtureForOwner(t, testUserID)
}

func newMemberManagementCapabilitiesFixtureForOwner(
	t *testing.T,
	ownerID string,
) memberManagementCapabilitiesFixture {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	memberID := createChannelPlainMember(t)
	managerID := createChannelPlainMember(t)
	agentMemberID := createHandlerTestAgent(t, "cap-member-"+uuid.NewString(), nil)
	agentManagerID := createHandlerTestAgent(t, "cap-manager-"+uuid.NewString(), nil)
	channelName := "member-management-capabilities-" + uuid.NewString()
	channelID := seedChannelForTest(
		t,
		channelName,
		ownerID,
		memberID,
		managerID,
	)

	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_member
		SET role = 'manager'
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = 'user'
		  AND member_id = $3`,
		channelID,
		testWorkspaceID,
		managerID,
	); err != nil {
		t.Fatalf("seed human manager role: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (
			channel_id, workspace_id, member_type, member_id, role
		)
		VALUES
			($1, $2, 'agent', $3, 'member'),
			($1, $2, 'agent', $4, 'manager')`,
		channelID,
		testWorkspaceID,
		agentMemberID,
		agentManagerID,
	); err != nil {
		t.Fatalf("seed agent capability matrix: %v", err)
	}

	return memberManagementCapabilitiesFixture{
		channelID:      channelID,
		ownerID:        ownerID,
		memberID:       memberID,
		managerID:      managerID,
		agentMemberID:  agentMemberID,
		agentManagerID: agentManagerID,
		channelName:    channelName,
	}
}

func invokeMemberManagementCapabilitiesHandler(
	t *testing.T,
	handlerName string,
	req *http.Request,
) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	method := reflect.ValueOf(testHandler).MethodByName(handlerName)
	if !method.IsValid() {
		t.Fatalf("production handler %s is not implemented", handlerName)
	}
	method.Call([]reflect.Value{reflect.ValueOf(rec), reflect.ValueOf(req)})
	return rec
}

func requestHumanMemberManagementCapabilities(
	t *testing.T,
	channelID, userID string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAs(
		userID,
		http.MethodGet,
		"/api/channels/"+channelID+"/member-management-capabilities",
		nil,
	)
	req = withChannelTestWorkspaceCtx(t, req, userID)
	req = withURLParam(req, "channelId", channelID)
	return invokeMemberManagementCapabilitiesHandler(
		t,
		humanMemberManagementCapabilitiesHandler,
		req,
	)
}

func requestAgentMemberManagementCapabilities(
	t *testing.T,
	channelID, agentID string,
) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequest(
		http.MethodGet,
		"/api/agent/channels/"+channelID+"/member-management-capabilities",
		nil,
	)
	req = withAgentPrincipal(req, agentID, testWorkspaceID, testUserID)
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParam(req, "channelId", channelID)
	return invokeMemberManagementCapabilitiesHandler(
		t,
		agentMemberManagementCapabilitiesHandler,
		req,
	)
}

func decodeMemberManagementCapabilities(
	t *testing.T,
	rec *httptest.ResponseRecorder,
) memberManagementCapabilitiesResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("capability status=%d want 200 body=%s", rec.Code, rec.Body.String())
	}
	var response memberManagementCapabilitiesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode capability response: %v body=%s", err, rec.Body.String())
	}
	if response.Targets == nil {
		t.Fatalf("capability targets must be a JSON array: %s", rec.Body.String())
	}
	return response
}

func capabilityTargetFor(
	t *testing.T,
	response memberManagementCapabilitiesResponse,
	memberType, memberID string,
) memberManagementCapabilityTarget {
	t.Helper()
	for _, target := range response.Targets {
		if target.MemberType == memberType && target.MemberID == memberID {
			return target
		}
	}
	t.Fatalf("capability target %s/%s missing from %+v", memberType, memberID, response.Targets)
	return memberManagementCapabilityTarget{}
}

func assertExactCapabilityJSONKeys(t *testing.T, object map[string]json.RawMessage, want ...string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("JSON keys=%v want exact %v", got, want)
	}
}

func assertMemberManagementCapabilityWhitelist(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	fixture memberManagementCapabilitiesFixture,
) memberManagementCapabilitiesResponse {
	t.Helper()
	return assertMemberManagementCapabilityWhitelistAtArchivedState(t, rec, fixture, false)
}

func assertMemberManagementCapabilityWhitelistAtArchivedState(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	fixture memberManagementCapabilitiesFixture,
	wantArchived bool,
) memberManagementCapabilitiesResponse {
	t.Helper()
	response := decodeMemberManagementCapabilities(t, rec)
	if response.ChannelID != fixture.channelID ||
		response.Name != fixture.channelName ||
		response.Kind != "group" ||
		response.Archived != wantArchived {
		t.Fatalf(
			"channel identity=%q/%q/%q archived=%v want %q/%q/group/%v",
			response.ChannelID,
			response.Name,
			response.Kind,
			response.Archived,
			fixture.channelID,
			fixture.channelName,
			wantArchived,
		)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode raw capability object: %v", err)
	}
	assertExactCapabilityJSONKeys(
		t,
		raw,
		"channel_id",
		"name",
		"kind",
		"archived",
		"can_add_members",
		"can_remove_members",
		"can_leave",
		"targets",
	)

	var targets []map[string]json.RawMessage
	if err := json.Unmarshal(raw["targets"], &targets); err != nil {
		t.Fatalf("decode raw capability targets: %v", err)
	}
	if len(targets) != len(response.Targets) || len(targets) == 0 {
		t.Fatalf("raw/typed target counts=%d/%d want same non-zero count", len(targets), len(response.Targets))
	}
	for index, target := range targets {
		assertExactCapabilityJSONKeys(
			t,
			target,
			"member_type",
			"member_id",
			"display_name",
			"avatar_url",
			"role",
			"can_remove",
			"can_promote_to_manager",
			"can_demote_to_member",
			"can_transfer_ownership",
		)
		typed := response.Targets[index]
		if typed.MemberType != "user" && typed.MemberType != "agent" {
			t.Fatalf("target[%d] member_type=%q want user or agent", index, typed.MemberType)
		}
		if _, err := uuid.Parse(typed.MemberID); err != nil {
			t.Fatalf("target[%d] member_id=%q is not UUID: %v", index, typed.MemberID, err)
		}
		if typed.DisplayName == "" {
			t.Fatalf("target[%d] display_name is empty", index)
		}
		if typed.Role != "owner" && typed.Role != "manager" && typed.Role != "member" {
			t.Fatalf("target[%d] role=%q is outside exact role vocabulary", index, typed.Role)
		}
	}
	return response
}

func setAgentWorkspaceRoleForCapabilityTest(t *testing.T, agentID, role string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent
		SET workspace_role = $2
		WHERE id = $1 AND workspace_id = $3`,
		agentID,
		role,
		testWorkspaceID,
	); err != nil {
		t.Fatalf("set agent workspace role: %v", err)
	}
}

func TestMemberManagementCapabilitiesReturnExactManagementWhitelist(t *testing.T) {
	fixture := newMemberManagementCapabilitiesFixture(t)
	response := assertMemberManagementCapabilityWhitelist(
		t,
		requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.ownerID),
		fixture,
	)
	if got := capabilityTargetFor(t, response, "user", fixture.ownerID).Role; got != "owner" {
		t.Fatalf("owner target role=%q want owner", got)
	}
	if got := capabilityTargetFor(t, response, "user", fixture.managerID).Role; got != "manager" {
		t.Fatalf("human manager target role=%q want manager", got)
	}
	if got := capabilityTargetFor(t, response, "agent", fixture.agentManagerID).Role; got != "manager" {
		t.Fatalf("agent manager target role=%q want manager", got)
	}
}

func TestMemberManagementCapabilitiesSeparateHumanAndAgentAuthentication(t *testing.T) {
	fixture := newMemberManagementCapabilitiesFixture(t)

	t.Run("human route rejects AgentPrincipal", func(t *testing.T) {
		req := newRequestAs(
			fixture.ownerID,
			http.MethodGet,
			"/api/channels/"+fixture.channelID+"/member-management-capabilities",
			nil,
		)
		req = withAgentPrincipal(req, fixture.agentManagerID, testWorkspaceID, fixture.ownerID)
		req = withChannelTestWorkspaceCtx(t, req, fixture.ownerID)
		req = withURLParam(req, "channelId", fixture.channelID)
		rec := invokeMemberManagementCapabilitiesHandler(
			t,
			humanMemberManagementCapabilitiesHandler,
			req,
		)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("human capability route with AgentPrincipal status=%d want 403 body=%s", rec.Code, rec.Body.String())
		}
	})

	t.Run("agent route rejects human session", func(t *testing.T) {
		req := newRequestAs(
			fixture.ownerID,
			http.MethodGet,
			"/api/agent/channels/"+fixture.channelID+"/member-management-capabilities",
			nil,
		)
		req = withChannelTestWorkspaceCtx(t, req, fixture.ownerID)
		req = withURLParam(req, "channelId", fixture.channelID)
		rec := invokeMemberManagementCapabilitiesHandler(
			t,
			agentMemberManagementCapabilitiesHandler,
			req,
		)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("agent capability route with human session status=%d want 403 body=%s", rec.Code, rec.Body.String())
		}
	})
}

func TestMemberManagementCapabilitiesHumanAgentRoleParity(t *testing.T) {
	fixture := newMemberManagementCapabilitiesFixture(t)
	adminID := createChannelWorkspaceAdmin(t)
	agentAdminID := createHandlerTestAgent(t, "cap-admin-"+uuid.NewString(), nil)
	setAgentWorkspaceRoleForCapabilityTest(t, agentAdminID, "admin")

	tests := []struct {
		name           string
		humanID        string
		agentID        string
		canAdd         bool
		canRemove      bool
		canLeave       bool
		ordinaryTarget bool
	}{
		{
			name:    "ordinary channel member",
			humanID: fixture.memberID, agentID: fixture.agentMemberID,
			canAdd: true, canRemove: false, canLeave: true,
		},
		{
			name:    "channel manager",
			humanID: fixture.managerID, agentID: fixture.agentManagerID,
			canAdd: true, canRemove: true, canLeave: true, ordinaryTarget: true,
		},
		{
			name:    "nonmember workspace admin",
			humanID: adminID, agentID: agentAdminID,
			canAdd: true, canRemove: true, canLeave: false, ordinaryTarget: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			human := assertMemberManagementCapabilityWhitelist(
				t,
				requestHumanMemberManagementCapabilities(t, fixture.channelID, tc.humanID),
				fixture,
			)
			agent := assertMemberManagementCapabilityWhitelist(
				t,
				requestAgentMemberManagementCapabilities(t, fixture.channelID, tc.agentID),
				fixture,
			)
			if human.CanAddMembers != tc.canAdd ||
				human.CanRemoveMembers != tc.canRemove ||
				human.CanLeave != tc.canLeave {
				t.Fatalf(
					"human actor caps=%+v want add=%v remove=%v leave=%v",
					human,
					tc.canAdd,
					tc.canRemove,
					tc.canLeave,
				)
			}
			if !reflect.DeepEqual(human, agent) {
				t.Fatalf("human/agent full capability payload parity diverged: human=%+v agent=%+v", human, agent)
			}
			humanTarget := capabilityTargetFor(t, human, "user", fixture.memberID)
			agentTarget := capabilityTargetFor(t, agent, "user", fixture.memberID)
			if humanTarget.CanRemove != tc.ordinaryTarget || agentTarget.CanRemove != tc.ordinaryTarget {
				t.Fatalf(
					"ordinary target can_remove human=%v agent=%v want=%v",
					humanTarget.CanRemove,
					agentTarget.CanRemove,
					tc.ordinaryTarget,
				)
			}
			if tc.humanID != adminID {
				if got := capabilityTargetFor(t, human, "user", tc.humanID).CanRemove; got {
					t.Fatalf("human actor self row can_remove=%v want false", got)
				}
				if got := capabilityTargetFor(t, agent, "agent", tc.agentID).CanRemove; got {
					t.Fatalf("agent actor self row can_remove=%v want false", got)
				}
			}
			for _, target := range human.Targets {
				if target.CanPromoteToManager ||
					target.CanDemoteToMember ||
					target.CanTransferOwnership {
					t.Fatalf("non-owner actor received owner-only action for %+v", target)
				}
			}
			if capabilityTargetFor(t, human, "user", fixture.managerID).CanRemove ||
				capabilityTargetFor(t, human, "agent", fixture.agentManagerID).CanRemove {
				t.Fatal("non-owner actor may not remove human or agent manager targets")
			}
		})
	}
}

func TestMemberManagementCapabilitiesKeepAddAndRemoveIndependent(t *testing.T) {
	fixture := newMemberManagementCapabilitiesFixture(t)
	response := decodeMemberManagementCapabilities(
		t,
		requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.memberID),
	)
	if !response.CanAddMembers || response.CanRemoveMembers {
		t.Fatalf(
			"ordinary member actor caps add=%v remove=%v want true/false",
			response.CanAddMembers,
			response.CanRemoveMembers,
		)
	}
	for _, target := range response.Targets {
		if target.CanRemove {
			t.Fatalf("ordinary member target %s/%s unexpectedly removable", target.MemberType, target.MemberID)
		}
	}
}

func TestMemberManagementCapabilitiesProjectOwnerOnlyTargetActions(t *testing.T) {
	fixture := newMemberManagementCapabilitiesFixture(t)
	owner := decodeMemberManagementCapabilities(
		t,
		requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.ownerID),
	)
	if !owner.CanAddMembers || !owner.CanRemoveMembers || owner.CanLeave {
		t.Fatalf("sole owner actor caps=%+v want add/remove true and leave false", owner)
	}

	ordinaryHuman := capabilityTargetFor(t, owner, "user", fixture.memberID)
	if !ordinaryHuman.CanRemove ||
		!ordinaryHuman.CanPromoteToManager ||
		ordinaryHuman.CanDemoteToMember ||
		!ordinaryHuman.CanTransferOwnership {
		t.Fatalf("ordinary human owner actions=%+v", ordinaryHuman)
	}
	ordinaryAgent := capabilityTargetFor(t, owner, "agent", fixture.agentMemberID)
	if !ordinaryAgent.CanRemove ||
		!ordinaryAgent.CanPromoteToManager ||
		ordinaryAgent.CanDemoteToMember ||
		ordinaryAgent.CanTransferOwnership {
		t.Fatalf("ordinary agent owner actions=%+v", ordinaryAgent)
	}
	managerHuman := capabilityTargetFor(t, owner, "user", fixture.managerID)
	if !managerHuman.CanRemove ||
		managerHuman.CanPromoteToManager ||
		!managerHuman.CanDemoteToMember ||
		!managerHuman.CanTransferOwnership {
		t.Fatalf("human manager owner actions=%+v", managerHuman)
	}
	managerAgent := capabilityTargetFor(t, owner, "agent", fixture.agentManagerID)
	if !managerAgent.CanRemove ||
		managerAgent.CanPromoteToManager ||
		!managerAgent.CanDemoteToMember ||
		managerAgent.CanTransferOwnership {
		t.Fatalf("agent manager owner actions=%+v", managerAgent)
	}
	ownerSelf := capabilityTargetFor(t, owner, "user", fixture.ownerID)
	if ownerSelf.CanRemove ||
		ownerSelf.CanPromoteToManager ||
		ownerSelf.CanDemoteToMember ||
		ownerSelf.CanTransferOwnership {
		t.Fatalf("owner self actions=%+v want all false", ownerSelf)
	}

	manager := decodeMemberManagementCapabilities(
		t,
		requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.managerID),
	)
	for _, target := range manager.Targets {
		if target.CanPromoteToManager ||
			target.CanDemoteToMember ||
			target.CanTransferOwnership {
			t.Fatalf("non-owner manager received owner-only action for %+v", target)
		}
	}
}

func archiveMemberManagementCapabilitiesFixture(
	t *testing.T,
	fixture memberManagementCapabilitiesFixture,
) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE channel
		SET archived_at = now()
		WHERE id = $1 AND workspace_id = $2`,
		fixture.channelID,
		testWorkspaceID,
	); err != nil {
		t.Fatalf("archive channel: %v", err)
	}
}

func assertArchivedMemberManagementCapabilities(
	t *testing.T,
	rec *httptest.ResponseRecorder,
	fixture memberManagementCapabilitiesFixture,
) {
	t.Helper()
	response := assertMemberManagementCapabilityWhitelistAtArchivedState(
		t,
		rec,
		fixture,
		true,
	)
	if response.CanAddMembers || response.CanRemoveMembers || response.CanLeave {
		t.Fatalf("archived actor capabilities=%+v want all false", response)
	}
	for _, target := range response.Targets {
		if target.CanRemove ||
			target.CanPromoteToManager ||
			target.CanDemoteToMember ||
			target.CanTransferOwnership {
			t.Fatalf("archived target actions=%+v want all false", target)
		}
	}
}

func TestMemberManagementCapabilitiesArchivedGroupReturnsNoActionsForEveryAuthority(t *testing.T) {
	fixture := newMemberManagementCapabilitiesFixture(t)
	humanAdminID := createChannelWorkspaceAdmin(t)
	agentAdminID := createHandlerTestAgent(t, "cap-archived-admin-"+uuid.NewString(), nil)
	setAgentWorkspaceRoleForCapabilityTest(t, agentAdminID, "admin")

	otherOwnerID := createChannelPlainMember(t)
	workspaceOwnerFallbackFixture := newMemberManagementCapabilitiesFixtureForOwner(t, otherOwnerID)

	archiveMemberManagementCapabilitiesFixture(t, fixture)
	archiveMemberManagementCapabilitiesFixture(t, workspaceOwnerFallbackFixture)

	tests := []struct {
		name    string
		fixture memberManagementCapabilitiesFixture
		call    func(*testing.T) *httptest.ResponseRecorder
	}{
		{
			name:    "ordinary human member",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.memberID)
			},
		},
		{
			name:    "ordinary agent member",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestAgentMemberManagementCapabilities(t, fixture.channelID, fixture.agentMemberID)
			},
		},
		{
			name:    "human manager",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.managerID)
			},
		},
		{
			name:    "agent manager",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestAgentMemberManagementCapabilities(t, fixture.channelID, fixture.agentManagerID)
			},
		},
		{
			name:    "human channel owner",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, fixture.channelID, fixture.ownerID)
			},
		},
		{
			name:    "nonmember human workspace admin",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, fixture.channelID, humanAdminID)
			},
		},
		{
			name:    "nonmember agent workspace admin",
			fixture: fixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestAgentMemberManagementCapabilities(t, fixture.channelID, agentAdminID)
			},
		},
		{
			name:    "nonmember human workspace owner",
			fixture: workspaceOwnerFallbackFixture,
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(
					t,
					workspaceOwnerFallbackFixture.channelID,
					testUserID,
				)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertArchivedMemberManagementCapabilities(t, tc.call(t), tc.fixture)
		})
	}
}

func TestMemberManagementCapabilitiesFailClosedForCreatorAndOrdinaryNonmember(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	creatorID := createChannelPlainMember(t)
	ordinaryNonmemberID := createChannelPlainMember(t)
	ordinaryNonmemberAgentID := createHandlerTestAgent(t, "cap-nonmember-"+uuid.NewString(), nil)
	channelID := seedChannelForTest(
		t,
		"member-management-capabilities-creator-"+uuid.NewString(),
		creatorID,
		testUserID,
	)
	ctx := context.Background()
	tx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin ownership transfer fixture: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'member'
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = 'user'
		  AND member_id = $3`,
		channelID,
		testWorkspaceID,
		creatorID,
	); err != nil {
		t.Fatalf("demote historical creator fixture: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE channel_member
		SET role = 'owner'
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = 'user'
		  AND member_id = $3`,
		channelID,
		testWorkspaceID,
		testUserID,
	); err != nil {
		t.Fatalf("promote successor owner fixture: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit ownership transfer fixture: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = 'user'
		  AND member_id = $3`,
		channelID,
		testWorkspaceID,
		creatorID,
	); err != nil {
		t.Fatalf("turn creator into historical provenance: %v", err)
	}

	for _, tc := range []struct {
		name string
		call func(*testing.T) *httptest.ResponseRecorder
	}{
		{
			name: "historical creator",
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, channelID, creatorID)
			},
		},
		{
			name: "ordinary human nonmember",
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, channelID, ordinaryNonmemberID)
			},
		},
		{
			name: "ordinary agent nonmember",
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestAgentMemberManagementCapabilities(t, channelID, ordinaryNonmemberAgentID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.call(t)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("%s status=%d want 404 body=%s", tc.name, rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMemberManagementCapabilitiesFailClosedAcrossWorkspaces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	suffix := uuid.NewString()
	var foreignWorkspaceID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ($1, $2, '', 'CP')
		RETURNING id`,
		"capability-foreign-"+suffix,
		"capability-foreign-"+suffix,
	).Scan(&foreignWorkspaceID); err != nil {
		t.Fatalf("create foreign workspace: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, foreignWorkspaceID)
	})
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')`,
		foreignWorkspaceID,
		testUserID,
	); err != nil {
		t.Fatalf("seed foreign workspace owner: %v", err)
	}
	var foreignChannelID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id`,
		foreignWorkspaceID,
		"capability-foreign-channel-"+suffix,
		testUserID,
	).Scan(&foreignChannelID); err != nil {
		t.Fatalf("create foreign channel: %v", err)
	}

	agentID := createHandlerTestAgent(t, "cap-cross-workspace-"+suffix, nil)
	for _, tc := range []struct {
		name string
		call func(*testing.T) *httptest.ResponseRecorder
	}{
		{
			name: "human principal",
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestHumanMemberManagementCapabilities(t, foreignChannelID, testUserID)
			},
		},
		{
			name: "agent principal",
			call: func(t *testing.T) *httptest.ResponseRecorder {
				return requestAgentMemberManagementCapabilities(t, foreignChannelID, agentID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := tc.call(t)
			if rec.Code != http.StatusNotFound {
				t.Fatalf("cross-workspace capability status=%d want 404 body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func seedPrivateCapabilityBoundaryRoot(
	t *testing.T,
	fixture memberManagementCapabilitiesFixture,
	authorID string,
) ChannelMessageResponse {
	t.Helper()
	root, err := testHandler.insertChannelMessage(
		context.Background(),
		parseUUID(fixture.channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(authorID),
		"Tester",
		"private capability boundary",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		strPtr("capability-content-deny-"+uuid.NewString()),
		0,
	)
	if err != nil {
		t.Fatalf("seed private channel root: %v", err)
	}
	return root
}

func assertNonmemberHumanPrivateContentDenied(
	t *testing.T,
	label string,
	userID string,
	fixture memberManagementCapabilitiesFixture,
	root ChannelMessageResponse,
) {
	t.Helper()
	for _, tc := range []struct {
		name    string
		path    string
		params  []string
		handler http.HandlerFunc
	}{
		{
			name:    "messages",
			path:    "/api/channels/" + fixture.channelID + "/messages",
			params:  []string{"channelId", fixture.channelID},
			handler: testHandler.ListChannelMessages,
		},
		{
			name:    "thread",
			path:    "/api/channels/" + fixture.channelID + "/messages/" + root.ID + "/thread",
			params:  []string{"channelId", fixture.channelID, "messageId", root.ID},
			handler: testHandler.ListChannelMessageThread,
		},
		{
			name:    "attachments",
			path:    "/api/channels/" + fixture.channelID + "/attachments",
			params:  []string{"channelId", fixture.channelID},
			handler: testHandler.ListChannelAttachments,
		},
	} {
		t.Run(label+"/"+tc.name, func(t *testing.T) {
			req := newRequestAs(userID, http.MethodGet, tc.path, nil)
			req = withChannelTestWorkspaceCtx(t, req, userID)
			req = withURLParams(req, tc.params...)
			rec := httptest.NewRecorder()
			tc.handler(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf(
					"nonmember %s %s status=%d want 403 body=%s",
					label,
					tc.name,
					rec.Code,
					rec.Body.String(),
				)
			}
		})
	}
}

func TestMemberManagementCapabilitiesAllowNonmemberWorkspaceOwnerWithoutContentAccess(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	var workspaceRole string
	if err := testPool.QueryRow(context.Background(), `
		SELECT role
		FROM member
		WHERE workspace_id = $1 AND user_id = $2`,
		testWorkspaceID,
		testUserID,
	).Scan(&workspaceRole); err != nil {
		t.Fatalf("load fixture workspace owner role: %v", err)
	}
	if workspaceRole != "owner" {
		t.Fatalf("fixture user workspace role=%q want owner", workspaceRole)
	}

	channelOwnerID := createChannelPlainMember(t)
	fixture := newMemberManagementCapabilitiesFixtureForOwner(t, channelOwnerID)
	assertChannelUserMembershipCount(t, fixture.channelID, testUserID, 0)
	root := seedPrivateCapabilityBoundaryRoot(t, fixture, channelOwnerID)

	t.Run("exact management projection", func(t *testing.T) {
		response := assertMemberManagementCapabilityWhitelist(
			t,
			requestHumanMemberManagementCapabilities(t, fixture.channelID, testUserID),
			fixture,
		)
		if !response.CanAddMembers || !response.CanRemoveMembers || response.CanLeave {
			t.Fatalf("nonmember workspace owner caps=%+v want add/remove true and leave false", response)
		}
	})
	assertNonmemberHumanPrivateContentDenied(
		t,
		"workspace owner content denied",
		testUserID,
		fixture,
		root,
	)
}

func TestMemberManagementCapabilitiesKeepAdminProjectionSeparateFromPrivateContent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	fixture := newMemberManagementCapabilitiesFixture(t)
	humanAdminID := createChannelWorkspaceAdmin(t)
	taskID, _ := createChannelCompletionTask(t, "group")
	agentAdminID := agentIDForTask(t, taskID)
	setAgentWorkspaceRoleForCapabilityTest(t, agentAdminID, "admin")

	root := seedPrivateCapabilityBoundaryRoot(t, fixture, testUserID)

	t.Run("human admin gets exact projection", func(t *testing.T) {
		assertMemberManagementCapabilityWhitelist(
			t,
			requestHumanMemberManagementCapabilities(t, fixture.channelID, humanAdminID),
			fixture,
		)
	})
	t.Run("agent admin gets exact projection", func(t *testing.T) {
		assertMemberManagementCapabilityWhitelist(
			t,
			requestAgentMemberManagementCapabilities(t, fixture.channelID, agentAdminID),
			fixture,
		)
	})

	assertNonmemberHumanPrivateContentDenied(
		t,
		"human admin content denied",
		humanAdminID,
		fixture,
		root,
	)

	for _, target := range []string{
		"#" + fixture.channelName,
		"#" + fixture.channelName + ":" + root.ID[:8],
	} {
		t.Run("agent admin message target denied/"+target, func(t *testing.T) {
			rec := agentTransportReadForTest(t, taskID, agentAdminID, map[string]any{
				"target": target,
				"limit":  20,
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("nonmember agent admin read %q status=%d want 400 body=%s", target, rec.Code, rec.Body.String())
			}
		})
	}

	t.Run("agent admin attachments denied", func(t *testing.T) {
		req := newRequest(
			http.MethodGet,
			"/api/agent/channels/"+fixture.channelID+"/attachments",
			nil,
		)
		req = withAgentPrincipal(req, agentAdminID, testWorkspaceID, testUserID)
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParam(req, "channelId", fixture.channelID)
		rec := httptest.NewRecorder()
		testHandler.ListAgentChannelAttachments(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("nonmember agent admin attachments status=%d want 404 body=%s", rec.Code, rec.Body.String())
		}
	})
}
