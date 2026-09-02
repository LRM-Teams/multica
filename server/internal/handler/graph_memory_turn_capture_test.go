package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// seedGraphCaptureFixture provisions a group channel with an active managed
// graph memory agent. withProfile=false leaves the workspace legacy (no
// graph_memory_profile row), which must suppress capture entirely.
func seedGraphCaptureFixture(t *testing.T, withProfile bool) (workspaceID, channelID pgtype.UUID, agentID string) {
	t.Helper()
	ctx := context.Background()
	workspaceID = createGraphMemoryTestWorkspace(t)
	mustGraphMemoryMember(t, workspaceID, "owner")
	channelID = createGraphMemoryTestChannel(t, workspaceID)
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
		  workspace_id, daemon_id, name, runtime_mode, provider,
		  status, device_info, metadata, last_seen_at
		) VALUES ($1,$2,$3,'local','pi','online','','{}'::jsonb,now())
		RETURNING id::text`,
		workspaceID, "graph-capture-daemon-"+uuid.NewString()[:8], "graph-capture",
	).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if withProfile {
		if _, err := testPool.Exec(ctx, `
			INSERT INTO graph_memory_profile (
			  workspace_id, memory_type, graph_memory_mode,
			  memory_agent_runtime_id, memory_agent_model, memory_agent_thinking
			) VALUES ($1,'graph','agent',$2,'profile/model','low')`,
			workspaceID, runtimeID); err != nil {
			t.Fatal(err)
		}
	}
	suffix := uuid.NewString()[:8]
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
		  workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id,
		  owner_id, managed_role, instructions, model, thinking_level
		) VALUES ($1,$2,'Graph capture','local','{}',$3,$4,'graph_memory_channel','managed memory','profile/model','low')
		RETURNING id::text`,
		workspaceID, "graph-capture-"+suffix, runtimeID, testUserID,
	).Scan(&agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent (
		  channel_id, workspace_id, agent_id, runtime_id, sponsor_user_id,
		  handle, display_name, status
		) VALUES ($1,$2,$3,$4,$5,$6,'Graph capture','active')`,
		channelID, workspaceID, agentID, runtimeID, testUserID, "graph-capture-"+suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id, role)
		VALUES ($1,$2,'agent',$3,'member')`,
		channelID, workspaceID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(ctx, `INSERT INTO graph_memory_agent_state (channel_id) VALUES ($1)`, channelID); err != nil {
		t.Fatal(err)
	}
	return workspaceID, channelID, agentID
}

func sendDirectedGraphCaptureMessage(t *testing.T, workspaceID, channelID pgtype.UUID, agentID, fact string) ChannelMessageResponse {
	t.Helper()
	message, err := testHandler.insertChannelMessageWithParts(
		context.Background(), channelID, workspaceID, "user", parseUUID(testUserID), "Tester",
		fact,
		[]protocol.MessagePart{
			{Type: protocol.MessagePartTypeText, Text: fact},
			{Type: protocol.MessagePartTypeReference, RefType: "mention", RefSubType: "agent", RefID: agentID, Label: "@memory"},
		},
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("insert directed Message: %v", err)
	}
	channel, found := testHandler.getChannel(context.Background(), uuidToString(workspaceID), channelID)
	if !found {
		t.Fatal("load channel")
	}
	if err := testHandler.deliverCanonicalMessageToChannelAgents(context.Background(), channel, message); err != nil {
		t.Fatalf("deliver canonical message: %v", err)
	}
	return message
}

