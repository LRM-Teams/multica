package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
)

// LRM-1081 / LRM-1079 P2: ordinary mention and reminder wakes are channel-only.

func TestChannelMentionEnqueueIsChannelOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "lrm-1081-mention-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentHandle, nil)
	channelID := seedChannelForTest(t, "lrm-1081-mention-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found")
	}
	triggerContent := "@" + agentHandle + " please review this"
	trigger, err := testHandler.insertChannelMessage(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID),
		"Tester", triggerContent, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("debate-thread"), 2,
	)
	if err != nil {
		t.Fatalf("insert trigger: %v", err)
	}

	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	var eventID string
	var chatSessionID pgtype.UUID
	var eventChannelID pgtype.UUID
	var rawContext []byte
	if err := testPool.QueryRow(ctx, `
		SELECT id::text, chat_session_id, channel_id, context
		FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND reason = 'mention'
		ORDER BY created_at DESC
		LIMIT 1`, channelID, agentID).Scan(&eventID, &chatSessionID, &eventChannelID, &rawContext); err != nil {
		t.Fatalf("load mention inbox event: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, eventID) })
	if chatSessionID.Valid {
		t.Fatal("mention wake must not create chat_session_id")
	}
	if uuidToString(eventChannelID) != channelID {
		t.Fatalf("channel_id=%q, want %q", uuidToString(eventChannelID), channelID)
	}
	prompt, ok := channelWakePromptFromContext(rawContext)
	if !ok {
		t.Fatalf("missing channel_wake context: %s", string(rawContext))
	}
	if !strings.Contains(prompt, triggerContent) {
		t.Fatalf("wake prompt missing trigger content:\n%s", prompt)
	}
	var wake channelWakeContext
	if err := json.Unmarshal(rawContext, &wake); err != nil {
		t.Fatalf("decode wake context: %v", err)
	}
	if wake.ThreadID != "debate-thread" || wake.TriggerDepth != 2 {
		t.Fatalf("wake thread/depth = %q/%d, want debate-thread/2", wake.ThreadID, wake.TriggerDepth)
	}
	var sessions int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_agent_session
		WHERE channel_id = $1 AND agent_id = $2`, channelID, agentID).Scan(&sessions); err != nil {
		t.Fatalf("count channel_agent_session: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("ordinary mention created %d channel_agent_session rows; want 0", sessions)
	}
}

func TestReminderFireEnqueueIsChannelOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire reminder: %v", err)
	}

	var chatSessionID pgtype.UUID
	var channelID pgtype.UUID
	var rawContext []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT event.chat_session_id, event.channel_id, event.context
		FROM agent_reminder_occurrence occurrence
		JOIN agent_inbox_event event ON event.id = occurrence.fired_task_id
		WHERE occurrence.reminder_id = $1`, reminderID,
	).Scan(&chatSessionID, &channelID, &rawContext); err != nil {
		t.Fatalf("load fired reminder task: %v", err)
	}
	if chatSessionID.Valid {
		t.Fatal("reminder wake must not create chat_session_id")
	}
	if uuidToString(channelID) != fixture.channel.ID {
		t.Fatalf("channel_id=%q, want %q", uuidToString(channelID), fixture.channel.ID)
	}
	prompt, ok := channelWakePromptFromContext(rawContext)
	if !ok {
		t.Fatalf("missing channel_wake context: %s", string(rawContext))
	}
	for _, want := range []string{
		"A self-scheduled reminder is due.",
		"Reminder id: " + reminderID,
		"msg-id: " + anchor.ID,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("reminder prompt missing %q:\n%s", want, prompt)
		}
	}
}
