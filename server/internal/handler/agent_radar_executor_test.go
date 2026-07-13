package handler

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
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
