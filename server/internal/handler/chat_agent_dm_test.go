package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentDirectMessage_CreatesDMForInitiator(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "DM Bot", []byte("[]"))

	// A running task whose initiator is the test user — the human being worked for.
	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_task_queue (agent_id, runtime_id, status, priority, initiator_user_id)
		SELECT $1, runtime_id, 'running', 0, $2 FROM agent WHERE id = $1
		RETURNING id`, agentID, testUserID).Scan(&taskID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE agent_id=$1 AND creator_id=$2`, agentID, testUserID)
		testPool.Exec(context.Background(), `DELETE FROM agent_task_queue WHERE id=$1`, taskID)
	})

	body, _ := json.Marshal(map[string]string{"content": "部署好了，3001 通了，请你确认下"})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/agent-dm", bytes.NewReader(body))
	// Task-scoped agent actor: resolveActor trusts X-Actor-Source=task_token.
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	req = withChatTestWorkspaceCtx(t, req)

	rec := httptest.NewRecorder()
	testHandler.AgentDirectMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}

	var role, content string
	var unreadSet bool
	if err := testPool.QueryRow(ctx, `
		SELECT cm.role, cm.content, cs.unread_since IS NOT NULL
		FROM chat_session cs JOIN chat_message cm ON cm.chat_session_id = cs.id
		WHERE cs.agent_id=$1 AND cs.creator_id=$2
		ORDER BY cm.created_at DESC LIMIT 1`, agentID, testUserID).Scan(&role, &content, &unreadSet); err != nil {
		t.Fatalf("DM session/message not created: %v", err)
	}
	if role != "assistant" {
		t.Fatalf("role = %q, want assistant", role)
	}
	if !unreadSet {
		t.Fatalf("unread_since not stamped — recipient badge would not light up")
	}
}

func TestListChatSessions_ExcludesChannelBackedSessions(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "DM Filter Bot", []byte("[]"))

	// A genuine 1:1 DM session.
	var dmSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, 'dm') RETURNING id`, testWorkspaceID, agentID, testUserID).Scan(&dmSessionID); err != nil {
		t.Fatalf("create dm session: %v", err)
	}
	// A channel-backed session for the same user/agent — must be hidden.
	var channelID, chSessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by) VALUES ($1, 'dm-filter-chan', $2)
		RETURNING id`, testWorkspaceID, testUserID).Scan(&channelID); err != nil {
		t.Fatalf("create channel: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title)
		VALUES ($1, $2, $3, '#dm-filter-chan') RETURNING id`, testWorkspaceID, agentID, testUserID).Scan(&chSessionID); err != nil {
		t.Fatalf("create channel session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
		VALUES ($1, $2, $3)`, channelID, agentID, chSessionID); err != nil {
		t.Fatalf("link channel session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id=$1`, channelID)
		testPool.Exec(context.Background(), `DELETE FROM chat_session WHERE id IN ($1,$2)`, dmSessionID, chSessionID)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/chat/sessions?status=all", nil)
	req.Header.Set("X-User-ID", testUserID)
	req = withChatTestWorkspaceCtx(t, req)
	rec := httptest.NewRecorder()
	testHandler.ListChatSessions(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}

	var sessions []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &sessions); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	ids := map[string]bool{}
	for _, s := range sessions {
		if id, ok := s["id"].(string); ok {
			ids[id] = true
		}
	}
	if !ids[dmSessionID] {
		t.Fatalf("genuine DM session %s missing from the list", dmSessionID)
	}
	if ids[chSessionID] {
		t.Fatalf("channel-backed session %s leaked into the DM list", chSessionID)
	}
}

func TestAgentDirectMessage_RejectsHumanCaller(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	body, _ := json.Marshal(map[string]string{"content": "hi"})
	req := httptest.NewRequest(http.MethodPost, "/api/chat/agent-dm", bytes.NewReader(body))
	req.Header.Set("X-User-ID", testUserID) // a human, no agent/task headers
	req = withChatTestWorkspaceCtx(t, req)

	rec := httptest.NewRecorder()
	testHandler.AgentDirectMessage(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (DMs are agent-only); body=%s", rec.Code, rec.Body.String())
	}
}
