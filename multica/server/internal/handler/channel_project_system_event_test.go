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

func TestSetChannelProjectWritesTypedSystemEvent(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "project-system-event-"+uuid.NewString(), testUserID)
	var projectID string
	projectTitle := "Project event " + uuid.NewString()
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, projectTitle).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	bind := httptest.NewRecorder()
	bindReq := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPut, "/api/channels/"+channelID+"/project", map[string]any{"project_id": projectID}), testUserID)
	bindReq = withURLParam(bindReq, "channelId", channelID)
	testHandler.SetChannelProject(bind, bindReq)
	if bind.Code != http.StatusOK {
		t.Fatalf("SetChannelProject bind = %d: %s", bind.Code, bind.Body.String())
	}
	bound := latestChannelProjectSystemEventForTest(t, channelID)
	if bound.Event != channelProjectBoundEvent || bound.Params.ProjectID != projectID || bound.Params.ProjectTitle != projectTitle || bound.Params.PreviousProjectID != "" || bound.Params.PreviousProjectTitle != "" {
		t.Fatalf("bound event = %#v, want project %s", bound, projectID)
	}
	if bound.Params.ActorID != testUserID || bound.Params.ActorType != "human" {
		t.Fatalf("bound actor = %#v, want current human %s", bound.Params, testUserID)
	}
	beforeRepeat := channelProjectSystemEventCountForTest(t, channelID)
	repeat := httptest.NewRecorder()
	repeatReq := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPut, "/api/channels/"+channelID+"/project", map[string]any{"project_id": projectID}), testUserID)
	repeatReq = withURLParam(repeatReq, "channelId", channelID)
	testHandler.SetChannelProject(repeat, repeatReq)
	if repeat.Code != http.StatusOK {
		t.Fatalf("SetChannelProject same project = %d: %s", repeat.Code, repeat.Body.String())
	}
	if got := channelProjectSystemEventCountForTest(t, channelID); got != beforeRepeat {
		t.Fatalf("same project binding wrote %d system events, want %d", got, beforeRepeat)
	}

	clear := httptest.NewRecorder()
	clearReq := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPut, "/api/channels/"+channelID+"/project", map[string]any{"project_id": nil}), testUserID)
	clearReq = withURLParam(clearReq, "channelId", channelID)
	testHandler.SetChannelProject(clear, clearReq)
	if clear.Code != http.StatusOK {
		t.Fatalf("SetChannelProject clear = %d: %s", clear.Code, clear.Body.String())
	}
	unbound := latestChannelProjectSystemEventForTest(t, channelID)
	if unbound.Event != channelProjectUnboundEvent || unbound.Params.ProjectID != "" || unbound.Params.ProjectTitle != "" || unbound.Params.PreviousProjectID != projectID || unbound.Params.PreviousProjectTitle != projectTitle {
		t.Fatalf("unbound event = %#v, want previous project %s", unbound, projectID)
	}
}

func TestSetChannelProjectPublishesSystemEventToChannelMembers(t *testing.T) {
	if testHandler == nil || testPool == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	channelID := seedChannelForTest(t, "project-system-event-publish-"+uuid.NewString(), testUserID)
	var projectID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO project (workspace_id, title) VALUES ($1, $2) RETURNING id`,
		testWorkspaceID, "Project publish "+uuid.NewString()).Scan(&projectID); err != nil {
		t.Fatalf("create project: %v", err)
	}
	t.Cleanup(func() { _, _ = testPool.Exec(context.Background(), `DELETE FROM project WHERE id = $1`, projectID) })

	eventsSeen := make(chan events.Event, 1)
	testHandler.Bus.Subscribe(protocol.EventChannelMessage, func(event events.Event) {
		message, ok := event.Payload.(ChannelMessageResponse)
		if ok && message.ChannelID == channelID && message.Type == "system" {
			eventsSeen <- event
		}
	})

	w := httptest.NewRecorder()
	req := withChannelTestWorkspaceCtx(t, newRequest(http.MethodPut, "/api/channels/"+channelID+"/project", map[string]any{"project_id": projectID}), testUserID)
	req = withURLParam(req, "channelId", channelID)
	testHandler.SetChannelProject(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("SetChannelProject = %d: %s", w.Code, w.Body.String())
	}

	select {
	case event := <-eventsSeen:
		if event.ActorType != "system" || event.ActorID != "" {
			t.Fatalf("published actor = %q/%q, want system/empty", event.ActorType, event.ActorID)
		}
		if len(event.RecipientUserIDs) != 1 || event.RecipientUserIDs[0] != testUserID {
			t.Fatalf("published recipients = %#v, want only channel member %s", event.RecipientUserIDs, testUserID)
		}
		message, ok := event.Payload.(ChannelMessageResponse)
		if !ok {
			t.Fatalf("published payload type = %T, want ChannelMessageResponse", event.Payload)
		}
		if message.ChannelID != channelID || message.Type != "system" {
			t.Fatalf("published message = %#v, want system message for %s", message, channelID)
		}
	default:
		t.Fatal("expected channel:message publication for project system event")
	}
}

type channelProjectSystemEventPart struct {
	Event  string                          `json:"event"`
	Params channelProjectSystemEventParams `json:"params"`
}

func latestChannelProjectSystemEventForTest(t *testing.T, channelID string) channelProjectSystemEventPart {
	t.Helper()
	var raw []byte
	if err := testPool.QueryRow(context.Background(), `
		SELECT parts
		FROM channel_message
		WHERE channel_id = $1 AND author_type = 'system'
		ORDER BY seq DESC
		LIMIT 1`, channelID).Scan(&raw); err != nil {
		t.Fatalf("load project system event: %v", err)
	}
	var parts []protocol.MessagePart
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("decode project system event parts: %v", err)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeSystemEvent {
		t.Fatalf("project system event parts = %#v", parts)
	}
	var result channelProjectSystemEventPart
	result.Event = parts[0].Event
	if err := json.Unmarshal(parts[0].EventParams, &result.Params); err != nil {
		t.Fatalf("decode project system event params: %v", err)
	}
	return result
}

func channelProjectSystemEventCountForTest(t *testing.T, channelID string) int {
	t.Helper()
	var count int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*)
		FROM channel_message
		WHERE channel_id = $1
		  AND author_type = 'system'
		  AND parts @> '[{"type":"system_event"}]'::jsonb`, channelID).Scan(&count); err != nil {
		t.Fatalf("count project system events: %v", err)
	}
	return count
}
