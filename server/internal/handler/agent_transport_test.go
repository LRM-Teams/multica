package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type agentDMPauseFailOnceDBTX struct {
	db.DBTX
	failed bool
}

func (f *agentDMPauseFailOnceDBTX) QueryRow(ctx context.Context, query string, args ...any) pgx.Row {
	if !f.failed && strings.Contains(query, "INSERT INTO inbox_item") {
		f.failed = true
		return agentDMPauseErrorRow{err: errors.New("forced owner inbox write failure")}
	}
	return f.DBTX.QueryRow(ctx, query, args...)
}

type agentDMPauseErrorRow struct {
	err error
}

func (r agentDMPauseErrorRow) Scan(...any) error {
	return r.err
}

func TestAgentTransportVoiceReplyPartsRequireSameTimeline(t *testing.T) {
	channelID := uuid.NewString()
	otherChannelID := uuid.NewString()
	rootID := uuid.NewString()
	otherRootID := uuid.NewString()
	threadRootID := rootID
	voiceTrigger := ChannelMessageResponse{
		ID:        rootID,
		Type:      "user",
		ChannelID: channelID,
		Parts: []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "语音问题"},
			{Type: protocol.MessagePartTypeVoice, DurationMS: 1000},
		},
	}
	threadTrigger := voiceTrigger
	threadTrigger.ThreadRootMessageID = &threadRootID
	agentTrigger := voiceTrigger
	agentTrigger.Type = "agent"
	textTrigger := voiceTrigger
	textTrigger.Parts = []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "文字问题"}}

	tests := []struct {
		name    string
		trigger ChannelMessageResponse
		target  agentTransportTarget
		want    bool
	}{
		{
			name:    "channel main reply",
			trigger: voiceTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetChannel, channel: ChannelResponse{ID: channelID}},
			want:    true,
		},
		{
			name:    "dm main reply",
			trigger: voiceTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetDM, channel: ChannelResponse{ID: channelID}},
			want:    true,
		},
		{
			name:    "matching thread reply",
			trigger: threadTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetThread, channel: ChannelResponse{ID: channelID}, threadRootMessageID: parseUUID(rootID)},
			want:    true,
		},
		{
			name:    "proactive other channel",
			trigger: voiceTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetChannel, channel: ChannelResponse{ID: otherChannelID}},
		},
		{
			name:    "different thread",
			trigger: threadTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetThread, channel: ChannelResponse{ID: channelID}, threadRootMessageID: parseUUID(otherRootID)},
		},
		{
			name:    "main trigger to its own thread",
			trigger: voiceTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetThread, channel: ChannelResponse{ID: channelID}, threadRootMessageID: parseUUID(rootID)},
			want:    true,
		},
		{
			name:    "main trigger to unrelated thread",
			trigger: voiceTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetThread, channel: ChannelResponse{ID: channelID}, threadRootMessageID: parseUUID(otherRootID)},
		},
		{
			name:    "agent voice does not force response",
			trigger: agentTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetChannel, channel: ChannelResponse{ID: channelID}},
		},
		{
			name:    "human text does not force voice",
			trigger: textTrigger,
			target:  agentTransportTarget{kind: chatOutputTargetChannel, channel: ChannelResponse{ID: channelID}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := agentTransportVoiceReplyParts(tt.trigger, tt.target, "完整回答", nil)
			if channelMessageHasVoicePart(got) != tt.want {
				t.Fatalf("voice part=%v, want %v: %+v", channelMessageHasVoicePart(got), tt.want, got)
			}
			if tt.want && !agentTransportPartsHaveText(got) {
				t.Fatalf("enforced voice reply lacks transcript text part: %+v", got)
			}
		})
	}

	sameTimeline := agentTransportTarget{kind: chatOutputTargetChannel, channel: ChannelResponse{ID: channelID}}
	sticker := []protocol.MessagePart{{Type: protocol.MessagePartTypeSticker, Alt: "打招呼"}}
	if got := agentTransportVoiceReplyParts(voiceTrigger, sameTimeline, "打招呼", sticker); channelMessageHasVoicePart(got) {
		t.Fatalf("sticker fallback was converted to speech: %+v", got)
	}
	if got := agentTransportVoiceReplyParts(voiceTrigger, sameTimeline, "这是文字回答", sticker); !channelMessageHasVoicePart(got) {
		t.Fatalf("explicit text with sticker did not preserve voice modality: %+v", got)
	}
	alreadyVoice := []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "已有语音"},
		{Type: protocol.MessagePartTypeVoice},
	}
	if got := agentTransportVoiceReplyParts(voiceTrigger, sameTimeline, "已有语音", alreadyVoice); len(got) != len(alreadyVoice) {
		t.Fatalf("existing voice part duplicated: %+v", got)
	}
}

func TestAgentTransportSendMessageEnforcesVoiceReplyForVoiceTrigger(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	trigger, err := testHandler.insertChannelMessageWithParts(
		ctx,
		parseUUID(channelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Tester",
		"请回答这个语音问题",
		[]protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "请回答这个语音问题"},
			{Type: protocol.MessagePartTypeVoice, DurationMS: 1200},
		},
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("seed voice trigger: %v", err)
	}
	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_inbox_event
		WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load task chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO chat_message (chat_session_id, role, content, task_id)
		VALUES ($1, 'user', $2, $3)`, chatSessionID, "Reaction target message id: "+trigger.ID, taskID); err != nil {
		t.Fatalf("seed task source prompt: %v", err)
	}

	content := "这是智能体的完整回答"
	rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "#" + channelNameForTransportTest(t, channelID),
		"content":           content,
		"client_message_id": "voice-enforced-" + uuid.NewString(),
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("transport send: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode transport response: %v", err)
	}
	if !channelMessageHasVoicePart(body.Message.Parts) || !agentTransportPartsHaveText(body.Message.Parts) {
		t.Fatalf("message parts=%+v, want accessible voice reply", body.Message.Parts)
	}
	if body.Message.Content != content {
		t.Fatalf("message content=%q, want %q", body.Message.Content, content)
	}
}

func TestAgentTransportSendMessageIdempotentAndSuppressesFinalOutput(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	clientID := "transport-" + uuid.NewString()
	content := "hello via transport " + uuid.NewString()

	first := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           content,
		"client_message_id": clientID,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first transport send: status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody AgentTransportSendResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode first send: %v", err)
	}
	if !firstBody.Created {
		t.Fatal("first transport send created=false, want true")
	}
	if firstBody.Message.Content != content || firstBody.Message.ClientMessageID == nil || *firstBody.Message.ClientMessageID != clientID {
		t.Fatalf("first message payload mismatch: %+v", firstBody.Message)
	}
	assertAgentMessageSentActivityText(t, firstBody.Message.ID, content)

	second := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           content,
		"client_message_id": clientID,
	})
	if second.Code != http.StatusCreated {
		t.Fatalf("second transport send: status=%d body=%s", second.Code, second.Body.String())
	}
	var secondBody AgentTransportSendResponse
	if err := json.Unmarshal(second.Body.Bytes(), &secondBody); err != nil {
		t.Fatalf("decode second send: %v", err)
	}
	if secondBody.Created {
		t.Fatal("second transport send created=true, want idempotent replay")
	}
	if secondBody.Message.ID != firstBody.Message.ID {
		t.Fatalf("idempotent replay message id=%s, want %s", secondBody.Message.ID, firstBody.Message.ID)
	}
	assertAgentMessageSentActivityCount(t, firstBody.Message.ID, 1)

	var messageRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND client_message_id = $2 AND content = $3`,
		channelID, clientID, content).Scan(&messageRows); err != nil {
		t.Fatalf("count idempotent channel messages: %v", err)
	}
	if messageRows != 1 {
		t.Fatalf("transport message rows=%d, want 1", messageRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionSend, 2)

	finalText := "duplicate final text " + uuid.NewString()
	done := completeTaskForTest(t, taskID, map[string]any{"output": finalText})
	if done.Code != http.StatusOK {
		t.Fatalf("complete task: status=%d body=%s", done.Code, done.Body.String())
	}
	assertTaskOutputSuppressedReason(t, taskID, protocol.ChannelOutputSuppressedReasonToolTransportOutput)
	assertNoChannelMessageContent(t, channelID, finalText)
}

