package handler

import (
	"context"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/integrations/volcenginertc"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
)

func TestVoiceCallFunctionBridgeDispatchesRealAgentAndReturnsExactResult(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Voice Function Agent", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	callID := seedVoiceCallAgentBridgeSession(
		t,
		channelID,
		agentID,
		testUserID,
		string(voicecall.StatusActive),
		voiceCallAgentProvider,
	)
	const roomID = "voice-call-function-nonce-1"
	const taskID = "voice-task-function-nonce-1"
	if _, err := testPool.Exec(context.Background(), `
		UPDATE voice_call_session
		SET room_id = $2, provider_task_id = $3
		WHERE id = $1`,
		callID,
		roomID,
		taskID,
	); err != nil {
		t.Fatalf("scope voice call session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE id = $1`, callID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	agentBridge, err := NewVoiceCallAgentBridge(testHandler, time.Second)
	if err != nil {
		t.Fatalf("create agent bridge: %v", err)
	}
	agentBridge.pollInterval = 5 * time.Millisecond
	updater := &fakeVoiceCallFunctionUpdater{
		requests: make(chan volcenginertc.UpdateVoiceChatRequest, 2),
	}
	functionBridge, err := NewVoiceCallFunctionBridge(
		testHandler,
		agentBridge,
		updater,
		"rtc-app",
	)
	if err != nil {
		t.Fatalf("create function bridge: %v", err)
	}

	err = functionBridge.HandleFunctionCalls(
		context.Background(),
		roomID,
		volcenginertc.FunctionCallMessage{
			SubscriberUserID: "voice-member-function-nonce-1",
			ToolCalls: []volcenginertc.FunctionToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: volcenginertc.FunctionToolCallFunction{
					Name:      volcenginertc.VoiceAgentToolName,
					Arguments: `{"request":"创建一个 issue，修复登录页报错。"}`,
				},
			}},
		},
	)
	if err != nil {
		t.Fatalf("handle function call: %v", err)
	}
	if err := functionBridge.HandleFunctionCalls(
		context.Background(),
		roomID,
		volcenginertc.FunctionCallMessage{
			SubscriberUserID: "voice-member-function-nonce-1",
			ToolCalls: []volcenginertc.FunctionToolCall{{
				ID:   "call-1",
				Type: "function",
				Function: volcenginertc.FunctionToolCallFunction{
					Name:      volcenginertc.VoiceAgentToolName,
					Arguments: `{"request":"创建一个 issue，修复登录页报错。"}`,
				},
			}},
		},
	); err != nil {
		t.Fatalf("handle duplicate function call: %v", err)
	}

	var eventID, chatSessionID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT event.id, event.chat_session_id
		FROM agent_inbox_event event
		JOIN channel_message message ON message.id = event.source_message_id
		WHERE message.channel_id = $1
		  AND message.content = '创建一个 issue，修复登录页报错。'
		  AND event.reason = 'dm'`,
		channelID,
	).Scan(&eventID, &chatSessionID); err != nil {
		t.Fatalf("load dispatched agent task: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'assistant', '已创建 issue MUL-42，并补充了验收条件。', $2)`,
		chatSessionID,
		eventID,
	); err != nil {
		t.Fatalf("persist dispatched agent result: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_inbox_event
		SET status = 'acked',
		    terminal_outcome = 'replied',
		    terminal_at = now(),
		    acked_at = now(),
		    updated_at = now()
		WHERE id = $1`,
		eventID,
	); err != nil {
		t.Fatalf("complete dispatched agent task: %v", err)
	}
	var messageCount, eventCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND content = '创建一个 issue，修复登录页报错。'`,
		channelID,
	).Scan(&messageCount); err != nil {
		t.Fatalf("count duplicate voice function messages: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE source_message_id IN (
			SELECT id
			FROM channel_message
			WHERE channel_id = $1
			  AND content = '创建一个 issue，修复登录页报错。'
		)`,
		channelID,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count duplicate voice function events: %v", err)
	}
	if messageCount != 1 || eventCount != 1 {
		t.Fatalf(
			"duplicate function state messages/events = %d/%d, want 1/1",
			messageCount,
			eventCount,
		)
	}

	select {
	case request := <-updater.requests:
		if request.AppID != "rtc-app" ||
			request.RoomID != roomID ||
			request.TaskID != taskID ||
			request.Command != volcenginertc.UpdateCommandExternalTextToSpeech ||
			request.Message != "我已经开始处理，请稍等。" ||
			request.InterruptMode != 2 {
			t.Fatalf("comfort request = %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RTC comfort speech")
	}
	select {
	case request := <-updater.requests:
		if request.AppID != "rtc-app" ||
			request.RoomID != roomID ||
			request.TaskID != taskID ||
			request.Command != volcenginertc.UpdateCommandFunction ||
			request.Message != `{"ToolCallID":"call-1","Content":"已创建 issue MUL-42，并补充了验收条件。"}` {
			t.Fatalf("function result request = %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RTC function result")
	}
}

func TestVoiceCallFunctionBridgeRejectsMismatchedSubscriberWithoutDispatch(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Voice Function Scope Agent", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	callID := seedVoiceCallAgentBridgeSession(
		t,
		channelID,
		agentID,
		testUserID,
		string(voicecall.StatusActive),
		voiceCallAgentProvider,
	)
	const roomID = "voice-call-function-scope"
	if _, err := testPool.Exec(context.Background(), `
		UPDATE voice_call_session
		SET room_id = $2, provider_task_id = 'voice-task-function-scope'
		WHERE id = $1`,
		callID,
		roomID,
	); err != nil {
		t.Fatalf("scope voice call session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE id = $1`, callID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	agentBridge, err := NewVoiceCallAgentBridge(testHandler, time.Second)
	if err != nil {
		t.Fatalf("create agent bridge: %v", err)
	}
	functionBridge, err := NewVoiceCallFunctionBridge(
		testHandler,
		agentBridge,
		&fakeVoiceCallFunctionUpdater{
			requests: make(chan volcenginertc.UpdateVoiceChatRequest, 1),
		},
		"rtc-app",
	)
	if err != nil {
		t.Fatalf("create function bridge: %v", err)
	}

	err = functionBridge.HandleFunctionCalls(
		context.Background(),
		roomID,
		volcenginertc.FunctionCallMessage{
			SubscriberUserID: "voice-member-another-call",
			ToolCalls: []volcenginertc.FunctionToolCall{{
				ID:   "call-scope",
				Type: "function",
				Function: volcenginertc.FunctionToolCallFunction{
					Name:      volcenginertc.VoiceAgentToolName,
					Arguments: `{"request":"不应执行"}`,
				},
			}},
		},
	)
	if err == nil {
		t.Fatal("mismatched subscriber was accepted")
	}

	var messageCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND content = '不应执行'`,
		channelID,
	).Scan(&messageCount); err != nil {
		t.Fatalf("count rejected voice function messages: %v", err)
	}
	if messageCount != 0 {
		t.Fatalf("rejected function message count = %d, want 0", messageCount)
	}
}

func TestVoiceCallFunctionBridgeReturnsTimeoutWithoutCancellingDurableTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Voice Function Timeout Agent", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	callID := seedVoiceCallAgentBridgeSession(
		t,
		channelID,
		agentID,
		testUserID,
		string(voicecall.StatusActive),
		voiceCallAgentProvider,
	)
	const roomID = "voice-call-function-timeout"
	const taskID = "voice-task-function-timeout"
	if _, err := testPool.Exec(context.Background(), `
		UPDATE voice_call_session
		SET room_id = $2, provider_task_id = $3
		WHERE id = $1`,
		callID,
		roomID,
		taskID,
	); err != nil {
		t.Fatalf("scope voice call session: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE id = $1`, callID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	agentBridge, err := NewVoiceCallAgentBridge(testHandler, 35*time.Millisecond)
	if err != nil {
		t.Fatalf("create agent bridge: %v", err)
	}
	agentBridge.pollInterval = 5 * time.Millisecond
	updater := &fakeVoiceCallFunctionUpdater{
		requests: make(chan volcenginertc.UpdateVoiceChatRequest, 2),
	}
	functionBridge, err := NewVoiceCallFunctionBridge(
		testHandler,
		agentBridge,
		updater,
		"rtc-app",
	)
	if err != nil {
		t.Fatalf("create function bridge: %v", err)
	}

	if err := functionBridge.HandleFunctionCalls(
		context.Background(),
		roomID,
		volcenginertc.FunctionCallMessage{
			SubscriberUserID: "voice-member-function-timeout",
			ToolCalls: []volcenginertc.FunctionToolCall{{
				ID:   "call-timeout",
				Type: "function",
				Function: volcenginertc.FunctionToolCallFunction{
					Name:      volcenginertc.VoiceAgentToolName,
					Arguments: `{"request":"执行一个耗时任务"}`,
				},
			}},
		},
	); err != nil {
		t.Fatalf("handle timeout function: %v", err)
	}

	<-updater.requests
	select {
	case request := <-updater.requests:
		if request.Command != volcenginertc.UpdateCommandFunction ||
			request.Message != `{"ToolCallID":"call-timeout","Content":"`+
				voiceCallFunctionTimeoutMessage+`"}` {
			t.Fatalf("timeout function result = %#v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for RTC timeout result")
	}

	var status, terminalOutcome string
	if err := testPool.QueryRow(context.Background(), `
		SELECT status, COALESCE(terminal_outcome, '')
		FROM agent_inbox_event event
		JOIN channel_message message ON message.id = event.source_message_id
		WHERE message.channel_id = $1
		  AND message.content = '执行一个耗时任务'`,
		channelID,
	).Scan(&status, &terminalOutcome); err != nil {
		t.Fatalf("load timed-out durable task: %v", err)
	}
	if status != "pending" || terminalOutcome != "" {
		t.Fatalf(
			"timed-out durable task status/outcome = %q/%q, want pending/blank",
			status,
			terminalOutcome,
		)
	}
}

type fakeVoiceCallFunctionUpdater struct {
	requests chan volcenginertc.UpdateVoiceChatRequest
	err      error
}

func (updater *fakeVoiceCallFunctionUpdater) UpdateVoiceChat(
	_ context.Context,
	request volcenginertc.UpdateVoiceChatRequest,
) (volcenginertc.Response, error) {
	updater.requests <- request
	return volcenginertc.Response{RequestID: "request-function-result"}, updater.err
}
