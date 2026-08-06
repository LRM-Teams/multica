package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis-backed implementation of AgentLifecycleDispatchStore. Same wire
// layout and claim discipline as AgentRestartStore/RedisLocalSkillImportStore's
// PopPendingBatch: namespaced keys, a ZSET-backed pending queue per runtime,
// atomic per-item claim via the shared claimPendingScript.
//
// Key layout:
//
//	mul:agent_lifecycle_dispatch:req:<operation_id>     → JSON-encoded AgentLifecycleDispatch, TTL = retention
//	mul:agent_lifecycle_dispatch:pending:<runtime_id>   → ZSET { member = operation_id, score = created_at UnixNano }
const (
	agentLifecycleDispatchKeyPrefix     = "mul:agent_lifecycle_dispatch:req:"
	agentLifecycleDispatchPendingPrefix = "mul:agent_lifecycle_dispatch:pending:"
	// agentLifecycleDispatchBatchLimit bounds how many pending dispatches one
	// heartbeat will claim at once — generous relative to how many agents
	// realistically share one runtime.
	agentLifecycleDispatchBatchLimit = 50
)

func agentLifecycleDispatchKey(operationID string) string {
	return agentLifecycleDispatchKeyPrefix + operationID
}
func agentLifecycleDispatchPendingKey(runtimeID string) string {
	return agentLifecycleDispatchPendingPrefix + runtimeID
}

// RedisAgentLifecycleDispatchStore stores dispatch entries in Redis so every
// API node agrees on the same pending / delivered state.
type RedisAgentLifecycleDispatchStore struct {
	rdb       *redis.Client
	onTimeout agentLifecycleDispatchTimeoutHook
}

// NewRedisAgentLifecycleDispatchStore wires exec as the timeout hook's
// database access (see newAgentLifecycleDispatchTimeoutFailer); pass nil in
// tests that don't care about the operation-row side effect.
func NewRedisAgentLifecycleDispatchStore(rdb *redis.Client, exec dbExecutor) *RedisAgentLifecycleDispatchStore {
	return &RedisAgentLifecycleDispatchStore{rdb: rdb, onTimeout: newAgentLifecycleDispatchTimeoutFailer(exec)}
}

func (s *RedisAgentLifecycleDispatchStore) Create(ctx context.Context, operationID, agentID, runtimeID, workspaceID, actionKind string) (*AgentLifecycleDispatch, error) {
	now := time.Now()
	d := &AgentLifecycleDispatch{
		OperationID: operationID,
		AgentID:     agentID,
		RuntimeID:   runtimeID,
		WorkspaceID: workspaceID,
		ActionKind:  actionKind,
		Status:      AgentLifecycleDispatchPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	data, err := json.Marshal(d)
	if err != nil {
		return nil, fmt.Errorf("marshal agent lifecycle dispatch: %w", err)
	}

	pipe := s.rdb.TxPipeline()
	pipe.Set(ctx, agentLifecycleDispatchKey(operationID), data, agentLifecycleDispatchStoreRetention)
	pipe.ZAdd(ctx, agentLifecycleDispatchPendingKey(runtimeID), redis.Z{
		Score:  float64(now.UnixNano()),
		Member: operationID,
	})
	pipe.Expire(ctx, agentLifecycleDispatchPendingKey(runtimeID), agentLifecycleDispatchStoreRetention*2)
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, fmt.Errorf("persist agent lifecycle dispatch: %w", err)
	}
	return d, nil
}