// TestAgentTransportAgentDMExchangeNeverAutoPausesAndMustReplyChain replaces
// the old "...ThreeRoundBudgetAndMustReplyChain" test: task #813/#830
// (Frank, 2026-07-28, reaffirmed 2026-07-31 #prj-daemon) tore out the
// automatic round/frequency pause gates — "把这个硬闸拆掉，改成只观测". This
// runs the exchange past the old 3-round/6-turn budget boundary and proves
// it keeps going: state stays active, no system pause message, no owner
// pause inbox item, and a send past the old boundary still succeeds.
func TestAgentTransportAgentDMExchangeNeverAutoPausesAndMustReplyChain(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstTaskID, _ := createChannelCompletionTask(t, "group")
	firstAgentID := agentIDForTask(t, firstTaskID)
	secondAgentID := createHandlerTestAgent(t, "A2A Peer "+uuid.NewString(), []byte("[]"))
	firstHandle := agentHandleForTransportTest(t, firstAgentID)
	secondHandle := agentHandleForTransportTest(t, secondAgentID)
	canonical := dmCanonicalName("agent", firstAgentID, "agent", secondAgentID)
	t.Cleanup(func() {
		testPool.Exec(ctx, `
			DELETE FROM channel
			WHERE workspace_id = $1 AND name = $2 AND kind = 'dm'`,
			testWorkspaceID, canonical)
		testPool.Exec(ctx, `
			DELETE FROM inbox_item
			WHERE workspace_id = $1 AND type = 'agent_dm_paused'`,
			testWorkspaceID)
	})

	send := func(taskID, agentID, target string, turn int) *httptest.ResponseRecorder {
		t.Helper()
		return agentTransportSendForTest(t, taskID, agentID, map[string]any{
			"target":            target,
			"content":           fmt.Sprintf("A2A turn %d", turn),
			"client_message_id": fmt.Sprintf("a2a-%d-%s", turn, uuid.NewString()),
		})
	}

	first := send(firstTaskID, firstAgentID, "dm:@"+secondHandle, 1)
	if first.Code != http.StatusCreated {
		t.Fatalf("turn 1 send: status=%d body=%s", first.Code, first.Body.String())
	}
	var firstBody AgentTransportSendResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstBody); err != nil {
		t.Fatalf("decode turn 1 send: %v", err)
	}

	var channelID, exchangeID string
	if err := testPool.QueryRow(ctx, `
		SELECT channel_id, id
		FROM agent_dm_exchange
		WHERE workspace_id = $1
		  AND agent_low_id = LEAST($2::uuid, $3::uuid)
		  AND agent_high_id = GREATEST($2::uuid, $3::uuid)
		ORDER BY created_at DESC
		LIMIT 1`,
		testWorkspaceID, firstAgentID, secondAgentID).Scan(&channelID, &exchangeID); err != nil {
		t.Fatalf("load A2A exchange: %v", err)
	}

	currentRecipient := secondAgentID
	currentTarget := "dm:@" + firstHandle + ":" + firstBody.Message.ID
	// The old round_limit=3 default paused the exchange at turn_count=6
	// (3 rounds * 2). Run to turn 9 (10 total turns) to prove there's no
	// boundary effect left anywhere near the old budget.
	const replyTurns = 9
	for turn := 1; turn <= replyTurns; turn++ {
		var eventID, responseMode string
		var requiresWake bool
		var eventTurn int
		if err := testPool.QueryRow(ctx, `
			SELECT id, response_mode, requires_wake, agent_dm_turn
			FROM agent_inbox_event
			WHERE agent_dm_exchange_id = $1
			  AND agent_dm_turn = $2
			  AND agent_id = $3`,
			exchangeID, turn, currentRecipient).Scan(
			&eventID, &responseMode, &requiresWake, &eventTurn,
		); err != nil {
			t.Fatalf("turn %d recipient inbox event: %v", turn, err)
		}
		if responseMode != "public_response" || !requiresWake || eventTurn != turn {
			t.Fatalf(
				"turn %d event mode=%q requires_wake=%v event_turn=%d",
				turn, responseMode, requiresWake, eventTurn,
			)
		}
		if _, err := testPool.Exec(ctx, `
			UPDATE agent_inbox_event
			SET status = 'draining', started_at = now(), updated_at = now()
			WHERE id = $1`, eventID); err != nil {
			t.Fatalf("activate turn %d inbox event: %v", turn, err)
		}
		reply := send(eventID, currentRecipient, currentTarget, turn+1)
		if reply.Code != http.StatusCreated {
			t.Fatalf("turn %d send: status=%d body=%s", turn+1, reply.Code, reply.Body.String())
		}
		if turn == 1 {
			var threadReply AgentTransportSendResponse
			if err := json.Unmarshal(reply.Body.Bytes(), &threadReply); err != nil {
				t.Fatalf("decode active A2A thread send: %v", err)
			}
			if threadReply.Message.ThreadRootMessageID == nil ||
				*threadReply.Message.ThreadRootMessageID != firstBody.Message.ID {
				t.Fatalf(
					"active A2A thread root=%v, want %s",
					threadReply.Message.ThreadRootMessageID, firstBody.Message.ID,
				)
			}
		}
		if currentRecipient == secondAgentID {
			currentRecipient = firstAgentID
			currentTarget = "dm:@" + secondHandle
		} else {
			currentRecipient = secondAgentID
			currentTarget = "dm:@" + firstHandle
		}
	}

	// 1 initial send + replyTurns replies = total turn count.
	wantTurns := replyTurns + 1
	var turnCount int
	var state string
	if err := testPool.QueryRow(ctx, `
		SELECT turn_count, state
		FROM agent_dm_exchange
		WHERE id = $1`, exchangeID).Scan(&turnCount, &state); err != nil {
		t.Fatalf("load final A2A exchange: %v", err)
	}
	if turnCount != wantTurns || state != "active" {
		t.Fatalf("final exchange turn_count=%d state=%q, want %d active (no auto-pause)", turnCount, state, wantTurns)
	}
	var agentMessageCount, wakeCount, systemMessageCount, ownerInboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent'`, channelID).Scan(&agentMessageCount); err != nil {
		t.Fatalf("count A2A messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE agent_dm_exchange_id = $1`, exchangeID).Scan(&wakeCount); err != nil {
		t.Fatalf("count A2A wake events: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'system'`, channelID).Scan(&systemMessageCount); err != nil {
		t.Fatalf("count A2A system messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM inbox_item
		WHERE workspace_id = $1
		  AND recipient_id = $2
		  AND type = 'agent_dm_paused'
		  AND details->>'exchange_id' = $3`,
		testWorkspaceID, testUserID, exchangeID).Scan(&ownerInboxCount); err != nil {
		t.Fatalf("count owner A2A inbox items: %v", err)
	}
	// No auto-pause ever fires: no system pause message, no owner pause
	// inbox item — every turn (including the last) produced a real agent
	// message and woke the next recipient, since nothing ever pauses.
	if agentMessageCount != wantTurns || wakeCount != wantTurns || systemMessageCount != 0 || ownerInboxCount != 0 {
		t.Fatalf(
			"messages=%d wakes=%d system=%d owner_inbox=%d, want %d/%d/0/0",
			agentMessageCount, wakeCount, systemMessageCount, ownerInboxCount, wantTurns, wantTurns,
		)
	}
	// A send well past the old 3-round/6-turn budget boundary still succeeds
	// (from whichever agent the strict must-reply alternation now expects —
	// that check is untouched by #813/#830 and still applies).
	var finalEventID string
	if err := testPool.QueryRow(ctx, `
		SELECT id
		FROM agent_inbox_event
		WHERE agent_dm_exchange_id = $1
		  AND agent_dm_turn = $2
		  AND agent_id = $3`,
		exchangeID, wantTurns, currentRecipient).Scan(&finalEventID); err != nil {
		t.Fatalf("load final-turn recipient inbox event: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'draining', started_at = now(), updated_at = now()
		WHERE id = $1`, finalEventID); err != nil {
		t.Fatalf("activate final-turn inbox event: %v", err)
	}
	pastOldBudget := send(
		finalEventID,
		currentRecipient,
		currentTarget,
		wantTurns+1,
	)
	if pastOldBudget.Code != http.StatusCreated {
		t.Fatalf("send past old budget boundary should succeed: status=%d body=%s", pastOldBudget.Code, pastOldBudget.Body.String())
	}
}

// TestAgentDMConcurrentDuplicateTurnStaysActiveNotPausedAtOldBudget replaces
// "...ConcurrentFinalTurnCannotOverrunBudget". The budget gate is gone
// (#813/#830), but the must-reply turn-alternation lock this test exercises
// (12 concurrent sends racing to claim the SAME turn as the SAME sender) is
// untouched — exactly one still wins that race, the rest still lose via
// agentDMTurnError (not agentDMPausedError, which no longer exists on this
// path). What changed is the exchange's resulting state: it stays "active"
// at turn_count=6 instead of auto-pausing to "paused_budget".
func TestAgentDMConcurrentDuplicateTurnStaysActiveNotPausedAtOldBudget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstAgentID := createHandlerTestAgent(t, "Concurrent A "+uuid.NewString(), []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Concurrent B "+uuid.NewString(), []byte("[]"))
	channel := createAgentAgentDMChannelForTest(t, firstAgentID, secondAgentID)
	lowID, highID, ok := normalizedAgentDMPair(parseUUID(firstAgentID), parseUUID(secondAgentID))
	if !ok {
		t.Fatal("normalize concurrent A2A pair failed")
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO agent_dm_pair_control (
		  workspace_id, agent_low_id, agent_high_id, window_message_count
		)
		VALUES ($1, $2, $3, 5)`,
		testWorkspaceID, lowID, highID); err != nil {
		t.Fatalf("seed concurrent pair control: %v", err)
	}
	var exchangeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_dm_exchange (
		  workspace_id, channel_id, agent_low_id, agent_high_id,
		  next_sender_agent_id, matter_id, turn_count
		)
		VALUES ($1, $2, $3, $4, $5, gen_random_uuid(), 5)
		RETURNING id`,
		testWorkspaceID, channel.ID, lowID, highID, secondAgentID).Scan(&exchangeID); err != nil {
		t.Fatalf("seed concurrent exchange: %v", err)
	}

	source := agentTransportSource{
		task: db.AgentInboxEvent{
			ID:                parseUUID(uuid.NewString()),
			AgentDmExchangeID: parseUUID(exchangeID),
		},
		origin: chatOutputOrigin{
			workspaceID: parseUUID(testWorkspaceID),
			agentID:     parseUUID(secondAgentID),
		},
	}
	target := agentTransportTarget{
		kind:          chatOutputTargetDM,
		channel:       channel,
		recipientType: "agent",
		recipientID:   parseUUID(firstAgentID),
	}

	const contenders = 12
	start := make(chan struct{})
	results := make(chan error, contenders)
	var wg sync.WaitGroup
	for i := 0; i < contenders; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			tx, err := testHandler.TxStarter.Begin(ctx)
			if err != nil {
				results <- err
				return
			}
			defer tx.Rollback(ctx)
			_, err = testHandler.reserveAgentDMSendTx(ctx, tx, source, target)
			if err == nil {
				err = tx.Commit(ctx)
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
			continue
		}
		// Losers now fail turn-alternation (this test's real subject — all
		// 12 contenders share one sender/recipient pair for the same turn),
		// not the removed budget-pause path.
		var turnErr *agentDMTurnError
		if !errors.As(err, &turnErr) {
			t.Fatalf("unexpected concurrent reservation error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent duplicate-turn successes=%d, want exactly 1", successes)
	}
	var turnCount int
	var state string
	if err := testPool.QueryRow(ctx, `
		SELECT turn_count, state
		FROM agent_dm_exchange
		WHERE id = $1`, exchangeID).Scan(&turnCount, &state); err != nil {
		t.Fatalf("load concurrent final exchange: %v", err)
	}
	if turnCount != 6 || state != "active" {
		t.Fatalf("concurrent final exchange turn_count=%d state=%q, want 6 active (no auto-pause)", turnCount, state)
	}
}

// TestAgentDMFrequencyAndBudgetNeverPauseAcrossMatters replaces
// "...FrequencyGateSpansMatters": #813/#830 tore out both the round-budget
// and frequency auto-pause gates. Running two separate matters back to
// back in the same agent pair — 12 total messages, past both the old
// 6-turn budget and the old 12-message/5min frequency limit — proves
// neither exchange ever auto-pauses. Turn/window counters are still
// tracked (kept for display), just never enforced.
func TestAgentDMFrequencyAndBudgetNeverPauseAcrossMatters(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstAgentID := createHandlerTestAgent(t, "Frequency A "+uuid.NewString(), []byte("[]"))
	secondAgentID := createHandlerTestAgent(t, "Frequency B "+uuid.NewString(), []byte("[]"))
	channel := createAgentAgentDMChannelForTest(t, firstAgentID, secondAgentID)

	runMatter := func(matterID string) string {
		t.Helper()
		exchangeID := ""
		for turn := 1; turn <= 6; turn++ {
			senderID, recipientID := firstAgentID, secondAgentID
			if turn%2 == 0 {
				senderID, recipientID = secondAgentID, firstAgentID
			}
			task := db.AgentInboxEvent{ID: parseUUID(matterID)}
			if exchangeID != "" {
				task.AgentDmExchangeID = parseUUID(exchangeID)
			}
			source := agentTransportSource{
				task: task,
				origin: chatOutputOrigin{
					workspaceID: parseUUID(testWorkspaceID),
					agentID:     parseUUID(senderID),
				},
			}
			target := agentTransportTarget{
				kind:          chatOutputTargetDM,
				channel:       channel,
				recipientType: "agent",
				recipientID:   parseUUID(recipientID),
			}
			tx, err := testHandler.TxStarter.Begin(ctx)
			if err != nil {
				t.Fatalf("begin frequency turn %d: %v", turn, err)
			}
			reservation, err := testHandler.reserveAgentDMSendTx(ctx, tx, source, target)
			if err != nil {
				tx.Rollback(ctx)
				t.Fatalf("reserve frequency turn %d: %v", turn, err)
			}
			if exchangeID == "" {
				exchangeID = uuidToString(reservation.ExchangeID)
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatalf("commit frequency turn %d: %v", turn, err)
			}
		}
		return exchangeID
	}

	firstExchangeID := runMatter(uuid.NewString())
	var firstState string
	if err := testPool.QueryRow(ctx, `
		SELECT state
		FROM agent_dm_exchange
		WHERE id = $1`, firstExchangeID).Scan(&firstState); err != nil {
		t.Fatalf("load first frequency exchange: %v", err)
	}
	if firstState != "active" {
		t.Fatalf("first exchange state=%q, want active (no auto-pause past old 6-turn budget)", firstState)
	}

	secondExchangeID := runMatter(uuid.NewString())
	var secondState, pairState string
	var windowCount int
	if err := testPool.QueryRow(ctx, `
		SELECT exchange.state, pair.state, pair.window_message_count
		FROM agent_dm_exchange exchange
		JOIN agent_dm_pair_control pair
		  ON pair.workspace_id = exchange.workspace_id
		 AND pair.agent_low_id = exchange.agent_low_id
		 AND pair.agent_high_id = exchange.agent_high_id
		WHERE exchange.id = $1`, secondExchangeID).Scan(
		&secondState, &pairState, &windowCount,
	); err != nil {
		t.Fatalf("load second frequency exchange: %v", err)
	}
	// window_message_count still accumulates (kept for display/telemetry —
	// see the "account agent dm pair frequency" update), it just never
	// triggers a pause anymore.
	if secondState != "active" || pairState != "active" || windowCount != 12 {
		t.Fatalf(
			"frequency states exchange=%q pair=%q count=%d, want active/active/12 (no auto-pause)",
			secondState, pairState, windowCount,
		)
	}
}

func createAgentAgentDMChannelForTest(t *testing.T, firstAgentID, secondAgentID string) ChannelResponse {
	t.Helper()
	canonical := dmCanonicalName("agent", firstAgentID, "agent", secondAgentID)
	channel, created := testHandler.createDMChannel(
		context.Background(),
		nil,
		testWorkspaceID,
		testUserID,
		canonical,
		[]dmMember{
			{memberType: "agent", memberID: parseUUID(firstAgentID)},
			{memberType: "agent", memberID: parseUUID(secondAgentID)},
		},
	)
	if !created {
		t.Fatal("create A2A DM channel failed")
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM channel WHERE id = $1`, channel.ID)
	})
	return channel
}

func TestAgentTransportSendFreshnessHoldSavesDraftAndDoesNotWriteMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before compose "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	newerText := "newer during compose " + uuid.NewString()
	newer, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", newerText, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed newer message: %v", err)
	}

	draftContent := "held draft " + uuid.NewString()
	clientID := "transport-held-" + uuid.NewString()
	heldRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           draftContent,
		"client_message_id": clientID,
		"seen_up_to_seq":    seen.Seq,
	})
	if heldRec.Code != http.StatusOK {
		t.Fatalf("freshness hold send: status=%d body=%s", heldRec.Code, heldRec.Body.String())
	}
	var held AgentTransportSendHeldResponse
	if err := json.Unmarshal(heldRec.Body.Bytes(), &held); err != nil {
		t.Fatalf("decode held response: %v", err)
	}
	if held.State != "held" || held.Outcome != "held" || held.Subtype != "freshness" || held.Reason != "newer_messages_available" {
		t.Fatalf("held envelope mismatch: %+v", held)
	}
	if held.SeenUpToSeq != seen.Seq || held.LatestSeq != newer.Seq || held.NewMessageCount != 1 || len(held.HeldMessages) != 1 || held.HeldMessages[0].ID != newer.ID {
		t.Fatalf("held context mismatch: seen=%d newer=%s body=%+v", seen.Seq, newer.ID, held)
	}
	if held.ContextWindow.OlderBoundary != "No older." || held.ContextWindow.NewerBoundary != "No newer." ||
		held.ContextWindow.OldestSeq != newer.Seq || held.ContextWindow.NewestSeq != newer.Seq {
		t.Fatalf("held bounded window mismatch: %+v", held.ContextWindow)
	}
	if got := strings.Join(held.AvailableActions, ","); got != "review_newer_messages,agent_decide,discard_draft" {
		t.Fatalf("held available actions=%q", got)
	}
	assertNoChannelMessageContent(t, channelID, draftContent)
	assertAgentTransportDraftContent(t, agentID, target, draftContent)
	assertAgentTransportFreshnessHoldActivity(t, taskID, target, 1)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 0)

	revised := "revised held draft " + uuid.NewString()
	replaced := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           revised,
		"client_message_id": agentTransportFreshnessRevisedClientMessageID(held.ProducerFactID),
		"seen_up_to_seq":    held.LatestSeq,
	})
	if replaced.Code != http.StatusCreated {
		t.Fatalf("send revised held draft: status=%d body=%s", replaced.Code, replaced.Body.String())
	}
	var replacedBody AgentTransportSendResponse
	if err := json.Unmarshal(replaced.Body.Bytes(), &replacedBody); err != nil {
		t.Fatalf("decode revised send response: %v", err)
	}
	if replacedBody.Message.Content != revised || replacedBody.FreshnessResolution == nil ||
		replacedBody.FreshnessResolution.Outcome != "revised_send" ||
		replacedBody.FreshnessResolution.ProducerFactID != held.ProducerFactID {
		t.Fatalf("revised freshness resolution mismatch: %+v", replacedBody)
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportFreshnessHoldActivity(t, taskID, target, 1)
	assertAgentTransportFreshnessResolutionActivity(t, taskID, target, held.ProducerFactID, "revised_send")
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
}

func TestAgentTransportRevisedSendRequiresFullReadyCommandProof(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester",
		"seen before proof "+uuid.NewString(), "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester",
		"newer before proof "+uuid.NewString(), "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}

	draftContent := "proof-held draft " + uuid.NewString()
	heldRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           draftContent,
		"client_message_id": "proof-held-" + uuid.NewString(),
		"seen_up_to_seq":    seen.Seq,
	})
	if heldRec.Code != http.StatusOK {
		t.Fatalf("freshness hold: status=%d body=%s", heldRec.Code, heldRec.Body.String())
	}
	var held AgentTransportSendHeldResponse
	if err := json.Unmarshal(heldRec.Body.Bytes(), &held); err != nil {
		t.Fatalf("decode held response: %v", err)
	}

	wrongContent := "wrong reconstructed revision " + uuid.NewString()
	wrongRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           wrongContent,
		"client_message_id": "wrong-proof-" + uuid.NewString(),
		"seen_up_to_seq":    held.LatestSeq,
	})
	if wrongRec.Code != http.StatusConflict ||
		!strings.Contains(wrongRec.Body.String(), errAgentTransportFreshnessDecisionProof.Error()) {
		t.Fatalf("boundary-only wrong-key send: status=%d body=%s, want proof conflict", wrongRec.Code, wrongRec.Body.String())
	}
	assertNoChannelMessageContent(t, channelID, wrongContent)
	assertAgentTransportDraftContent(t, agentID, target, draftContent)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 0)

	revisedContent := "ready-command revision " + uuid.NewString()
	revisedRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           revisedContent,
		"client_message_id": agentTransportFreshnessRevisedClientMessageID(held.ProducerFactID),
		"seen_up_to_seq":    held.LatestSeq,
	})
	if revisedRec.Code != http.StatusCreated {
		t.Fatalf("ready-command revision: status=%d body=%s", revisedRec.Code, revisedRec.Body.String())
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
	assertAgentTransportFreshnessResolutionActivity(t, taskID, target, held.ProducerFactID, "revised_send")

	retryWrongContent := "post-resolution wrong-key retry " + uuid.NewString()
	retryWrongRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           retryWrongContent,
		"client_message_id": "retry-wrong-proof-" + uuid.NewString(),
		"seen_up_to_seq":    held.LatestSeq,
	})
	if retryWrongRec.Code != http.StatusConflict ||
		!strings.Contains(retryWrongRec.Body.String(), errAgentTransportFreshnessDecisionProof.Error()) {
		t.Fatalf("post-resolution wrong-key retry: status=%d body=%s, want proof conflict", retryWrongRec.Code, retryWrongRec.Body.String())
	}
	assertNoChannelMessageContent(t, channelID, retryWrongContent)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
	assertAgentTransportFreshnessResolutionActivity(t, taskID, target, held.ProducerFactID, "revised_send")
}

func TestAgentTransportFirstFreshnessHoldCannotCrossSourceCompletionFence(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester",
		"seen before source fence "+uuid.NewString(), "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID),
		"user", parseUUID(testUserID), "Tester",
		"newer before source fence "+uuid.NewString(), "multica", nil,
		pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}
	event, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load source event: %v", err)
	}

	completionTx, err := testHandler.TxStarter.Begin(ctx)
	if err != nil {
		t.Fatalf("begin completion fence transaction: %v", err)
	}
	defer completionTx.Rollback(ctx)
	publications, err := testHandler.abandonAgentTransportFreshnessDraftsWithExec(
		ctx, completionTx, event, event.RuntimeID,
	)
	if err != nil {
		t.Fatalf("enumerate empty completion drafts: %v", err)
	}
	if len(publications) != 0 {
		t.Fatalf("empty completion publications=%d, want 0", len(publications))
	}

	draftContent := "must not hold after completion scan " + uuid.NewString()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/send", taskID, agentID, map[string]any{
		"target":            target,
		"content":           draftContent,
		"client_message_id": "source-fence-" + uuid.NewString(),
		"seen_up_to_seq":    seen.Seq,
	})
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		testHandler.AgentTransportSendMessage(rec, req)
		close(done)
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		select {
		case <-done:
			t.Fatalf("freshness send completed before completion released the source fence: status=%d body=%s", rec.Code, rec.Body.String())
		default:
		}
		var waiting bool
		if err := testPool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM pg_stat_activity
				WHERE datname = current_database()
				  AND wait_event_type = 'Lock'
				  AND lower(COALESCE(wait_event, '')) = 'advisory'
				  AND query LIKE '%agent_transport_source%'
			)`).Scan(&waiting); err != nil {
			t.Fatalf("inspect source-fence waiter: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for freshness send on source-wide advisory lock")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := completionTx.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'no_reply', updated_at = now()
		WHERE id = $1`, parseUUID(taskID)); err != nil {
		t.Fatalf("terminalize source under completion fence: %v", err)
	}
	if err := completionTx.Commit(ctx); err != nil {
		t.Fatalf("commit source completion: %v", err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("freshness send did not finish after completion committed")
	}
	if rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), errAgentTransportSourceNotActive.Error()) {
		t.Fatalf("post-completion first hold: status=%d body=%s, want inactive-source conflict", rec.Code, rec.Body.String())
	}
	assertNoChannelMessageContent(t, channelID, draftContent)
	assertAgentTransportDraftMissing(t, agentID, target)
	var holdAudits int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_task_transport_audit
		WHERE task_id = $1
		  AND action = 'message_send'
		  AND context_pack->>'held' = 'true'`,
		parseUUID(taskID)).Scan(&holdAudits); err != nil {
		t.Fatalf("count post-completion hold audits: %v", err)
	}
	if holdAudits != 0 {
		t.Fatalf("post-completion hold audits=%d, want 0", holdAudits)
	}
}

func TestAgentTransportHeldResponseMakesBoundedWindowEdgesExplicit(t *testing.T) {
	rec := httptest.NewRecorder()
	writeAgentTransportHeldResponse(rec, agentTransportTarget{raw: "#multica"}, agentTransportFreshnessDecision{
		Hold:        true,
		SeenUpToSeq: 4,
		LatestSeq:   9,
		TotalNewer:  8,
		Messages: []ChannelMessageResponse{
			{Seq: 5},
			{Seq: 6},
			{Seq: 7},
			{Seq: 8},
			{Seq: 9},
		},
		Omitted:    3,
		ProducerID: "freshness_decision_fact:bounded",
	}, "transport-bounded")
	if rec.Code != http.StatusOK {
		t.Fatalf("held response status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body AgentTransportSendHeldResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode held response: %v", err)
	}
	if body.ContextWindow.OldestSeq != 5 || body.ContextWindow.NewestSeq != 9 ||
		body.ContextWindow.OlderBoundary != "3 older messages omitted." ||
		body.ContextWindow.NewerBoundary != "No newer." {
		t.Fatalf("bounded context edges=%+v", body.ContextWindow)
	}
	if got := strings.Join(body.AvailableActions, ","); got != "review_newer_messages,agent_decide,discard_draft" {
		t.Fatalf("bounded available actions=%q", got)
	}
}

func TestAgentTransportRevisedSendRefreshesOnlyForContextAfterHeldBoundary(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before refreshed decision "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "first newer during compose "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed first newer message: %v", err)
	}
	firstHold := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "first held draft " + uuid.NewString(),
		"client_message_id": "transport-held-first-" + uuid.NewString(),
		"seen_up_to_seq":    seen.Seq,
	})
	if firstHold.Code != http.StatusOK {
		t.Fatalf("first freshness hold: status=%d body=%s", firstHold.Code, firstHold.Body.String())
	}
	var firstDecision AgentTransportSendHeldResponse
	if err := json.Unmarshal(firstHold.Body.Bytes(), &firstDecision); err != nil {
		t.Fatalf("decode first freshness hold: %v", err)
	}

	latest, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "newer after held decision "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed newer-after-hold message: %v", err)
	}
	secondDecisionContent := "replacement after new context " + uuid.NewString()
	secondDecision := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           secondDecisionContent,
		"client_message_id": agentTransportFreshnessRevisedClientMessageID(firstDecision.ProducerFactID),
		"seen_up_to_seq":    firstDecision.LatestSeq,
	})
	if secondDecision.Code != http.StatusOK {
		t.Fatalf("replacement with newer context: status=%d body=%s", secondDecision.Code, secondDecision.Body.String())
	}
	var secondDecisionBody AgentTransportSendHeldResponse
	if err := json.Unmarshal(secondDecision.Body.Bytes(), &secondDecisionBody); err != nil {
		t.Fatalf("decode replacement with newer context: %v", err)
	}
	if secondDecisionBody.ProducerFactID == firstDecision.ProducerFactID ||
		secondDecisionBody.SeenUpToSeq != firstDecision.LatestSeq ||
		secondDecisionBody.LatestSeq != latest.Seq ||
		secondDecisionBody.NewMessageCount != 1 {
		t.Fatalf("newer context must create a distinct decision: %+v", secondDecisionBody)
	}
	assertAgentTransportDraftContent(t, agentID, target, secondDecisionContent)

	final := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           secondDecisionContent,
		"client_message_id": agentTransportFreshnessRevisedClientMessageID(secondDecisionBody.ProducerFactID),
		"seen_up_to_seq":    secondDecisionBody.LatestSeq,
	})
	if final.Code != http.StatusCreated {
		t.Fatalf("resolve refreshed hold: status=%d body=%s", final.Code, final.Body.String())
	}
	var finalBody AgentTransportSendResponse
	if err := json.Unmarshal(final.Body.Bytes(), &finalBody); err != nil {
		t.Fatalf("decode refreshed resolution: %v", err)
	}
	if finalBody.FreshnessResolution == nil ||
		finalBody.FreshnessResolution.Outcome != "revised_send" ||
		finalBody.FreshnessResolution.ProducerFactID != secondDecisionBody.ProducerFactID {
		t.Fatalf("refreshed resolution mismatch: %+v", finalBody)
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportFreshnessResolutionActivity(t, taskID, target, secondDecisionBody.ProducerFactID, "revised_send")
}

