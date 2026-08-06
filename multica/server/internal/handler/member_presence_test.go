package handler

import (
	"context"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/events"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMemoryMemberPresenceStore_ConnectDisconnect(t *testing.T) {
	s := NewMemoryMemberPresenceStore()
	ctx := context.Background()
	ws, user := "ws-1", "user-1"

	became, err := s.Connect(ctx, ws, user)
	if err != nil || !became {
		t.Fatalf("first connect: became=%v err=%v", became, err)
	}
	became, err = s.Connect(ctx, ws, user)
	if err != nil || became {
		t.Fatalf("second connect should not become online: became=%v err=%v", became, err)
	}
	online, err := s.IsOnline(ctx, ws, user)
	if err != nil || !online {
		t.Fatalf("expected online, got %v err=%v", online, err)
	}
	ids, err := s.OnlineUserIDs(ctx, ws)
	if err != nil || len(ids) != 1 || ids[0] != user {
		t.Fatalf("OnlineUserIDs=%v err=%v", ids, err)
	}

	became, err = s.Disconnect(ctx, ws, user)
	if err != nil || became {
		t.Fatalf("first disconnect with 2 sessions: became=%v err=%v", became, err)
	}
	became, err = s.Disconnect(ctx, ws, user)
	if err != nil || !became {
		t.Fatalf("last disconnect: became=%v err=%v", became, err)
	}
	online, err = s.IsOnline(ctx, ws, user)
	if err != nil || online {
		t.Fatalf("expected offline, got %v err=%v", online, err)
	}
}

func TestMemoryMemberPresenceStore_MarkActiveWithoutSession(t *testing.T) {
	s := NewMemoryMemberPresenceStore()
	ctx := context.Background()
	ws, user := "ws-2", "user-2"

	became, err := s.MarkActive(ctx, ws, user)
	if err != nil || !became {
		t.Fatalf("mark-active should become online: became=%v err=%v", became, err)
	}
	online, err := s.IsOnline(ctx, ws, user)
	if err != nil || !online {
		t.Fatalf("expected online after mark-active, got %v err=%v", online, err)
	}
	became, err = s.MarkActive(ctx, ws, user)
	if err != nil || became {
		t.Fatalf("second mark-active should not become online: became=%v err=%v", became, err)
	}
}

func TestHandler_NoteMemberActivityForcePublishesOnline(t *testing.T) {
	bus := events.New()
	var mu sync.Mutex
	var got []events.Event
	bus.Subscribe(protocol.EventMemberPresence, func(e events.Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})

	h := &Handler{
		Bus:                 bus,
		MemberPresenceStore: NewMemoryMemberPresenceStore(),
	}

	// Already-online member: forcePublish still emits so FE heals stale Offline.
	_, _ = h.MemberPresenceStore.Connect(context.Background(), "ws-1", "user-1")
	h.noteMemberActivity("ws-1", "user-1", true)

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 online event, got %d", len(got))
	}
	payload, _ := got[0].Payload.(map[string]any)
	if payload["status"] != "online" || payload["user_id"] != "user-1" {
		t.Fatalf("payload=%v", payload)
	}
}

func TestHandler_NoteMemberActivityPongOnlyPublishesOnRestore(t *testing.T) {
	bus := events.New()
	var mu sync.Mutex
	var got []events.Event
	bus.Subscribe(protocol.EventMemberPresence, func(e events.Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})

	h := &Handler{
		Bus:                 bus,
		MemberPresenceStore: NewMemoryMemberPresenceStore(),
	}

	h.noteMemberActivity("ws-1", "user-1", false)
	mu.Lock()
	if len(got) != 1 {
		mu.Unlock()
		t.Fatalf("first pong restore should publish online, got %d", len(got))
	}
	got = got[:0]
	mu.Unlock()

	h.noteMemberActivity("ws-1", "user-1", false)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 0 {
		t.Fatalf("already-online pong should not republish, got %d", len(got))
	}
}

func TestHandler_MemberPresenceTransitionPublishesOnce(t *testing.T) {
	bus := events.New()
	var mu sync.Mutex
	var got []events.Event
	bus.Subscribe(protocol.EventMemberPresence, func(e events.Event) {
		mu.Lock()
		got = append(got, e)
		mu.Unlock()
	})

	h := &Handler{
		Bus:                 bus,
		MemberPresenceStore: NewMemoryMemberPresenceStore(),
	}

	h.handleMemberPresenceTransition("ws-1", "user-1", true)
	h.handleMemberPresenceTransition("ws-1", "user-1", true)

	mu.Lock()
	if len(got) != 1 {
		t.Fatalf("expected 1 online event, got %d", len(got))
	}
	if got[0].Type != protocol.EventMemberPresence {
		t.Fatalf("type=%q", got[0].Type)
	}
	got = got[:0]
	mu.Unlock()

	h.handleMemberPresenceTransition("ws-1", "user-1", false)
	mu.Lock()
	if len(got) != 0 {
		t.Fatalf("expected no offline yet, got %d", len(got))
	}
	mu.Unlock()

	h.handleMemberPresenceTransition("ws-1", "user-1", false)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected 1 offline event, got %d", len(got))
	}
	payload, _ := got[0].Payload.(map[string]any)
	if payload["status"] != "offline" {
		t.Fatalf("payload=%v", payload)
	}
}