func (s *RedisAgentLifecycleDispatchStore) loadDispatch(ctx context.Context, operationID string) (*AgentLifecycleDispatch, error) {
	raw, err := s.rdb.Get(ctx, agentLifecycleDispatchKey(operationID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get agent lifecycle dispatch: %w", err)
	}
	var d AgentLifecycleDispatch
	if err := json.Unmarshal(raw, &d); err != nil {
		return nil, fmt.Errorf("decode agent lifecycle dispatch: %w", err)
	}
	if applyAgentLifecycleDispatchTimeout(ctx, &d, time.Now(), s.onTimeout) {
		if err := s.persistDispatch(ctx, &d); err != nil {
			return nil, err
		}
		s.rdb.ZRem(ctx, agentLifecycleDispatchPendingKey(d.RuntimeID), d.OperationID)
	}
	return &d, nil
}

func (s *RedisAgentLifecycleDispatchStore) persistDispatch(ctx context.Context, d *AgentLifecycleDispatch) error {
	data, err := json.Marshal(d)
	if err != nil {
		return fmt.Errorf("marshal agent lifecycle dispatch: %w", err)
	}
	if err := s.rdb.Set(ctx, agentLifecycleDispatchKey(d.OperationID), data, agentLifecycleDispatchStoreRetention).Err(); err != nil {
		return fmt.Errorf("persist agent lifecycle dispatch: %w", err)
	}
	return nil
}

// HasPending is a cheap read-only ZCARD probe used by the heartbeat hot path
// to decide whether to invoke the side-effecting PopAllPending.
func (s *RedisAgentLifecycleDispatchStore) HasPending(ctx context.Context, runtimeID string) (bool, error) {
	cnt, err := s.rdb.ZCard(ctx, agentLifecycleDispatchPendingKey(runtimeID)).Result()
	if err != nil {
		return false, fmt.Errorf("zcard pending: %w", err)
	}
	return cnt > 0, nil
}

func (s *RedisAgentLifecycleDispatchStore) PopAllPending(ctx context.Context, runtimeID string) ([]*AgentLifecycleDispatch, error) {
	pendingKey := agentLifecycleDispatchPendingKey(runtimeID)

	ids, err := s.rdb.ZRange(ctx, pendingKey, 0, agentLifecycleDispatchBatchLimit-1).Result()
	if err != nil {
		return nil, fmt.Errorf("zrange pending: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}

	var claimed []*AgentLifecycleDispatch
	for _, id := range ids {
		d, err := s.loadDispatch(ctx, id)
		if err != nil {
			return claimed, err
		}
		if d == nil {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}
		if d.Status != AgentLifecycleDispatchPending {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}

		now := time.Now()
		d.Status = AgentLifecycleDispatchDelivered
		d.DeliveredAt = &now
		d.UpdatedAt = now
		data, err := json.Marshal(d)
		if err != nil {
			return claimed, fmt.Errorf("marshal agent lifecycle dispatch: %w", err)
		}

		result, err := claimPendingScript.Run(
			ctx, s.rdb,
			[]string{pendingKey, agentLifecycleDispatchKey(id)},
			id, data, int(agentLifecycleDispatchStoreRetention.Seconds()),
		).Int64()
		if err != nil {
			return claimed, fmt.Errorf("claim pending: %w", err)
		}
		if result == 1 {
			claimed = append(claimed, d)
		}
	}
	return claimed, nil
}

// SweepTimedOut scans every runtime's pending set, not just one runtime's —
// see the interface doc comment. HasPending/PopAllPending only evaluate a
// runtime's own dispatches when that runtime's own heartbeat calls them, so
// a runtime whose daemon goes offline and never comes back would otherwise
// never have its stuck dispatch evaluated. Reuses loadDispatch (same
// timeout-check, same persist, same onTimeout hook as the heartbeat path) —
// this is a different trigger for the same clock, not a new one.
func (s *RedisAgentLifecycleDispatchStore) SweepTimedOut(ctx context.Context) (int, error) {
	swept := 0
	iter := s.rdb.Scan(ctx, 0, agentLifecycleDispatchPendingPrefix+"*", 0).Iterator()
	for iter.Next(ctx) {
		pendingKey := iter.Val()
		ids, err := s.rdb.ZRange(ctx, pendingKey, 0, -1).Result()
		if err != nil {
			return swept, fmt.Errorf("zrange pending %q: %w", pendingKey, err)
		}
		for _, id := range ids {
			d, err := s.loadDispatch(ctx, id)
			if err != nil {
				return swept, err
			}
			if d == nil {
				s.rdb.ZRem(ctx, pendingKey, id)
				continue
			}
			if d.Status == AgentLifecycleDispatchTimeout {
				swept++
			}
		}
	}
	if err := iter.Err(); err != nil {
		return swept, fmt.Errorf("scan pending keys: %w", err)
	}
	return swept, nil
}
