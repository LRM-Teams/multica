package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestUniversalDAGTransportMessageBoundary(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	taskID, channelID := createChannelCompletionTask(t, "group")
	cleanupUniversalDAGTask(t, taskID)
	agentID := agentIDForTask(t, taskID)
	clientID := "task3-transport-" + uuid.NewString()
	response := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "#" + channelNameForTransportTest(t, channelID),
		"content":           "synthetic visible action",
		"client_message_id": clientID,
	})
	if response.Code != http.StatusCreated {
		t.Fatalf("transport send: status=%d body=%s", response.Code, response.Body.String())
	}
	var body AgentTransportSendResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode transport response: %v", err)
	}

	ctx := context.Background()
	var messageCount, segmentCount, outboxCount int
	var canonicalActionID, closeKind string
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_message WHERE task_id = $1`, taskID).Scan(&messageCount); err != nil {
		t.Fatalf("count canonical task messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*), COALESCE(max(canonical_action_id::text), ''), COALESCE(max(close_action_kind), '')
		FROM interaction_dag_segment
		WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, taskID).Scan(&segmentCount, &canonicalActionID, &closeKind); err != nil {
		t.Fatalf("load universal DAG segment: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM interaction_dag_publish_outbox outbox
		JOIN interaction_dag_segment segment
		  ON segment.workspace_id = outbox.workspace_id AND segment.segment_id = outbox.segment_id
		WHERE segment.workspace_id = $1 AND segment.agent_run_id = $2`, testWorkspaceID, taskID).Scan(&outboxCount); err != nil {
		t.Fatalf("count universal DAG outbox rows: %v", err)
	}
	if messageCount != 1 || segmentCount != 1 || outboxCount != 1 || canonicalActionID != body.Message.ID || closeKind != "message" {
		t.Fatalf("canonical message/segment/outbox/action/kind=%d/%d/%d/%q/%q, want 1/1/1/%q/message", messageCount, segmentCount, outboxCount, canonicalActionID, closeKind, body.Message.ID)
	}

	replay := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "#" + channelNameForTransportTest(t, channelID),
		"content":           "synthetic visible action",
		"client_message_id": clientID,
	})
	if replay.Code != http.StatusCreated {
		t.Fatalf("transport replay: status=%d body=%s", replay.Code, replay.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, taskID).Scan(&segmentCount); err != nil {
		t.Fatalf("count replay segments: %v", err)
	}
	if segmentCount != 1 {
		t.Fatalf("duplicate action created %d segments, want 1", segmentCount)
	}
	conflict := agentTransportSendForTest(t, taskID, agentID, map[string]any{
		"target":            "#" + channelNameForTransportTest(t, channelID),
		"content":           "conflicting synthetic visible action",
		"client_message_id": clientID,
	})
	if conflict.Code != http.StatusConflict {
		t.Fatalf("transport conflicting replay: status=%d body=%s, want 409", conflict.Code, conflict.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id = $1 AND agent_run_id = $2`, testWorkspaceID, taskID).Scan(&segmentCount); err != nil {
		t.Fatalf("count conflict replay segments: %v", err)
	}
	if segmentCount != 1 {
		t.Fatalf("conflicting replay changed segment count to %d, want 1", segmentCount)
	}

	reactionRec := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": body.Message.ID,
		"emoji":      "👍",
	})
	if reactionRec.Code != http.StatusOK {
		t.Fatalf("transport reaction: status=%d body=%s", reactionRec.Code, reactionRec.Body.String())
	}
	var reaction AgentTransportReactResponse
	if err := json.Unmarshal(reactionRec.Body.Bytes(), &reaction); err != nil {
		t.Fatalf("decode transport reaction: %v", err)
	}
	if reaction.Reaction == nil {
		t.Fatalf("transport reaction did not return canonical identity: %+v", reaction)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*)
		FROM interaction_dag_segment
		WHERE workspace_id=$1 AND agent_run_id=$2
		  AND close_action_kind='reaction' AND canonical_action_id=$3`,
		testWorkspaceID, taskID, reaction.Reaction.ID).Scan(&segmentCount); err != nil {
		t.Fatalf("count canonical reaction segments: %v", err)
	}
	if segmentCount != 1 {
		t.Fatalf("canonical reaction created %d reaction segments, want 1", segmentCount)
	}
	duplicateReaction := agentTransportReactForTest(t, taskID, agentID, map[string]any{
		"message_id": body.Message.ID,
		"emoji":      "👍",
	})
	if duplicateReaction.Code != http.StatusOK {
		t.Fatalf("transport reaction replay: status=%d body=%s", duplicateReaction.Code, duplicateReaction.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='reaction'`, testWorkspaceID, taskID).Scan(&segmentCount); err != nil {
		t.Fatalf("count replay reaction segments: %v", err)
	}
	if segmentCount != 1 {
		t.Fatalf("duplicate reaction created %d segments, want 1", segmentCount)
	}
}

func TestUniversalDAGChatDoneBridgeMessageAndReactionBoundaries(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	taskID, channelID := createChannelCompletionTask(t, "group")
	cleanupUniversalDAGTask(t, taskID)
	var chatSessionID, agentID string
	if err := testPool.QueryRow(ctx, `SELECT chat_session_id, agent_id FROM agent_inbox_event WHERE id=$1`, taskID).Scan(&chatSessionID, &agentID); err != nil {
		t.Fatalf("load bridge task identity: %v", err)
	}
	const content = "synthetic ChatDone bridge output"
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: chatSessionID,
		TaskID:        taskID,
		Type:          protocol.ChatOutputKindMessage,
		Content:       content,
	}})

	var messageID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM channel_message
		WHERE channel_id=$1 AND author_type='agent' AND author_id=$2 AND content=$3
		ORDER BY created_at DESC LIMIT 1`, channelID, agentID, content).Scan(&messageID); err != nil {
		t.Fatalf("load bridged channel message: %v", err)
	}
	var messageSegments int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_segment
		WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='message' AND canonical_action_id=$3`,
		testWorkspaceID, taskID, messageID).Scan(&messageSegments); err != nil {
		t.Fatalf("count bridged message segment: %v", err)
	}
	if messageSegments != 1 {
		t.Fatalf("bridged message segments=%d, want 1", messageSegments)
	}

	var triggerID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content, source, external_message_id)
		VALUES ($1, $2, 'user', $3, 'Tester', 'synthetic reaction target', 'multica', $4)
		RETURNING id`, channelID, testWorkspaceID, testUserID, "task3-chatdone-react-"+uuid.NewString()).Scan(&triggerID); err != nil {
		t.Fatalf("seed ChatDone reaction target: %v", err)
	}
	testHandler.handleChannelChatDone(events.Event{Payload: protocol.ChatDonePayload{
		ChatSessionID: chatSessionID,
		TaskID:        taskID,
		Type:          protocol.ChatOutputKindReaction,
		Reaction:      &protocol.ChatReactionPayload{MessageID: triggerID, Emoji: "👍"},
	}})
	var reactionID string
	if err := testPool.QueryRow(ctx, `
		SELECT id FROM channel_message_reaction
		WHERE channel_message_id=$1 AND actor_type='agent' AND actor_id=$2 AND emoji='👍'`, triggerID, agentID).Scan(&reactionID); err != nil {
		t.Fatalf("load bridged reaction: %v", err)
	}
	var reactionSegments, outboxCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_segment
		WHERE workspace_id=$1 AND agent_run_id=$2 AND close_action_kind='reaction' AND canonical_action_id=$3`,
		testWorkspaceID, taskID, reactionID).Scan(&reactionSegments); err != nil {
		t.Fatalf("count bridged reaction segment: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_publish_outbox o
		JOIN interaction_dag_segment s ON s.workspace_id=o.workspace_id AND s.segment_id=o.segment_id
		WHERE s.workspace_id=$1 AND s.agent_run_id=$2`, testWorkspaceID, taskID).Scan(&outboxCount); err != nil {
		t.Fatalf("count ChatDone bridge outbox: %v", err)
	}
	if reactionSegments != 1 || outboxCount != 2 {
		t.Fatalf("bridged reaction segments/outbox=%d/%d, want 1/2", reactionSegments, outboxCount)
	}
}

