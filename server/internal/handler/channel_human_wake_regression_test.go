package handler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// #2295: ordinary channel human traffic is delivery-only. These regressions
// assert agent_message_delivery recipient policy (not task-shaped wakes).

func TestChannelGroupCommandWakesAllAgentsRestoresAndongDefault(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	channelID := seedChannelForTest(t, "group-command-wake-all-"+suffix, testUserID)
	managerID := createHandlerTestAgent(t, "group-mgr-"+suffix, nil)
	peerID := createHandlerTestAgent(t, "group-peer-"+suffix, nil)
	for _, member := range []struct {
		agentID string
		role    string
	}{
		{agentID: managerID, role: "manager"},
		{agentID: peerID, role: "member"},
	} {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
			VALUES ($1, $2, 'agent', $3, $4)
			ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, member.agentID, member.role); err != nil {
			t.Fatalf("seed agent member %s: %v", member.agentID, err)
		}
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "大家出来打个招呼", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, strPtr("group-command-wake-all"), 0)
	if err != nil {
		t.Fatalf("insert group command: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, trigger)

	assertChannelNoInboxWakes(t, channelID)
	for _, agentID := range []string{managerID, peerID} {
		assertChannelAgentDeliveryCount(t, agentID, trigger.ID, 1)
	}
}

func TestChannelHumanMentionDirectsTargetAndOrdinaryWakesOnlyUnmutedBystanders(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	targetID := createHandlerTestAgent(t, "Ambient Target "+uuid.NewString()[:8], nil)
	bystanderID := createHandlerTestAgent(t, "Ambient Bystander "+uuid.NewString()[:8], nil)
	mutedBystanderID := createHandlerTestAgent(t, "Muted Ambient Bystander "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-skip-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, muted_at)
		VALUES ($1, $2, 'agent', $3, NULL),
		       ($1, $2, 'agent', $4, NULL),
		       ($1, $2, 'agent', $5, now())
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, targetID, bystanderID, mutedBystanderID); err != nil {
		t.Fatalf("seed agent members: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	mentionContent := "@target please handle"
	mentionParts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: targetID, Label: "@target"}}
	humanMention, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", mentionContent, mentionParts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert human mention: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, humanMention, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, humanMention)

	assertChannelNoInboxWakes(t, channelID)

	// Mentioned target + unmuted bystander receive delivery; muted non-target does not.
	assertChannelAgentDeliveryCount(t, targetID, humanMention.ID, 1)
	assertChannelAgentDeliveryCount(t, bystanderID, humanMention.ID, 1)
	assertChannelAgentDeliveryCount(t, mutedBystanderID, humanMention.ID, 0)
}

func TestChannelMessageWakeSkipsMutedAgentButMentionPierces(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentName := "muted-direct-agent-" + uuid.NewString()[:8]
	agentID := createHandlerTestAgent(t, agentName, nil)
	channelID := seedChannelForTest(t, "muted-direct-agent-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, muted_at)
		VALUES ($1, $2, 'agent', $3, now())
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed muted agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}

	ordinary, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "ordinary message while muted", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert ordinary trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, ordinary, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, ordinary)
	assertChannelAgentDeliveryCount(t, agentID, ordinary.ID, 0)

	directContent := fmt.Sprintf("@%s please answer even while muted", agentName)
	directParts := []protocol.MessagePart{{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, Label: "@" + agentName}}
	direct, err := testHandler.insertChannelMessageWithParts(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", directContent, directParts, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert direct trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, direct, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, direct)

	assertChannelNoInboxWakes(t, channelID)
	// Explicit mention pierces mute via delivery recipient policy.
	assertChannelAgentDeliveryCount(t, agentID, direct.ID, 1)
}

func TestChannelMessageWakeDoesNotSkipObviousNoise(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	withChannelAmbientGateTestConfig(t)
	agentID := createHandlerTestAgent(t, "Ambient Noise Gate "+uuid.NewString()[:8], nil)
	channelID := seedChannelForTest(t, "ambient-noise-gate-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
		ON CONFLICT DO NOTHING`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("seed agent member: %v", err)
	}
	ch, found := testHandler.getChannel(ctx, testWorkspaceID, parseUUID(channelID))
	if !found {
		t.Fatal("channel not found after seed")
	}
	trigger, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", "!!!", "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert noise trigger: %v", err)
	}

	testHandler.dispatchChannelMessageToAgents(ctx, ch, trigger, parseUUID(testUserID))
	testHandler.deliverCanonicalMessageToChannelAgents(ctx, ch, trigger)

	assertChannelNoInboxWakes(t, channelID)
	// Noise still reaches unmuted agents via delivery; no task-shaped wake.
	assertChannelAgentDeliveryCount(t, agentID, trigger.ID, 1)
}

func assertChannelNoInboxWakes(t *testing.T, channelID string) {
	t.Helper()
	var events int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1
		  AND reason IN ('mention', 'channel_message', 'thread_reply', 'ambient', 'dm')`,
		channelID).Scan(&events); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if events != 0 {
		t.Fatalf("channel created %d task-shaped chat inbox events, want 0", events)
	}
}

func assertChannelAgentDeliveryCount(t *testing.T, agentID, messageID string, want int) {
	t.Helper()
	var got int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM agent_message_delivery
		WHERE agent_id = $1 AND message_id = $2`, agentID, messageID).Scan(&got); err != nil {
		t.Fatalf("count deliveries for agent %s message %s: %v", agentID, messageID, err)
	}
	if got != want {
		t.Fatalf("delivery count for agent %s message %s = %d, want %d", agentID, messageID, got, want)
	}
}

func withChannelAmbientGateTestConfig(t *testing.T) {
	t.Helper()
	prev := testHandler.cfg
	testHandler.cfg.ChannelAmbientGateMode = channelAmbientGateModeGate
	testHandler.cfg.ChannelAmbientGateWindow = time.Minute
	testHandler.cfg.ChannelAmbientGateMaxRecentPerAgent = 1
	testHandler.cfg.ChannelAmbientGateMaxRecentPerChannel = 32
	testHandler.cfg.ChannelAmbientGateMaxRecentPerRuntime = 64
	t.Cleanup(func() { testHandler.cfg = prev })
}
