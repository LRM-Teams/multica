package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

func TestVoiceCallAgentBridgeWaitsForExactAssistantCompletion(t *testing.T) {
	bridge, dispatch, _, cleanup := voiceCallAgentResultFixture(t)
	defer cleanup()

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', '不相关的新消息', $2)`,
		dispatch.Event.ChatSessionID,
		uuid.NewString(),
	); err != nil {
		t.Fatalf("seed unrelated assistant message: %v", err)
	}

	completionErr := make(chan error, 1)
	go func() {
		time.Sleep(30 * time.Millisecond)
		tx, err := testPool.Begin(context.Background())
		if err != nil {
			completionErr <- err
			return
		}
		defer tx.Rollback(context.Background())
		if _, err = tx.Exec(context.Background(), `
			INSERT INTO chat_message (chat_session_id, role, content, task_id)
			VALUES ($1, 'assistant', '  这是当前通话任务的准确回答。  ', $2)`,
			dispatch.Event.ChatSessionID,
			dispatch.Event.ID,
		); err == nil {
			_, err = tx.Exec(context.Background(), `
				UPDATE agent_inbox_event
				SET status = 'acked',
				    terminal_outcome = 'replied',
				    terminal_at = now(),
				    acked_at = now(),
				    updated_at = now()
				WHERE id = $1`,
				dispatch.Event.ID,
			)
		}
		if err == nil {
			err = tx.Commit(context.Background())
		}
		completionErr <- err
	}()

	content, err := bridge.waitForCompletion(context.Background(), dispatch)
	if err != nil {
		t.Fatalf("wait for assistant completion: %v", err)
	}
	if err := <-completionErr; err != nil {
		t.Fatalf("persist assistant completion: %v", err)
	}
	if content != "这是当前通话任务的准确回答。" {
		t.Fatalf("spoken completion = %q", content)
	}
}

