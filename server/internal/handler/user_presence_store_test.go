package handler

import (
	"context"
	"testing"
	"time"
)

func TestNoopUserPresenceStore_AlwaysOffline(t *testing.T) {
	s := NewNoopUserPresenceStore()
	if s.Available() {
		t.Fatal("noop store reported Available()=true")
	}
	if err := s.Touch(context.Background(), "user-1"); err != nil {
		t.Fatalf("noop Touch returned error: %v", err)
	}
	got := s.GetBatch(context.Background(), []string{"user-1"})
	if got["user-1"].Online {
		t.Fatal("noop GetBatch must not invent online (LRM-238)")
	}
}

func TestRedisUserPresenceStore_TouchAndGetBatch(t *testing.T) {
	rdb := newRedisTestClient(t)
	ctx := context.Background()
	s := NewRedisUserPresenceStore(rdb)

	if !s.Available() {
		t.Fatal("redis store reported Available()=false with a live client")
	}

	before := time.Now().UTC().Add(-time.Second)
	if err := s.Touch(ctx, "user-1"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	after := time.Now().UTC().Add(time.Second)

	got := s.GetBatch(ctx, []string{"user-1", "user-missing"})
	if !got["user-1"].Online {
		t.Fatal("user-1 was just touched but GetBatch reported offline")
	}
	if got["user-1"].LastSeenAt == nil {
		t.Fatal("expected last_seen_at on online user")
	}
	if got["user-1"].LastSeenAt.Before(before) || got["user-1"].LastSeenAt.After(after) {
		t.Fatalf("last_seen_at %v outside expected window [%v, %v]", got["user-1"].LastSeenAt, before, after)
	}
	if got["user-missing"].Online {
		t.Fatal("user-missing was never touched but GetBatch reported online")
	}
	if presenceLabel(got["user-1"]) != userPresenceOnline {
		t.Fatalf("presenceLabel online = %q", presenceLabel(got["user-1"]))
	}
	if presenceLabel(got["user-missing"]) != userPresenceOffline {
		t.Fatalf("presenceLabel offline = %q", presenceLabel(got["user-missing"]))
	}
}

func TestRedisUserPresenceStore_TTLExpiry(t *testing.T) {
	rdb := newRedisTestClient(t)
	ctx := context.Background()
	s := &RedisUserPresenceStore{rdb: rdb, ttl: time.Second}

	if err := s.Touch(ctx, "user-expire"); err != nil {
		t.Fatalf("Touch: %v", err)
	}
	got := s.GetBatch(ctx, []string{"user-expire"})
	if !got["user-expire"].Online {
		t.Fatal("expected fresh touch to be online")
	}

	time.Sleep(1500 * time.Millisecond)

	got = s.GetBatch(ctx, []string{"user-expire"})
	if got["user-expire"].Online {
		t.Fatal("expected key to expire after TTL but GetBatch reported online")
	}
}

func TestRedisUserPresenceStore_BatchEmptyInput(t *testing.T) {
	rdb := newRedisTestClient(t)
	s := NewRedisUserPresenceStore(rdb)

	got := s.GetBatch(context.Background(), nil)
	if len(got) != 0 {
		t.Fatalf("empty input should return empty map, got %#v", got)
	}
}
