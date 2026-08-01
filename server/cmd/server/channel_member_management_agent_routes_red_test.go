package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/multica-ai/multica/server/internal/auth"
)

type agentMemberManagementFixture struct {
	agentID string
	channel string
	token   string
	userIDs []string
}

func newAgentMemberManagementFixture(t *testing.T, targetCount int) agentMemberManagementFixture {
	t.Helper()
	if testServer == nil || testPool == nil {
		t.Skip("database-backed router is unavailable")
	}

	ctx := context.Background()
	var agentID string
	if err := testPool.QueryRow(ctx, `
		SELECT id::text
		FROM agent
		WHERE workspace_id = $1
		  AND archived_at IS NULL
		ORDER BY created_at ASC
		LIMIT 1
	`, testWorkspaceID).Scan(&agentID); err != nil {
		t.Fatalf("load fixture agent: %v", err)
	}

	var channelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id::text
	`, testWorkspaceID, "agent-member-management-"+uuid.NewString(), testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create group channel: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (
			channel_id, workspace_id, member_type, member_id, role
		)
		VALUES
			($1, $2, 'user', $3, 'owner'),
			($1, $2, 'agent', $4, 'manager')
		ON CONFLICT (channel_id, member_type, member_id)
		DO UPDATE SET role = EXCLUDED.role
	`, channelID, testWorkspaceID, testUserID, agentID); err != nil {
		t.Fatalf("seed channel authorities: %v", err)
	}

	userIDs := make([]string, 0, targetCount)
	for i := 0; i < targetCount; i++ {
		suffix := uuid.NewString()
		var userID string
		if err := testPool.QueryRow(ctx, `
			INSERT INTO "user" (name, display_name, email)
			VALUES ($1, $2, $3)
			RETURNING id::text
		`, "agent-route-target-"+suffix, "Agent route target", suffix+"@agent-route.test").Scan(&userID); err != nil {
			t.Fatalf("create target user %d: %v", i, err)
		}
		if _, err := testPool.Exec(ctx, `
			INSERT INTO member (workspace_id, user_id, role)
			VALUES ($1, $2, 'member')
		`, testWorkspaceID, userID); err != nil {
			t.Fatalf("add target user %d to workspace: %v", i, err)
		}
		userIDs = append(userIDs, userID)
	}

	rawToken, err := auth.GenerateAgentCredentialToken()
	if err != nil {
		t.Fatalf("generate agent credential: %v", err)
	}
	tokenHash := auth.HashToken(rawToken)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_credential (
			token_hash, token_prefix, agent_id, workspace_id, user_id, expires_at
		)
		VALUES ($1, $2, $3, $4, $5, $6)
	`, tokenHash, rawToken[:12], agentID, testWorkspaceID, testUserID, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create agent credential: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx := context.Background()
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM channel WHERE id = $1`, channelID)
		_, _ = testPool.Exec(cleanupCtx, `DELETE FROM agent_credential WHERE token_hash = $1`, tokenHash)
		for _, userID := range userIDs {
			_, _ = testPool.Exec(cleanupCtx, `DELETE FROM "user" WHERE id = $1`, userID)
		}
	})

	return agentMemberManagementFixture{
		agentID: agentID,
		channel: channelID,
		token:   rawToken,
		userIDs: userIDs,
	}
}

func agentCredentialRequest(t *testing.T, token, method, path string, body any) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, testServer.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Workspace-ID", testWorkspaceID)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	return resp
}

func requireResponseStatus(t *testing.T, resp *http.Response, want int) {
	t.Helper()
	defer resp.Body.Close()
	if resp.StatusCode == want {
		return
	}
	body, _ := io.ReadAll(resp.Body)
	t.Fatalf("status = %d, want %d: %s", resp.StatusCode, want, body)
}

