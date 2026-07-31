package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis-backed implementation of RestartStore. Same wire layout and claim
// discipline as RedisModelListStore/RedisLocalSkillListStore: namespaced
// keys, a ZSET-backed pending queue per runtime, atomic claim via the shared
// claimPendingScript (defined in runtime_local_skills_redis_store.go).
//
// Key layout:
//
//	mul:restart:req:<request_id>       → JSON-encoded RestartRequest, TTL = retention
//	mul:restart:pending:<runtime_id>   → ZSET { member = request_id, score = created_at UnixNano }
const (
	restartKeyPrefix     = "mul:restart:req:"
	restartPendingPrefix = "mul:restart:pending:"
)

func restartKey(id string) string               { return restartKeyPrefix + id }
func restartPendingKey(runtimeID string) string { return restartPendingPrefix + runtimeID }

// RedisRestartStore stores restart requests in Redis so every API node
// agrees on the same pending / delivered state.
type RedisRestartStore struct {
	rdb *redis.Client
}

func NewRedisRestartStore(rdb *redis.Client) *RedisRestartStore {
	return &RedisRestartStore{rdb: rdb}
}

func (s *RedisRestartStore) Create(ctx context.Context, runtimeID string) (*RestartRequest, error) {
	now := time.Now()
	req := &RestartRequest{
		ID:        randomID(),
		RuntimeID: runtimeID,
		Status:    RestartPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal restart request: %w", err)
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, restartKey(req.ID), data, restartStoreRetention)
	pipe.ZAdd(ctx, restartPendingKey(runtimeID), redis.Z{
		Score:  float64(now.UnixNano()),
		Member: req.ID,
	})
	pipe.Expire(ctx, restartPendingKey(runtimeID), restartStoreRetention*2)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("persist restart request: %w", err)
	}
	return req, nil
}

func (s *RedisRestartStore) Get(ctx context.Context, id string) (*RestartRequest, error) {
	return s.loadRequest(ctx, id)
}

func (s *RedisRestartStore) loadRequest(ctx context.Context, id string) (*RestartRequest, error) {
	raw, err := s.rdb.Get(ctx, restartKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get restart request: %w", err)
	}
	var req RestartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode restart request: %w", err)
	}
	if applyRestartTimeout(&req, time.Now()) {
		if err := s.persistRequest(ctx, &req); err != nil {
			return nil, err
		}
		s.rdb.ZRem(ctx, restartPendingKey(req.RuntimeID), req.ID)
	}
	return &req, nil
}

func (s *RedisRestartStore) persistRequest(ctx context.Context, req *RestartRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal restart request: %w", err)
	}
	if err := s.rdb.Set(ctx, restartKey(req.ID), data, restartStoreRetention).Err(); err != nil {
		return fmt.Errorf("persist restart request: %w", err)
	}
	return nil
}

// HasPending is a cheap read-only ZCARD probe used by the heartbeat hot path
// to decide whether to invoke the side-effecting PopPending.
func (s *RedisRestartStore) HasPending(ctx context.Context, runtimeID string) (bool, error) {
	cnt, err := s.rdb.ZCard(ctx, restartPendingKey(runtimeID)).Result()
	if err != nil {
		return false, fmt.Errorf("zcard pending: %w", err)
	}
	return cnt > 0, nil
}

func (s *RedisRestartStore) PopPending(ctx context.Context, runtimeID string) (*RestartRequest, error) {
	pendingKey := restartPendingKey(runtimeID)

	for attempt := 0; attempt < modelListRedisPopMaxRetries; attempt++ {
		ids, err := s.rdb.ZRange(ctx, pendingKey, 0, 0).Result()
		if err != nil {
			return nil, fmt.Errorf("zrange pending: %w", err)
		}
		if len(ids) == 0 {
			return nil, nil
		}
		id := ids[0]

		req, err := s.loadRequest(ctx, id)
		if err != nil {
			return nil, err
		}
		if req == nil {
			// Record expired but the zset still references it — drop and retry.
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}
		if req.Status != RestartPending {
			// Timed out inside loadRequest, or another node already claimed it.
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}

		now := time.Now()
		req.Status = RestartDelivered
		req.DeliveredAt = &now
		req.UpdatedAt = now
		data, err := json.Marshal(req)
		if err != nil {
			return nil, fmt.Errorf("marshal restart request: %w", err)
		}

		result, err := claimPendingScript.Run(
			ctx, s.rdb,
			[]string{pendingKey, restartKey(id)},
			id, data, int(restartStoreRetention.Seconds()),
		).Int64()
		if err != nil {
			return nil, fmt.Errorf("claim pending: %w", err)
		}
		if result == 0 {
			// Another node won the race — retry to pick up whatever else is queued.
			continue
		}
		return req, nil
	}
	return nil, nil
}
