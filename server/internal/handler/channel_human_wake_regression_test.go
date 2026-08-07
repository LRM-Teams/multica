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

	for _, agentID := range []string{managerID, peerID} {
		assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
		assertChannelAgentWakeReasonPriority(t, channelID, agentID, trigger.ID, channelMessageWakeReason, channelMessageWakePriority)
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
		VALUES ($1, $2, 'agent', $3, now()),
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

	type inboxFact struct {
		agentID      string
		reason       string
		requiresWake bool
		priority     int32
	}
	rows, err := testPool.Query(ctx, `
		SELECT agent_id, reason, requires_wake, priority
		FROM agent_inbox_event
		WHERE channel_id = $1 AND source_message_id = $2
		ORDER BY agent_id`, channelID, humanMention.ID)
	if err != nil {
		t.Fatalf("load mention inbox facts: %v", err)
	}
	defer rows.Close()
	facts := map[string]inboxFact{}
	for rows.Next() {
		var fact inboxFact
		if err := rows.Scan(&fact.agentID, &fact.reason, &fact.requiresWake, &fact.priority); err != nil {
			t.Fatalf("scan mention inbox fact: %v", err)
		}
		if _, duplicate := facts[fact.agentID]; duplicate {
			t.Fatalf("duplicate inbox fact for agent %s", fact.agentID)
		}
		facts[fact.agentID] = fact
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate mention inbox facts: %v", err)
	}
	if len(facts) != 2 {
		t.Fatalf("mention created %d inbox facts, want target + unmuted bystander only: %+v", len(facts), facts)
	}
	if got := facts[targetID]; got.reason != "mention" || !got.requiresWake || got.priority != channelDirectedWakePriority {
		t.Fatalf("target fact = %+v, want directed mention wake", got)
	}
	if got := facts[bystanderID]; got.reason != channelMessageWakeReason || !got.requiresWake || got.priority != channelMessageWakePriority {
		t.Fatalf("bystander fact = %+v, want ordinary coalesced channel wake", got)
	}
	if _, ok := facts[mutedBystanderID]; ok {
		t.Fatalf("muted non-target received inbox fact: %+v", facts[mutedBystanderID])
	}

	var targetCount, bystanderCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE agent_id = $2),
		       count(*) FILTER (WHERE agent_id = $3)
		FROM agent_inbox_event
		WHERE channel_id = $1 AND source_message_id = $4`, channelID, targetID, bystanderID, humanMention.ID).Scan(&targetCount, &bystanderCount); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if targetCount != 1 || bystanderCount != 1 {
		t.Fatalf("per-agent inbox counts target=%d bystander=%d, want 1/1", targetCount, bystanderCount)
	}
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

	directContent := fmt.Sprintf("@%s please answer even while muted", agentName)
	direct, err := testHandler.insertChannelMessage(ctx, parseUUID(channelID), parseUUID(testWorkspaceID), "user", parseUUID(testUserID), "Tester", directContent, "multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0)
	if err != nil {
		t.Fatalf("insert direct trigger: %v", err)
	}
	testHandler.dispatchChannelMessageToAgents(ctx, ch, direct, parseUUID(testUserID))

	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReason(t, channelID, agentID, direct.ID, "mention")
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

	var sessions, events int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_session
		WHERE channel_id = $1`, channelID).Scan(&sessions); err != nil {
		t.Fatalf("count agent sessions: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE channel_id = $1`, channelID).Scan(&events); err != nil {
		t.Fatalf("count inbox events: %v", err)
	}
	if sessions != 1 || events != 1 {
		t.Fatalf("noise ordinary message created %d sessions and %d inbox events, want one runnable wake", sessions, events)
	}
	assertChannelAgentInboxEventCounts(t, channelID, agentID, 0, 1)
	assertChannelAgentWakeReasonPriority(t, channelID, agentID, trigger.ID, channelMessageWakeReason, channelMessageWakePriority)
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