func TestUniversalDAGActionProposalBoundaryReplayAndRollback(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	taskID, channelID := createChannelCompletionTaskWithCapabilities(t, "group", nil)
	cleanupUniversalDAGTask(t, taskID)
	agentID := agentIDForTask(t, taskID)
	bindOnboardingAgentForTest(t, agentID)
	target := "#" + channelNameForTransportTest(t, channelID)

	prepare := func(clientID, name string) *httptest.ResponseRecorder {
		req := agentTransportRequest(t, http.MethodPost, "/api/agent/actions/prepare", taskID, agentID, map[string]any{
			"action_type": "agent:create", "name": name, "description": "synthetic proposal",
			"target": target, "client_request_id": clientID,
		})
		rec := httptest.NewRecorder()
		testHandler.AgentTransportPrepareAction(rec, req)
		return rec
	}

	clientID := "task3-action-" + uuid.NewString()
	rec := prepare(clientID, "task3-proposal")
	if rec.Code != http.StatusCreated {
		t.Fatalf("prepare action: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var proposal agentActionProposalResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	ctx := context.Background()
	var actionCount, messageCount, segmentCount, outboxCount int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM agent_action WHERE channel_message_id=$1`, proposal.MessageID).Scan(&actionCount); err != nil {
		t.Fatalf("count proposal action: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM task_message WHERE task_id=$1`, taskID).Scan(&messageCount); err != nil {
		t.Fatalf("count proposal task messages: %v", err)
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2 AND canonical_action_id=$3`, testWorkspaceID, taskID, proposal.MessageID).Scan(&segmentCount); err != nil {
		t.Fatalf("count proposal segment: %v", err)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_publish_outbox o
		JOIN interaction_dag_segment s ON s.workspace_id=o.workspace_id AND s.segment_id=o.segment_id
		WHERE s.workspace_id=$1 AND s.agent_run_id=$2`, testWorkspaceID, taskID).Scan(&outboxCount); err != nil {
		t.Fatalf("count proposal outbox: %v", err)
	}
	if actionCount != 1 || messageCount != 1 || segmentCount != 1 || outboxCount != 1 {
		t.Fatalf("proposal action/message/segment/outbox=%d/%d/%d/%d, want 1/1/1/1", actionCount, messageCount, segmentCount, outboxCount)
	}

	replay := prepare(clientID, "task3-proposal")
	if replay.Code != http.StatusCreated {
		t.Fatalf("proposal replay: status=%d body=%s", replay.Code, replay.Body.String())
	}
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM interaction_dag_segment WHERE workspace_id=$1 AND agent_run_id=$2`, testWorkspaceID, taskID).Scan(&segmentCount); err != nil {
		t.Fatalf("count replay proposal segments: %v", err)
	}
	if segmentCount != 1 {
		t.Fatalf("proposal replay segments=%d, want 1", segmentCount)
	}

	originalDAG := testHandler.TaskService.UniversalDAG
	testHandler.TaskService.UniversalDAG = nil
	rollbackClientID := "task3-action-rollback-" + uuid.NewString()
	rollback := prepare(rollbackClientID, "task3-rollback")
	testHandler.TaskService.UniversalDAG = originalDAG
	if rollback.Code != http.StatusInternalServerError {
		t.Fatalf("proposal rollback status=%d body=%s, want 500", rollback.Code, rollback.Body.String())
	}
	var durableRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE workspace_id=$1 AND author_id=$2 AND client_message_id=$3`, testWorkspaceID, agentID, rollbackClientID).Scan(&durableRows); err != nil {
		t.Fatalf("count rolled-back proposal message: %v", err)
	}
	if durableRows != 0 {
		t.Fatalf("rolled-back proposal messages=%d, want 0", durableRows)
	}
}
