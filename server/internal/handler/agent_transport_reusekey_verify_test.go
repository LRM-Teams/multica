package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// TestReuseKeyDistinctContentNotDedupedVerify is an isolated-DB verification for
// LRM-1526 "不误并" boundary: the SAME agent to the SAME channel with TWO
// DIFFERENT client_message_id (distinct intents) must each insert its own row
// (2 total), proving distinct intents are NOT folded/absorbed by the
// (workspace,channel,author,client_message_id) dedup that only collides on the
// exact same client_message_id.
func TestReuseKeyDistinctContentNotDedupedVerify(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	agentID := agentIDForTask(t, taskID)
	target := "#" + channelNameForTransportTest(t, channelID)

	cmidA := "distinct-A-" + uuid.NewString()
	cmidB := "distinct-B-" + uuid.NewString()
	contentA := "reusekey-verify A " + uuid.NewString()
	contentB := "reusekey-verify B " + uuid.NewString()

	first := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": contentA, "client_message_id": cmidA,
	})
	if first.Code != http.StatusCreated {
		t.Fatalf("first distinct send: status=%d body=%s", first.Code, first.Body.String())
	}
	second := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target": target, "content": contentB, "client_message_id": cmidB,
	})
	if second.Code != http.StatusCreated {
		t.Fatalf("second distinct send: status=%d body=%s", second.Code, second.Body.String())
	}
	var bodyB AgentTransportSendResponse
	if err := json.Unmarshal(second.Body.Bytes(), &bodyB); err != nil {
		t.Fatalf("decode second send: %v", err)
	}
	if !bodyB.Created {
		t.Fatalf("second distinct sender created=false, want true (distinct cmid must not be folded)")
	}

	// Same agent, same channel, two rows (one per distinct client_message_id).
	var rows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND author_id = $2
		  AND client_message_id IN ($3, $4)`,
		channelID, agentID, cmidA, cmidB).Scan(&rows); err != nil {
		t.Fatalf("count distinct messages: %v", err)
	}
	if rows != 2 {
		t.Fatalf("distinct cmid rows=%d, want 2 (each distinct intent inserted once, not folded)", rows)
	}
}