func graphCaptureAnchorCount(t *testing.T, messageID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM agent_inbox_event
		WHERE source_message_id = $1 AND reason = 'graph_capture'`,
		parseUUID(messageID)).Scan(&count); err != nil {
		t.Fatalf("count graph capture anchors: %v", err)
	}
	return count
}

func TestGraphCaptureDirectedTurnMintsAnchorSegmentAtoms(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID, channelID, agentID := seedGraphCaptureFixture(t, true)
	token := "GC-" + uuid.NewString()[:8]
	fact := "Stargazer's fusion reactor runs on deuterium shipped from the lunar ice mines, token " + token + "."
	message := sendDirectedGraphCaptureMessage(t, workspaceID, channelID, agentID, fact)

	var anchorID, status, outcome string
	var requiresWake bool
	if err := testPool.QueryRow(ctx, `
		SELECT id::text, status, terminal_outcome, requires_wake
		FROM agent_inbox_event
		WHERE source_message_id = $1 AND reason = 'graph_capture'`,
		parseUUID(message.ID)).Scan(&anchorID, &status, &outcome, &requiresWake); err != nil {
		t.Fatalf("load graph capture anchor: %v", err)
	}
	if status != "acked" || outcome != "completed" || requiresWake {
		t.Fatalf("anchor status/outcome/requires_wake = %s/%s/%v, want acked/completed/false", status, outcome, requiresWake)
	}

	var messageCount int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM task_message WHERE task_id = $1 AND type = 'text'`,
		anchorID).Scan(&messageCount); err != nil {
		t.Fatalf("count anchor task messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("anchor task messages = %d, want 1", messageCount)
	}

	var segmentID, memoryType string
	var eligible bool
	if err := testPool.QueryRow(ctx, `
		SELECT segment_id::text, memory_type_at_event, graph_projection_eligible_at_event
		FROM interaction_dag_segment
		WHERE workspace_id = $1 AND agent_run_id = $2`,
		workspaceID, anchorID).Scan(&segmentID, &memoryType, &eligible); err != nil {
		t.Fatalf("load anchor segment: %v", err)
	}
	if memoryType != "graph" || !eligible {
		t.Fatalf("anchor segment memory_type/eligible = %s/%v, want graph/true", memoryType, eligible)
	}

	// Drain the publish outbox until this segment's atom exists; the shared
	// fixture workspace queues segments from other handler tests too.
	publisher := service.NewInteractionDAGPublisher(testPool)
	var atomBody string
	for attempt := 0; ; attempt++ {
		published, err := publisher.PublishClaim(ctx, 10)
		if err != nil {
			t.Fatalf("publish claim: %v", err)
		}
		err = testPool.QueryRow(ctx, `
			SELECT body::text FROM graph_memory_atom
			WHERE workspace_id = $1 AND segment_id = $2`,
			workspaceID, segmentID).Scan(&atomBody)
		if err == nil {
			break
		}
		if published == 0 || attempt >= 200 {
			var statusText, lastError string
			var attempts int32
			_ = testPool.QueryRow(ctx, `
				SELECT o.status, o.attempts, COALESCE(o.last_error,'')
				FROM interaction_dag_publish_outbox o
				WHERE o.workspace_id = $1 AND o.segment_id = $2`,
				workspaceID, segmentID).Scan(&statusText, &attempts, &lastError)
			t.Fatalf("load atom for anchor segment %s: %v (outbox status=%s attempts=%d last_error=%q)",
				segmentID, err, statusText, attempts, lastError)
		}
	}
	if !strings.Contains(atomBody, token) {
		t.Fatalf("atom body %q does not carry the taught fact token %s", atomBody, token)
	}
}

func TestGraphCaptureAgentReplyAnchor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	workspaceID, channelID, agentID := seedGraphCaptureFixture(t, true)
	token := "REPLY-" + uuid.NewString()[:8]
	req := agentTransportRequest(t, http.MethodPost, "/api/agent/messages/send", "", agentID, map[string]any{
		"target":            "#" + channelNameForTransportTest(t, uuidToString(channelID)),
		"content":           "The coolant loop is stable and the reactor holds nominal output, marker " + token + ".",
		"client_message_id": "graph-capture-reply-" + uuid.NewString(),
	})
	// The credential transport derives its origin workspace from the member
	// context; bind it to the fixture workspace, not the shared test one.
	memberRow, err := testHandler.Queries.GetMemberByUserAndWorkspace(context.Background(), db.GetMemberByUserAndWorkspaceParams{
		UserID:      util.MustParseUUID(testUserID),
		WorkspaceID: workspaceID,
	})
	if err != nil {
		t.Fatalf("load fixture member row: %v", err)
	}
	req = req.WithContext(middleware.SetMemberContext(req.Context(), uuidToString(workspaceID), memberRow))
	rec := httptest.NewRecorder()
	testHandler.AgentTransportSendMessage(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("agent transport send: status=%d body=%s", rec.Code, rec.Body.String())
	}
	var replyID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM channel_message
		WHERE workspace_id = $1 AND content LIKE '%' || $2 || '%'`,
		workspaceID, token).Scan(&replyID); err != nil {
		t.Fatalf("load reply message: %v", err)
	}
	if got := graphCaptureAnchorCount(t, replyID); got != 1 {
		t.Fatalf("reply anchors = %d, want 1", got)
	}
}

func TestGraphCaptureGatesLegacyProfileMentionAndAuthor(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	// Legacy workspace (no graph profile): the directed turn must not mint.
	legacyWS, legacyChannel, legacyAgent := seedGraphCaptureFixture(t, false)
	legacyMessage := sendDirectedGraphCaptureMessage(t, legacyWS, legacyChannel, legacyAgent, "no capture without a graph profile")
	if got := graphCaptureAnchorCount(t, legacyMessage.ID); got != 0 {
		t.Fatalf("legacy workspace anchors = %d, want 0", got)
	}

	// Graph workspace, human message WITHOUT a mention: the managed memory
	// agent is the conversational counterpart for every human turn, so this
	// mints exactly like a directed turn.
	workspaceID, channelID, _ := seedGraphCaptureFixture(t, true)
	undirected, err := testHandler.insertChannelMessageWithParts(
		context.Background(), channelID, workspaceID, "user", parseUUID(testUserID), "Tester",
		"just talking to the room, no mention",
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "just talking to the room, no mention"}},
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("insert undirected Message: %v", err)
	}
	channel, found := testHandler.getChannel(context.Background(), uuidToString(workspaceID), channelID)
	if !found {
		t.Fatal("load channel")
	}
	if err := testHandler.deliverCanonicalMessageToChannelAgents(context.Background(), channel, undirected); err != nil {
		t.Fatalf("deliver undirected message: %v", err)
	}
	if got := graphCaptureAnchorCount(t, undirected.ID); got != 1 {
		t.Fatalf("undirected anchors = %d, want 1", got)
	}

	// Graph workspace, agent-authored human-shaped message: agent traffic owns
	// its own DAG paths (reply hook), so the user-turn hook must not mint.
	var agentID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT agent_id::text FROM graph_memory_channel_agent WHERE channel_id = $1`,
		channelID).Scan(&agentID); err != nil {
		t.Fatalf("load managed agent: %v", err)
	}
	agentAuthored, err := testHandler.insertChannelMessageWithParts(
		context.Background(), channelID, workspaceID, "agent", parseUUID(agentID), "Memory",
		"an agent message that must not mint a user turn anchor",
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "an agent message that must not mint a user turn anchor"}},
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("insert agent-authored Message: %v", err)
	}
	if err := testHandler.deliverCanonicalMessageToChannelAgents(context.Background(), channel, agentAuthored); err != nil {
		t.Fatalf("deliver agent-authored message: %v", err)
	}
	if got := graphCaptureAnchorCount(t, agentAuthored.ID); got != 0 {
		t.Fatalf("agent-authored anchors = %d, want 0", got)
	}
}

