package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	// memberPresenceTTL is refreshed on connect and WS pong. If all
	// connections die without a clean unregister, the key expires and the
	// member is treated as offline (common IM grace window).
	memberPresenceTTL = 90 * time.Second

	memberPresenceKeyPrefix = "mul:member:presence:"
)

// MemberPresenceStore tracks human-member online sessions for a workspace.
// Unlike agent runtime liveness (daemon heartbeat), this is driven by the
// browser/app realtime WebSocket connection count (LRM-462).
type MemberPresenceStore interface {
	Available() bool
	// Connect increments the session count. becameOnline is true when the
	// member had zero sessions before this call.
	Connect(ctx context.Context, workspaceID, userID string) (becameOnline bool, err error)
	// Disconnect decrements the session count. becameOffline is true when
	// the count reaches zero (or the key is removed).
	Disconnect(ctx context.Context, workspaceID, userID string) (becameOffline bool, err error)
	// Touch refreshes TTL while a session stays open (WS pong).
	Touch(ctx context.Context, workspaceID, userID string) error
	// OnlineUserIDs returns currently-online member user IDs for a workspace.
	OnlineUserIDs(ctx context.Context, workspaceID string) ([]string, error)
	// IsOnline reports whether a single user is online in the workspace.
	IsOnline(ctx context.Context, workspaceID, userID string) (bool, error)
}

func memberPresenceKey(workspaceID, userID string) string {
	return memberPresenceKeyPrefix + workspaceID + ":" + userID
}

func memberPresenceIndexKey(workspaceID string) string {
	return memberPresenceKeyPrefix + "idx:" + workspaceID
}

// MemoryMemberPresenceStore is the default for tests / no-Redis deployments.
type MemoryMemberPresenceStore struct {
	mu    sync.Mutex
	conns map[string]int // workspace:user -> count
}

func NewMemoryMemberPresenceStore() *MemoryMemberPresenceStore {
	return &MemoryMemberPresenceStore{conns: make(map[string]int)}
}

func (s *MemoryMemberPresenceStore) Available() bool { return s != nil }

func memoryPresenceSlot(workspaceID, userID string) string {
	return workspaceID + "\x00" + userID
}

