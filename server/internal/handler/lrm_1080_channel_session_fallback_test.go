package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1080 / LRM-1079 P1: channel-bound paths must not hard-require chat_session.

func TestIsChannelAgentTask_ChannelIDWithoutChatSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "lrm-1080-channel-task-"+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "lrm-1080-channel-task-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, channel_id, status, priority,
			reason, delivery_mode, response_mode, requires_wake, started_at
		)
		VALUES ($1, $2, $3, $4, 'draining', 2, 'channel_role_changed', 'execute', 'public_response', true, now())
		RETURNING id`,
		testWorkspaceID, agentID, handlerTestRuntimeID(t), channelID,
	).Scan(&eventID); err != nil {
		t.Fatalf("seed channel-only inbox event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	event, err := testHandler.Queries.GetAgentInboxEvent(ctx, parseUUID(eventID))
	if err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	if event.ChatSessionID.Valid {
		t.Fatal("expected chat_session_id NULL")
	}
	if !testHandler.isChannelAgentTask(ctx, event) {
		t.Fatal("isChannelAgentTask=false for channel_id-bound wake without chat_session")
	}
}

func TestChannelInitiatorForTask_PrefersWakeInitiatorWithoutSession(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "lrm-1080-initiator-"+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "lrm-1080-initiator-"+uuid.NewString(), testUserID)

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, channel_id, initiator_user_id,
			status, priority, reason, requires_wake, started_at
		)
		VALUES ($1, $2, $3, $4, $5, 'draining', 2, 'ambient', true, now())
		RETURNING id`,
		testWorkspaceID, agentID, handlerTestRuntimeID(t), channelID, testUserID,
	).Scan(&eventID); err != nil {
		t.Fatalf("seed initiator event: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	event, err := testHandler.Queries.GetAgentInboxEvent(ctx, parseUUID(eventID))
	if err != nil {
		t.Fatalf("load inbox event: %v", err)
	}
	got := testHandler.channelInitiatorForTask(ctx, event)
	if uuidToString(got) != testUserID {
		t.Fatalf("channelInitiatorForTask=%q, want wake initiator %q", uuidToString(got), testUserID)
	}
	if testHandler.channelInitiatorForChatSession(ctx, pgtype.UUID{}).Valid {
		t.Fatal("channelInitiatorForChatSession must no-op on empty session id")
	}
}

func TestCompleteTask_ChannelOnlyWakeSuppressesUnsentFinalOutput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	metadata, err := json.Marshal(map[string]any{"capabilities": []string{protocol.DaemonCapabilityChannelOutputActions}})
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status, device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'handler_test_runtime', 'online', 'lrm-1080 runtime', $4, now())
		RETURNING id
	`, testWorkspaceID, "lrm-1080-"+uuid.NewString(), "LRM-1080 Runtime "+uuid.NewString(), metadata).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
	})

	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args, mcp_config, model
		) VALUES ($1, $2, '', 'local', '{}'::jsonb, $3, 1, $4, '', '{}'::jsonb, '[]'::jsonb, NULL, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, "LRM-1080 Agent "+uuid.NewString(), runtimeID, testUserID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID) })

	channelID := seedChannelForTest(t, "lrm-1080-complete-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}

	var taskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, agent_id, runtime_id, channel_id, status, priority,
			reason, delivery_mode, response_mode, requires_wake, started_at
		)
		VALUES ($1, $2, $3, $4, 'draining', 2, 'channel_role_changed', 'execute', 'public_response', true, now())
		RETURNING id`,
		testWorkspaceID, agentID, runtimeID, channelID,
	).Scan(&taskID); err != nil {
		t.Fatalf("create channel-only draining task: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, taskID)
	})

	const visibleReply = "LRM-1080 channel-only final text must not bridge"
	w := completeTaskForTest(t, taskID, map[string]any{
		"action": "message_send",
		"output": visibleReply,
	})
	if w.Code != http.StatusOK {
		t.Fatalf("CompleteTask: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var channelMessageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content = $2
	`, channelID, visibleReply).Scan(&channelMessageCount); err != nil {
		t.Fatalf("count bridged channel messages: %v", err)
	}
	if channelMessageCount != 0 {
		t.Fatalf("channel message count = %d, want 0 (channel-only wake still suppresses unsent final)", channelMessageCount)
	}
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonUnsentFinalOutput)
}
