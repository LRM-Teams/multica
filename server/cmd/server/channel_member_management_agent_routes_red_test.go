package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}
