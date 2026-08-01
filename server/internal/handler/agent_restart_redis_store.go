package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis-backed implementation of AgentRestartStore. Same wire layout and
// claim discipline as RestartStore/RedisLocalSkillImportStore's
// PopPendingBatch: namespaced keys, a ZSET-backed pending queue per runtime,
// atomic per-item claim via the shared claimPendingScript.
//
// Key layout:
//
//	mul:agent_restart:req:<request_id>     → JSON-encoded AgentRestartRequest, TTL = retention
//	mul:agent_restart:pending:<runtime_id> → ZSET { member = request_id, score = created_at UnixNano }
const (
	agentRestartKeyPrefix     = "mul:agent_restart:req:"
	agentRestartPendingPrefix = "mul:agent_restart:pending:"
	// agentRestartBatchLimit bounds how many pending agent-restart requests
	// one heartbeat will claim at once — generous relative to how many
	// agents realistically share one runtime.
	agentRestartBatchLimit = 50
)

func agentRestartKey(id string) string            { return agentRestartKeyPrefix + id }
func agentRestartPendingKey(runtimeID string) string { return agentRestartPendingPrefix + runtimeID }

// RedisAgentRestartStore stores agent-restart requests in Redis so every API
// node agrees on the same pending / delivered state.
type RedisAgentRestartStore struct {
	rdb *redis.Client
}

func NewRedisAgentRestartStore(rdb *redis.Client) *RedisAgentRestartStore {
	return &RedisAgentRestartStore{rdb: rdb}
}

func (s *RedisAgentRestartStore) Create(ctx context.Context, agentID, runtimeID string) (*AgentRestartRequest, error) {
	now := time.Now()
	req := &AgentRestartRequest{
		ID:        randomID(),
		AgentID:   agentID,
		RuntimeID: runtimeID,
		Status:    AgentRestartPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("marshal agent restart request: %w", err)
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, agentRestartKey(req.ID), data, agentRestartStoreRetention)
	pipe.ZAdd(ctx, agentRestartPendingKey(runtimeID), redis.Z{
		Score:  float64(now.UnixNano()),
		Member: req.ID,
	})
	pipe.Expire(ctx, agentRestartPendingKey(runtimeID), agentRestartStoreRetention*2)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("persist agent restart request: %w", err)
	}
	return req, nil
}

func (s *RedisAgentRestartStore) Get(ctx context.Context, id string) (*AgentRestartRequest, error) {
	return s.loadRequest(ctx, id)
}

func (s *RedisAgentRestartStore) loadRequest(ctx context.Context, id string) (*AgentRestartRequest, error) {
	raw, err := s.rdb.Get(ctx, agentRestartKey(id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent restart request: %w", err)
	}
	var req AgentRestartRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("decode agent restart request: %w", err)
	}
	if applyAgentRestartTimeout(&req, time.Now()) {
		if err := s.persistRequest(ctx, &req); err != nil {
			return nil, err
		}
		s.rdb.ZRem(ctx, agentRestartPendingKey(req.RuntimeID), req.ID)
	}
	return &req, nil
}

func (s *RedisAgentRestartStore) persistRequest(ctx context.Context, req *AgentRestartRequest) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal agent restart request: %w", err)
	}
	if err := s.rdb.Set(ctx, agentRestartKey(req.ID), data, agentRestartStoreRetention).Err(); err != nil {
		return fmt.Errorf("persist agent restart request: %w", err)
	}
	return nil
}

// HasPending is a cheap read-only ZCARD probe used by the heartbeat hot path
// to decide whether to invoke the side-effecting PopAllPending.
func (s *RedisAgentRestartStore) HasPending(ctx context.Context, runtimeID string) (bool, error) {
	cnt, err := s.rdb.ZCard(ctx, agentRestartPendingKey(runtimeID)).Result()
	if err != nil {
		return false, fmt.Errorf("zcard pending: %w", err)
	}
	return cnt > 0, nil
}

func (s *RedisAgentRestartStore) PopAllPending(ctx context.Context, runtimeID string) ([]*AgentRestartRequest, error) {
	pendingKey := agentRestartPendingKey(runtimeID)

	ids, err := s.rdb.ZRange(ctx, pendingKey, 0, agentRestartBatchLimit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("zrange pending: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	var claimed []*AgentRestartRequest
	for _, id := range ids {
		req, err := s.loadRequest(ctx, id)
		if err != nil {
			return claimed, err
		}
		if req == nil {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}
		if req.Status != AgentRestartPending {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}

		now := time.Now()
		req.Status = AgentRestartDelivered
		req.DeliveredAt = &now
		req.UpdatedAt = now
		data, err := json.Marshal(req)
		if err != nil {
			return claimed, fmt.Errorf("marshal agent restart request: %w", err)
		}

		result, err := claimPendingScript.Run(
			ctx, s.rdb,
			[]string{pendingKey, agentRestartKey(id)},
			id, data, int(agentRestartStoreRetention.Seconds()),
		).Int64()
		if err != nil {
			return claimed, fmt.Errorf("claim pending: %w", err)
		}
		if result == 1 {
			claimed = append(claimed, req)
		}
	}
	return claimed, nil
}