func requireChannelMembershipCount(t *testing.T, channelID, memberType, memberID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_member
		WHERE channel_id = $1
		  AND member_type = $2
		  AND member_id = $3
	`, channelID, memberType, memberID).Scan(&got); err != nil {
		t.Fatalf("count channel membership: %v", err)
	}
	if got != want {
		t.Fatalf("membership count = %d, want %d", got, want)
	}
}

func TestMemberManagementCapabilityRoutesAreWired(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 0)

	t.Run("human route", func(t *testing.T) {
		resp := authRequest(
			t,
			http.MethodGet,
			fmt.Sprintf(
				"/api/channels/%s/member-management-capabilities",
				fixture.channel,
			),
			nil,
		)
		requireResponseStatus(t, resp, http.StatusOK)
	})

	t.Run("agent route", func(t *testing.T) {
		resp := agentCredentialRequest(
			t,
			fixture.token,
			http.MethodGet,
			fmt.Sprintf(
				"/api/agent/channels/%s/member-management-capabilities",
				fixture.channel,
			),
			nil,
		)
		requireResponseStatus(t, resp, http.StatusOK)
	})
}

func TestAgentMemberManagerUsesDedicatedWriteRoutes(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 4)

	t.Run("single add", func(t *testing.T) {
		targetID := fixture.userIDs[0]
		resp := agentCredentialRequest(t, fixture.token, http.MethodPost,
			fmt.Sprintf("/api/agent/channels/%s/members", fixture.channel),
			map[string]string{"member_type": "user", "member_id": targetID},
		)
		requireResponseStatus(t, resp, http.StatusCreated)
		requireChannelMembershipCount(t, fixture.channel, "user", targetID, 1)
	})

	t.Run("batch add", func(t *testing.T) {
		targets := fixture.userIDs[1:3]
		resp := agentCredentialRequest(t, fixture.token, http.MethodPost,
			fmt.Sprintf("/api/agent/channels/%s/members/batch", fixture.channel),
			map[string]any{"members": []map[string]string{
				{"member_type": "user", "member_id": targets[0]},
				{"member_type": "user", "member_id": targets[1]},
			}},
		)
		requireResponseStatus(t, resp, http.StatusCreated)
		for _, targetID := range targets {
			requireChannelMembershipCount(t, fixture.channel, "user", targetID, 1)
		}
	})

	t.Run("remove ordinary member with exact effect", func(t *testing.T) {
		targetID := fixture.userIDs[3]
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO channel_member (
				channel_id, workspace_id, member_type, member_id, role
			)
			VALUES ($1, $2, 'user', $3, 'member')
		`, fixture.channel, testWorkspaceID, targetID); err != nil {
			t.Fatalf("seed removable member: %v", err)
		}
		resp := agentCredentialRequest(t, fixture.token, http.MethodDelete,
			fmt.Sprintf(
				"/api/agent/channels/%s/members/user/%s?expected_remove_effect=none",
				fixture.channel,
				targetID,
			),
			nil,
		)
		requireResponseStatus(t, resp, http.StatusOK)
		requireChannelMembershipCount(t, fixture.channel, "user", targetID, 0)
	})
}