func TestAgentTransportConcurrentRevisedDecisionCreatesOneMessageAndResolution(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before concurrent revised decision "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "newer before concurrent revised decision "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}
	heldRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "initial concurrent draft " + uuid.NewString(),
		"client_message_id": "transport-concurrent-initial-" + uuid.NewString(),
		"seen_up_to_seq":    seen.Seq,
	})
	if heldRec.Code != http.StatusOK {
		t.Fatalf("hold concurrent revised decision: status=%d body=%s", heldRec.Code, heldRec.Body.String())
	}
	var held AgentTransportSendHeldResponse
	if err := json.Unmarshal(heldRec.Body.Bytes(), &held); err != nil {
		t.Fatalf("decode concurrent revised hold: %v", err)
	}

	revised := "concurrent revised winner " + uuid.NewString()
	clientID := agentTransportFreshnessRevisedClientMessageID(held.ProducerFactID)
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			<-start
			results <- agentTransportSendForTest(t, taskID, agentID, map[string]any{
				"target":            target,
				"content":           revised,
				"client_message_id": clientID,
				"seen_up_to_seq":    held.LatestSeq,
			})
		}()
	}
	close(start)

	created := 0
	messageIDs := map[string]struct{}{}
	for range 2 {
		rec := <-results
		if rec.Code != http.StatusCreated {
			t.Fatalf("concurrent revised send: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var body AgentTransportSendResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode concurrent revised send: %v", err)
		}
		if body.Created {
			created++
		}
		messageIDs[body.Message.ID] = struct{}{}
	}
	if created != 1 || len(messageIDs) != 1 {
		t.Fatalf("concurrent revised results created=%d message_ids=%+v, want one canonical message", created, messageIDs)
	}
	var messageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE workspace_id = $1
		  AND channel_id = $2
		  AND author_id = $3
		  AND client_message_id = $4`,
		testWorkspaceID, channelID, agentID, clientID).Scan(&messageCount); err != nil {
		t.Fatalf("count concurrent revised messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("concurrent revised canonical message count=%d, want 1", messageCount)
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportFreshnessResolutionActivity(t, taskID, target, held.ProducerFactID, "revised_send")
}

func TestAgentTransportFreshnessDraftsAreScopedToTheirTask(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	firstTaskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, firstTaskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before source scoped hold "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "newer source scoped hold "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}

	var runtimeID, chatSessionID string
	if err := testPool.QueryRow(ctx, `SELECT runtime_id, chat_session_id FROM agent_inbox_event WHERE id = $1`, firstTaskID).Scan(&runtimeID, &chatSessionID); err != nil {
		t.Fatalf("load first task source: %v", err)
	}
	var secondTaskID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, chat_session_id, status, priority, started_at)
		VALUES ($1, $2, $3, 'draining', 2, now())
		RETURNING id`, agentID, runtimeID, chatSessionID).Scan(&secondTaskID); err != nil {
		t.Fatalf("create second task: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, secondTaskID)
	})

	producerFacts := make([]string, 0, 2)
	for _, tc := range []struct {
		taskID  string
		content string
	}{
		{firstTaskID, "first source scoped draft " + uuid.NewString()},
		{secondTaskID, "second source scoped draft " + uuid.NewString()},
	} {
		rec := agentTransportSendForTest(t, tc.taskID, agentID, map[string]any{
			"target": target, "content": tc.content, "client_message_id": "transport-source-" + uuid.NewString(), "seen_up_to_seq": seen.Seq,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("source-scoped freshness hold: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var held AgentTransportSendHeldResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &held); err != nil {
			t.Fatalf("decode source-scoped freshness hold: %v", err)
		}
		producerFacts = append(producerFacts, held.ProducerFactID)
	}
	if producerFacts[0] == "" || producerFacts[0] == producerFacts[1] {
		t.Fatalf("task sources must have distinct decision facts: %q", producerFacts)
	}

	var draftCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_transport_draft
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3
		  AND task_id IN ($4, $5)`, testWorkspaceID, agentID, target, firstTaskID, secondTaskID).Scan(&draftCount); err != nil {
		t.Fatalf("count task-scoped drafts: %v", err)
	}
	if draftCount != 2 {
		t.Fatalf("task-scoped drafts=%d, want 2", draftCount)
	}
}

func TestAgentTransportFreshnessDraftSQLBranchesAreScopedToTaskOrInbox(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load transport task: %v", err)
	}
	origin, ok := testHandler.chatOutputOriginForTask(ctx, task)
	if !ok {
		t.Fatal("resolve transport task origin")
	}
	targetName := "#" + channelNameForTransportTest(t, channelID)
	target, err := testHandler.resolveAgentTransportTarget(ctx, task, origin, targetName, true)
	if err != nil {
		t.Fatalf("resolve transport target: %v", err)
	}
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "task inbox branch seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed task inbox branch seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "task inbox branch newer "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed task inbox branch newer message: %v", err)
	}

	var inboxEventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
			workspace_id, channel_id, agent_id, reason, status, priority,
			seq_from, seq_to, delivery_mode, response_mode
		)
		VALUES ($1, $2, $3, 'mention', 'draining', 100, $4, $4, 'execute', 'public_response')
		RETURNING id`, testWorkspaceID, channelID, uuidToString(task.AgentID), seen.Seq).Scan(&inboxEventID); err != nil {
		t.Fatalf("insert transport inbox source: %v", err)
	}
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, inboxEventID)
	})

	taskSource := agentTransportSource{task: task, origin: origin}
	inboxSource := agentTransportSource{task: task, origin: origin, inboxEventID: parseUUID(inboxEventID)}
	for _, tc := range []struct {
		name      string
		source    agentTransportSource
		content   string
		clientID  string
		wantTask  bool
		wantInbox bool
	}{
		{name: "task", source: taskSource, content: "task branch draft " + uuid.NewString(), clientID: "task-branch-" + uuid.NewString(), wantTask: true},
		{name: "inbox", source: inboxSource, content: "inbox branch draft " + uuid.NewString(), clientID: "inbox-branch-" + uuid.NewString(), wantInbox: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := testHandler.agentTransportFreshnessDecisionWithSeen(ctx, testHandler.DB, tc.source, target, seen.Seq)
			if err != nil || !decision.Hold {
				t.Fatalf("freshness decision = %+v, err=%v", decision, err)
			}
			chosen, _, err := testHandler.holdAgentTransportSend(ctx, tc.source, target, tc.content, []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: tc.content}}, tc.clientID, decision)
			if err != nil {
				t.Fatalf("hold %s source draft: %v", tc.name, err)
			}
			draft, found, err := testHandler.loadAgentTransportDraft(ctx, tc.source, target.raw)
			if err != nil || !found {
				t.Fatalf("load %s source draft: found=%v err=%v", tc.name, found, err)
			}
			if draft.Content != tc.content || draft.DecisionFactID != chosen.ProducerID || draft.DecisionFactID == "" {
				t.Fatalf("%s source draft = %+v, decision=%+v", tc.name, draft, chosen)
			}
			var taskRef, inboxRef pgtype.UUID
			if err := testPool.QueryRow(ctx, `
				SELECT task_id, inbox_event_id
				FROM agent_transport_draft
				WHERE id = $1`, draft.ID).Scan(&taskRef, &inboxRef); err != nil {
				t.Fatalf("load %s draft source refs: %v", tc.name, err)
			}
			if taskRef.Valid != tc.wantTask || inboxRef.Valid != tc.wantInbox {
				t.Fatalf("%s draft refs task=%s inbox=%s", tc.name, uuidToString(taskRef), uuidToString(inboxRef))
			}
		})
	}

	var draftCount, taskAuditCount, inboxAuditCount int
	if err := testPool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM agent_transport_draft WHERE workspace_id = $1 AND agent_id = $2 AND target = $3),
			(SELECT count(*) FROM agent_task_transport_audit WHERE workspace_id = $1 AND agent_id = $2 AND target = $3 AND task_id = $4),
			(SELECT count(*) FROM agent_task_transport_audit WHERE workspace_id = $1 AND agent_id = $2 AND target = $3 AND inbox_event_id = $5)`,
		testWorkspaceID, uuidToString(task.AgentID), target.raw, taskID, inboxEventID).Scan(&draftCount, &taskAuditCount, &inboxAuditCount); err != nil {
		t.Fatalf("count task/inbox SQL branch rows: %v", err)
	}
	if draftCount != 2 || taskAuditCount != 1 || inboxAuditCount != 1 {
		t.Fatalf("task/inbox SQL branch rows drafts=%d taskAudits=%d inboxAudits=%d, want 2/1/1", draftCount, taskAuditCount, inboxAuditCount)
	}

	if err := testHandler.deleteAgentTransportDraftWithExec(ctx, testHandler.DB, inboxSource, target.raw); err != nil {
		t.Fatalf("delete inbox source draft: %v", err)
	}
	if _, found, err := testHandler.loadAgentTransportDraft(ctx, inboxSource, target.raw); err != nil || found {
		t.Fatalf("inbox source draft after delete: found=%v err=%v", found, err)
	}
	if draft, found, err := testHandler.loadAgentTransportDraft(ctx, taskSource, target.raw); err != nil || !found || draft.Content == "" {
		t.Fatalf("task sibling after inbox delete: draft=%+v found=%v err=%v", draft, found, err)
	}
	if err := testHandler.deleteAgentTransportDraftWithExec(ctx, testHandler.DB, taskSource, target.raw); err != nil {
		t.Fatalf("delete task source draft: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_transport_draft
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3`,
		testWorkspaceID, uuidToString(task.AgentID), target.raw).Scan(&draftCount); err != nil {
		t.Fatalf("count task/inbox drafts after scoped deletes: %v", err)
	}
	if draftCount != 0 {
		t.Fatalf("task/inbox drafts after scoped deletes=%d, want 0", draftCount)
	}
}

