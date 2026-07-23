package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

// UserPresence is the derived online/offline signal for a human member.
// Online means the user currently has a live Multica WebSocket session
// (desktop or mobile) that has touched Redis within UserPresenceTTL.
// LRM-238: absence of a signal must surface as Offline — never invent Online.
type UserPresence struct {
	Online     bool
	LastSeenAt *time.Time
}

// UserPresenceStore tracks short-lived "this human user has Multica open"
// records. Distinct from runtime LivenessStore (daemon heartbeat): human
// presence is session-based, not agent-runtime reachability.
//
// MVP (LRM-462): binary online|offline only — no stealth / DND / fine privacy.
type UserPresenceStore interface {
	Available() bool
	// Touch records a fresh session heartbeat for userID using UserPresenceTTL.
	Touch(ctx context.Context, userID string) error
	// GetBatch returns presence for every input ID. When the store is
	// unavailable or Redis errors, every ID is Offline (LRM-238 — no fake
	// online). Missing keys are Offline with nil LastSeenAt.
	GetBatch(ctx context.Context, userIDs []string) map[string]UserPresence
}

// UserPresenceTTL is how long a human session stays Online after the last
// WebSocket touch. Sized above the ~54s server WS ping period so one missed
// pong does not flap Offline; within the product 60–120s offline window.
const UserPresenceTTL = 120 * time.Second

// UserPresenceTouchMinGap throttles Redis writes from high-frequency pong
// callbacks. Server ping is ~54s, so a 25s floor still refreshes every beat.
const UserPresenceTouchMinGap = 25 * time.Second

const (
	userPresenceOnline  = "online"
	userPresenceOffline = "offline"
	userPresenceKeyPrefix = "mul:user:hb:"
)

func userPresenceKey(userID string) string {
	return userPresenceKeyPrefix + userID
}

type noopUserPresenceStore struct{}

// NewNoopUserPresenceStore returns a store that always reports unavailable.
// Callers treat every member as offline (no silent fake online).
func NewNoopUserPresenceStore() UserPresenceStore { return noopUserPresenceStore{} }

func (noopUserPresenceStore) Available() bool { return false }

func (noopUserPresenceStore) Touch(_ context.Context, _ string) error { return nil }

func (noopUserPresenceStore) GetBatch(_ context.Context, userIDs []string) map[string]UserPresence {
	out := make(map[string]UserPresence, len(userIDs))
	for _, id := range userIDs {
		out[id] = UserPresence{Online: false}
	}
	return out
}

// RedisUserPresenceStore writes one TTL'd key per user session heartbeat.
// Value is an RFC3339Nano last_seen timestamp so list/profile APIs can
// expose last_seen_at without a DB column (MVP is Redis-only).
type RedisUserPresenceStore struct {
	rdb *redis.Client
	ttl time.Duration
}

func NewRedisUserPresenceStore(rdb *redis.Client) *RedisUserPresenceStore {
	return &RedisUserPresenceStore{rdb: rdb, ttl: UserPresenceTTL}
}

func (s *RedisUserPresenceStore) Available() bool { return s != nil && s.rdb != nil }

func (s *RedisUserPresenceStore) Touch(ctx context.Context, userID string) error {
	if !s.Available() {
		return errors.New("redis user presence store: unavailable")
	}
	if userID == "" {
		return errors.New("redis user presence store: empty user id")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := s.rdb.Set(ctx, userPresenceKey(userID), now, s.ttl).Err(); err != nil {
		return fmt.Errorf("user presence touch: %w", err)
	}
	return nil
}

func (s *RedisUserPresenceStore) GetBatch(ctx context.Context, userIDs []string) map[string]UserPresence {
	out := make(map[string]UserPresence, len(userIDs))
	for _, id := range userIDs {
		out[id] = UserPresence{Online: false}
	}
	if !s.Available() || len(userIDs) == 0 {
		return out
	}
	keys := make([]string, len(userIDs))
	for i, id := range userIDs {
		keys[i] = userPresenceKey(id)
	}
	values, err := s.rdb.MGet(ctx, keys...).Result()
	if err != nil {
		slog.Warn("user presence mget failed; treating members as offline",
			"error", err, "count", len(keys))
		return out
	}
	for i, id := range userIDs {
		if values[i] == nil {
			continue
		}
		raw, _ := values[i].(string)
		p := UserPresence{Online: true}
		if raw != "" {
			if ts, err := time.Parse(time.RFC3339Nano, raw); err == nil {
				t := ts.UTC()
				p.LastSeenAt = &t
			}
		}
		out[id] = p
	}
	return out
}

// presenceLabel returns the API string for a UserPresence.
func presenceLabel(p UserPresence) string {
	if p.Online {
		return userPresenceOnline
	}
	return userPresenceOffline
}

func userPresenceLastSeenPtr(p UserPresence) *string {
	if p.LastSeenAt == nil {
		return nil
	}
	s := p.LastSeenAt.UTC().Format(time.RFC3339Nano)
	return &s
}

func (h *Handler) userPresenceBatch(ctx context.Context, userIDs []string) map[string]UserPresence {
	store := h.UserPresenceStore
	if store == nil {
		store = NewNoopUserPresenceStore()
	}
	return store.GetBatch(ctx, userIDs)
}

func (h *Handler) lookupUserPresence(ctx context.Context, userID string) UserPresence {
	return h.userPresenceBatch(ctx, []string{userID})[userID]
}
