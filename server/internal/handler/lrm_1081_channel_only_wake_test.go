package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1081 / #2295: ordinary mention traffic is delivery-only (no task-shaped wake).

func TestChannelMentionEnqueueIsDeliveryOnly(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentHandle := "lrm-1081-mention-" + uuid.NewString()[:8]
	// createHandlerTestAgent binds a runtime so canonical delivery can insert.
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

	// Publish path schedules canonical delivery; post-ack fanout must not mint wakes.
	testHandler.publishChannelToMembers(ctx, protocol.EventChannelMessage, testWorkspaceID, "member", testUserID, parseUUID(channelID), trigger)
	testHandler.dispatchChannelMentions(ctx, ch, trigger, parseUUID(testUserID))

	var wakeCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2
		  AND reason IN ('mention', 'channel_message', 'thread_reply', 'ambient', 'dm')`,
		channelID, agentID).Scan(&wakeCount); err != nil {
		t.Fatalf("count inbox wakes: %v", err)
	}
	if wakeCount != 0 {
		t.Fatalf("ordinary mention created %d task-shaped inbox wakes; want 0 (delivery-only)", wakeCount)
	}

	var deliveryCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id = $2`, agentID, parseUUID(trigger.ID)).Scan(&deliveryCount); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveryCount != 1 {
		t.Fatalf("canonical delivery count = %d, want 1", deliveryCount)
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

func TestReminderFireIsTransientOwnerInputWithoutMessageOrInboxWake(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	fixture := newChannelAgentRuntimeFixture(t, []channelAgentRuntimeSpec{{}})
	anchor := fixture.insertMessage(t, "user", testUserID, "anchor", nil)
	reminderID := seedDueReminder(t, fixture.agentIDs[0], fixture.channel.ID, anchor.ID, "", "")
	notifier := &capturedReminderOwnerInputNotifier{}
	fixture.handler.ReminderOwnerInputNotifier = notifier
	var messagesBefore, deliveriesBefore, wakesBefore int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_message WHERE channel_id = $1),
		  (SELECT count(*) FROM agent_message_delivery WHERE agent_id = $2),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $2 AND requires_wake = true)`,
		fixture.channel.ID, fixture.agentIDs[0]).Scan(&messagesBefore, &deliveriesBefore, &wakesBefore); err != nil {
		t.Fatal(err)
	}
	if err := fireReminderAttempt(fixture.handler, reminderID); err != nil {
		t.Fatalf("fire reminder: %v", err)
	}

	var messagesAfter, deliveriesAfter, wakesAfter int
	if err := testPool.QueryRow(context.Background(), `
		SELECT
		  (SELECT count(*) FROM channel_message WHERE channel_id = $1),
		  (SELECT count(*) FROM agent_message_delivery WHERE agent_id = $2),
		  (SELECT count(*) FROM agent_inbox_event WHERE agent_id = $2 AND requires_wake = true)`,
		fixture.channel.ID, fixture.agentIDs[0]).Scan(&messagesAfter, &deliveriesAfter, &wakesAfter); err != nil {
		t.Fatal(err)
	}
	if messagesAfter != messagesBefore || deliveriesAfter != deliveriesBefore || wakesAfter != wakesBefore {
		t.Fatalf("Reminder fire changed Message/delivery/inbox counts %d/%d/%d -> %d/%d/%d",
			messagesBefore, deliveriesBefore, wakesBefore, messagesAfter, deliveriesAfter, wakesAfter)
	}
	calls := notifier.snapshot()
	if len(calls) != 1 || calls[0].payload.ReminderID != reminderID {
		t.Fatalf("transient owner inputs=%+v, want one for %s", calls, reminderID)
	}
}
