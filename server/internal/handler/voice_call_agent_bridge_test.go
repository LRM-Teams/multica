package handler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/service/voicecall"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestVoiceCallAgentBridgePersistsAndDispatchesOneIdempotentDMTurn(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	agentID := createHandlerTestAgent(t, "Voice Call Dispatch Agent", []byte("[]"))
	channelID := seedAgentDMChannel(t, agentID)
	callID := seedVoiceCallAgentBridgeSession(
		t,
		channelID,
		agentID,
		testUserID,
		string(voicecall.StatusConnecting),
		voiceCallAgentProvider,
	)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE id = $1`, callID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})

	bridge, err := NewVoiceCallAgentBridge(testHandler, 5*time.Second)
	if err != nil {
		t.Fatalf("create voice call agent bridge: %v", err)
	}
	input := VoiceCallLLMInput{
		VoiceCallID: callID,
		RoundID:     "17",
		Transcript:  "  帮我检查项目当前的测试失败。  ",
	}
	first, err := bridge.dispatch(context.Background(), input)
	if err != nil {
		t.Fatalf("dispatch first voice call turn: %v", err)
	}
	if !first.Created {
		t.Fatal("first voice call turn was not created")
	}
	if first.Scope.ChannelID != channelID ||
		first.Scope.AgentID != agentID ||
		first.Scope.UserID != testUserID {
		t.Fatalf("dispatch scope = %#v", first.Scope)
	}
	if first.Message.Content != "帮我检查项目当前的测试失败。" {
		t.Fatalf("message content = %q", first.Message.Content)
	}
	if first.Event.Reason != protocol.AgentInboxReasonVoiceCall ||
		uuidToString(first.Event.SourceMessageID) != first.Message.ID ||
		uuidToString(first.Event.AgentID) != agentID {
		t.Fatalf("inbox event = %#v", first.Event)
	}

	second, err := bridge.dispatch(context.Background(), input)
	if err != nil {
		t.Fatalf("dispatch duplicate voice call turn: %v", err)
	}
	if second.Created {
		t.Fatal("duplicate voice call turn created new state")
	}
	if second.Message.ID != first.Message.ID ||
		uuidToString(second.Event.ID) != uuidToString(first.Event.ID) {
		t.Fatalf(
			"duplicate IDs message=%s/%s event=%s/%s",
			first.Message.ID,
			second.Message.ID,
			uuidToString(first.Event.ID),
			uuidToString(second.Event.ID),
		)
	}
	conflictingInput := input
	conflictingInput.Transcript = "同一轮次被改成另一段内容"
	if _, err := bridge.dispatch(context.Background(), conflictingInput); !errors.Is(
		err,
		errVoiceCallAgentTurnConflict,
	) {
		t.Fatalf("conflicting duplicate error = %v, want persisted-state conflict", err)
	}

	var messageCount, eventCount int
	var clientMessageID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*), min(client_message_id)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'user'
		  AND content = $2`,
		channelID,
		first.Message.Content,
	).Scan(&messageCount, &clientMessageID); err != nil {
		t.Fatalf("count persisted voice call messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("persisted message count = %d, want 1", messageCount)
	}
	if clientMessageID != voiceCallAgentClientMessageID(callID, input.RoundID) ||
		len(clientMessageID) > channelClientMessageIDMaxLen {
		t.Fatalf("client_message_id = %q", clientMessageID)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE source_message_id = $1
		  AND agent_id = $2
		  AND reason = $3`,
		first.Message.ID,
		agentID,
		protocol.AgentInboxReasonVoiceCall,
	).Scan(&eventCount); err != nil {
		t.Fatalf("count persisted voice call inbox events: %v", err)
	}
	if eventCount != 1 {
		t.Fatalf("persisted inbox event count = %d, want 1", eventCount)
	}

	var prompt string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content
		FROM chat_message
		WHERE task_id = $1 AND role = 'user'`,
		first.Event.ID,
	).Scan(&prompt); err != nil {
		t.Fatalf("load voice call agent prompt: %v", err)
	}
	for _, want := range []string{
		first.Message.Content,
		"Live voice call delivery:",
		"normal Multica memory, tools, permissions, and project context",
		"RTC synthesizes your reply as speech",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("voice call prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestVoiceCallAgentBridgeRejectsInactiveOrMismatchedScopeWithoutWriting(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	targetAgentID := createHandlerTestAgent(t, "Voice Call Scope Agent", []byte("[]"))
	otherAgentID := createHandlerTestAgent(t, "Voice Call Wrong Agent", []byte("[]"))
	channelID := seedAgentDMChannel(t, targetAgentID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM voice_call_session WHERE channel_id = $1`, channelID)
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channelID)
	})
	bridge, err := NewVoiceCallAgentBridge(testHandler, 5*time.Second)
	if err != nil {
		t.Fatalf("create voice call agent bridge: %v", err)
	}

	testCases := []struct {
		name     string
		agentID  string
		status   string
		provider string
		wantErr  error
	}{
		{
			name:     "ended call",
			agentID:  targetAgentID,
			status:   string(voicecall.StatusEnded),
			provider: voiceCallAgentProvider,
			wantErr:  errVoiceCallAgentTurnUnavailable,
		},
		{
			name:     "wrong provider",
			agentID:  targetAgentID,
			status:   string(voicecall.StatusActive),
			provider: "other-provider",
			wantErr:  errVoiceCallAgentTurnUnavailable,
		},
		{
			name:     "agent outside canonical DM",
			agentID:  otherAgentID,
			status:   string(voicecall.StatusActive),
			provider: voiceCallAgentProvider,
			wantErr:  voicecall.ErrScopeNotFound,
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			callID := seedVoiceCallAgentBridgeSession(
				t,
				channelID,
				testCase.agentID,
				testUserID,
				testCase.status,
				testCase.provider,
			)
			_, err := bridge.dispatch(context.Background(), VoiceCallLLMInput{
				VoiceCallID: callID,
				RoundID:     uuid.NewString(),
				Transcript:  "不应写入",
			})
			if !errors.Is(err, testCase.wantErr) {
				t.Fatalf("dispatch error = %v, want %v", err, testCase.wantErr)
			}
			if _, cleanupErr := testPool.Exec(
				context.Background(),
				`DELETE FROM voice_call_session WHERE id = $1`,
				callID,
			); cleanupErr != nil {
				t.Fatalf("cleanup voice call session: %v", cleanupErr)
			}
		})
	}

	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND content = '不应写入'`,
		channelID,
	).Scan(&count); err != nil {
		t.Fatalf("count rejected voice call messages: %v", err)
	}
	if count != 0 {
		t.Fatalf("rejected voice call message count = %d, want 0", count)
	}
}

func TestVoiceCallAgentBridgeConstructorRequiresRuntimeDependencies(t *testing.T) {
	if _, err := NewVoiceCallAgentBridge(&Handler{}, time.Second); err == nil {
		t.Fatal("bridge constructor accepted an unconfigured handler")
	}
	if _, err := NewVoiceCallAgentBridge(testHandler, 0); err == nil {
		t.Fatal("bridge constructor accepted a zero wait timeout")
	}
}

func seedVoiceCallAgentBridgeSession(
	t *testing.T,
	channelID string,
	agentID string,
	userID string,
	status string,
	provider string,
) string {
	t.Helper()
	callID := uuid.NewString()
	endedAt := "NULL"
	if status == string(voicecall.StatusEnded) || status == string(voicecall.StatusFailed) {
		endedAt = "now()"
	}
	query := `
		INSERT INTO voice_call_session (
		  id, workspace_id, channel_id, agent_id, user_id, provider, status, ended_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, ` + endedAt + `)`
	if _, err := testPool.Exec(
		context.Background(),
		query,
		callID,
		testWorkspaceID,
		channelID,
		agentID,
		userID,
		provider,
		status,
	); err != nil {
		t.Fatalf("seed voice call agent bridge session: %v", err)
	}
	return callID
}
