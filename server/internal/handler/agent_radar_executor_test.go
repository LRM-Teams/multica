package handler

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/internal/radar"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestExecuteRadarChannelPostPublishesMessageToChannelMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Radar Publisher "+uuid.NewString(), nil)
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	channelID := seedChannelForTest(t, "radar-publish-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add radar agent to channel: %v", err)
	}

	content := "found a useful next step " + uuid.NewString()
	payload, err := json.Marshal(radarChannelPayload{ChannelID: channelID, Content: content})
	if err != nil {
		t.Fatal(err)
	}
	eventsSeen := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(e events.Event) {
		msg, ok := e.Payload.(ChannelMessageResponse)
		if ok && msg.Content == "主动发现："+content {
			eventsSeen <- e
		}
	})

	result, err := testHandler.executeRadarChannelPost(ctx, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
	}, agent, radar.RadarAction{
		Type:    radar.ActionPostChannelMessage,
		Payload: payload,
	})
	if err != nil {
		t.Fatalf("execute radar channel post: %v", err)
	}
	if result["channel_message_id"] == nil {
		t.Fatalf("missing channel message id: %#v", result)
	}

	select {
	case event := <-eventsSeen:
		if event.ActorType != "agent" || event.ActorID != agentID {
			t.Fatalf("event actor = %s/%s, want agent/%s", event.ActorType, event.ActorID, agentID)
		}
		if len(event.RecipientUserIDs) != 1 || event.RecipientUserIDs[0] != testUserID {
			t.Fatalf("event recipients = %#v, want [%s]", event.RecipientUserIDs, testUserID)
		}
	default:
		t.Fatal("radar message was persisted but no realtime channel event was published")
	}
}

func TestExecuteRadarChannelPostRejectsAgentOutsidePrivateChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Radar Channel Outsider "+uuid.NewString(), nil)
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	channelID := seedChannelForTest(t, "radar-private-"+uuid.NewString(), testUserID)
	payload, err := json.Marshal(radarChannelPayload{
		ChannelID: channelID,
		Content:   "must not enter a private channel",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = testHandler.executeRadarChannelPost(ctx, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
	}, agent, radar.RadarAction{
		Type:    radar.ActionPostChannelMessage,
		Payload: payload,
	})
	if err == nil {
		t.Fatal("non-member radar agent posted into a private channel")
	}
	if got := radarChannelMessageCountForTest(t, channelID); got != 0 {
		t.Fatalf("private channel contains %d radar message(s), want 0", got)
	}
}