func TestVoiceCallAgentBridgeUsesExactTransportMessagesInOrder(t *testing.T) {
	bridge, dispatch, _, cleanup := voiceCallAgentResultFixture(t)
	defer cleanup()

	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', '不应覆盖消息工具的输出', $2)`,
		dispatch.Event.ChatSessionID,
		dispatch.Event.ID,
	); err != nil {
		t.Fatalf("seed assistant fallback: %v", err)
	}
	for index, content := range []string{"第一段回答。", "第二段回答。"} {
		var messageID string
		if err := testPool.QueryRow(context.Background(), `
			INSERT INTO channel_message (
			  channel_id, workspace_id, author_type, author_id, author_name,
			  content, parts, source, client_message_id
			)
			VALUES ($1, $2, 'agent', $3, 'Voice Agent', $4, '[]'::jsonb, 'multica', $5)
			RETURNING id`,
			dispatch.Scope.ChannelID,
			dispatch.Scope.WorkspaceID,
			dispatch.Event.AgentID,
			content,
			"voice-result-"+uuid.NewString(),
		).Scan(&messageID); err != nil {
			t.Fatalf("seed transport message %d: %v", index, err)
		}
		if _, err := testPool.Exec(context.Background(), `
			INSERT INTO agent_task_transport_audit (
			  workspace_id, inbox_event_id, agent_id, action, target,
			  channel_id, channel_message_id, context_pack
			)
			VALUES ($1, $2, $3, 'message_send', $4, $5, $6, '{}'::jsonb)`,
			dispatch.Scope.WorkspaceID,
			dispatch.Event.ID,
			dispatch.Event.AgentID,
			"dm:@member",
			dispatch.Scope.ChannelID,
			messageID,
		); err != nil {
			t.Fatalf("seed transport audit %d: %v", index, err)
		}
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked',
		    terminal_outcome = 'replied',
		    terminal_at = now(),
		    acked_at = now(),
		    updated_at = now()
		WHERE id = $1`,
		dispatch.Event.ID,
	); err != nil {
		t.Fatalf("complete transport event: %v", err)
	}

	content, err := bridge.waitForCompletion(context.Background(), dispatch)
	if err != nil {
		t.Fatalf("wait for transport completion: %v", err)
	}
	if content != "第一段回答。\n第二段回答。" {
		t.Fatalf("transport completion = %q", content)
	}
}

func TestVoiceCallAgentBridgeTerminalOutcomesDoNotFabricateSpeech(t *testing.T) {
	bridge, firstDispatch, callID, cleanup := voiceCallAgentResultFixture(t)
	defer cleanup()

	testCases := []struct {
		outcome string
		wantErr error
	}{
		{outcome: "no_reply", wantErr: errVoiceCallAgentNoReply},
		{outcome: "held", wantErr: errVoiceCallAgentHeld},
		{outcome: "failed", wantErr: errVoiceCallAgentFailed},
		{outcome: "replied", wantErr: errVoiceCallAgentNoReply},
	}
	for index, testCase := range testCases {
		t.Run(testCase.outcome, func(t *testing.T) {
			dispatch := firstDispatch
			if index > 0 {
				var err error
				dispatch, err = bridge.dispatch(context.Background(), VoiceCallLLMInput{
					VoiceCallID: callID,
					RoundID:     uuid.NewString(),
					Transcript:  "测试终态",
				})
				if err != nil {
					t.Fatalf("dispatch terminal outcome turn: %v", err)
				}
			}
			if _, err := testPool.Exec(context.Background(), `
				UPDATE agent_inbox_event
				SET status = 'acked',
				    terminal_outcome = $2,
				    terminal_at = now(),
				    acked_at = now(),
				    updated_at = now()
				WHERE id = $1`,
				dispatch.Event.ID,
				testCase.outcome,
			); err != nil {
				t.Fatalf("set terminal outcome: %v", err)
			}
			if _, err := bridge.waitForCompletion(context.Background(), dispatch); !errors.Is(
				err,
				testCase.wantErr,
			) {
				t.Fatalf("completion error = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

func TestVoiceCallAgentBridgeWaitTimeoutKeepsDurableTask(t *testing.T) {
	bridge, dispatch, _, cleanup := voiceCallAgentResultFixture(t)
	defer cleanup()
	bridge.waitTimeout = 35 * time.Millisecond
	bridge.pollInterval = 5 * time.Millisecond
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'failed',
		    last_error = 'retryable delivery failure',
		    updated_at = now()
		WHERE id = $1`,
		dispatch.Event.ID,
	); err != nil {
		t.Fatalf("mark delivery retryable: %v", err)
	}

	if _, err := bridge.waitForCompletion(context.Background(), dispatch); !errors.Is(
		err,
		errVoiceCallAgentTurnTimeout,
	) {
		t.Fatalf("wait timeout error = %v", err)
	}

	var status string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status
		FROM agent_inbox_event
		WHERE id = $1`,
		dispatch.Event.ID,
	).Scan(&status); err != nil {
		t.Fatalf("reload timed-out inbox event: %v", err)
	}
	if status != "failed" {
		t.Fatalf("timed-out inbox status = %q, want retryable failed", status)
	}
}

func voiceCallAgentResultFixture(
	t *testing.T,
) (*VoiceCallAgentBridge, voiceCallAgentDispatchResult, string, func()) {
	t.Helper()
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Voice Call Result Agent", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	callID := seedVoiceCallAgentBridgeSession(
		t,
		channelID,
		agentID,
		testUserID,
		string(voicecall.StatusActive),
		voiceCallAgentProvider,
	)
	cleanup := func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE id = $1`, callID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	}

	bridge, err := NewVoiceCallAgentBridge(testHandler, time.Second)
	if err != nil {
		cleanup()
		t.Fatalf("create voice call result bridge: %v", err)
	}
	bridge.pollInterval = 5 * time.Millisecond
	dispatch, err := bridge.dispatch(context.Background(), VoiceCallLLMInput{
		VoiceCallID: callID,
		RoundID:     uuid.NewString(),
		Transcript:  "请给出准确结果",
	})
	if err != nil {
		cleanup()
		t.Fatalf("dispatch voice call result fixture: %v", err)
	}
	return bridge, dispatch, callID, cleanup
}
