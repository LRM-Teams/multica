package main

import (
	"errors"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// fakeBroadcaster records every fanout call so tests can assert which scope a
// given event landed on.
type fakeBroadcaster struct {
	mu              sync.Mutex
	scopeCalls      []scopeCall
	workspaceCalls  []workspaceCall
	userCalls       []userCall
	idUserCalls     []idUserCall
	broadcastCalled int
	idUserErr       error
}

type scopeCall struct {
	scopeType, scopeID string
	msg                []byte
}
type workspaceCall struct {
	workspaceID string
	msg         []byte
}
type userCall struct {
	userID  string
	msg     []byte
	exclude []string
}
type idUserCall struct {
	userID, eventID string
	msg             []byte
}

func (f *fakeBroadcaster) BroadcastToScope(scopeType, scopeID string, message []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scopeCalls = append(f.scopeCalls, scopeCall{scopeType, scopeID, message})
}
func (f *fakeBroadcaster) BroadcastToWorkspace(workspaceID string, message []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaceCalls = append(f.workspaceCalls, workspaceCall{workspaceID, message})
}
func (f *fakeBroadcaster) SendToUser(userID string, message []byte, excludeWorkspace ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.userCalls = append(f.userCalls, userCall{userID, message, excludeWorkspace})
}
func (f *fakeBroadcaster) SendToUserWithID(userID string, message []byte, eventID string, _ ...string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.idUserCalls = append(f.idUserCalls, idUserCall{userID: userID, eventID: eventID, msg: message})
	return f.idUserErr
}
func (f *fakeBroadcaster) Broadcast(message []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broadcastCalled++
}

// TestRegisterListeners_TaskChatGoToWorkspace pins the must-fix #1 contract
// from the PR #1429 review: until the WS client supports scope-subscribe and
// reconnect-replay, high-frequency task/chat events MUST keep going through
// workspace fanout. Routing them via BroadcastToScope("task"|"chat", ...)
// with no client-side subscriber would silently drop every chat / task
// message and break the live timeline + chat unread badges.
func TestRegisterListeners_TaskChatGoToWorkspace(t *testing.T) {
	cases := []struct {
		name      string
		eventType string
		taskID    string
		chatID    string
	}{
		{"task:message with TaskID", protocol.EventTaskMessage, "task-1", ""},
		{"task:progress with TaskID", protocol.EventTaskProgress, "task-2", ""},
		{"chat:message with ChatSessionID", protocol.EventChatMessage, "", "chat-1"},
		{"chat:done with ChatSessionID", protocol.EventChatDone, "", "chat-2"},
		{"chat:session_read with ChatSessionID", protocol.EventChatSessionRead, "", "chat-3"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bus := events.New()
			fb := &fakeBroadcaster{}
			registerListeners(bus, fb)

			bus.Publish(events.Event{
				Type:          tc.eventType,
				WorkspaceID:   "ws-1",
				TaskID:        tc.taskID,
				ChatSessionID: tc.chatID,
				Payload:       map[string]any{"hello": "world"},
			})

			if len(fb.scopeCalls) != 0 {
				t.Fatalf("expected no BroadcastToScope calls (must-fix #1: keep workspace fanout until client lands), got %+v", fb.scopeCalls)
			}
			if len(fb.workspaceCalls) != 1 {
				t.Fatalf("expected exactly 1 BroadcastToWorkspace call, got %d", len(fb.workspaceCalls))
			}
			if fb.workspaceCalls[0].workspaceID != "ws-1" {
				t.Fatalf("expected workspace ws-1, got %q", fb.workspaceCalls[0].workspaceID)
			}
		})
	}
}

func TestRegisterListeners_RecipientScopedEventsSkipWorkspaceFanout(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:             protocol.EventChannelMessage,
		WorkspaceID:      "ws-1",
		ActorType:        "member",
		ActorID:          "sender-1",
		RecipientUserIDs: []string{"user-1", "user-2", "user-1", " "},
		Payload:          map[string]any{"content": "private channel text"},
	})

	if len(fb.workspaceCalls) != 0 {
		t.Fatalf("expected no BroadcastToWorkspace calls for recipient-scoped event, got %d", len(fb.workspaceCalls))
	}
	if len(fb.userCalls) != 2 {
		t.Fatalf("expected 2 SendToUser calls, got %d", len(fb.userCalls))
	}
	got := map[string]bool{}
	for _, call := range fb.userCalls {
		got[call.userID] = true
	}
	for _, want := range []string{"user-1", "user-2"} {
		if !got[want] {
			t.Fatalf("missing SendToUser recipient %q; got %+v", want, got)
		}
	}
}

func TestRegisterListeners_RecipientScopedEmptyListFailsClosed(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	bus.Publish(events.Event{
		Type:             protocol.EventChannelMessage,
		WorkspaceID:      "ws-1",
		RecipientUserIDs: []string{},
		Payload:          map[string]any{"content": "unroutable private text"},
	})

	if len(fb.workspaceCalls) != 0 {
		t.Fatalf("expected no BroadcastToWorkspace calls for empty recipient-scoped event, got %d", len(fb.workspaceCalls))
	}
	if len(fb.userCalls) != 0 {
		t.Fatalf("expected no SendToUser calls for empty recipient-scoped event, got %d", len(fb.userCalls))
	}
}

func TestRegisterListeners_RecipientScopedStableIDUsesIdempotentFanout(t *testing.T) {
	bus := events.New()
	fb := &fakeBroadcaster{}
	registerListeners(bus, fb)

	var acknowledged bool
	bus.Publish(events.Event{
		Type:             protocol.EventChannelMessage,
		WorkspaceID:      "ws-1",
		RecipientUserIDs: []string{"user-1", "user-1"},
		RealtimeEventID:  "system-message-1",
		Payload:          map[string]any{"content": "joined"},
		RealtimeDeliveryAck: func(err error) {
			if err != nil {
				t.Fatalf("stable recipient publication ack: %v", err)
			}
			acknowledged = true
		},
	})

	if len(fb.userCalls) != 0 {
		t.Fatalf("stable recipient event used non-idempotent fanout: %+v", fb.userCalls)
	}
	if len(fb.idUserCalls) != 1 {
		t.Fatalf("stable recipient event fanout calls = %d, want 1", len(fb.idUserCalls))
	}
	if got := fb.idUserCalls[0]; got.userID != "user-1" || got.eventID != "system-message-1" {
		t.Fatalf("stable recipient call = %+v", got)
	}
	if !acknowledged {
		t.Fatal("stable recipient publication was not acknowledged")
	}
}

func TestRegisterListeners_RecipientScopedStableIDPropagatesDeliveryFailure(t *testing.T) {
	bus := events.New()
	wantErr := errors.New("relay unavailable")
	fb := &fakeBroadcaster{idUserErr: wantErr}
	registerListeners(bus, fb)

	var gotErr error
	bus.Publish(events.Event{
		Type:             protocol.EventChannelMessage,
		WorkspaceID:      "ws-1",
		RecipientUserIDs: []string{"user-1"},
		RealtimeEventID:  "system-message-1",
		Payload:          map[string]any{"content": "joined"},
		RealtimeDeliveryAck: func(err error) {
			gotErr = err
		},
	})

	if !errors.Is(gotErr, wantErr) {
		t.Fatalf("stable recipient publication ack error = %v, want %v", gotErr, wantErr)
	}
}