func TestExecuteRadarChannelPostRejectsChannelOutsideRunWorkspace(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Radar Cross Workspace "+uuid.NewString(), nil)
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	otherWorkspaceID := createOtherTestWorkspace(t)
	var otherChannelID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, created_by)
		VALUES ($1, $2, $3)
		RETURNING id
	`, otherWorkspaceID, "radar-foreign-"+uuid.NewString(), testUserID).Scan(&otherChannelID); err != nil {
		t.Fatalf("create foreign channel: %v", err)
	}
	payload, err := json.Marshal(radarChannelPayload{
		ChannelID: otherChannelID,
		Content:   "must not cross workspace boundary",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = testHandler.executeRadarChannelPost(ctx, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
	}, agent, radar.RadarAction{
		Type:    radar.ActionPostChannelMessage,
		Payload: payload,
	})
	if err == nil {
		t.Fatal("radar agent posted into a channel from another workspace")
	}
	if got := radarChannelMessageCountForTest(t, otherChannelID); got != 0 {
		t.Fatalf("foreign channel contains %d cross-workspace radar message(s), want 0", got)
	}
}

func TestExecuteRadarChannelPostRejectsThreadRootFromAnotherChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Radar Thread Boundary "+uuid.NewString(), nil)
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	targetChannelID := seedChannelForTest(t, "radar-thread-target-"+uuid.NewString(), testUserID)
	otherChannelID := seedChannelForTest(t, "radar-thread-other-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, targetChannelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add radar agent to target channel: %v", err)
	}
	root, err := testHandler.insertChannelMessage(
		ctx,
		parseUUID(otherChannelID),
		parseUUID(testWorkspaceID),
		"user",
		parseUUID(testUserID),
		"Thread Author",
		"root in another channel",
		"multica",
		nil,
		pgtype.UUID{},
		pgtype.UUID{},
		nil,
		0,
	)
	if err != nil {
		t.Fatalf("create foreign thread root: %v", err)
	}
	payload, err := json.Marshal(radarChannelPayload{
		ChannelID:           targetChannelID,
		ThreadRootMessageID: root.ID,
		Content:             "must not attach to another channel's thread",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = testHandler.executeRadarChannelPost(ctx, db.AgentRadarRun{
		WorkspaceID: parseUUID(testWorkspaceID),
	}, agent, radar.RadarAction{
		Type:    radar.ActionReplyThread,
		Payload: payload,
	})
	if err == nil {
		t.Fatal("radar reply accepted a thread root from another channel")
	}
	if got := radarChannelMessageCountForTest(t, targetChannelID); got != 0 {
		t.Fatalf("target channel contains %d invalid radar reply message(s), want 0", got)
	}
}

func TestExecuteAgentRadarActionDoesNotBroadcastUnverifiedTargetReason(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Radar Activity Boundary "+uuid.NewString(), nil)
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	run := createRadarRunForExecutorTest(t, agent)
	channelID := seedChannelForTest(t, "radar-activity-private-"+uuid.NewString(), testUserID)
	secretReason := "workspace-secret-reason-" + uuid.NewString()
	payload, err := json.Marshal(radarChannelPayload{
		ChannelID: channelID,
		Content:   "unauthorized post",
	})
	if err != nil {
		t.Fatal(err)
	}
	broadcasts := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventAgentActivityEvent, func(e events.Event) {
		activity, ok := e.Payload.(AgentActivityEventRealtimePayload)
		if ok && activity.AgentID == agentID && activity.Event != nil && activity.Event.EventType == "radar_action_failed" {
			broadcasts <- e
		}
	})

	err = testHandler.executeAgentRadarAction(ctx, run, agent, radar.RadarAction{
		Type:       radar.ActionPostChannelMessage,
		TargetKind: "none",
		Reason:     secretReason,
		Payload:    payload,
	})
	if err == nil {
		t.Fatal("unauthorized radar channel action unexpectedly succeeded")
	}

	var eventID, visibility, targetKind, message string
	if err := testPool.QueryRow(ctx, `
		SELECT id, visibility, target_kind, message
		FROM agent_activity_event
		WHERE workspace_id = $1 AND agent_id = $2 AND event_type = 'radar_action_failed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, agentID).Scan(&eventID, &visibility, &targetKind, &message); err != nil {
		t.Fatalf("load failed radar activity: %v", err)
	}
	if visibility != "diagnostic_only" {
		t.Fatalf("unverified radar target visibility = %q, want diagnostic_only", visibility)
	}
	if targetKind != "none" {
		t.Fatalf("unverified radar activity target kind = %q, want none", targetKind)
	}
	if strings.Contains(message, secretReason) {
		t.Fatalf("unverified radar activity leaked model reason in message: %q", message)
	}
	activities := listAgentActivityEventsForUser(t, testUserID, agentID, "")
	if event := findActivityTimelineEvent(activities, eventID); event != nil {
		t.Fatalf("unverified radar activity appeared in the user-facing timeline: %+v", *event)
	}
	select {
	case event := <-broadcasts:
		t.Fatalf("unverified radar activity was broadcast to workspace: %+v", event)
	default:
	}
}