func TestAgentTransportFreshnessHoldSameRangeEmitsOneActivityUnderConcurrency(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "concurrent hold seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "concurrent hold newer "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}

	results := make(chan *httptest.ResponseRecorder, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results <- agentTransportSendForTest(t, taskID, agentID, map[string]any{
				"target": target, "content": fmt.Sprintf("concurrent held %d", i), "client_message_id": "concurrent-held-" + uuid.NewString(), "seen_up_to_seq": seen.Seq,
			})
		}(i)
	}
	wg.Wait()
	close(results)
	for rec := range results {
		if rec.Code != http.StatusOK {
			t.Fatalf("concurrent freshness hold: status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	assertAgentTransportFreshnessHoldActivity(t, taskID, target, 1)
}

func TestAgentTransportFreshnessHoldLoserReturnsPersistedWinnerDecision(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load transport task: %v", err)
	}
	origin, ok := testHandler.chatOutputOriginForTask(ctx, task)
	if !ok {
		t.Fatal("resolve transport task origin")
	}
	target, err := testHandler.resolveAgentTransportTarget(ctx, task, origin, "#"+channelNameForTransportTest(t, channelID), true)
	if err != nil {
		t.Fatalf("resolve transport target: %v", err)
	}
	firstSeen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "loser first seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed loser first seen: %v", err)
	}
	winnerSeen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "winner seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed winner seen: %v", err)
	}
	latest, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "winner newer "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed winner newer: %v", err)
	}
	source := agentTransportSource{task: task, origin: origin}
	winnerDecision, err := testHandler.agentTransportFreshnessDecisionWithSeen(ctx, testHandler.DB, source, target, winnerSeen.Seq)
	if err != nil || !winnerDecision.Hold {
		t.Fatalf("winner freshness decision = %+v, err=%v", winnerDecision, err)
	}
	winner, _, err := testHandler.holdAgentTransportSend(ctx, source, target, "winner draft", []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "winner draft"}}, "winner-"+uuid.NewString(), winnerDecision)
	if err != nil {
		t.Fatalf("persist winner freshness decision: %v", err)
	}
	loserDecision, err := testHandler.agentTransportFreshnessDecisionWithSeen(ctx, testHandler.DB, source, target, firstSeen.Seq)
	if err != nil || !loserDecision.Hold {
		t.Fatalf("loser freshness decision = %+v, err=%v", loserDecision, err)
	}
	loser, transportID, err := testHandler.holdAgentTransportSend(ctx, source, target, "loser replacement", []protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "loser replacement"}}, "loser-"+uuid.NewString(), loserDecision)
	if err != nil {
		t.Fatalf("persist loser freshness retry: %v", err)
	}
	if transportID != "" {
		t.Fatalf("loser emitted transport audit %q, want none", transportID)
	}
	if loser.ProducerID != winner.ProducerID || loser.SeenUpToSeq != winnerSeen.Seq || loser.LatestSeq != latest.Seq || loser.TotalNewer != 1 {
		t.Fatalf("loser response = %+v, want persisted winner fact=%q seen=%d latest=%d total=1", loser, winner.ProducerID, winnerSeen.Seq, latest.Seq)
	}
	assertAgentTransportFreshnessHoldActivity(t, taskID, target.raw, 1)
}

func TestAgentTransportFreshnessDecisionFactIsScopedToTaskOrInboxSource(t *testing.T) {
	workspaceID := parseUUID(uuid.NewString())
	agentID := parseUUID(uuid.NewString())
	target := agentTransportTarget{raw: "#same-target"}
	base := agentTransportSource{
		task:   db.AgentInboxEvent{ID: parseUUID(uuid.NewString())},
		origin: chatOutputOrigin{workspaceID: workspaceID, agentID: agentID},
	}
	otherTask := base
	otherTask.task.ID = parseUUID(uuid.NewString())
	inbox := base
	inbox.inboxEventID = parseUUID(uuid.NewString())
	otherInbox := inbox
	otherInbox.inboxEventID = parseUUID(uuid.NewString())

	fact := agentTransportFreshnessProducerID(base, target, 4, 9)
	for label, got := range map[string]string{
		"other task":  agentTransportFreshnessProducerID(otherTask, target, 4, 9),
		"inbox":       agentTransportFreshnessProducerID(inbox, target, 4, 9),
		"other inbox": agentTransportFreshnessProducerID(otherInbox, target, 4, 9),
	} {
		if got == fact {
			t.Fatalf("%s source reused task decision fact %q", label, got)
		}
	}
}

func TestMigration201BackfillsExactDecisionFactOrFailsClosed(t *testing.T) {
	if testPool == nil {
		t.Skip("database not available")
	}
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current test file")
	}
	migration, err := os.ReadFile(filepath.Join(filepath.Dir(thisFile), "..", "..", "migrations", "201_agent_transport_draft_decision_scope.up.sql"))
	if err != nil {
		t.Fatalf("read migration 201: %v", err)
	}

	for _, tc := range []struct {
		name             string
		sourceKind       string
		audits           int
		fact             string
		auditSourceKinds []string
		auditFacts       []string
		auditLatestSeqs  []int64
		wantFact         string
		wantErr          bool
	}{
		{name: "valid exact task audit", sourceKind: "task", audits: 1, fact: "freshness_decision_fact:valid-task"},
		{name: "valid exact inbox audit", sourceKind: "inbox", audits: 1, fact: "freshness_decision_fact:valid-inbox"},
		{name: "repeated equivalent current-range audits", sourceKind: "inbox", audits: 2, fact: "freshness_decision_fact:equivalent"},
		{
			name:       "historical same-source audits use current-range fact",
			sourceKind: "inbox",
			audits:     3,
			auditFacts: []string{
				"freshness_decision_fact:older",
				"freshness_decision_fact:newer",
				"freshness_decision_fact:winner",
			},
			auditLatestSeqs: []int64{5, 7, 9},
			wantFact:        "freshness_decision_fact:winner",
		},
		{name: "missing exact audit", sourceKind: "task", fact: "freshness_decision_fact:missing", wantErr: true},
		{name: "missing fact", sourceKind: "task", audits: 1, fact: "", wantErr: true},
		{
			name:            "same source without current draft range",
			sourceKind:      "inbox",
			audits:          2,
			fact:            "freshness_decision_fact:historical-only",
			auditLatestSeqs: []int64{5, 8},
			wantErr:         true,
		},
		{
			name:       "same source current range has conflicting facts",
			sourceKind: "inbox",
			audits:     2,
			auditFacts: []string{
				"freshness_decision_fact:conflict-a",
				"freshness_decision_fact:conflict-b",
			},
			wantErr: true,
		},
		{
			name:             "distinct sources across exact audits",
			sourceKind:       "both",
			audits:           2,
			fact:             "freshness_decision_fact:ambiguous-source",
			auditSourceKinds: []string{"task", "inbox"},
			wantErr:          true,
		},
		{name: "single audit with both sources", sourceKind: "both", audits: 1, fact: "freshness_decision_fact:both", wantErr: true},
		{name: "single audit with neither source", sourceKind: "neither", audits: 1, fact: "freshness_decision_fact:neither", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			tx, err := testPool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin migration test transaction: %v", err)
			}
			defer tx.Rollback(ctx)
			if _, err := tx.Exec(ctx, `
				CREATE TEMP TABLE agent_task_queue (id UUID PRIMARY KEY) ON COMMIT DROP;
				CREATE TEMP TABLE agent_inbox_event (id UUID PRIMARY KEY) ON COMMIT DROP;
				CREATE TEMP TABLE agent_transport_draft (
					id UUID PRIMARY KEY, workspace_id UUID NOT NULL, agent_id UUID NOT NULL, target TEXT NOT NULL,
					channel_id UUID NOT NULL, thread_root_message_id UUID, content TEXT NOT NULL DEFAULT '', parts JSONB NOT NULL DEFAULT '[]'::jsonb,
					client_message_id TEXT NOT NULL, seen_up_to_seq BIGINT NOT NULL, held_from_seq BIGINT NOT NULL, held_to_seq BIGINT NOT NULL,
					shown_from_seq BIGINT, shown_to_seq BIGINT, created_at TIMESTAMPTZ NOT NULL DEFAULT now(), updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
					UNIQUE (workspace_id, agent_id, target)
				) ON COMMIT DROP;
				CREATE TEMP TABLE agent_task_transport_audit (
					id UUID PRIMARY KEY, workspace_id UUID NOT NULL, task_id UUID, inbox_event_id UUID, agent_id UUID NOT NULL,
					action TEXT NOT NULL, target TEXT NOT NULL, client_message_id TEXT, context_pack JSONB NOT NULL,
					created_at TIMESTAMPTZ NOT NULL DEFAULT now()
				) ON COMMIT DROP;`); err != nil {
				t.Fatalf("create legacy migration fixtures: %v", err)
			}
			workspaceID, agentID, sourceID, draftID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
			if tc.sourceKind == "inbox" || tc.sourceKind == "both" {
				if _, err := tx.Exec(ctx, `INSERT INTO agent_inbox_event (id) VALUES ($1)`, sourceID); err != nil {
					t.Fatalf("insert legacy inbox event: %v", err)
				}
			}
			if tc.sourceKind == "task" || tc.sourceKind == "both" {
				if _, err := tx.Exec(ctx, `INSERT INTO agent_task_queue (id) VALUES ($1)`, sourceID); err != nil {
					t.Fatalf("insert legacy task: %v", err)
				}
			}
			if _, err := tx.Exec(ctx, `INSERT INTO agent_transport_draft (id, workspace_id, agent_id, target, channel_id, client_message_id, seen_up_to_seq, held_from_seq, held_to_seq) VALUES ($1,$2,$3,'#held',$4,'exact-client',4,5,9)`, draftID, workspaceID, agentID, uuid.NewString()); err != nil {
				t.Fatalf("insert legacy draft: %v", err)
			}
			for i := 0; i < tc.audits; i++ {
				var taskID, inboxEventID any
				auditSourceKind := tc.sourceKind
				if i < len(tc.auditSourceKinds) {
					auditSourceKind = tc.auditSourceKinds[i]
				}
				switch auditSourceKind {
				case "inbox":
					inboxEventID = sourceID
				case "task":
					taskID = sourceID
				case "both":
					taskID = sourceID
					inboxEventID = sourceID
				}
				auditFact := tc.fact
				if i < len(tc.auditFacts) {
					auditFact = tc.auditFacts[i]
				}
				auditLatestSeq := int64(9)
				if i < len(tc.auditLatestSeqs) {
					auditLatestSeq = tc.auditLatestSeqs[i]
				}
				auditCreatedAt := time.Date(2026, time.July, 20, 0, 0, i, 0, time.UTC)
				if _, err := tx.Exec(ctx, `INSERT INTO agent_task_transport_audit (id, workspace_id, task_id, inbox_event_id, agent_id, action, target, client_message_id, context_pack, created_at) VALUES ($1,$2,$3,$4,$5,'message_send','#held','exact-client',jsonb_build_object('held', true, 'producer_fact_id', $6::text, 'seen_up_to_seq', 4, 'latest_seq', $7::bigint),$8)`, uuid.NewString(), workspaceID, taskID, inboxEventID, agentID, auditFact, auditLatestSeq, auditCreatedAt); err != nil {
					t.Fatalf("insert legacy audit: %v", err)
				}
			}
			_, err = tx.Exec(ctx, string(migration))
			if tc.wantErr {
				if err == nil {
					t.Fatal("migration succeeded, want fail-closed error")
				}
				return
			}
			if err != nil {
				t.Fatalf("apply migration: %v", err)
			}
			var gotTaskID, gotInboxEventID pgtype.UUID
			var gotFact string
			if err := tx.QueryRow(ctx, `SELECT task_id, inbox_event_id, decision_fact_id FROM agent_transport_draft WHERE id = $1`, draftID).Scan(&gotTaskID, &gotInboxEventID, &gotFact); err != nil {
				t.Fatalf("read migrated draft: %v", err)
			}
			gotSourceID := uuidToString(gotTaskID)
			if tc.sourceKind == "inbox" {
				gotSourceID = uuidToString(gotInboxEventID)
			}
			wantFact := tc.fact
			if tc.wantFact != "" {
				wantFact = tc.wantFact
			}
			if gotSourceID != sourceID || gotFact != wantFact || gotTaskID.Valid == gotInboxEventID.Valid {
				t.Fatalf("migrated draft source/fact = task:%s inbox:%s/%q, want %s:%s/%q", uuidToString(gotTaskID), uuidToString(gotInboxEventID), gotFact, tc.sourceKind, sourceID, wantFact)
			}
		})
	}
}

