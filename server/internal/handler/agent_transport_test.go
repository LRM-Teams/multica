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
	"github.com/multica-ai/multica/server/internal/events"
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
	if got := strings.Join(held.AvailableActions, ","); got != "review_newer_messages" {
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
	if replacedBody.Message.Content != revised || replacedBody.FreshnessResolution != nil {
		t.Fatalf("fresh message must not resolve or publish the held draft: %+v", replacedBody)
	}
	assertAgentTransportDraftMissing(t, agentID, target)
	assertAgentTransportFreshnessHoldActivity(t, taskID, target, 1)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
}

func TestAgentTransportFreshnessIgnoresOwnVisibleProgressMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seen, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "request before progress "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed seen message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentID), "Agent", "visible progress "+uuid.NewString(), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed own progress message: %v", err)
	}
	finalContent := "final after own progress " + uuid.NewString()
	rec := agentTransportSendForTest(t, taskID, agentID, map[string]any{"target": target, "content": finalContent, "client_message_id": "after-progress-" + uuid.NewString(), "seen_up_to_seq": seen.Seq})
	if rec.Code != http.StatusCreated {
		t.Fatalf("send after own progress: status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertChannelMessageContentCount(t, channelID, finalContent, 1)
	assertAgentTransportVisibleOutputAuditCount(t, taskID, 1)
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
	if got := strings.Join(body.AvailableActions, ","); got != "review_newer_messages" {
		t.Fatalf("bounded available actions=%q", got)
	}
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

func TestAgentTransportSendMessageRejectsAgentControlledPartsAndAttachmentOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	parts := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            target,
		"content":           "arbitrary parts are not allowed",
		"parts":             []protocol.MessagePart{{Type: protocol.MessagePartTypeSticker, StickerID: "hi"}},
		"client_message_id": "transport-parts-rejected-" + uuid.NewString(),
	})
	if parts.Code != http.StatusBadRequest {
		t.Fatalf("agent-controlled parts: status=%d body=%s", parts.Code, parts.Body.String())
	}
	attachmentOnly := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "attachment_ids": []string{uuid.NewString()},
		"client_message_id": "transport-attachment-only-rejected-" + uuid.NewString(),
	})
	if attachmentOnly.Code != http.StatusBadRequest {
		t.Fatalf("attachment-only transport send: status=%d body=%s", attachmentOnly.Code, attachmentOnly.Body.String())
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
	seedCompletedAgentAttachmentUploadSessionForTest(t, agentID, channelID, "", "channel:"+channelID, ownedAttachmentID)
	otherAgentID := uuid.NewString()
	foreignAttachmentID := seedUnboundAgentAttachmentForTest(t, otherAgentID, "not-mine.png")

	clientID := "transport-attachment-" + uuid.NewString()
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": "here's the file", "attachment_ids": []string{ownedAttachmentID},
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
	seedCompletedAgentAttachmentUploadSessionForTest(t, agentID, channelID, "", "channel:"+channelID, ownedAttachmentID)
	foreignAttachmentID := seedUnboundAgentAttachmentForTest(t, uuid.NewString(), "foreign.png")
	clientID := "transport-mixed-foreign-" + uuid.NewString()

	response := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": "#" + channelNameForTransportTest(t, channelID), "content": "must stay atomic",
		"attachment_ids":    []string{ownedAttachmentID, foreignAttachmentID},
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

func TestAgentTransportSendMessageRejectsAttachmentSessionForAnotherTarget(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, groupChannelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	attachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "reuse-across-conversations.png")
	seedCompletedAgentAttachmentUploadSessionForTest(t, agentID, groupChannelID, "", "channel:"+groupChannelID, attachmentID)

	groupResponse := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": "#" + channelNameForTransportTest(t, groupChannelID), "content": "group copy",
		"attachment_ids":    []string{attachmentID},
		"client_message_id": "transport-group-reuse-" + uuid.NewString(),
	})
	if groupResponse.Code != http.StatusCreated {
		t.Fatalf("send shared attachment to group: status=%d body=%s", groupResponse.Code, groupResponse.Body.String())
	}

	humanHandle := userHandleForTransportTest(t, testUserID)
	dmClientID := "transport-dm-reuse-" + uuid.NewString()
	dmResponse := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle, "content": "dm copy", "attachment_ids": []string{attachmentID},
		"client_message_id": dmClientID,
	})
	if dmResponse.Code != http.StatusBadRequest {
		t.Fatalf("send attachment session to another target: status=%d body=%s", dmResponse.Code, dmResponse.Body.String())
	}

	var groupBody AgentTransportSendResponse
	if err := json.Unmarshal(groupResponse.Body.Bytes(), &groupBody); err != nil {
		t.Fatalf("decode group send: %v", err)
	}
	if len(groupBody.Message.Attachments) != 1 || groupBody.Message.Attachments[0].ID != attachmentID {
		t.Fatalf("group message attachments=%+v, want %s", groupBody.Message.Attachments, attachmentID)
	}

	var dmMessages int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message WHERE client_message_id = $1`, dmClientID).Scan(&dmMessages); err != nil {
		t.Fatalf("count rejected DM message: %v", err)
	}
	if dmMessages != 0 {
		t.Fatalf("rejected DM send left %d messages", dmMessages)
	}

	expiredAttachmentID := seedUnboundAgentAttachmentForTest(t, agentID, "expired-session.png")
	seedCompletedAgentAttachmentUploadSessionForTest(t, agentID, groupChannelID, "", "channel:"+groupChannelID, expiredAttachmentID)
	if _, err := testPool.Exec(ctx, `
		UPDATE agent_attachment_upload_session
		SET created_at = now() - interval '2 hours',
		    expires_at = now() - interval '1 hour'
		WHERE attachment_id = $1`, expiredAttachmentID); err != nil {
		t.Fatalf("expire attachment upload session: %v", err)
	}
	expiredClientID := "transport-expired-session-" + uuid.NewString()
	expiredResponse := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": "#" + channelNameForTransportTest(t, groupChannelID), "content": "expired copy",
		"attachment_ids": []string{expiredAttachmentID}, "client_message_id": expiredClientID,
	})
	if expiredResponse.Code != http.StatusBadRequest {
		t.Fatalf("send expired attachment session: status=%d body=%s", expiredResponse.Code, expiredResponse.Body.String())
	}
	var expiredMessages int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_message WHERE client_message_id = $1`, expiredClientID).Scan(&expiredMessages); err != nil {
		t.Fatalf("count expired-session messages: %v", err)
	}
	if expiredMessages != 0 {
		t.Fatalf("expired attachment session left %d messages", expiredMessages)
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
	seedCompletedAgentAttachmentUploadSessionForTest(t, agentID, channelID, "", "channel:"+channelID, attachmentID)
	if _, err := testPool.Exec(ctx, `
		UPDATE attachment
		SET content_type = 'audio/mpeg'
		WHERE id = $1`, attachmentID); err != nil {
		t.Fatalf("mark attachment as audio: %v", err)
	}

	response := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": "你好～", "attachment_ids": []string{attachmentID},
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
	seedCompletedAgentAttachmentUploadSessionForTest(t, agentID, channelID, "", "channel:"+channelID, preboundID)

	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": "prebound shot", "attachment_ids": []string{preboundID},
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

func TestAgentTransportSendMessageRejectsAttachmentOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	resp := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "attachment_ids": []string{uuid.NewString()},
		"client_message_id": "transport-attachment-only-" + uuid.NewString(),
	})
	if resp.Code != http.StatusBadRequest {
		t.Fatalf("attachment-only transport send: status=%d body=%s", resp.Code, resp.Body.String())
	}
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

	reactRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": seeded.ID,
		"emoji":      "+1",
	})
	if reactRec.Code != http.StatusOK {
		t.Fatalf("transport react: status=%d body=%s", reactRec.Code, reactRec.Body.String())
	}
	var reactBody AgentTransportReactResponse
	if err := json.Unmarshal(reactRec.Body.Bytes(), &reactBody); err != nil {
		t.Fatalf("decode transport react: %v", err)
	}
	if !reactBody.Added || reactBody.Reaction == nil || reactBody.Reaction.MessageID != seeded.ID || reactBody.Reaction.ActorID != agentID || reactBody.Reaction.Emoji != "👍" {
		t.Fatalf("reaction payload mismatch: %+v", reactBody)
	}

	var reactionRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '👍'`,
		seeded.ID, agentID).Scan(&reactionRows); err != nil {
		t.Fatalf("count agent reaction: %v", err)
	}
	if reactionRows != 1 {
		t.Fatalf("agent reaction rows=%d, want 1", reactionRows)
	}
	assertAgentTransportAuditCount(t, taskID, agentTransportActionRead, 1)
	assertAgentTransportAuditCount(t, taskID, agentTransportActionSearch, 1)
}

func TestAgentTransportResolveReturnsOneVisibleCanonicalMessage(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	parts := []protocol.MessagePart{
		{Type: protocol.MessagePartTypeText, Text: "resolve exact content"},
		{Type: protocol.MessagePartTypeSticker, StickerID: "got-it"},
	}
	resolved, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "resolve exact content", parts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed resolvable message: %v", err)
	}
	if _, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "resolve neighboring message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0); err != nil {
		t.Fatalf("seed neighboring message: %v", err)
	}

	fullRec := agentTransportResolveForTest(t, taskID, agentID, map[string]any{"message_id": resolved.ID})
	if fullRec.Code != http.StatusOK {
		t.Fatalf("resolve full id: status=%d body=%s", fullRec.Code, fullRec.Body.String())
	}
	var fullWire map[string]any
	if err := json.Unmarshal(fullRec.Body.Bytes(), &fullWire); err != nil {
		t.Fatalf("decode resolve wire response: %v", err)
	}
	for _, forbidden := range []string{"messages", "results", "total", "limit"} {
		if _, found := fullWire[forbidden]; found {
			t.Fatalf("exact resolve must not return history/search field %q: %#v", forbidden, fullWire)
		}
	}
	var full AgentTransportResolveResponse
	if err := json.Unmarshal(fullRec.Body.Bytes(), &full); err != nil {
		t.Fatalf("decode full resolve: %v", err)
	}
	if full.Action != agentTransportActionResolve || full.Message.ID != resolved.ID || full.Message.ChannelID != channelID || full.Message.Seq != resolved.Seq || full.Message.Type != "user" || full.Message.AuthorID == nil || *full.Message.AuthorID != testUserID || full.Message.Content != "resolve exact content" {
		t.Fatalf("full resolve = %+v, want exact canonical message", full)
	}
	if full.Target.ChannelID != channelID || full.Target.ThreadRootMessageID != nil {
		t.Fatalf("resolve target = %+v, want main-channel identity", full.Target)
	}
	if len(full.Message.Parts) != len(parts) || full.Message.Parts[0].Text != parts[0].Text || full.Message.Parts[1].StickerID != parts[1].StickerID {
		t.Fatalf("resolve parts = %+v, want %+v", full.Message.Parts, parts)
	}

	shortRec := agentTransportResolveForTest(t, taskID, agentID, map[string]any{"message_id": resolved.ID[:8]})
	if shortRec.Code != http.StatusOK {
		t.Fatalf("resolve short id: status=%d body=%s", shortRec.Code, shortRec.Body.String())
	}
	var short AgentTransportResolveResponse
	if err := json.Unmarshal(shortRec.Body.Bytes(), &short); err != nil {
		t.Fatalf("decode short resolve: %v", err)
	}
	if short.Message.ID != resolved.ID {
		t.Fatalf("short resolve id = %q, want %q", short.Message.ID, resolved.ID)
	}

	ambiguousPrefix := ""
	for range 16 {
		candidate := uuid.NewString()[:4]
		var collisions int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_message WHERE workspace_id = $1 AND replace(id::text, '-', '') LIKE $2 || '%'`, testWorkspaceID, candidate).Scan(&collisions); err != nil {
			t.Fatalf("check ambiguous prefix collision: %v", err)
		}
		if collisions == 0 {
			ambiguousPrefix = candidate
			break
		}
	}
	if ambiguousPrefix == "" {
		t.Fatal("could not allocate an unambiguous test prefix")
	}
	for _, id := range []string{
		ambiguousPrefix + "0000-0000-4000-8000-000000000001",
		ambiguousPrefix + "0000-0000-4000-8000-000000000002",
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_message (id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source)
			VALUES ($1, $2, $3, 'user', $4, 'Tester', 'ambiguous resolve message', '[]'::jsonb, 'multica')`,
			parseUUID(id), parseUUID(channelID), parseUUID(testWorkspaceID), parseUUID(testUserID)); err != nil {
			t.Fatalf("seed ambiguous message %s: %v", id, err)
		}
	}
	ambiguousRec := agentTransportResolveForTest(t, taskID, agentID, map[string]any{"message_id": ambiguousPrefix})
	if ambiguousRec.Code != http.StatusConflict || !strings.Contains(ambiguousRec.Body.String(), "ambiguous") {
		t.Fatalf("resolve ambiguous short id: status=%d body=%s", ambiguousRec.Code, ambiguousRec.Body.String())
	}

	hiddenChannelID := seedChannelForTest(t, "hidden-resolve-"+uuid.NewString()[:8], testUserID)
	hidden, err := testHandler.insertChannelMessage(ctx, parseUUID(hiddenChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hidden resolve content", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed hidden message: %v", err)
	}
	hiddenRec := agentTransportResolveForTest(t, taskID, agentID, map[string]any{"message_id": hidden.ID})
	missingRec := agentTransportResolveForTest(t, taskID, agentID, map[string]any{"message_id": uuid.NewString()})
	if hiddenRec.Code != http.StatusNotFound || missingRec.Code != http.StatusNotFound || hiddenRec.Body.String() != missingRec.Body.String() {
		t.Fatalf("unauthorized/missing resolve must share one response: hidden=%d/%s missing=%d/%s", hiddenRec.Code, hiddenRec.Body.String(), missingRec.Code, missingRec.Body.String())
	}
	invalidRec := agentTransportResolveForTest(t, taskID, agentID, map[string]any{"message_id": "not-a-message-id"})
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("resolve malformed id: status=%d body=%s", invalidRec.Code, invalidRec.Body.String())
	}

	var auditCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_task_transport_audit WHERE task_id = $1`, taskID).Scan(&auditCount); err != nil {
		t.Fatalf("count resolve transport side effects: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("exact resolve must not write transport/context state; audit rows=%d", auditCount)
	}
}