func TestExecuteAgentRadarActionDerivesActivityTargetFromVerifiedChannel(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}

	ctx := context.Background()
	agentID := createHandlerTestAgent(t, "Radar Verified Activity Target "+uuid.NewString(), nil)
	agent, err := testHandler.Queries.GetAgent(ctx, parseUUID(agentID))
	if err != nil {
		t.Fatalf("load agent: %v", err)
	}
	run := createRadarRunForExecutorTest(t, agent)
	channelID := seedChannelForTest(t, "radar-verified-activity-"+uuid.NewString(), testUserID)
	if _, err := testPool.Exec(ctx, `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, channelID, testWorkspaceID, agentID); err != nil {
		t.Fatalf("add radar agent to channel: %v", err)
	}
	payload, err := json.Marshal(radarChannelPayload{ChannelID: channelID, Content: "verified activity target"})
	if err != nil {
		t.Fatal(err)
	}

	err = testHandler.executeAgentRadarAction(ctx, run, agent, radar.RadarAction{
		Type:       radar.ActionPostChannelMessage,
		TargetKind: "agent",
		TargetID:   agentID,
		Reason:     "publish useful finding",
		Payload:    payload,
	})
	if err != nil {
		t.Fatalf("execute verified radar action: %v", err)
	}

	var targetKind, targetID string
	if err := testPool.QueryRow(ctx, `
		SELECT target_kind, target_id
		FROM agent_activity_event
		WHERE workspace_id = $1 AND agent_id = $2 AND event_type = 'radar_action_executed'
		ORDER BY created_at DESC, id DESC
		LIMIT 1
	`, testWorkspaceID, agentID).Scan(&targetKind, &targetID); err != nil {
		t.Fatalf("load executed radar activity: %v", err)
	}
	if targetKind != "channel" || targetID != channelID {
		t.Fatalf("radar activity target = %s/%s, want channel/%s", targetKind, targetID, channelID)
	}
}

func TestExecuteRadarPlanUpdateRejectsRunAgentMismatchBeforeRuntimeRequest(t *testing.T) {
	workspaceID := parseUUID(uuid.NewString())
	agentID := parseUUID(uuid.NewString())
	otherID := parseUUID(uuid.NewString())
	action := radar.RadarAction{
		Type:    radar.ActionUpdateAgentPlan,
		Payload: json.RawMessage(`{"content":"inspect the next issue"}`),
	}

	tests := []struct {
		name      string
		run       db.AgentRadarRun
		agent     db.Agent
		wantError string
	}{
		{
			name:      "workspace mismatch",
			run:       db.AgentRadarRun{WorkspaceID: workspaceID, AgentID: agentID},
			agent:     db.Agent{ID: agentID, WorkspaceID: otherID},
			wantError: "radar agent does not belong to the run workspace",
		},
		{
			name:      "agent mismatch",
			run:       db.AgentRadarRun{WorkspaceID: workspaceID, AgentID: otherID},
			agent:     db.Agent{ID: agentID, WorkspaceID: workspaceID},
			wantError: "radar agent does not match the run",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, target, err := (&Handler{}).executeApprovedRadarActionWithTarget(
				t.Context(), tt.run, tt.agent, action,
			)
			if err == nil || err.Error() != tt.wantError {
				t.Fatalf("error = %v, want %q", err, tt.wantError)
			}
			if result != nil || target.Trusted {
				t.Fatalf("rejected plan update returned result=%#v target=%+v", result, target)
			}
		})
	}
}

func createRadarRunForExecutorTest(t *testing.T, agent db.Agent) db.AgentRadarRun {
	t.Helper()
	run, err := testHandler.Queries.CreateAgentRadarRun(context.Background(), db.CreateAgentRadarRunParams{
		WorkspaceID:    agent.WorkspaceID,
		AgentID:        agent.ID,
		RuntimeID:      agent.RuntimeID,
		TriggerKind:    "manual",
		TriggerRef:     "executor-test",
		Status:         "running",
		CooldownKey:    "executor-test-" + uuid.NewString(),
		ContextSummary: "executor authorization test",
		ScheduledFor:   pgtype.Timestamptz{Time: time.Now(), Valid: true},
	})
	if err != nil {
		t.Fatalf("create radar run: %v", err)
	}
	return run
}

func radarChannelMessageCountForTest(t *testing.T, channelID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'agent' AND content LIKE '主动发现：%'
	`, channelID).Scan(&count); err != nil {
		t.Fatalf("count radar channel messages: %v", err)
	}
	return count
}
