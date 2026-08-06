package handler

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// #4 escalation ladder: nudging an agent raises its count; real progress resets it.
func TestNudgeLadderIncrementAndReset(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	ws := parseUUID(testWorkspaceID)
	agentID := createHandlerTestAgent(t, "Ladder Worker "+uuid.NewString(), nil)
	channelID := seedChannelForTest(t, "ladder-"+uuid.NewString(), testUserID)
	t.Cleanup(func() {
		testPool.Exec(context.Background(), `DELETE FROM wendy_nudge_ladder WHERE channel_id = $1`, parseUUID(channelID))
	})

	// Two nudges → count 2.
	h := testHandler
	h.incrementNudgeLadder(ctx, ws, parseUUID(channelID), parseUUID(agentID))
	h.incrementNudgeLadder(ctx, ws, parseUUID(channelID), parseUUID(agentID))
	if got := h.channelNudgeLadder(ctx, parseUUID(channelID))[agentID]; got != 2 {
		t.Fatalf("nudge count = %d, want 2", got)
	}

	// Real progress resets the agent's escalation.
	h.resetNudgeLadderForAgent(ctx, ws, parseUUID(agentID))
	if got := h.channelNudgeLadder(ctx, parseUUID(channelID))[agentID]; got != 0 {
		t.Fatalf("nudge count after progress reset = %d, want 0", got)
	}

	// Nudging again after reset starts back at 1 (fresh escalation).
	h.incrementNudgeLadder(ctx, ws, parseUUID(channelID), parseUUID(agentID))
	if got := h.channelNudgeLadder(ctx, parseUUID(channelID))[agentID]; got != 1 {
		t.Fatalf("nudge count after re-nudge = %d, want 1", got)
	}
}