func TestAgentTransportSendDraftSendsSavedDraftAndClearsDraft(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "draft send seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "draft send newer "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer message: %v", err)
	}
	content := "saved draft content " + uuid.NewString()
	clientID := "transport-draft-" + uuid.NewString()
	held := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           content,
		"client_message_id": clientID,
		"seen_up_to_seq":    seen.Seq,
	})
	if held.Code != http.StatusOK {
		t.Fatalf("freshness hold send: status=%d body=%s", held.Code, held.Body.String())
	}
	var heldBody AgentTransportSendHeldResponse
	if err := json.Unmarshal(held.Body.Bytes(), &heldBody); err != nil {
		t.Fatalf("decode held draft: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_task_transport_audit
		SET created_at = now() - interval '2 seconds'
		WHERE task_id = $1
		  AND context_pack->>'producer_fact_id' = $2`,
		taskID, heldBody.ProducerFactID); err != nil {
		t.Fatalf("age canonical hold audit: %v", err)
	}
	sent := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":     target,
		"send_draft": true,
	})
	if sent.Code != http.StatusCreated {
		t.Fatalf("send saved draft with the real CLI request shape: status=%d body=%s", sent.Code, sent.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(sent.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode draft send: %v", err)
	}
	if body.Message.Content != content || body.Message.ClientMessageID == nil || *body.Message.ClientMessageID != clientID {
		t.Fatalf("draft send message mismatch: %+v", body.Message)
	}
	if body.FreshnessResolution == nil ||
		body.FreshnessResolution.Outcome != "send_draft" ||
		body.FreshnessResolution.ProducerFactID == "" ||
		body.FreshnessResolution.FreshnessHoldResolutionSeconds < 2 {
		t.Fatalf("draft freshness resolution mismatch: %+v", body.FreshnessResolution)
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportFreshnessResolutionActivity(t, taskID, target, body.FreshnessResolution.ProducerFactID, "send_draft")
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
}

func TestAgentTransportSendDraftReloadsWinnerAfterConcurrentReplacement(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	task, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load transport task: %v", err)
	}
	origin, ok := testHandler.chatOutputOriginForTask(ctx, task)
	if !ok {
		t.Fatal("resolve transport task origin")
	}
	targetName := "#" + channelNameForTransportTest(t, channelID)
	target, err := testHandler.resolveAgentTransportTarget(ctx, task, origin, targetName, true)
	if err != nil {
		t.Fatalf("resolve transport target: %v", err)
	}
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "race seen "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed race seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "race newer "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed race newer message: %v", err)
	}

	oldContent := "race draft A " + uuid.NewString()
	oldClientID := "race-draft-a-" + uuid.NewString()
	held := agentTransportSendForTest(t, taskID, uuidToString(task.AgentID), map[string]any{
		"target": targetName, "content": oldContent, "client_message_id": oldClientID, "seen_up_to_seq": seen.Seq,
	})
	if held.Code != http.StatusOK {
		t.Fatalf("hold race draft A: status=%d body=%s", held.Code, held.Body.String())
	}
	newest, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "race newest decision "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed race newest decision message: %v", err)
	}

	source := agentTransportSource{task: task, origin: origin}
	replacementTx, err := testPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin concurrent replacement: %v", err)
	}
	defer replacementTx.Rollback(ctx)
	if err := testHandler.lockAgentTransportDraftSource(ctx, replacementTx, source, target); err != nil {
		t.Fatalf("lock concurrent replacement: %v", err)
	}
	newContent := "race draft B " + uuid.NewString()
	newClientID := "race-draft-b-" + uuid.NewString()
	newDecisionFactID := agentTransportFreshnessProducerID(source, target, seen.Seq, newest.Seq)
	partsJSON, err := json.Marshal([]protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: newContent}})
	if err != nil {
		t.Fatalf("marshal replacement parts: %v", err)
	}
	if _, err := replacementTx.Exec(ctx, `
		UPDATE agent_transport_draft
		SET content = $5,
		    parts = $6::jsonb,
		    client_message_id = $7,
		    seen_up_to_seq = $8::bigint,
		    held_from_seq = $8::bigint + 1,
		    held_to_seq = $9,
		    shown_from_seq = $9,
		    shown_to_seq = $9,
		    decision_fact_id = $10,
		    updated_at = now()
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3 AND task_id = $4 AND inbox_event_id IS NULL`,
		origin.workspaceID, origin.agentID, target.raw, task.ID, newContent, partsJSON, newClientID, seen.Seq, newest.Seq, newDecisionFactID); err != nil {
		t.Fatalf("replace draft under held source lock: %v", err)
	}
	if _, err := testHandler.recordAgentTransportAuditExec(ctx, replacementTx, source, agentTransportActionSend, target.raw, parseUUID(target.channel.ID), pgtype.UUID{}, newClientID, map[string]any{
		"held":             true,
		"subtype":          "freshness",
		"producer_fact_id": newDecisionFactID,
		"seen_up_to_seq":   seen.Seq,
		"latest_seq":       newest.Seq,
	}); err != nil {
		t.Fatalf("record replacement hold audit: %v", err)
	}

	resultCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		resultCh <- agentTransportSendForTest(t, taskID, uuidToString(task.AgentID), map[string]any{
			"target": targetName, "send_draft": true,
		})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var blocked int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_stat_activity
			WHERE wait_event_type = 'Lock'
			  AND wait_event = 'advisory'
			  AND query LIKE '%pg_advisory_xact_lock%'`).Scan(&blocked); err != nil {
			t.Fatalf("observe blocked send_draft: %v", err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("send_draft did not block on the source-target lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := replacementTx.Commit(ctx); err != nil {
		t.Fatalf("commit concurrent replacement: %v", err)
	}

	var sent *httptest.ResponseRecorder
	select {
	case sent = <-resultCh:
	case <-time.After(5 * time.Second):
		t.Fatal("send_draft did not complete after replacement commit")
	}
	if sent.Code != http.StatusCreated {
		t.Fatalf("send winner draft: status=%d body=%s", sent.Code, sent.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(sent.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode winner draft send: %v", err)
	}
	if body.Message.Content != newContent || body.Message.ClientMessageID == nil || *body.Message.ClientMessageID != newClientID {
		t.Fatalf("sent stale draft after concurrent replacement: %+v, want content/client %q/%q", body.Message, newContent, newClientID)
	}
	assertAgentMessageSentActivityText(t, body.Message.ID, newContent)
	var activityDecisionFactID string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(details->>'decision_fact_id', '')
		FROM agent_activity_event
		WHERE workspace_id = $1
		  AND agent_id = $2
		  AND event_type = 'message_sent'
		  AND details->>'message_id' = $3
		ORDER BY created_at DESC
		LIMIT 1`, testWorkspaceID, uuidToString(task.AgentID), body.Message.ID).Scan(&activityDecisionFactID); err != nil {
		t.Fatalf("load winner draft Activity fact: %v", err)
	}
	if activityDecisionFactID != newDecisionFactID {
		t.Fatalf("winner draft Activity decision fact=%q, want locked replacement %q", activityDecisionFactID, newDecisionFactID)
	}
	assertNoChannelMessageContent(t, channelID, oldContent)
	assertAgentTransportDraftMissing(t, uuidToString(task.AgentID), target.raw)
}

func TestAgentTransportSendDraftRebuildsMentionForCurrentDestinationMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	senderID := agentIDForTask(t, taskID)
	targetChannelID := seedChannelForTest(t, "transport-draft-destination-"+uuid.NewString(), testUserID)
	oldTargetName := "draft-old-" + uuid.NewString()[:8]
	newTargetName := "draft-new-" + uuid.NewString()[:8]
	oldTargetID := createHandlerTestAgent(t, oldTargetName, nil)
	newTargetID := createHandlerTestAgent(t, newTargetName, nil)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent
		SET display_name = $2
		WHERE id = ANY($1::uuid[])
	`, []string{oldTargetID, newTargetID}, "Draft Destination "+uuid.NewString()); err != nil {
		t.Fatalf("set duplicate display names: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES
			($1, $2, 'agent', $3),
			($1, $2, 'agent', $4)

ON CONFLICT DO NOTHING`, targetChannelID, testWorkspaceID, senderID, oldTargetID); err != nil {
		t.Fatalf("seed destination members: %v", err)
	}
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(targetChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "seen before held destination draft", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen destination message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(targetChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "newer destination message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed newer destination message: %v", err)
	}
	target := "#" + channelNameForTransportTest(t, targetChannelID)
	content := "please @" + newTargetName + " review the held draft"
	clientID := "transport-draft-members-" + uuid.NewString()
	held := agentTransportSendForTest(t, taskID, senderID, map[string]any{
		"target":            target,
		"content":           content,
		"client_message_id": clientID,
		"seen_up_to_seq":    seen.Seq,
	})
	if held.Code != http.StatusOK {
		t.Fatalf("hold destination draft: status=%d body=%s", held.Code, held.Body.String())
	}
	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND workspace_id = $2 AND member_type = 'agent' AND member_id = $3
	`, targetChannelID, testWorkspaceID, oldTargetID); err != nil {
		t.Fatalf("remove original destination member: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)

ON CONFLICT DO NOTHING`, targetChannelID, testWorkspaceID, newTargetID); err != nil {
		t.Fatalf("add current destination member: %v", err)
	}

	var heldBody AgentTransportSendHeldResponse
	if err := json.Unmarshal(held.Body.Bytes(), &heldBody); err != nil {
		t.Fatalf("decode held destination draft: %v", err)
	}
	sent := agentTransportSendForTest(t, taskID, senderID, map[string]any{
		"target":         target,
		"send_draft":     true,
		"seen_up_to_seq": heldBody.LatestSeq,
	})
	// Adding the replacement destination agent commits a canonical membership
	// system row. That is genuinely newer context, so the first retry must hold
	// again; the next retry consumes the refreshed draft and sends it once.
	if sent.Code == http.StatusOK {
		var refreshed AgentTransportSendHeldResponse
		if err := json.Unmarshal(sent.Body.Bytes(), &refreshed); err != nil {
			t.Fatalf("decode refreshed held draft: %v", err)
		}
		if refreshed.Subtype != "freshness" || len(refreshed.HeldMessages) != 1 || refreshed.HeldMessages[0].Type != "system" {
			t.Fatalf("membership change hold = %+v, want one system membership row", refreshed)
		}
		sent = agentTransportSendForTest(t, taskID, senderID, map[string]any{
			"target":     target,
			"send_draft": true,
		})
	}
	if sent.Code != http.StatusCreated {
		t.Fatalf("send held destination draft: status=%d body=%s", sent.Code, sent.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(sent.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode held draft response: %v", err)
	}
	start := strings.Index(content, "@")
	startUTF16, endUTF16 := contentUTF16Span(content, start, start+len("@"+newTargetName))
	assertSingleMentionReferenceForTest(t, body.Message.Parts, newTargetID, startUTF16, endUTF16)
	for _, part := range body.Message.Parts {
		if part.Type == protocol.MessagePartTypeReference && part.RefType == "mention" && part.RefID == oldTargetID {
			t.Fatalf("draft retained removed destination member: %+v", part)
		}
	}
}

func TestAgentTransportSendMessageStickerOnlyAndWithText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	stickerOnlyID := "transport-sticker-" + uuid.NewString()
	stickerOnly := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target,
		"parts": []protocol.MessagePart{{
			Type:      protocol.MessagePartTypeSticker,
			StickerID: "hi",
		}},
		"client_message_id": stickerOnlyID,
	})
	if stickerOnly.Code != http.StatusCreated {
		t.Fatalf("sticker-only transport send: status=%d body=%s", stickerOnly.Code, stickerOnly.Body.String())
	}
	var stickerOnlyBody AgentTransportSendResponse
	if err := json.Unmarshal(stickerOnly.Body.Bytes(), &stickerOnlyBody); err != nil {
		t.Fatalf("decode sticker-only send: %v", err)
	}
	if len(stickerOnlyBody.Message.Parts) != 1 || stickerOnlyBody.Message.Parts[0].Type != protocol.MessagePartTypeSticker || stickerOnlyBody.Message.Parts[0].StickerID != "hi" {
		t.Fatalf("sticker-only parts = %+v, want hi sticker", stickerOnlyBody.Message.Parts)
	}
	if stickerOnlyBody.Message.Parts[0].Alt == "" {
		t.Fatalf("sticker-only alt is empty: %+v", stickerOnlyBody.Message.Parts[0])
	}
	assertAgentMessageSentActivityText(t, stickerOnlyBody.Message.ID, stickerOnlyBody.Message.Parts[0].Alt)

	explanation := "这个问题是因为 transport sticker test " + uuid.NewString()
	combinedID := "transport-combined-" + uuid.NewString()
	combined := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  target,
		"content": explanation,
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeSticker, StickerID: "got-it"},
			{Type: protocol.MessagePartTypeText, Text: explanation},
		},
		"client_message_id": combinedID,
	})
	if combined.Code != http.StatusCreated {
		t.Fatalf("combined transport send: status=%d body=%s", combined.Code, combined.Body.String())
	}
	var combinedBody AgentTransportSendResponse
	if err := json.Unmarshal(combined.Body.Bytes(), &combinedBody); err != nil {
		t.Fatalf("decode combined send: %v", err)
	}
	if len(combinedBody.Message.Parts) != 2 ||
		combinedBody.Message.Parts[0].Type != protocol.MessagePartTypeSticker ||
		combinedBody.Message.Parts[0].StickerID != "got-it" ||
		combinedBody.Message.Parts[1].Type != protocol.MessagePartTypeText ||
		combinedBody.Message.Parts[1].Text != explanation {
		t.Fatalf("combined parts = %+v, want got-it sticker then text", combinedBody.Message.Parts)
	}

	var messageRows int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND client_message_id IN ($2, $3)`,
		channelID, stickerOnlyID, combinedID).Scan(&messageRows); err != nil {
		t.Fatalf("count transport sticker messages: %v", err)
	}
	if messageRows != 2 {
		t.Fatalf("transport sticker message rows=%d, want 2", messageRows)
	}
}

func TestAgentTransportSendMessageLinksOwnedAttachmentsOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	ownedAttachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "agent-file.png")
	otherAgentID := uuid.NewString()
	foreignAttachmentID := seedUnboundAgentAttachmentForTest(t, otherAgentID, "not-mine.png")

	clientID := "transport-attachment-" + uuid.NewString()
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  target,
		"content": "here's the file",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "here's the file"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: ownedAttachmentID},
		},
		"client_message_id": clientID,
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("transport send with attachments: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode transport send: %v", err)
	}
	if len(body.Message.Attachments) != 1 || body.Message.Attachments[0].ID != ownedAttachmentID {
		t.Fatalf("message attachments = %+v, want only owned attachment %s", body.Message.Attachments, ownedAttachmentID)
	}
	if len(body.Message.Parts) < 2 {
		t.Fatalf("message parts = %+v, want text + attachment parts", body.Message.Parts)
	}

	var ownedChannelID pgtype.UUID
	var ownedReferences int
	if err := testPool.QueryRow(ctx, `
		SELECT attachment.channel_id,
		       count(reference.channel_message_id)
		FROM attachment
		LEFT JOIN channel_message_attachment reference ON reference.attachment_id = attachment.id
		WHERE attachment.id = $1
		GROUP BY attachment.channel_id
	`, ownedAttachmentID).Scan(&ownedChannelID, &ownedReferences); err != nil {
		t.Fatalf("load owned attachment: %v", err)
	}
	if ownedChannelID.Valid || ownedReferences != 1 {
		t.Fatalf("owned attachment link: upload_channel=%+v references=%d, want NULL/1", ownedChannelID, ownedReferences)
	}

	var foreignChannelID pgtype.UUID
	var foreignReferences int
	if err := testPool.QueryRow(ctx, `
		SELECT attachment.channel_id,
		       count(reference.channel_message_id)
		FROM attachment
		LEFT JOIN channel_message_attachment reference ON reference.attachment_id = attachment.id
		WHERE attachment.id = $1
		GROUP BY attachment.channel_id
	`, foreignAttachmentID).Scan(&foreignChannelID, &foreignReferences); err != nil {
		t.Fatalf("load foreign attachment: %v", err)
	}
	if foreignChannelID.Valid || foreignReferences != 0 {
		t.Fatalf("foreign attachment got linked: channel_id=%+v references=%d, want NULL/0", foreignChannelID, foreignReferences)
	}
}

func TestAgentTransportSendMessageRejectsMixedForeignAttachmentsAtomically(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	ownedAttachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "owned.png")
	foreignAttachmentID := seedUnboundAgentAttachmentForTest(t, uuid.NewString(), "foreign.png")
	clientID := "transport-mixed-foreign-" + uuid.NewString()

	response := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  "#" + channelNameForTransportTest(t, channelID),
		"content": "must stay atomic",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "must stay atomic"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: ownedAttachmentID},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: foreignAttachmentID},
		},
		"client_message_id": clientID,
	})
	if response.Code != http.StatusBadRequest {
		t.Fatalf("mixed foreign attachment send: status=%d body=%s", response.Code, response.Body.String())
	}

	var messages, references int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND client_message_id = $2`, channelID, clientID).Scan(&messages); err != nil {
		t.Fatalf("count rolled-back messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_attachment
		WHERE attachment_id = ANY($1::uuid[])`, []string{ownedAttachmentID, foreignAttachmentID}).Scan(&references); err != nil {
		t.Fatalf("count rolled-back attachment references: %v", err)
	}
	if messages != 0 || references != 0 {
		t.Fatalf("mixed foreign send left messages/references=%d/%d, want 0/0", messages, references)
	}
}

