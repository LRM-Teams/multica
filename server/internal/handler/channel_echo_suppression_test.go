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

	// 1) Pure confirmation by agent A mentioning agent B -> no delivery for B
	// (echo suppression) and no task-shaped wake.
	pureTrigger, err := testHandler.insertChannelMessageWithParts(
		ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentA),
		"Agent A", mentionMarkdown+" 收到", []protocol.MessagePart{mention}, "multica",
		nil, pgtype.UUID{}, pgtype.UUID{}, nil, 2,
	)
	if err != nil {
		t.Fatalf("insert pure confirmation trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, pureTrigger, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, pureTrigger)
	assertChannelAgentDeliveryCount(t, agentB, pureTrigger.ID, 0)
	assertChannelNoInboxWakes(t, channelID)

	// 2) Control: agent A sends real content mentioning agent B -> B receives
	// canonical Message delivery (no inbox wake after #2295 hard-cut).
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
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, realTrigger)
	assertChannelAgentDeliveryCount(t, agentB, realTrigger.ID, 1)
	assertChannelNoInboxWakes(t, channelID)
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

	var lexiconSource string
	if err := testPool.QueryRow(ctx, `SELECT kind_source FROM channel_message WHERE id = $1`, parseUUID(pure.ID)).Scan(&lexiconSource); err != nil {
		t.Fatalf("read lexicon kind_source: %v", err)
	}
	if lexiconSource != protocol.ChannelMessageKindSourceLexicon {
		t.Fatalf("lexicon kind_source=%q want lexicon", lexiconSource)
	}

	hinted, err := insertChannelMessageWithPartsExec(
		ctx, testPool, parseUUID(channelID), parseUUID(testWorkspaceID), "agent", parseUUID(agentA),
		"Agent A", "收到", nil, "multica", nil, nil, pgtype.UUID{}, pgtype.UUID{}, nil, pgtype.UUID{}, nil, 2,
		channelMessageKindHint{Kind: protocol.ChannelMessageKindConfirmation, Source: protocol.ChannelMessageKindSourceStructured},
	)
	if err != nil {
		t.Fatalf("insert structured confirmation: %v", err)
	}
	if hinted.Message.Kind != protocol.ChannelMessageKindConfirmation || hinted.Message.KindSource != protocol.ChannelMessageKindSourceStructured {
		t.Fatalf("structured insert = kind=%q source=%q", hinted.Message.Kind, hinted.Message.KindSource)
	}
	if !channelMessageIsConfirmationNoWake(hinted.Message) {
		t.Fatal("structured confirmation must be no-wake")
	}
}