func TestAgentMemberSelfLeaveUsesAgentProvenance(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 0)
	ctx := context.Background()
	if _, err := testPool.Exec(ctx, `
		UPDATE channel_member
		SET role = 'member'
		WHERE channel_id = $1
		  AND workspace_id = $2
		  AND member_type = 'agent'
		  AND member_id = $3`,
		fixture.channel, testWorkspaceID, fixture.agentID); err != nil {
		t.Fatalf("demote self-leaving agent to ordinary member: %v", err)
	}

	auditBefore := countAgentMemberActivityForRouteTest(t, fixture.channel, "member_left")
	leftBefore := countAgentMemberSystemEventForRouteTest(t, fixture.channel, "channel_member_left")
	removedBefore := countAgentMemberSystemEventForRouteTest(t, fixture.channel, "channel_member_removed")

	resp := agentCredentialRequest(t, fixture.token, http.MethodDelete,
		fmt.Sprintf(
			"/api/agent/channels/%s/members/agent/%s?expected_remove_effect=none",
			fixture.channel,
			fixture.agentID,
		),
		nil,
	)
	requireResponseStatus(t, resp, http.StatusOK)
	requireChannelMembershipCount(t, fixture.channel, "agent", fixture.agentID, 0)

	if got := countAgentMemberActivityForRouteTest(t, fixture.channel, "member_left"); got != auditBefore+1 {
		t.Fatalf("agent member_left audit count=%d want=%d", got, auditBefore+1)
	}
	var auditActorType, auditActorID string
	if err := testPool.QueryRow(ctx, `
		SELECT actor_type, actor_id::text
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'member_left'
		  AND details->>'channel_id' = $2
		ORDER BY created_at DESC, id DESC
		LIMIT 1`,
		testWorkspaceID, fixture.channel,
	).Scan(&auditActorType, &auditActorID); err != nil {
		t.Fatalf("load agent member_left audit actor: %v", err)
	}
	if auditActorType != "agent" || auditActorID != fixture.agentID {
		t.Fatalf("agent member_left audit actor=%s/%s want=agent/%s", auditActorType, auditActorID, fixture.agentID)
	}

	if got := countAgentMemberSystemEventForRouteTest(t, fixture.channel, "channel_member_left"); got != leftBefore+1 {
		t.Fatalf("agent channel_member_left event count=%d want=%d", got, leftBefore+1)
	}
	if got := countAgentMemberSystemEventForRouteTest(t, fixture.channel, "channel_member_removed"); got != removedBefore {
		t.Fatalf("agent self-leave emitted removed event count=%d want=%d", got, removedBefore)
	}
	var eventActorType, eventActorID, targetType, targetID string
	if err := testPool.QueryRow(ctx, `
		SELECT parts->0->'event_params'->>'actor_type',
		       parts->0->'event_params'->>'actor_id',
		       parts->0->'event_params'->>'target_type',
		       parts->0->'event_params'->>'target_id'
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'system'
		  AND parts->0->>'event' = 'channel_member_left'
		ORDER BY seq DESC
		LIMIT 1`,
		fixture.channel,
	).Scan(&eventActorType, &eventActorID, &targetType, &targetID); err != nil {
		t.Fatalf("load agent self-leave system event provenance: %v", err)
	}
	if eventActorType != "agent" || eventActorID != fixture.agentID ||
		targetType != "agent" || targetID != fixture.agentID {
		t.Fatalf(
			"agent self-leave system provenance actor=%s/%s target=%s/%s want agent/%s agent/%s",
			eventActorType,
			eventActorID,
			targetType,
			targetID,
			fixture.agentID,
			fixture.agentID,
		)
	}
}

func TestAgentMemberWriteRoutesRejectHumanSession(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 1)
	targetID := fixture.userIDs[0]

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   any
	}{
		{
			name:   "single_add",
			method: http.MethodPost,
			path:   fmt.Sprintf("/api/agent/channels/%s/members", fixture.channel),
			body:   map[string]string{"member_type": "user", "member_id": targetID},
		},
		{
			name:   "batch_add",
			method: http.MethodPost,
			path:   fmt.Sprintf("/api/agent/channels/%s/members/batch", fixture.channel),
			body: map[string]any{"members": []map[string]string{{
				"member_type": "user",
				"member_id":   targetID,
			}}},
		},
		{
			name:   "remove",
			method: http.MethodDelete,
			path: fmt.Sprintf(
				"/api/agent/channels/%s/members/user/%s?expected_remove_effect=none",
				fixture.channel,
				targetID,
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := authRequest(t, tc.method, tc.path, tc.body)
			requireResponseStatus(t, resp, http.StatusForbidden)
			requireChannelMembershipCount(t, fixture.channel, "user", targetID, 0)
		})
	}
}