func TestAgentTransportSendMessageReusesOwnedAttachmentAcrossGroupAndDM(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, groupChannelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	attachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "reuse-across-conversations.png")

	groupResponse := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  "#" + channelNameForTransportTest(t, groupChannelID),
		"content": "group copy",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "group copy"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: attachmentID},
		},
		"client_message_id": "transport-group-reuse-" + uuid.NewString(),
	})
	if groupResponse.Code != http.StatusCreated {
		t.Fatalf("send shared attachment to group: status=%d body=%s", groupResponse.Code, groupResponse.Body.String())
	}

	humanHandle := userHandleForTransportTest(t, testUserID)
	dmResponse := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  "dm:@" + humanHandle,
		"content": "dm copy",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "dm copy"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: attachmentID},
		},
		"client_message_id": "transport-dm-reuse-" + uuid.NewString(),
	})
	if dmResponse.Code != http.StatusCreated {
		t.Fatalf("reuse shared attachment in DM: status=%d body=%s", dmResponse.Code, dmResponse.Body.String())
	}

	var groupBody, dmBody AgentTransportSendResponse
	if err := json.Unmarshal(groupResponse.Body.Bytes(), &groupBody); err != nil {
		t.Fatalf("decode group send: %v", err)
	}
	if err := json.Unmarshal(dmResponse.Body.Bytes(), &dmBody); err != nil {
		t.Fatalf("decode DM send: %v", err)
	}
	for label, message := range map[string]ChannelMessageResponse{
		"group": groupBody.Message,
		"dm":    dmBody.Message,
	} {
		if len(message.Attachments) != 1 || message.Attachments[0].ID != attachmentID {
			t.Fatalf("%s message attachments=%+v, want reused attachment %s", label, message.Attachments, attachmentID)
		}
	}
	if groupBody.Message.ChannelID == dmBody.Message.ChannelID {
		t.Fatalf("group and DM unexpectedly share channel %s", groupBody.Message.ChannelID)
	}

	var referenceCount, distinctMessageCount, distinctChannelCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*),
		       count(DISTINCT reference.channel_message_id),
		       count(DISTINCT message.channel_id)
		FROM channel_message_attachment reference
		JOIN channel_message message ON message.id = reference.channel_message_id
		WHERE reference.attachment_id = $1
	`, attachmentID).Scan(&referenceCount, &distinctMessageCount, &distinctChannelCount); err != nil {
		t.Fatalf("load shared attachment references: %v", err)
	}
	if referenceCount != 2 || distinctMessageCount != 2 || distinctChannelCount != 2 {
		t.Fatalf("shared attachment references=%d messages=%d channels=%d, want 2/2/2", referenceCount, distinctMessageCount, distinctChannelCount)
	}
}

func TestAgentTransportSendMessageNormalizesLegacyOwnedAudioAsVoice(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	attachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "nihao.mp3")
	if _, err := testPool.Exec(ctx, `
		UPDATE attachment
		SET content_type = 'audio/mpeg'
		WHERE id = $1`, attachmentID); err != nil {
		t.Fatalf("mark attachment as audio: %v", err)
	}

	response := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  target,
		"content": "你好～",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "你好～"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: attachmentID},
		},
		"client_message_id": "transport-legacy-audio-" + uuid.NewString(),
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("legacy audio transport send: status=%d body=%s", response.Code, response.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode legacy audio send: %v", err)
	}
	var voiceParts int
	for _, part := range body.Message.Parts {
		if part.Type == protocol.MessagePartTypeVoice {
			voiceParts++
		}
	}
	if voiceParts != 1 {
		t.Fatalf("voice parts=%d in %+v, want one normalized marker", voiceParts, body.Message.Parts)
	}
}

// Regression: `multica attachment upload --target '#channel'` records the upload
// provenance before send. The message still needs its own canonical reference;
// otherwise its attachment part hydrates as "Attachment unavailable".
func TestAgentTransportSendMessageLinksChannelPreboundOwnedAttachments(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	preboundID := seedChannelPreboundAgentAttachmentForTest(t, agentID, channelID, "prebound.png")

	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":  target,
		"content": "prebound shot",
		"parts": []protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: "prebound shot"},
			{Type: protocol.MessagePartTypeAttachment, AttachmentID: preboundID},
		},
		"client_message_id": "transport-prebound-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("transport send with prebound attachment: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode transport send: %v", err)
	}
	if len(body.Message.Attachments) != 1 || body.Message.Attachments[0].ID != preboundID {
		t.Fatalf("message attachments = %+v, want prebound attachment %s", body.Message.Attachments, preboundID)
	}
	for _, part := range body.Message.Parts {
		if part.Type == protocol.MessagePartTypeVoice {
			t.Fatalf("image attachment was reclassified as voice: %+v", body.Message.Parts)
		}
	}

	var gotChannelID, gotMessageID string
	if err := testPool.QueryRow(ctx, `
		SELECT attachment.channel_id, reference.channel_message_id
		FROM attachment
		JOIN channel_message_attachment reference ON reference.attachment_id = attachment.id
		WHERE attachment.id = $1
	`, preboundID).Scan(&gotChannelID, &gotMessageID); err != nil {
		t.Fatalf("load prebound attachment: %v", err)
	}
	if gotChannelID != channelID || gotMessageID != body.Message.ID {
		t.Fatalf("prebound attachment link: channel_id=%s message_id=%s, want channel=%s message=%s", gotChannelID, gotMessageID, channelID, body.Message.ID)
	}
}

// Regression: transport/radar publish used to omit author_avatar_url on the
// WS/API payload, so group bubbles showed initials while the profile card had
// the real photo. Attach before publish so the stream matches the DB avatar.
func TestAgentTransportSendMessageIncludesAuthorAvatarURL(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	avatarURL := "/files/transport-agent-avatar.png"
	if _, err := testPool.Exec(ctx, `UPDATE agent SET avatar_url = $1 WHERE id = $2`, avatarURL, agentID); err != nil {
		t.Fatalf("seed agent avatar: %v", err)
	}
	target := "#" + channelNameForTransportTest(t, channelID)

	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "avatar stream check",
		"client_message_id": "transport-avatar-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("transport send: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode transport send: %v", err)
	}
	if body.Message.AuthorAvatarURL == nil || *body.Message.AuthorAvatarURL != avatarURL {
		t.Fatalf("message author_avatar_url = %v, want %q", body.Message.AuthorAvatarURL, avatarURL)
	}
}

func TestAgentTransportSendMessageAttachmentOnlyActivityText(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	attachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "activity-report.pdf")

	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target,
		"parts": []protocol.MessagePart{{
			Type:         protocol.MessagePartTypeAttachment,
			AttachmentID: attachmentID,
		}},
		"client_message_id": "transport-attachment-only-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("attachment-only transport send: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode attachment-only send: %v", err)
	}
	if body.Message.Content != "" || len(body.Message.Attachments) != 1 || body.Message.Attachments[0].Filename != "activity-report.pdf" {
		t.Fatalf("attachment-only message = %+v, want one linked activity-report.pdf attachment and empty content", body.Message)
	}
	if len(body.Message.Parts) != 1 || body.Message.Parts[0].Type != protocol.MessagePartTypeAttachment || body.Message.Parts[0].AttachmentID != attachmentID {
		t.Fatalf("attachment-only parts = %+v, want one attachment part for %s", body.Message.Parts, attachmentID)
	}
	assertAgentMessageSentActivityText(t, body.Message.ID, "Sent attachment: activity-report.pdf")
}

func TestAgentTransportSendThreadReplyIDFlattensToRoot(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)

	// Create a root message in the channel.
	var rootID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, seq)
		VALUES ($1, $2, 'user', $3, 'test-user', 'thread root msg', 1)
		RETURNING id`,
		channelID, testWorkspaceID, testUserID).Scan(&rootID); err != nil {
		t.Fatalf("create root message: %v", err)
	}

	// Create a thread reply under that root.
	var replyID string
	var threadID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, seq, reply_to_message_id, thread_root_message_id, thread_id, trigger_depth)
		VALUES ($1, $2, 'agent', $3, 'test-agent', 'thread reply msg', 2, $4, $4, gen_random_uuid()::text, 1)
		RETURNING id, thread_id`,
		channelID, testWorkspaceID, agentID, rootID).Scan(&replyID, &threadID); err != nil {
		t.Fatalf("create thread reply: %v", err)
	}

	// Look up the channel name for the target.
	var channelName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		t.Fatalf("get channel name: %v", err)
	}

	// Send using the REPLY id as the thread target — should flatten to root.
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "#" + channelName + ":" + replyID,
		"content":           "reply-to-thread-reply-id should flatten",
		"client_message_id": "flatten-test-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("send targeting thread reply id: status=%d body=%s", resp.Code, resp.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	// The message must land in the thread (thread_root_message_id = rootID, not the reply).
	if body.Message.ThreadRootMessageID == nil || *body.Message.ThreadRootMessageID != rootID {
		t.Fatalf("thread reply id did not flatten to root: got thread_root_message_id=%v, want %s",
			body.Message.ThreadRootMessageID, rootID)
	}
}

func TestAgentTransportSendThreadReplyFollowsAgentForPlainFollowup(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	threadID := "transport-follow-" + uuid.NewString()
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "transport follow root", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, &threadID, 0)
	if err != nil {
		t.Fatalf("insert root: %v", err)
	}

	target := "#" + channelNameForTransportTest(t, channelID) + ":" + root.ID
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "agent reply that should follow thread",
		"client_message_id": "transport-follow-" + uuid.NewString(),
	})
	if resp.Code != http.StatusCreated {
		t.Fatalf("transport thread send: status=%d body=%s", resp.Code, resp.Body.String())
	}

	var followed bool
	var wakeState string
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at IS NOT NULL, wake_state
		FROM thread_participant
		WHERE root_message_id = $1
		  AND member_type = 'agent'
		  AND member_id = $2`, root.ID, agentID).Scan(&followed, &wakeState); err != nil {
		t.Fatalf("load transport thread participant: %v", err)
	}
	if !followed || wakeState != "active" {
		t.Fatalf("transport thread participant = followed:%v wake_state:%q, want true/active", followed, wakeState)
	}

	req := newRequest(http.MethodPost, "/api/channels/"+channelID+"/messages/"+root.ID+"/thread", map[string]string{"content": "plain human follow-up"})
	req = withChannelTestWorkspaceCtx(t, req, testUserID)
	req = withURLParams(req, "channelId", channelID, "messageId", root.ID)
	rec := httptest.NewRecorder()
	testHandler.SendChannelMessageThreadReply(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("send human thread reply: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var followup ChannelMessageResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &followup); err != nil {
		t.Fatalf("decode human thread reply: %v", err)
	}

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, channelID, agentID, followup.ID, "thread_reply", channelThreadReplyPriority)
}

func TestAgentTransportReadSearchAndReactAudit(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	needle := "needle transport search " + uuid.NewString()
	seeded, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", needle, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed channel message: %v", err)
	}
	systemNotice := "system transport notice " + uuid.NewString()
	systemMsg, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", systemNotice, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed system channel message: %v", err)
	}

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{"target": target, "limit": 5})
	if readRec.Code != http.StatusOK {
		t.Fatalf("transport read: status=%d body=%s", readRec.Code, readRec.Body.String())
	}
	var readBody AgentTransportReadResponse
	if err := json.Unmarshal(readRec.Body.Bytes(), &readBody); err != nil {
		t.Fatalf("decode transport read: %v", err)
	}
	if !transportMessagesContain(readBody.Messages, seeded.ID, needle) {
		t.Fatalf("read messages did not include seeded message %s: %+v", seeded.ID, readBody.Messages)
	}
	if !transportMessagesContainType(readBody.Messages, systemMsg.ID, systemNotice, "system") {
		t.Fatalf("read messages did not include system message %s: %+v", systemMsg.ID, readBody.Messages)
	}

	searchRec := agentTransportSearchForTest(t, taskID, agentID, map[string]any{
		"target": target,
		"query":  "needle transport search",
		"limit":  10,
	})
	if searchRec.Code != http.StatusOK {
		t.Fatalf("transport search: status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}
	var searchBody AgentTransportSearchResponse
	if err := json.Unmarshal(searchRec.Body.Bytes(), &searchBody); err != nil {
		t.Fatalf("decode transport search: %v", err)
	}
	if searchBody.Total < 1 || !transportSearchResultsContain(searchBody.Results, seeded.ID, needle) {
		t.Fatalf("search results did not include seeded message %s: %+v", seeded.ID, searchBody.Results)
	}

	reactClientID := "transport-react-" + uuid.NewString()
	reactRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"message_id":        seeded.ID,
		"emoji":             "+1",
		"client_message_id": reactClientID,
	})
	if reactRec.Code != http.StatusCreated {
		t.Fatalf("transport react: status=%d body=%s", reactRec.Code, reactRec.Body.String())
	}
	var reactBody AgentTransportReactResponse
	if err := json.Unmarshal(reactRec.Body.Bytes(), &reactBody); err != nil {
		t.Fatalf("decode transport react: %v", err)
	}
	if reactBody.Reaction.MessageID != seeded.ID || reactBody.Reaction.ActorID != agentID || reactBody.Reaction.Emoji != "+1" {
		t.Fatalf("reaction payload mismatch: %+v", reactBody.Reaction)
	}

	var reactionRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '+1'`,
		seeded.ID, agentID).Scan(&reactionRows); err != nil {
		t.Fatalf("count agent reaction: %v", err)
	}
	if reactionRows != 1 {
		t.Fatalf("agent reaction rows=%d, want 1", reactionRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionRead, 1)
	assertAgentTransportAuditCount(t, taskID, agentTransportActionSearch, 1)
	assertAgentTransportAuditCount(t, taskID, agentTransportActionReact, 1)
}

func TestAgentTransportRequiresExplicitTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	visible := "missing explicit target should not send " + uuid.NewString()

	sendRec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"content":           visible,
		"client_message_id": "missing-target-" + uuid.NewString(),
	})
	if sendRec.Code != http.StatusBadRequest {
		t.Fatalf("send without target: status=%d body=%s", sendRec.Code, sendRec.Body.String())
	}
	assertNoChannelMessageContent(t, channelID, visible)

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{"limit": 5})
	if readRec.Code != http.StatusBadRequest {
		t.Fatalf("read without target: status=%d body=%s", readRec.Code, readRec.Body.String())
	}

	searchRec := agentTransportSearchForTest(t, taskID, agentID, map[string]any{
		"query": "needle",
		"limit": 5,
	})
	if searchRec.Code != http.StatusBadRequest {
		t.Fatalf("search without target: status=%d body=%s", searchRec.Code, searchRec.Body.String())
	}

	reactRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id":        uuid.NewString(),
		"emoji":             "+1",
		"client_message_id": "missing-target-react-" + uuid.NewString(),
	})
	if reactRec.Code != http.StatusBadRequest {
		t.Fatalf("react without target: status=%d body=%s", reactRec.Code, reactRec.Body.String())
	}
}

func TestAgentTransportDMThreadTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	humanHandle := userHandleForTransportTest(t, testUserID)
	dmChannel, ok := testHandler.ensureAgentHumanDMChannel(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(testUserID))
	if !ok {
		t.Fatal("create agent-human DM channel")
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), humanHandle, "dm thread root "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}

	clientID := "dm-thread-" + uuid.NewString()
	rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@" + humanHandle + ":" + root.ID,
		"content":           "dm thread reply " + uuid.NewString(),
		"client_message_id": clientID,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send to dm thread target: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode dm thread response: %v", err)
	}
	if body.Message.ChannelID != dmChannel.ID {
		t.Fatalf("message channel_id=%s, want dm channel %s", body.Message.ChannelID, dmChannel.ID)
	}
	if body.Message.ThreadRootMessageID == nil || *body.Message.ThreadRootMessageID != root.ID {
		t.Fatalf("message thread_root_message_id=%v, want %s", body.Message.ThreadRootMessageID, root.ID)
	}
}

func TestAgentTransportUnfollowDMThreadTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	humanHandle := userHandleForTransportTest(t, testUserID)
	dmChannel, ok := testHandler.ensureAgentHumanDMChannel(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(testUserID))
	if !ok {
		t.Fatal("create agent-human DM channel")
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), humanHandle, "dm unfollow root "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}
	rec := agentTransportUnfollowThreadForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle + ":" + root.ID,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("unfollow dm thread target: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body AgentTransportThreadUnfollowResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode unfollow response: %v", err)
	}
	if body.Action != agentTransportActionThreadUnfollow || body.ChannelID != dmChannel.ID || body.MessageID != root.ID {
		t.Fatalf("unfollow response = %+v, want dm channel %s root %s", body, dmChannel.ID, root.ID)
	}
	repeatRec := agentTransportUnfollowThreadForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle + ":" + root.ID,
	})
	if repeatRec.Code != http.StatusOK {
		t.Fatalf("repeat unfollow dm thread target: status=%d body=%s", repeatRec.Code, repeatRec.Body.String())
	}

	var followedAt pgtype.Timestamptz
	var wakeState string
	if err := testPool.QueryRow(ctx, `
		SELECT followed_at, wake_state
		FROM thread_participant
		WHERE root_message_id = $1 AND member_type = 'agent' AND member_id = $2`,
		root.ID, agentID).Scan(&followedAt, &wakeState); err != nil {
		t.Fatalf("load agent thread participant: %v", err)
	}
	if followedAt.Valid {
		t.Fatalf("agent followed_at still set after unfollow: %+v", followedAt)
	}
	if wakeState != "unfollowed" {
		t.Fatalf("agent wake_state=%q, want unfollowed", wakeState)
	}

	var eventRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND thread_root_message_id = $2
		  AND author_type = 'system'
		  AND content LIKE '%unfollowed this thread%'`,
		dmChannel.ID, root.ID).Scan(&eventRows); err != nil {
		t.Fatalf("count thread unfollow system event: %v", err)
	}
	if eventRows != 1 {
		t.Fatalf("thread unfollow system event rows = %d, want 1", eventRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionThreadUnfollow, 2)
}