func (s *MemoryMemberPresenceStore) Connect(_ context.Context, workspaceID, userID string) (bool, error) {
	if s == nil || workspaceID == "" || userID == "" {
		return false, errors.New("member presence: invalid args")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryPresenceSlot(workspaceID, userID)
	prev := s.conns[key]
	s.conns[key] = prev + 1
	return prev == 0, nil
}

func (s *MemoryMemberPresenceStore) Disconnect(_ context.Context, workspaceID, userID string) (bool, error) {
	if s == nil || workspaceID == "" || userID == "" {
		return false, errors.New("member presence: invalid args")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := memoryPresenceSlot(workspaceID, userID)
	prev := s.conns[key]
	if prev <= 1 {
		delete(s.conns, key)
		return prev > 0, nil
	}
	s.conns[key] = prev - 1
	return false, nil
}

func (s *MemoryMemberPresenceStore) Touch(_ context.Context, workspaceID, userID string) error {
	return nil
}

func (s *MemoryMemberPresenceStore) OnlineUserIDs(_ context.Context, workspaceID string) ([]string, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prefix := workspaceID + "\x00"
	out := make([]string, 0)
	for key, n := range s.conns {
		if n > 0 && strings.HasPrefix(key, prefix) {
			out = append(out, strings.TrimPrefix(key, prefix))
		}
	}
	return out, nil
}

func (s *MemoryMemberPresenceStore) IsOnline(_ context.Context, workspaceID, userID string) (bool, error) {
	if s == nil {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conns[memoryPresenceSlot(workspaceID, userID)] > 0, nil
}

// RedisMemberPresenceStore uses a per-user connection counter + workspace index set.
type RedisMemberPresenceStore struct {
	rdb *redis.Client
}

func NewRedisMemberPresenceStore(rdb *redis.Client) *RedisMemberPresenceStore {
	return &RedisMemberPresenceStore{rdb: rdb}
}

func (s *RedisMemberPresenceStore) Available() bool { return s != nil && s.rdb != nil }

func (s *RedisMemberPresenceStore) Connect(ctx context.Context, workspaceID, userID string) (bool, error) {
	if !s.Available() {
		return false, errors.New("redis member presence: unavailable")
	}
	if workspaceID == "" || userID == "" {
		return false, errors.New("redis member presence: empty id")
	}
	key := memberPresenceKey(workspaceID, userID)
	n, err := s.rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("member presence incr: %w", err)
	}
	_ = s.rdb.Expire(ctx, key, memberPresenceTTL).Err()
	_ = s.rdb.SAdd(ctx, memberPresenceIndexKey(workspaceID), userID).Err()
	_ = s.rdb.Expire(ctx, memberPresenceIndexKey(workspaceID), memberPresenceTTL*2).Err()
	return n == 1, nil
}

func (s *RedisMemberPresenceStore) Disconnect(ctx context.Context, workspaceID, userID string) (bool, error) {
	if !s.Available() {
		return false, errors.New("redis member presence: unavailable")
	}
	if workspaceID == "" || userID == "" {
		return false, errors.New("redis member presence: empty id")
	}
	key := memberPresenceKey(workspaceID, userID)
	n, err := s.rdb.Decr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("member presence decr: %w", err)
	}
	if n > 0 {
		_ = s.rdb.Expire(ctx, key, memberPresenceTTL).Err()
		return false, nil
	}
	_ = s.rdb.Del(ctx, key).Err()
	_ = s.rdb.SRem(ctx, memberPresenceIndexKey(workspaceID), userID).Err()
	return true, nil
}

func (s *RedisMemberPresenceStore) Touch(ctx context.Context, workspaceID, userID string) error {
	if !s.Available() || workspaceID == "" || userID == "" {
		return nil
	}
	key := memberPresenceKey(workspaceID, userID)
	ok, err := s.rdb.Expire(ctx, key, memberPresenceTTL).Result()
	if err != nil {
		return fmt.Errorf("member presence touch: %w", err)
	}
	if ok {
		_ = s.rdb.Expire(ctx, memberPresenceIndexKey(workspaceID), memberPresenceTTL*2).Err()
	}
	return nil
}

func (s *RedisMemberPresenceStore) OnlineUserIDs(ctx context.Context, workspaceID string) ([]string, error) {
	if !s.Available() || workspaceID == "" {
		return nil, nil
	}
	ids, err := s.rdb.SMembers(ctx, memberPresenceIndexKey(workspaceID)).Result()
	if err != nil {
		return nil, fmt.Errorf("member presence smembers: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	alive := make([]string, 0, len(ids))
	for _, id := range ids {
		nStr, err := s.rdb.Get(ctx, memberPresenceKey(workspaceID, id)).Result()
		if err == redis.Nil {
			_ = s.rdb.SRem(ctx, memberPresenceIndexKey(workspaceID), id).Err()
			continue
		}
		if err != nil {
			slog.Warn("member presence get failed", "error", err, "user_id", id)
			continue
		}
		n, _ := strconv.Atoi(nStr)
		if n > 0 {
			alive = append(alive, id)
		} else {
			_ = s.rdb.SRem(ctx, memberPresenceIndexKey(workspaceID), id).Err()
		}
	}
	return alive, nil
}

func (s *RedisMemberPresenceStore) IsOnline(ctx context.Context, workspaceID, userID string) (bool, error) {
	if !s.Available() || workspaceID == "" || userID == "" {
		return false, nil
	}
	n, err := s.rdb.Get(ctx, memberPresenceKey(workspaceID, userID)).Int()
	if err == redis.Nil {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