func TestAgentWorkspaceAdminCannotChangeItsOwnRole(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 0)
	ctx := context.Background()

	var columnExists bool
	if err := testPool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM information_schema.columns
			WHERE table_schema = current_schema()
			  AND table_name = 'agent'
			  AND column_name = 'workspace_role'
		)
	`).Scan(&columnExists); err != nil {
		t.Fatalf("inspect agent workspace-role schema: %v", err)
	}
	if !columnExists {
		t.Fatal("agent.workspace_role is absent; the additive role migration must land before the full-router self-promotion guard can execute")
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET workspace_role = 'admin'
		WHERE id = $1
		  AND workspace_id = $2
	`, fixture.agentID, testWorkspaceID); err != nil {
		t.Fatalf("seed agent workspace admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			UPDATE agent
			SET workspace_role = 'member'
			WHERE id = $1
		`, fixture.agentID)
	})

	var auditBefore int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditBefore); err != nil {
		t.Fatalf("count role-change audit before request: %v", err)
	}

	path := fmt.Sprintf(
		"/api/workspaces/%s/agents/%s/role",
		testWorkspaceID,
		fixture.agentID,
	)
	resp := agentCredentialRequest(t, fixture.token, http.MethodPatch, path, map[string]string{
		"role": "member",
	})
	requireResponseStatus(t, resp, http.StatusForbidden)

	var role string
	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read agent workspace role: %v", err)
	}
	if role != "admin" {
		t.Fatalf("agent workspace role = %q, want unchanged admin", role)
	}

	var auditAfter int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditAfter); err != nil {
		t.Fatalf("count role-change audit after request: %v", err)
	}
	if auditAfter != auditBefore {
		t.Fatalf("role-change audit count = %d, want unchanged %d", auditAfter, auditBefore)
	}

	ownerResp := authRequest(t, http.MethodPatch, path, map[string]string{
		"role": "member",
	})
	requireResponseStatus(t, ownerResp, http.StatusOK)
	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read owner-updated agent workspace role: %v", err)
	}
	if role != "member" {
		t.Fatalf("owner-updated agent workspace role = %q, want member", role)
	}
	var auditAfterOwner int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditAfterOwner); err != nil {
		t.Fatalf("count owner role-change audit: %v", err)
	}
	if auditAfterOwner != auditBefore+1 {
		t.Fatalf("owner role-change audit count = %d, want %d", auditAfterOwner, auditBefore+1)
	}
	var actorType, actorID string
	var detailsJSON []byte
	if err := testPool.QueryRow(ctx, `
		SELECT actor_type, actor_id::text, details
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID).Scan(&actorType, &actorID, &detailsJSON); err != nil {
		t.Fatalf("read owner role-change audit: %v", err)
	}
	if actorType != "member" || actorID != testUserID {
		t.Fatalf("role-change audit actor = %s/%s, want member/%s", actorType, actorID, testUserID)
	}
	var details map[string]any
	if err := json.Unmarshal(detailsJSON, &details); err != nil {
		t.Fatalf("decode owner role-change audit: %v", err)
	}
	if details["actor_workspace_role"] != "owner" ||
		details["agent_id"] != fixture.agentID ||
		details["previous_role"] != "admin" ||
		details["role"] != "member" ||
		details["request_id"] == "" {
		t.Fatalf("role-change audit details mismatch: %#v", details)
	}

	idempotentResp := authRequest(t, http.MethodPatch, path, map[string]string{
		"role": "member",
	})
	requireResponseStatus(t, idempotentResp, http.StatusOK)
	var auditAfterIdempotent int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditAfterIdempotent); err != nil {
		t.Fatalf("count idempotent role-change audit: %v", err)
	}
	if auditAfterIdempotent != auditAfterOwner {
		t.Fatalf("idempotent role change added audit row: got %d, want %d", auditAfterIdempotent, auditAfterOwner)
	}
}