func TestDMThreadDeliveryHonorsFollowUnfollowAndAgentPost(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	humanHandle := userHandleForTransportTest(t, testUserID)
	dmChannel, ok := testHandler.ensureAgentHumanDMChannel(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(testUserID))
	if !ok {
		t.Fatal("create agent-human DM channel")
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannel.ID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "DM Agent", "dm delivery root "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert dm root: %v", err)
	}
	target := "dm:@" + humanHandle + ":" + root.ID

	sendHumanReply := func(content string, parts ...protocol.MessagePart) ChannelMessageResponse {
		t.Helper()
		req := newRequest(http.MethodPost, "/api/channels/"+dmChannel.ID+"/messages/"+root.ID+"/thread", map[string]any{"content": content, "parts": parts})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParams(req, "channelId", dmChannel.ID, "messageId", root.ID)
		rec := httptest.NewRecorder()
		testHandler.SendChannelMessageThreadReply(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send human dm thread reply: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var message ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil {
			t.Fatalf("decode human dm thread reply: %v", err)
		}
		return message
	}
	assertWakeCount := func(messageID string, want int) {
		t.Helper()
		var got int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM agent_inbox_event
			WHERE channel_id = $1 AND agent_id = $2 AND source_message_id = $3 AND requires_wake`,
			dmChannel.ID, agentID, messageID).Scan(&got); err != nil {
			t.Fatalf("count dm thread wake events: %v", err)
		}
		if got != want {
			t.Fatalf("dm thread wake events for message %s = %d, want %d", messageID, got, want)
		}
	}
	assertFollowState := func(wantFollowed bool, wantWakeState string) {
		t.Helper()
		var followed bool
		var wakeState string
		if err := testPool.QueryRow(ctx, `
			SELECT followed_at IS NOT NULL, wake_state
			FROM thread_participant
			WHERE root_message_id = $1 AND member_type = 'agent' AND member_id = $2`,
			root.ID, agentID).Scan(&followed, &wakeState); err != nil {
			t.Fatalf("load dm thread participant: %v", err)
		}
		if followed != wantFollowed || wakeState != wantWakeState {
			t.Fatalf("dm thread participant = followed:%v wake_state:%q, want %v/%q", followed, wakeState, wantFollowed, wantWakeState)
		}
	}
	unfollow := func() {
		t.Helper()
		rec := agentTransportUnfollowThreadForTest(t, taskID, agentID, map[string]any{"target": target})
		if rec.Code != http.StatusOK {
			t.Fatalf("unfollow dm thread: status=%d body=%s", rec.Code, rec.Body.String())
		}
		assertFollowState(false, "unfollowed")
	}

	var agentHandle string
	if err := testPool.QueryRow(ctx, `SELECT name FROM agent WHERE id = $1`, agentID).Scan(&agentHandle); err != nil {
		t.Fatalf("load agent handle: %v", err)
	}
	mentionPart := protocol.MessagePart{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      agentID,
		Label:      "@" + agentHandle,
	}
	firstReply := sendHumanReply("first dm thread reply")
	assertWakeCount(firstReply.ID, 1)
	assertChannelAgentWakeReasonPriority(t, dmChannel.ID, agentID, firstReply.ID, "thread_reply", channelThreadReplyPriority)
	assertFollowState(true, "active")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', acked_at = now(), terminal_outcome = 'no_reply', retryable = false, terminal_at = now(), updated_at = now()
		WHERE channel_id = $1 AND agent_id = $2 AND source_message_id = $3`, dmChannel.ID, agentID, firstReply.ID); err != nil {
		t.Fatalf("ack first thread reply wake: %v", err)
	}

	ordinary := sendHumanReply("ordinary followed reply " + uuid.NewString())
	assertWakeCount(ordinary.ID, 1)
	assertChannelAgentWakeReasonPriority(t, dmChannel.ID, agentID, ordinary.ID, "thread_reply", channelThreadReplyPriority)

	unfollow()
	ignored := sendHumanReply("ordinary unfollowed reply " + uuid.NewString())
	assertWakeCount(ignored.ID, 0)
	assertFollowState(false, "unfollowed")

	mentioned := sendHumanReply("@"+agentHandle+" attempted dm thread mention after unfollow", mentionPart)
	assertWakeCount(mentioned.ID, 0)
	assertFollowState(false, "unfollowed")

	post := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "agent post refollows dm thread " + uuid.NewString(),
		"client_message_id": "dm-thread-refollow-" + uuid.NewString(),
	})
	if post.Code != http.StatusCreated {
		t.Fatalf("agent post to unfollowed dm thread: status=%d body=%s", post.Code, post.Body.String())
	}
	assertFollowState(true, "active")
}

func TestDMHumanRootThreadReplyWakesAgentUnlessExplicitlyUnfollowed(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	humanHandle := userHandleForTransportTest(t, testUserID)
	dmChannel, ok := testHandler.ensureAgentHumanDMChannel(ctx, parseUUID(testWorkspaceID), parseUUID(agentID), parseUUID(testUserID))
	if !ok {
		t.Fatal("create agent-human DM channel")
	}
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(dmChannel.ID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), humanHandle, "human-authored dm root "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert human-authored dm root: %v", err)
	}

	sendReply := func(content string) ChannelMessageResponse {
		t.Helper()
		req := newRequest(http.MethodPost, "/api/channels/"+dmChannel.ID+"/messages/"+root.ID+"/thread", map[string]any{"content": content})
		req = withChannelTestWorkspaceCtx(t, req, testUserID)
		req = withURLParams(req, "channelId", dmChannel.ID, "messageId", root.ID)
		rec := httptest.NewRecorder()
		testHandler.SendChannelMessageThreadReply(rec, req)
		if rec.Code != http.StatusCreated {
			t.Fatalf("send human-root dm thread reply: status=%d body=%s", rec.Code, rec.Body.String())
		}
		var message ChannelMessageResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &message); err != nil {
			t.Fatalf("decode human-root dm thread reply: %v", err)
		}
		return message
	}
	assertWakeCount := func(messageID string, want int) {
		t.Helper()
		var got int
		if err := testPool.QueryRow(ctx, `
			SELECT count(*)
			FROM agent_inbox_event
			WHERE channel_id = $1 AND agent_id = $2 AND source_message_id = $3 AND requires_wake`,
			dmChannel.ID, agentID, messageID).Scan(&got); err != nil {
			t.Fatalf("count human-root dm thread wakes: %v", err)
		}
		if got != want {
			t.Fatalf("human-root dm thread wakes for %s = %d, want %d", messageID, got, want)
		}
	}
	assertFollowState := func(wantFollowed bool, wantWakeState string) {
		t.Helper()
		var followed bool
		var wakeState string
		if err := testPool.QueryRow(ctx, `
			SELECT followed_at IS NOT NULL, wake_state
			FROM thread_participant
			WHERE root_message_id = $1 AND member_type = 'agent' AND member_id = $2`,
			root.ID, agentID).Scan(&followed, &wakeState); err != nil {
			t.Fatalf("load human-root dm agent participant: %v", err)
		}
		if followed != wantFollowed || wakeState != wantWakeState {
			t.Fatalf("human-root dm participant = followed:%v wake_state:%q, want %v/%q", followed, wakeState, wantFollowed, wantWakeState)
		}
	}

	first := sendReply("ordinary reply to my own dm root " + uuid.NewString())
	assertWakeCount(first.ID, 1)
	assertChannelAgentWakeReasonPriority(t, dmChannel.ID, agentID, first.ID, "thread_reply", channelThreadReplyPriority)
	assertFollowState(true, "active")

	unfollow := agentTransportUnfollowThreadForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle + ":" + root.ID,
	})
	if unfollow.Code != http.StatusOK {
		t.Fatalf("unfollow human-root dm thread: status=%d body=%s", unfollow.Code, unfollow.Body.String())
	}
	assertFollowState(false, "unfollowed")

	second := sendReply("ordinary reply after agent unfollow " + uuid.NewString())
	assertWakeCount(second.ID, 0)
	assertFollowState(false, "unfollowed")
}

func TestAgentTransportRejectsNonRaftTargetForms(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	for _, target := range []string{
		uuid.NewString(),
		"#workspace:channel:" + uuid.NewString(),
		"dm:@" + userHandleForTransportTest(t, testUserID) + ":thread:" + uuid.NewString(),
	} {
		t.Run(target, func(t *testing.T) {
			content := "rejected non-raft target " + uuid.NewString()
			rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
				"target":            target,
				"content":           content,
				"client_message_id": "bad-target-" + uuid.NewString(),
			})
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("target %q: status=%d body=%s", target, rec.Code, rec.Body.String())
			}
			assertNoChannelMessageContent(t, channelID, content)
		})
	}
}

func TestAgentTransportReadThreadIncludesSystemReplies(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	threadID := "thread-system-" + uuid.NewString()
	root, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "thread root for system read", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr(threadID), 0)
	if err != nil {
		t.Fatalf("seed thread root: %v", err)
	}
	systemNotice := "thread system notice " + uuid.NewString()
	systemReply, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "system", pgtype.UUID{}, "system", systemNotice, "multica", nil, parseUUID(root.ID), parseUUID(root.ID), strPtr(threadID), 0)
	if err != nil {
		t.Fatalf("seed thread system reply: %v", err)
	}
	var channelName string
	if err := testPool.QueryRow(ctx, `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&channelName); err != nil {
		t.Fatalf("load channel name: %v", err)
	}

	readRec := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": "#" + channelName + ":" + root.ID,
		"limit":  5,
	})
	if readRec.Code != http.StatusOK {
		t.Fatalf("transport thread read: status=%d body=%s", readRec.Code, readRec.Body.String())
	}
	var readBody AgentTransportReadResponse
	if err := json.Unmarshal(readRec.Body.Bytes(), &readBody); err != nil {
		t.Fatalf("decode transport thread read: %v", err)
	}
	if !transportMessagesContainType(readBody.Messages, systemReply.ID, systemNotice, "system") {
		t.Fatalf("thread read messages did not include system reply %s: %+v", systemReply.ID, readBody.Messages)
	}
}

func TestAgentTransportDMHandleTargetRejectsMissingAndAmbiguousHandles(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, _ := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)

	missing := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@missing-" + uuid.NewString(),
		"content":           "should not send",
		"client_message_id": "transport-" + uuid.NewString(),
	})
	if missing.Code != http.StatusBadRequest {
		t.Fatalf("missing dm handle target: status=%d body=%s", missing.Code, missing.Body.String())
	}

	handle := "ambiguous" + strings.ReplaceAll(uuid.NewString()[:8], "-", "")
	lowerUserID := seedWorkspaceUserForTransportTargetTest(t, handle)
	upperUserID := seedWorkspaceUserForTransportTargetTest(t, strings.ToUpper(handle))
	if lowerUserID == upperUserID {
		t.Fatal("ambiguous fixture reused the same user id")
	}

	ambiguous := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@" + handle,
		"content":           "should not send",
		"client_message_id": "transport-" + uuid.NewString(),
	})
	if ambiguous.Code != http.StatusBadRequest {
		t.Fatalf("ambiguous dm handle target: status=%d body=%s", ambiguous.Code, ambiguous.Body.String())
	}

	crossTypeHandle := "cross-type-" + uuid.NewString()[:8]
	seedWorkspaceUserForTransportTargetTest(t, crossTypeHandle)
	crossTypeAgentID := createHandlerTestAgent(t, "Cross Type Agent "+uuid.NewString(), []byte("[]"))
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent
		SET name = $2
		WHERE id = $1`, crossTypeAgentID, crossTypeHandle); err != nil {
		t.Fatalf("seed cross-type ambiguous agent handle: %v", err)
	}
	crossType := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@" + crossTypeHandle,
		"content":           "should not send",
		"client_message_id": "transport-" + uuid.NewString(),
	})
	if crossType.Code != http.StatusBadRequest {
		t.Fatalf("cross-type ambiguous dm target: status=%d body=%s", crossType.Code, crossType.Body.String())
	}

	self := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "dm:@" + agentHandleForTransportTest(t, agentID),
		"content":           "should not send",
		"client_message_id": "transport-" + uuid.NewString(),
	})
	if self.Code != http.StatusBadRequest {
		t.Fatalf("self agent dm target: status=%d body=%s", self.Code, self.Body.String())
	}
}