// TestGraphCaptureManagedOnlyChannel mints through the standalone path: with
// no regular agent members the delivery plan set is empty, and the turn still
// gets its anchor (the managed memory agent is not a channel_member).
func TestGraphCaptureManagedOnlyChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID, channelID, agentID := seedGraphCaptureFixture(t, true)
	if _, err := testPool.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = $1 AND member_type = 'agent'`, channelID); err != nil {
		t.Fatalf("remove agent member: %v", err)
	}
	message, err := testHandler.insertChannelMessageWithParts(
		ctx, channelID, workspaceID, "user", parseUUID(testUserID), "Tester",
		"talking to the memory agent alone, token "+uuid.NewString()[:8],
		[]protocol.MessagePart{{Type: protocol.MessagePartTypeText, Text: "talking to the memory agent alone"}},
		"multica", nil, pgtype.UUID{}, pgtype.UUID{}, nil, 0,
	)
	if err != nil {
		t.Fatalf("insert managed-only Message: %v", err)
	}
	channel, found := testHandler.getChannel(ctx, uuidToString(workspaceID), channelID)
	if !found {
		t.Fatal("load channel")
	}
	if err := testHandler.deliverCanonicalMessageToChannelAgents(ctx, channel, message); err != nil {
		t.Fatalf("deliver managed-only message: %v", err)
	}
	if got := graphCaptureAnchorCount(t, message.ID); got != 1 {
		t.Fatalf("managed-only anchors = %d, want 1", got)
	}
	var anchorAgent string
	if err := testPool.QueryRow(ctx, `
		SELECT agent_id::text FROM agent_inbox_event
		WHERE source_message_id = $1 AND reason = 'graph_capture'`,
		parseUUID(message.ID)).Scan(&anchorAgent); err != nil {
		t.Fatalf("load managed-only anchor: %v", err)
	}
	if anchorAgent != agentID {
		t.Fatalf("managed-only anchor agent = %s, want the managed agent %s", anchorAgent, agentID)
	}
}

func TestGraphCaptureIdempotentReplay(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	workspaceID, channelID, agentID := seedGraphCaptureFixture(t, true)
	fact := "Replay discipline: one anchor per message even when delivery re-runs, token " + uuid.NewString()[:8] + "."
	message := sendDirectedGraphCaptureMessage(t, workspaceID, channelID, agentID, fact)

	// Re-deliver the same persisted message: agent_message_delivery replays
	// (conflict no-op) and the mint must collapse on the unique index.
	channel, found := testHandler.getChannel(ctx, uuidToString(workspaceID), channelID)
	if !found {
		t.Fatal("load channel")
	}
	if err := testHandler.deliverCanonicalMessageToChannelAgents(ctx, channel, message); err != nil {
		t.Fatalf("re-deliver canonical message: %v", err)
	}

	if got := graphCaptureAnchorCount(t, message.ID); got != 1 {
		t.Fatalf("replayed anchors = %d, want 1", got)
	}
	var segmentCount, messageRows int
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM interaction_dag_segment
		WHERE workspace_id = $1 AND agent_run_id IN (
		  SELECT id FROM agent_inbox_event WHERE source_message_id = $2 AND reason = 'graph_capture'
		)`, workspaceID, parseUUID(message.ID)).Scan(&segmentCount); err != nil {
		t.Fatalf("count replay segments: %v", err)
	}
	if segmentCount != 1 {
		t.Fatalf("replayed segments = %d, want 1", segmentCount)
	}
	if err := testPool.QueryRow(ctx, `
		SELECT count(*) FROM task_message
		WHERE task_id IN (
		  SELECT id FROM agent_inbox_event WHERE source_message_id = $1 AND reason = 'graph_capture'
		)`, parseUUID(message.ID)).Scan(&messageRows); err != nil {
		t.Fatalf("count replay task messages: %v", err)
	}
	if messageRows != 1 {
		t.Fatalf("replayed task messages = %d, want 1", messageRows)
	}
}