// TestAgentWorkspaceRoleIsHumanOwnerOrAdminOnly pins task #32: workspace
// admins (not just the owner) can edit an agent's workspace role, but plain
// members still cannot. It also covers the surrounding invariants that were
// never actor-role-specific (invalid target role, unknown agent, agent
// credentials, the generic agent PUT route).
func TestAgentWorkspaceRoleIsHumanOwnerOrAdminOnly(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 0)
	ctx := context.Background()
	suffix := uuid.NewString()
	var adminUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, display_name, email)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "workspace-admin-"+suffix, "Workspace admin", suffix+"@workspace-role.test").Scan(&adminUserID); err != nil {
		t.Fatalf("create workspace admin: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'admin')
	`, testWorkspaceID, adminUserID); err != nil {
		t.Fatalf("add workspace admin: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, adminUserID)
	})
	adminToken, err := generateTestJWT(adminUserID, suffix+"@workspace-role.test", "Workspace admin")
	if err != nil {
		t.Fatalf("generate workspace admin token: %v", err)
	}

	var memberUserID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO "user" (name, display_name, email)
		VALUES ($1, $2, $3)
		RETURNING id::text
	`, "workspace-member-"+suffix, "Workspace member", suffix+"@workspace-role-member.test").Scan(&memberUserID); err != nil {
		t.Fatalf("create workspace member: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')
	`, testWorkspaceID, memberUserID); err != nil {
		t.Fatalf("add workspace member: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, memberUserID)
	})
	memberToken, err := generateTestJWT(memberUserID, suffix+"@workspace-role-member.test", "Workspace member")
	if err != nil {
		t.Fatalf("generate workspace member token: %v", err)
	}

	var auditBefore int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditBefore); err != nil {
		t.Fatalf("count role-change audit before requests: %v", err)
	}

	path := fmt.Sprintf(
		"/api/workspaces/%s/agents/%s/role",
		testWorkspaceID,
		fixture.agentID,
	)

	// fixture.agentID is a shared, pre-existing workspace agent (the oldest
	// row, reused by every test in this file via newAgentMemberManagementFixture),
	// not a fresh fixture — so any mutation this test makes must be undone via
	// t.Cleanup, which still runs on t.Fatal, unlike inline reset code below it.
	// A prior version of this test mutated the role without a guaranteed reset
	// and left "admin" behind for TestAgentWorkspaceRoleSchemaExcludesOwner to
	// trip over.
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `
			UPDATE agent SET workspace_role = 'member' WHERE id = $1
		`, fixture.agentID)
	})

	adminResp := agentCredentialRequest(t, adminToken, http.MethodPatch, path, map[string]string{
		"role": "admin",
	})
	requireResponseStatus(t, adminResp, http.StatusOK)

	var role string
	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read agent workspace role: %v", err)
	}
	if role != "admin" {
		t.Fatalf("workspace admin actor set agent role to %q, want admin", role)
	}
	var auditAfterAdmin int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditAfterAdmin); err != nil {
		t.Fatalf("count role-change audit after admin request: %v", err)
	}
	if auditAfterAdmin != auditBefore+1 {
		t.Fatalf("admin role-change audit count = %d, want %d", auditAfterAdmin, auditBefore+1)
	}

	// Reset out-of-band (bypassing the handler, so no extra audit row) now, in
	// addition to the guaranteed t.Cleanup above, so the remaining
	// denied-request assertions below observe the same "member" baseline the
	// rest of this test expects.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET workspace_role = 'member' WHERE id = $1
	`, fixture.agentID); err != nil {
		t.Fatalf("reset agent workspace role after admin request: %v", err)
	}

	memberResp := agentCredentialRequest(t, memberToken, http.MethodPatch, path, map[string]string{
		"role": "admin",
	})
	requireResponseStatus(t, memberResp, http.StatusForbidden)
	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read agent workspace role: %v", err)
	}
	if role != "member" {
		t.Fatalf("workspace member actor changed agent role to %q, want unchanged member", role)
	}
	auditBefore = auditAfterAdmin

	// An agent cannot self-expand via this route even when its own
	// workspace_role is already "admin" — rejectAgentOnHumanRoute fails
	// closed on principal type before the actor-role check task #32 widened
	// is ever reached. UpdateAgentWorkspaceRole documents "has no /api/agent
	// alias", so this is the real human path, not the always-404 alias below.
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET workspace_role = 'admin' WHERE id = $1
	`, fixture.agentID); err != nil {
		t.Fatalf("set fixture agent to admin for self-expansion probe: %v", err)
	}
	agentSelfResp := agentCredentialRequest(t, fixture.token, http.MethodPatch, path, map[string]string{
		"role": "admin",
	})
	agentSelfBody, err := io.ReadAll(agentSelfResp.Body)
	if err != nil {
		t.Fatalf("read agent self-request body: %v", err)
	}
	agentSelfResp.Body.Close()
	if agentSelfResp.StatusCode != http.StatusForbidden {
		t.Fatalf("agent self-request status = %d, want 403: %s", agentSelfResp.StatusCode, agentSelfBody)
	}
	var agentSelfError struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(agentSelfBody, &agentSelfError); err != nil {
		t.Fatalf("decode agent self-request error body: %v", err)
	}
	if agentSelfError.Error != "agent must use dedicated /api/agent/* route" {
		t.Fatalf("agent self-request error = %q, want %q", agentSelfError.Error, "agent must use dedicated /api/agent/* route")
	}
	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read agent workspace role: %v", err)
	}
	if role != "admin" {
		t.Fatalf("rejected agent self-request changed workspace role to %q, want unchanged admin", role)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent SET workspace_role = 'member' WHERE id = $1
	`, fixture.agentID); err != nil {
		t.Fatalf("reset agent workspace role after self-expansion probe: %v", err)
	}
	var auditAfterAgentSelf int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditAfterAgentSelf); err != nil {
		t.Fatalf("count role-change audit after agent self-request: %v", err)
	}
	if auditAfterAgentSelf != auditBefore {
		t.Fatalf("rejected agent self-request added audit row: got %d, want %d", auditAfterAgentSelf, auditBefore)
	}

	invalidResp := authRequest(t, http.MethodPatch, path, map[string]string{
		"role": "owner",
	})
	requireResponseStatus(t, invalidResp, http.StatusBadRequest)

	unknownResp := authRequest(t, http.MethodPatch,
		fmt.Sprintf("/api/workspaces/%s/agents/%s/role", testWorkspaceID, uuid.NewString()),
		map[string]string{"role": "admin"},
	)
	requireResponseStatus(t, unknownResp, http.StatusNotFound)

	agentAliasResp := agentCredentialRequest(t, fixture.token, http.MethodPatch,
		fmt.Sprintf("/api/agent/workspaces/%s/agents/%s/role", testWorkspaceID, fixture.agentID),
		map[string]string{"role": "admin"},
	)
	requireResponseStatus(t, agentAliasResp, http.StatusNotFound)

	genericUpdateResp := authRequest(t, http.MethodPut,
		fmt.Sprintf("/api/agents/%s", fixture.agentID),
		map[string]string{"workspace_role": "admin"},
	)
	requireResponseStatus(t, genericUpdateResp, http.StatusBadRequest)

	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read final agent workspace role: %v", err)
	}
	if role != "member" {
		t.Fatalf("denied role requests changed agent role to %q, want member", role)
	}
	var auditAfter int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM activity_log
		WHERE workspace_id = $1
		  AND action = 'agent_workspace_role_changed'
	`, testWorkspaceID).Scan(&auditAfter); err != nil {
		t.Fatalf("count role-change audit after denied requests: %v", err)
	}
	if auditAfter != auditBefore {
		t.Fatalf("denied role requests changed audit count to %d, want %d", auditAfter, auditBefore)
	}
}

func TestAgentWorkspaceRoleSchemaExcludesOwner(t *testing.T) {
	fixture := newAgentMemberManagementFixture(t, 0)
	ctx := context.Background()

	var nullable, defaultExpression string
	if err := testPool.QueryRow(ctx, `
		SELECT is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'agent'
		  AND column_name = 'workspace_role'
	`).Scan(&nullable, &defaultExpression); err != nil {
		t.Fatalf("read agent workspace-role column: %v", err)
	}
	if nullable != "NO" || !strings.Contains(defaultExpression, "'member'") {
		t.Fatalf("workspace_role schema = nullable %q default %q, want NOT NULL DEFAULT member", nullable, defaultExpression)
	}

	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET workspace_role = 'owner'
		WHERE id = $1
	`, fixture.agentID); err == nil {
		t.Fatal("agent workspace role accepted owner, want database constraint rejection")
	}
	var role string
	if err := testPool.QueryRow(ctx, `
		SELECT workspace_role
		FROM agent
		WHERE id = $1
	`, fixture.agentID).Scan(&role); err != nil {
		t.Fatalf("read constrained agent workspace role: %v", err)
	}
	if role != "member" {
		t.Fatalf("constraint failure changed workspace role to %q, want member", role)
	}
}

func countAgentMemberActivityForRouteTest(t *testing.T, channelID, action string) int {
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
		t.Fatalf("count agent member activity %s: %v", action, err)
	}
	return got
}

func countAgentMemberSystemEventForRouteTest(t *testing.T, channelID, event string) int {
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
		t.Fatalf("count agent member system event %s: %v", event, err)
	}
	return got
}