func TestAgentTransportAutoRetryReassignsPendingWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_inbox_event
		WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_ambient_pending_wake (
			conversation_id, channel_id, workspace_id, agent_id, chat_session_id, task_id,
			status, pending_from_seq, pending_to_seq, delivered_to_seq
		)
		SELECT c.id, $1, $2, $3, $4, $5, 'queued', 1, 1, 0
		FROM conversation c
		WHERE c.channel_id = $1`,
		channelID, testWorkspaceID, agentID, chatSessionID, taskID); err != nil {
		t.Fatalf("seed pending wake: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'failed', terminal_at = now(),
		    acked_at = now(), failure_reason = 'runtime_offline', attempt = 1, max_attempts = 3
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("mark parent failed: %v", err)
	}
	parent, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load failed parent task: %v", err)
	}

	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask: %v", err)
	}
	if child == nil {
		t.Fatal("MaybeRetryFailedTask returned nil child")
	}

	var pendingTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT task_id
		FROM channel_ambient_pending_wake
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&pendingTaskID); err != nil {
		t.Fatalf("load pending wake task: %v", err)
	}
	if pendingTaskID != uuidToString(child.ID) {
		t.Fatalf("pending wake task_id=%s, want retry child %s", pendingTaskID, uuidToString(child.ID))
	}
}

// TestAgentTransportAutoRetryStripsArealProxyFromChildContext verifies D9: a
// retry child does NOT inherit the parent's areal_proxy RL-session config.
// CreateRetryTask copies the parent's context verbatim, so the retry path strips
// areal_proxy (keeping other keys + the chat session_id/work_dir resume pointers)
// so the child opens a fresh areal session at its own session-open chokepoint.
func TestAgentTransportAutoRetryStripsArealProxyFromChildContext(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	taskID, _ := createChannelCompletionTask(t, "group")
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'failed', terminal_at = now(),
		    acked_at = now(), failure_reason = 'runtime_offline', attempt = 1, max_attempts = 3,
		    context = '{"areal_proxy":{"session_id":"sess-parent","api_key":"key-parent"},"squad_id":"squad-9"}'::jsonb
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("seed failed parent with areal_proxy context: %v", err)
	}
	parent, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load failed parent task: %v", err)
	}
	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err != nil {
		t.Fatalf("MaybeRetryFailedTask: %v", err)
	}
	if child == nil {
		t.Fatal("MaybeRetryFailedTask returned nil child")
	}
	// Re-fetch: MaybeRetryFailedTask returns the pre-strip in-memory row; the
	// strip is a DB UPDATE inside createRetryTaskWithPendingWakeTransfer's tx.
	reloaded, err := testHandler.Queries.GetAgentTask(ctx, child.ID)
	if err != nil {
		t.Fatalf("load retry child: %v", err)
	}
	var ctxMap map[string]json.RawMessage
	if err := json.Unmarshal(reloaded.Context, &ctxMap); err != nil {
		t.Fatalf("unmarshal child context: %v", err)
	}
	if _, ok := ctxMap["areal_proxy"]; ok {
		t.Errorf("retry child inherited areal_proxy (D9 violation); context=%s", string(reloaded.Context))
	}
	if _, ok := ctxMap["squad_id"]; !ok {
		t.Errorf("retry child lost squad_id; want it preserved, context=%s", string(reloaded.Context))
	}
}

func TestAgentTransportAutoRetryFailsClosedForSettledPendingWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	var chatSessionID string
	if err := testPool.QueryRow(ctx, `
		SELECT chat_session_id
		FROM agent_inbox_event
		WHERE id = $1`, taskID).Scan(&chatSessionID); err != nil {
		t.Fatalf("load chat session: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_ambient_pending_wake (
			conversation_id, channel_id, workspace_id, agent_id, chat_session_id, task_id,
			status, pending_from_seq, pending_to_seq, delivered_to_seq, completed_at
		)
		SELECT c.id, $1, $2, $3, $4, $5, 'failed', 1, 1, 0, now()
		FROM conversation c
		WHERE c.channel_id = $1`,
		channelID, testWorkspaceID, agentID, chatSessionID, taskID); err != nil {
		t.Fatalf("seed settled pending wake: %v", err)
	}
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_inbox_event
		SET status = 'acked', terminal_outcome = 'failed', terminal_at = now(),
		    acked_at = now(), failure_reason = 'runtime_offline', attempt = 1, max_attempts = 3
		WHERE id = $1`, taskID); err != nil {
		t.Fatalf("mark parent failed: %v", err)
	}
	parent, err := testHandler.Queries.GetAgentTask(ctx, parseUUID(taskID))
	if err != nil {
		t.Fatalf("load failed parent task: %v", err)
	}

	child, err := testHandler.TaskService.MaybeRetryFailedTask(ctx, parent)
	if err == nil {
		t.Fatal("MaybeRetryFailedTask succeeded, want fail-closed error")
	}
	if child != nil {
		t.Fatalf("MaybeRetryFailedTask child=%s, want nil", uuidToString(child.ID))
	}

	var childCount int
	if err := testPool.QueryRow(ctx, `
		SELECT COUNT(*)
		FROM agent_inbox_event
		WHERE parent_task_id = $1`, taskID).Scan(&childCount); err != nil {
		t.Fatalf("count retry children: %v", err)
	}
	if childCount != 0 {
		t.Fatalf("retry child count = %d, want 0", childCount)
	}
	var pendingTaskID string
	if err := testPool.QueryRow(ctx, `
		SELECT task_id
		FROM channel_ambient_pending_wake
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&pendingTaskID); err != nil {
		t.Fatalf("load pending wake task: %v", err)
	}
	if pendingTaskID != taskID {
		t.Fatalf("pending wake task_id=%s, want original parent %s", pendingTaskID, taskID)
	}
}

func agentTransportSendForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/send", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(rec, req)
	return rec
}

func agentTransportReactForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/react", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportReactMessage(rec, req)
	return rec
}

func agentTransportReadForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/read", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportReadMessages(rec, req)
	return rec
}

func agentTransportSearchForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/search", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSearchMessages(rec, req)
	return rec
}

func agentTransportUnfollowThreadForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/threads/unfollow", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportUnfollowThread(rec, req)
	return rec
}

func agentTransportRequest(t *testing.T, method, path, taskID, agentID string, body any) *http.Request {
	t.Helper()
	req := newRequest(method, path, body)
	req = withChatTestWorkspaceCtx(t, req)
	req.Header.Set("X-Actor-Source", "task_token")
	req.Header.Set("X-Agent-ID", agentID)
	req.Header.Set("X-Task-ID", taskID)
	return req
}

func agentIDForTask(t *testing.T, taskID string) string {
	t.Helper()
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT agent_id
		FROM agent_inbox_event
		WHERE id = $1`, taskID).Scan(&agentID); err != nil {
		t.Fatalf("load task agent_id: %v", err)
	}
	return agentID
}

func channelNameForTransportTest(t *testing.T, channelID string) string {
	t.Helper()
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT name FROM channel WHERE id = $1`, channelID).Scan(&name); err != nil {
		t.Fatalf("load channel name: %v", err)
	}
	return name
}

func userHandleForTransportTest(t *testing.T, userID string) string {
	t.Helper()
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT name FROM "user" WHERE id = $1`, userID).Scan(&name); err != nil {
		t.Fatalf("load user name: %v", err)
	}
	return name
}

func agentHandleForTransportTest(t *testing.T, agentID string) string {
	t.Helper()
	var name string
	if err := testPool.QueryRow(context.Background(), `
		SELECT name
		FROM agent
		WHERE id = $1`, agentID).Scan(&name); err != nil {
		t.Fatalf("load agent name: %v", err)
	}
	return name
}

func seedWorkspaceUserForTransportTargetTest(t *testing.T, name string) string {
	t.Helper()
	var userID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO "user" (name, display_name, email)
		VALUES ($1, $2, $3)
		RETURNING id`,
		name, name, name+"@example.test").Scan(&userID); err != nil {
		t.Fatalf("seed user %s: %v", name, err)
	}
	t.Cleanup(func() { testPool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID) })
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'member')`, testWorkspaceID, userID); err != nil {
		t.Fatalf("seed workspace member %s: %v", name, err)
	}
	return userID
}

func seedUnboundAgentAttachmentForTest(t *testing.T, agentID, filename string) string {
	t.Helper()
	var attachmentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, 'agent', $2, $3, 's3://'||$3, 'image/png', 42)
		RETURNING id`,
		testWorkspaceID, agentID, filename).Scan(&attachmentID); err != nil {
		t.Fatalf("seed unbound agent attachment: %v", err)
	}
	return attachmentID
}

func seedChannelPreboundAgentAttachmentForTest(t *testing.T, agentID, channelID, filename string) string {
	t.Helper()
	var attachmentID string
	if err := testPool.QueryRow(context.Background(), `
		INSERT INTO attachment (workspace_id, channel_id, uploader_type, uploader_id, filename, url, content_type, size_bytes)
		VALUES ($1, $2, 'agent', $3, $4, 's3://'||$4, 'image/png', 42)
		RETURNING id`,
		testWorkspaceID, channelID, agentID, filename).Scan(&attachmentID); err != nil {
		t.Fatalf("seed channel-prebound agent attachment: %v", err)
	}
	return attachmentID
}

func assertAgentTransportAuditCount(t *testing.T, taskID, action string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_transport_audit
		WHERE task_id = $1 AND action = $2`, taskID, action).Scan(&got); err != nil {
		t.Fatalf("count transport audit %s: %v", action, err)
	}
	if got != want {
		t.Fatalf("transport audit %s count=%d, want %d", action, got, want)
	}
}

func assertAgentTransportVisibleOutputAuditCount(t *testing.T, taskID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_task_transport_audit
		WHERE task_id = $1 AND action IN ('message_send', 'message_react') AND channel_message_id IS NOT NULL`, taskID).Scan(&got); err != nil {
		t.Fatalf("count visible transport audit: %v", err)
	}
	if got != want {
		t.Fatalf("visible transport audit count=%d, want %d", got, want)
	}
}

func assertAgentTransportDraftContent(t *testing.T, agentID, target, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(context.Background(), `
		SELECT content
		FROM agent_transport_draft
		WHERE workspace_id = $1 AND agent_id = $2 AND target = $3`,
		testWorkspaceID, agentID, target).Scan(&got); err != nil {
		t.Fatalf("load transport draft: %v", err)
	}
	if got != want {
		t.Fatalf("draft content = %q, want %q", got, want)
	}
}

func assertAgentTransportDraftMissing(t *testing.T, agentID, target string) {
	t.Helper()
	var exists bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT EXISTS (
			SELECT 1
			FROM agent_transport_draft
			WHERE workspace_id = $1 AND agent_id = $2 AND target = $3
		)`, testWorkspaceID, agentID, target).Scan(&exists); err != nil {
		t.Fatalf("check transport draft: %v", err)
	}
	if exists {
		t.Fatalf("transport draft still exists for agent=%s target=%s", agentID, target)
	}
}

func assertAgentTransportFreshnessHoldActivity(t *testing.T, taskID, target string, newMessages int) {
	t.Helper()
	var holdCount, obsoleteDetailCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE task_id = $1
		  AND event_type = 'send_freshness_hold'
		  AND message = 'Send held by freshness check'
		  AND target_slug = $2
		  AND details->>'new_message_count' = $3
		  AND details->>'decision' = 'local_hold'
		  AND details->>'recommended_action' = 'review_newer_messages'`, taskID, target, fmt.Sprint(newMessages)).Scan(&holdCount); err != nil {
		t.Fatalf("count freshness hold activity: %v", err)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE task_id = $1
		  AND event_type = 'send_freshness_hold_detail'`, taskID).Scan(&obsoleteDetailCount); err != nil {
		t.Fatalf("count obsolete freshness hold detail activity: %v", err)
	}
	if holdCount != 1 || obsoleteDetailCount != 0 {
		t.Fatalf("freshness hold activity=%d obsolete detail=%d, want 1/0", holdCount, obsoleteDetailCount)
	}
}

func assertAgentTransportFreshnessResolutionActivity(t *testing.T, taskID, target, producerFactID, outcome string) {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE task_id = $1
		  AND event_type = 'send_freshness_resolved'
		  AND message = 'Freshness hold resolved'
		  AND target_slug = $2
		  AND details->>'producer_fact_id' = $3
		  AND details->>'outcome' = $4
		  AND details ? 'freshness_hold_resolution_seconds'
		  AND details ? 'resolution_ms'`,
		taskID, target, producerFactID, outcome).Scan(&count); err != nil {
		t.Fatalf("count freshness resolution activity: %v", err)
	}
	if count != 1 {
		t.Fatalf("freshness resolution activity count=%d, want 1 for fact=%s outcome=%s", count, producerFactID, outcome)
	}
}

func assertAgentMessageSentActivityText(t *testing.T, messageID, want string) {
	t.Helper()
	var got string
	if err := testPool.QueryRow(context.Background(), `
		SELECT message
		FROM agent_activity_event
		WHERE event_type = 'message_sent'
		  AND details->>'message_id' = $1
		ORDER BY created_at DESC
		LIMIT 1`, messageID).Scan(&got); err != nil {
		t.Fatalf("load message_sent activity for message %s: %v", messageID, err)
	}
	if got != want {
		t.Fatalf("message_sent activity text = %q, want %q", got, want)
	}
	if strings.Contains(got, "Agent sent a visible message") {
		t.Fatalf("message_sent activity leaked legacy machine phrase: %q", got)
	}
}

func assertAgentMessageSentActivityCount(t *testing.T, messageID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_activity_event
		WHERE event_type = 'message_sent'
		  AND details->>'message_id' = $1`, messageID).Scan(&got); err != nil {
		t.Fatalf("count message_sent activity for message %s: %v", messageID, err)
	}
	if got != want {
		t.Fatalf("message_sent activity count for message %s = %d, want %d", messageID, got, want)
	}
}

func transportMessagesContain(messages []ChannelMessageResponse, id, content string) bool {
	for _, msg := range messages {
		if msg.ID == id && msg.Content == content {
			return true
		}
	}
	return false
}

func transportMessagesContainType(messages []ChannelMessageResponse, id, content, typ string) bool {
	for _, msg := range messages {
		if msg.ID == id && msg.Content == content && msg.Type == typ {
			return true
		}
	}
	return false
}

func transportSearchResultsContain(results []ChannelMessageSearchResult, id, content string) bool {
	for _, result := range results {
		if result.MessageID == id && result.Content == content {
			return true
		}
	}
	return false
}

// TestRunAfterChannelMessageAckAsyncDoesNotBlock documents the LRM-272/297
// contract: when SyncChannelMessageSideEffects is false, the caller proceeds
// while post-ack fanout is still blocked.
func TestRunAfterChannelMessageAckAsyncDoesNotBlock(t *testing.T) {
	h := &Handler{SyncChannelMessageSideEffects: false}
	entered := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})

	start := time.Now()
	h.runAfterChannelMessageAck(context.Background(), func(context.Context) {
		close(entered)
		<-release
		close(finished)
	})
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("runAfterChannelMessageAck blocked for %v", elapsed)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("async post-ack side effect did not start")
	}

	select {
	case <-finished:
		t.Fatal("async post-ack finished before release")
	default:
	}

	close(release)
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("async post-ack did not finish after release")
	}
}

func TestRunAfterChannelMessageAckSyncRunsInline(t *testing.T) {
	h := &Handler{SyncChannelMessageSideEffects: true}
	order := make([]string, 0, 2)
	h.runAfterChannelMessageAck(context.Background(), func(context.Context) {
		order = append(order, "side")
	})
	order = append(order, "after")
	if len(order) != 2 || order[0] != "side" || order[1] != "after" {
		t.Fatalf("order = %v, want [side after]", order)
	}
}

func TestAgentTransportSendReturnsWhilePostAckBlocked(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	prevSync := testHandler.SyncChannelMessageSideEffects
	testHandler.SyncChannelMessageSideEffects = false
	defer func() { testHandler.SyncChannelMessageSideEffects = prevSync }()

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	prevHook := testHandler.channelMessagePostAckTestHook
	testHandler.channelMessagePostAckTestHook = func(context.Context) {
		once.Do(func() { close(entered) })
		<-release
	}
	defer func() {
		testHandler.channelMessagePostAckTestHook = prevHook
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	start := time.Now()
	rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "lrm-297 ack-before-fanout " + uuid.NewString(),
		"client_message_id": "lrm297-" + uuid.NewString(),
	})
	elapsed := time.Since(start)
	if rec.Code != http.StatusCreated {
		t.Fatalf("transport send: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if elapsed > 2*time.Second {
		t.Fatalf("agent transport send blocked on post-ack for %v (channel=%s)", elapsed, channelID)
	}

	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("post-ack side effect did not start after agent send")
	}
}