func TestNormalizeAgentTransportReactionEmoji(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "trim and normalize plus one", input: " +1 ", want: "👍"},
		{name: "text variation becomes emoji variation", input: "❤︎", want: "❤️"},
		{name: "skin tone modifier", input: "👍🏽", want: "👍🏽"},
		{name: "joined emoji", input: "🏳️‍🌈", want: "🏳️‍🌈"},
		{name: "keycap", input: "1️⃣", want: "1️⃣"},
		{name: "plain text", input: "ack", wantErr: true},
		{name: "two reactions", input: "👍👎", wantErr: true},
		{name: "empty", input: "  ", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeAgentTransportReactionEmoji(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("normalize(%q) = %q, want error", tt.input, got)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("normalize(%q) = %q, %v; want %q, nil", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestAgentTransportReactUsesCanonicalIdentityIdempotently(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "react by exact canonical identity", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed reaction message: %v", err)
	}

	var addedEvents, removedEvents []events.Event
	testHandler.Bus.Subscribe(protocol.EventChannelReactionAdded, func(event events.Event) {
		payload, ok := event.Payload.(map[string]any)
		if ok && payload["message_id"] == message.ID {
			addedEvents = append(addedEvents, event)
		}
	})
	testHandler.Bus.Subscribe(protocol.EventChannelReactionRemoved, func(event events.Event) {
		payload, ok := event.Payload.(map[string]any)
		if ok && payload["message_id"] == message.ID {
			removedEvents = append(removedEvents, event)
		}
	})

	invalidRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": message.ID,
		"emoji":      "not an emoji",
	})
	if invalidRec.Code != http.StatusBadRequest {
		t.Fatalf("invalid emoji: status=%d body=%s", invalidRec.Code, invalidRec.Body.String())
	}
	targetRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": message.ID,
		"emoji":      "👍",
		"target":     "#must-not-be-accepted",
	})
	if targetRec.Code != http.StatusBadRequest {
		t.Fatalf("target-bearing reaction must be rejected: status=%d body=%s", targetRec.Code, targetRec.Body.String())
	}
	cursorRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id":        message.ID,
		"emoji":             "👍",
		"client_message_id": "must-not-be-accepted",
	})
	if cursorRec.Code != http.StatusBadRequest {
		t.Fatalf("cursor-bearing reaction must be rejected: status=%d body=%s", cursorRec.Code, cursorRec.Body.String())
	}

	addRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": message.ID[:8],
		"emoji":      "+1",
	})
	if addRec.Code != http.StatusOK {
		t.Fatalf("add by short id: status=%d body=%s", addRec.Code, addRec.Body.String())
	}
	var addedWire map[string]any
	if err := json.Unmarshal(addRec.Body.Bytes(), &addedWire); err != nil {
		t.Fatalf("decode added reaction wire response: %v", err)
	}
	for _, forbidden := range []string{"target", "transport_id", "client_message_id"} {
		if _, found := addedWire[forbidden]; found {
			t.Fatalf("canonical reaction must not expose %q: %#v", forbidden, addedWire)
		}
	}
	var added AgentTransportReactResponse
	if err := json.Unmarshal(addRec.Body.Bytes(), &added); err != nil {
		t.Fatalf("decode added reaction: %v", err)
	}
	if !added.Added || added.Removed || added.ChannelID != channelID || added.MessageID != message.ID || added.Emoji != "👍" || added.Reaction == nil || added.Reaction.MessageID != message.ID || added.Reaction.ActorID != agentID {
		t.Fatalf("added reaction response = %+v, want canonical added projection", added)
	}
	if len(addedEvents) != 1 || addedEvents[0].Type != protocol.EventChannelReactionAdded || addedEvents[0].WorkspaceID != testWorkspaceID || addedEvents[0].ActorType != "agent" || addedEvents[0].ActorID != agentID {
		t.Fatalf("added realtime events = %+v, want one canonical agent projection", addedEvents)
	}
	addedPayload := addedEvents[0].Payload.(map[string]any)
	if addedPayload["channel_id"] != channelID || addedPayload["message_id"] != message.ID {
		t.Fatalf("added realtime payload = %#v, want channel/message identity", addedPayload)
	}

	duplicateAddRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": message.ID,
		"emoji":      "👍",
	})
	if duplicateAddRec.Code != http.StatusOK {
		t.Fatalf("duplicate add: status=%d body=%s", duplicateAddRec.Code, duplicateAddRec.Body.String())
	}
	var duplicateAdded AgentTransportReactResponse
	if err := json.Unmarshal(duplicateAddRec.Body.Bytes(), &duplicateAdded); err != nil {
		t.Fatalf("decode duplicate added reaction: %v", err)
	}
	if duplicateAdded.Added || duplicateAdded.Reaction == nil || duplicateAdded.Reaction.ID != added.Reaction.ID || len(addedEvents) != 1 {
		t.Fatalf("duplicate add must be idempotent: response=%+v events=%+v", duplicateAdded, addedEvents)
	}

	removeRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": message.ID,
		"emoji":      "+1",
		"remove":     true,
	})
	if removeRec.Code != http.StatusOK {
		t.Fatalf("remove: status=%d body=%s", removeRec.Code, removeRec.Body.String())
	}
	var removed AgentTransportReactResponse
	if err := json.Unmarshal(removeRec.Body.Bytes(), &removed); err != nil {
		t.Fatalf("decode removed reaction: %v", err)
	}
	if removed.Added || !removed.Removed || removed.Reaction != nil || removed.ChannelID != channelID || removed.MessageID != message.ID || removed.Emoji != "👍" {
		t.Fatalf("removed reaction response = %+v, want canonical removal projection", removed)
	}
	if len(removedEvents) != 1 || removedEvents[0].Type != protocol.EventChannelReactionRemoved || removedEvents[0].WorkspaceID != testWorkspaceID || removedEvents[0].ActorType != "agent" || removedEvents[0].ActorID != agentID {
		t.Fatalf("removed realtime events = %+v, want one canonical agent projection", removedEvents)
	}
	removedPayload := removedEvents[0].Payload.(map[string]any)
	if removedPayload["channel_id"] != channelID || removedPayload["message_id"] != message.ID || removedPayload["emoji"] != "👍" || removedPayload["actor_type"] != "agent" || removedPayload["actor_id"] != agentID {
		t.Fatalf("removed realtime payload = %#v, want channel/message/actor identity", removedPayload)
	}

	duplicateRemoveRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": message.ID,
		"emoji":      "👍",
		"remove":     true,
	})
	if duplicateRemoveRec.Code != http.StatusOK {
		t.Fatalf("duplicate remove: status=%d body=%s", duplicateRemoveRec.Code, duplicateRemoveRec.Body.String())
	}
	var duplicateRemoved AgentTransportReactResponse
	if err := json.Unmarshal(duplicateRemoveRec.Body.Bytes(), &duplicateRemoved); err != nil {
		t.Fatalf("decode duplicate removed reaction: %v", err)
	}
	if duplicateRemoved.Removed || len(removedEvents) != 1 {
		t.Fatalf("duplicate remove must be idempotent: response=%+v events=%+v", duplicateRemoved, removedEvents)
	}

	var reactionRows, auditRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_reaction
		WHERE channel_message_id = $1 AND actor_type = 'agent' AND actor_id = $2 AND emoji = '👍'`, message.ID, agentID).Scan(&reactionRows); err != nil {
		t.Fatalf("count canonical reactions: %v", err)
	}
	if reactionRows != 0 {
		t.Fatalf("reaction rows after idempotent removal = %d, want 0", reactionRows)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_task_transport_audit WHERE task_id = $1`, taskID).Scan(&auditRows); err != nil {
		t.Fatalf("count react transport side effects: %v", err)
	}
	if auditRows != 0 {
		t.Fatalf("canonical reactions must not write transport audit rows; got %d", auditRows)
	}

	hiddenChannelID := seedChannelForTest(t, "hidden-react-"+uuid.NewString()[:8], testUserID)
	hidden, err := testHandler.insertChannelMessage(ctx, parseUUID(hiddenChannelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "hidden reaction message", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("seed hidden reaction message: %v", err)
	}
	hiddenRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{"message_id": hidden.ID, "emoji": "👍"})
	missingRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{"message_id": uuid.NewString(), "emoji": "👍"})
	if hiddenRec.Code != http.StatusNotFound || missingRec.Code != http.StatusNotFound || hiddenRec.Body.String() != missingRec.Body.String() {
		t.Fatalf("unauthorized/missing reaction must share one response: hidden=%d/%s missing=%d/%s", hiddenRec.Code, hiddenRec.Body.String(), missingRec.Code, missingRec.Body.String())
	}

	ambiguousPrefix := ""
	for range 16 {
		candidate := uuid.NewString()[:4]
		var collisions int
		if err := testPool.QueryRow(ctx, `SELECT count(*) FROM channel_message WHERE workspace_id = $1 AND replace(id::text, '-', '') LIKE $2 || '%'`, testWorkspaceID, candidate).Scan(&collisions); err != nil {
			t.Fatalf("check ambiguous reaction prefix collision: %v", err)
		}
		if collisions == 0 {
			ambiguousPrefix = candidate
			break
		}
	}
	if ambiguousPrefix == "" {
		t.Fatal("could not allocate an unambiguous reaction prefix")
	}
	for _, id := range []string{
		ambiguousPrefix + "0000-0000-4000-8000-000000000001",
		ambiguousPrefix + "0000-0000-4000-8000-000000000002",
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_message (id, channel_id, workspace_id, author_type, author_id, author_name, content, parts, source)
			VALUES ($1, $2, $3, 'user', $4, 'Tester', 'ambiguous reaction message', '[]'::jsonb, 'multica')`,
			parseUUID(id), parseUUID(channelID), parseUUID(testWorkspaceID), parseUUID(testUserID)); err != nil {
			t.Fatalf("seed ambiguous reaction message %s: %v", id, err)
		}
	}
	ambiguousRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{"message_id": ambiguousPrefix, "emoji": "👍"})
	if ambiguousRec.Code != http.StatusConflict || !strings.Contains(ambiguousRec.Body.String(), "ambiguous") {
		t.Fatalf("react ambiguous short id: status=%d body=%s", ambiguousRec.Code, ambiguousRec.Body.String())
	}
}

func TestAgentTransportTargetedCommandsRequireExplicitTarget(t *testing.T) {
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

	mainRead := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle, "limit": 5,
	})
	if mainRead.Code != http.StatusOK {
		t.Fatalf("read DM history: status=%d body=%s", mainRead.Code, mainRead.Body.String())
	}
	var mainReadBody AgentTransportReadResponse
	if err := json.Unmarshal(mainRead.Body.Bytes(), &mainReadBody); err != nil {
		t.Fatalf("decode DM history: %v", err)
	}
	if mainReadBody.ContextTarget != "channel:"+dmChannel.ID {
		t.Fatalf("DM context target = %q, want channel:%s", mainReadBody.ContextTarget, dmChannel.ID)
	}

	threadRead := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": "dm:@" + humanHandle + ":" + root.ID, "limit": 5,
	})
	if threadRead.Code != http.StatusOK {
		t.Fatalf("read DM thread history: status=%d body=%s", threadRead.Code, threadRead.Body.String())
	}
	var threadReadBody AgentTransportReadResponse
	if err := json.Unmarshal(threadRead.Body.Bytes(), &threadReadBody); err != nil {
		t.Fatalf("decode DM thread history: %v", err)
	}
	if threadReadBody.ContextTarget != "thread:"+root.ID {
		t.Fatalf("DM thread context target = %q, want thread:%s", threadReadBody.ContextTarget, root.ID)
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

func TestAgentTransportReadAnchorsUseCanonicalTargetWindows(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)
	seeded := make([]ChannelMessageResponse, 0, 4)
	for i := 1; i <= 4; i++ {
		message, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", fmt.Sprintf("anchored history %d %s", i, uuid.NewString()), "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
		if err != nil {
			t.Fatalf("seed message %d: %v", i, err)
		}
		seeded = append(seeded, message)
	}

	before := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": target, "before": seeded[2].ID, "limit": 2,
	})
	if before.Code != http.StatusOK {
		t.Fatalf("read before: status=%d body=%s", before.Code, before.Body.String())
	}
	var beforeBody AgentTransportReadResponse
	if err := json.Unmarshal(before.Body.Bytes(), &beforeBody); err != nil {
		t.Fatalf("decode before read: %v", err)
	}
	assertAgentTransportReadMessageIDs(t, beforeBody.Messages, seeded[0].ID, seeded[1].ID)
	if beforeBody.ContextTarget != "channel:"+channelID {
		t.Fatalf("context target = %q, want channel:%s", beforeBody.ContextTarget, channelID)
	}

	after := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": target, "after": seeded[0].ID[:8], "limit": 2,
	})
	if after.Code != http.StatusOK {
		t.Fatalf("read after short id: status=%d body=%s", after.Code, after.Body.String())
	}
	var afterBody AgentTransportReadResponse
	if err := json.Unmarshal(after.Body.Bytes(), &afterBody); err != nil {
		t.Fatalf("decode after read: %v", err)
	}
	assertAgentTransportReadMessageIDs(t, afterBody.Messages, seeded[1].ID, seeded[2].ID)

	around := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": target, "around": fmt.Sprint(seeded[2].Seq), "limit": 3,
	})
	if around.Code != http.StatusOK {
		t.Fatalf("read around sequence: status=%d body=%s", around.Code, around.Body.String())
	}
	var aroundBody AgentTransportReadResponse
	if err := json.Unmarshal(around.Body.Bytes(), &aroundBody); err != nil {
		t.Fatalf("decode around read: %v", err)
	}
	assertAgentTransportReadMessageIDs(t, aroundBody.Messages, seeded[1].ID, seeded[2].ID, seeded[3].ID)

	multipleAnchors := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": target, "before": seeded[2].ID, "after": seeded[2].ID,
	})
	if multipleAnchors.Code != http.StatusBadRequest {
		t.Fatalf("multiple anchors status=%d body=%s, want 400", multipleAnchors.Code, multipleAnchors.Body.String())
	}
	missingAnchor := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": target, "around": uuid.NewString(),
	})
	if missingAnchor.Code != http.StatusNotFound {
		t.Fatalf("missing anchor status=%d body=%s, want 404", missingAnchor.Code, missingAnchor.Body.String())
	}
	nonDecimalSequence := agentTransportReadForTest(t, taskID, agentID, map[string]any{
		"target": target, "around": "+7",
	})
	if nonDecimalSequence.Code != http.StatusBadRequest {
		t.Fatalf("non-decimal sequence status=%d body=%s, want 400", nonDecimalSequence.Code, nonDecimalSequence.Body.String())
	}
}

func assertAgentTransportReadMessageIDs(t *testing.T, messages []ChannelMessageResponse, want ...string) {
	t.Helper()
	if len(messages) != len(want) {
		t.Fatalf("message count = %d, want %d: %+v", len(messages), len(want), messages)
	}
	for i, message := range messages {
		if message.ID != want[i] {
			t.Fatalf("messages[%d] = %s, want %s (all=%+v)", i, message.ID, want[i], messages)
		}
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
	if readBody.ContextTarget != "thread:"+root.ID {
		t.Fatalf("thread context target = %q, want thread:%s", readBody.ContextTarget, root.ID)
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

func agentTransportResolveForTest(t *testing.T, taskID, agentID string, body map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/resolve", taskID, agentID, body)
	rec := httptest.NewRecorder()
	testHandler.AgentTransportResolveMessage(rec, req)
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

func seedCompletedAgentAttachmentUploadSessionForTest(t *testing.T, agentID, channelID, threadRootID, contextTarget, attachmentID string) {
	t.Helper()
	var threadRoot any
	if threadRootID != "" {
		threadRoot = threadRootID
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO agent_attachment_upload_session (
			id, workspace_id, agent_id, channel_id, thread_root_message_id,
			context_target, object_key, filename, content_type, size_bytes,
			checksum_sha256, upload_mode, state, expires_at, attachment_id, completed_at
		) VALUES (
			$1, $2, $3, $4, $5,
			$6, $7, 'test-upload.png', 'image/png', 42,
			repeat('0', 64), 'local', 'completed', now() + interval '15 minutes', $8, now()
		)`,
		uuid.NewString(), testWorkspaceID, agentID, channelID, threadRoot,
		contextTarget, "test-agent-upload-session/"+uuid.NewString(), attachmentID,
	); err != nil {
		t.Fatalf("seed completed attachment upload session: %v", err)
	}
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

func TestAgentTransportFreshnessResolutionActivityMessage(t *testing.T) {
	for _, tc := range []struct {
		outcome string
		want    string
	}{
		{outcome: "abandoned", want: "Held message was not sent"},
		{outcome: "revised_send", want: "Freshness hold resolved"},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			if got := agentTransportFreshnessResolutionActivityMessage(tc.outcome); got != tc.want {
				t.Fatalf("message = %q, want %q", got, tc.want)
			}
		})
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
		  AND message = CASE
		    WHEN $4 = 'abandoned' THEN 'Held message was not sent'
		    ELSE 'Freshness hold resolved'
		  END
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
