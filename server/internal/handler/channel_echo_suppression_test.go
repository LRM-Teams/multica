package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1523 echo suppression: an agent-authored pure confirmation (收到/明白/OK)
// that @-mentions another agent must NOT create a wake-required run for that
// agent. Only concrete new content should wake a mentioned agent.
func TestAgentPureConfirmationDoesNotWakeMentionedAgent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	suffix := uuid.NewString()[:8]
	agentA := createHandlerTestAgent(t, "echo-a-"+suffix, nil)
	agentB := createHandlerTestAgent(t, "echo-b-"+suffix, nil)
	channelID := seedChannelForTest(t, "echo-"+suffix, testUserID)
	for _, agentID := range []string{agentA, agentB} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
			VALUES ($1, $2, 'agent', $3)
			ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
			t.Fatalf("seed agent member: %v", err)
		}
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found")
	}

	mention := protocol.MessagePart{
		Type:       protocol.MessagePartTypeReference,
		RefType:    "mention",
		RefSubType: "agent",
		RefID:      agentB,
		Label:      "@echo-b-" + suffix,
	}
	mentionMarkdown := "[@echo-b-" + suffix + "](mention://agent/" + agentB + ")"

	// 1) Pure confirmation by agent A mentioning agent B -> no wake for B.
	pureTrigger, err := testHandler.insertChannelMessageWithParts(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentA),
		"Agent A", mentionMarkdown+" 收到", []protocol.MessagePart{mention}, "multica",
		nil, pgtype.UUID{}, pgtype.UUID{}, nil, 2,
	)
	if err != nil {
		t.Fatalf("insert pure confirmation trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, pureTrigger, parseUUID(testUserID))
	if wakeCount, err := countChannelWakeEvents(ctx, channelID, agentB); err != nil {
		t.Fatalf("count wake events (pure ack): %v", err)
	} else if wakeCount != 0 {
		t.Fatalf("pure confirmation created %d wake-required events for mentioned agent; want 0 (echo)", wakeCount)
	}

	// 2) Control: agent A sends real content mentioning agent B -> B must be woken.
	realTrigger, err := testHandler.insertChannelMessageWithParts(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentA),
		"Agent A", mentionMarkdown+" 请你 review 一下这个 PR，方案在这里：xxx",
		[]protocol.MessagePart{mention}, "multica",
		nil, pgtype.UUID{}, pgtype.UUID{}, nil, 2,
	)
	if err != nil {
		t.Fatalf("insert real content trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, realTrigger, parseUUID(testUserID))
	if wakeCount, err := countChannelWakeEvents(ctx, channelID, agentB); err != nil {
		t.Fatalf("count wake events (real content): %v", err)
	} else if wakeCount == 0 {
		t.Fatal("real @content did not wake mentioned agent; suppression is over-eager")
	}

	// Cleanup: remove any wake events created by the control case.
	var eventIDs []string
	rows, err := testPool.Query(ctx, `
		SELECT id::text FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake = true`, channelID, agentB)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			if rows.Scan(&id) == nil {
				eventIDs = append(eventIDs, id)
			}
		}
	}
	for _, id := range eventIDs {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_inbox_event WHERE id = $1`, id)
	}
}

func countChannelWakeEvents(ctx context.Context, channelID, agentID string) (int, error) {
	var count int
	err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event
		WHERE channel_id = $1 AND agent_id = $2 AND requires_wake = true`, channelID, agentID).Scan(&count)
	return count, err
}

// TestChannelMessageKindPersistence (LRM-1523 L1) verifies the structured kind
// is derived and persisted: an agent-authored pure acknowledgement is stored
// with kind='confirmation' and surfaces on the insert result, while real agent
// content and any human message are stored as 'content'. The persisted kind is
// what the dispatch path then uses to enforce confirmation no-wake structurally.
func TestChannelMessageKindPersistence(t *testing.T) {
	// Unit: kind derivation.
	if got := channelMessageKindFor("agent", "收到", nil); got != protocol.ChannelMessageKindConfirmation {
		t.Fatalf("agent pure confirmation kind=%q want confirmation", got)
	}
	if got := channelMessageKindFor("agent", "请你把接口改一下然后提 PR", nil); got != protocol.ChannelMessageKindContent {
		t.Fatalf("agent actionable content kind=%q want content", got)
	}
	if got := channelMessageKindFor("user", "收到", nil); got != protocol.ChannelMessageKindContent {
		t.Fatalf("human confirmation kind=%q want content (humans are not echo)", got)
	}

	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	agentA := createHandlerTestAgent(t, "l1k-"+suffix, nil)
	channelID := seedChannelForTest(t, "l1k-ch-"+suffix, testUserID)

	pure, err := testHandler.insertChannelMessageWithParts(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentA),
		"Agent A", "收到", nil, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 2,
	)
	if err != nil {
		t.Fatalf("insert pure confirmation: %v", err)
	}
	if pure.Kind != protocol.ChannelMessageKindConfirmation {
		t.Fatalf("insert result kind=%q want confirmation", pure.Kind)
	}
	var dbKind string
	if err := testPool.QueryRow(ctx, `SELECT kind FROM channel_message WHERE id = $1`, parseUUID(pure.ID)).Scan(&dbKind); err != nil {
		t.Fatalf("read persisted kind: %v", err)
	}
	if dbKind != protocol.ChannelMessageKindConfirmation {
		t.Fatalf("persisted kind=%q want confirmation", dbKind)
	}

	content, err := testHandler.insertChannelMessageWithParts(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentA),
		"Agent A", "完成，接口已提 PR", nil, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 2,
	)
	if err != nil {
		t.Fatalf("insert content: %v", err)
	}
	var contentKind string
	if err := testPool.QueryRow(ctx, `SELECT kind FROM channel_message WHERE id = $1`, parseUUID(content.ID)).Scan(&contentKind); err != nil {
		t.Fatalf("read content kind: %v", err)
	}
	if contentKind != protocol.ChannelMessageKindContent {
		t.Fatalf("content kind=%q want content", contentKind)
	}
}
