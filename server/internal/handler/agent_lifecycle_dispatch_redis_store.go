package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis-backed implementation of AgentLifecycleDispatchStore. It uses
// namespaced records plus a ZSET-backed delivery schedule per runtime. Unlike
// one-shot request queues, a heartbeat claim only leases a lifecycle dispatch;
// the daemon's terminal result is the acknowledgement that removes it.
//
// Key layout:
//
//	mul:agent_lifecycle_dispatch:req:<operation_id>     → JSON-encoded AgentLifecycleDispatch, TTL = retention
//	mul:agent_lifecycle_dispatch:pending:<runtime_id>   → ZSET { member = operation_id, score = next_delivery_at UnixNano }
const (
	agentLifecycleDispatchKeyPrefix     = "mul:agent_lifecycle_dispatch:req:"
	agentLifecycleDispatchPendingPrefix = "mul:agent_lifecycle_dispatch:pending:"
	// agentLifecycleDispatchBatchLimit bounds how many pending dispatches one
	// heartbeat will claim at once — generous relative to how many agents
	// realistically share one runtime.
	agentLifecycleDispatchBatchLimit = 50
)

// leaseAgentLifecycleDispatchScript atomically moves one due dispatch's score
// to its next delivery lease while persisting the delivered attempt. Keeping
// the member in the ZSET is intentional: a heartbeat response is not an ack.
var leaseAgentLifecycleDispatchScript = redis.NewScript(`
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if not score or tonumber(score) > tonumber(ARGV[4]) then
    return 0
end
if redis.call('EXISTS', KEYS[2]) == 0 then
    redis.call('ZREM', KEYS[1], ARGV[1])
    return 0
end
redis.call('SET', KEYS[2], ARGV[2], 'EX', tonumber(ARGV[3]))
redis.call('ZADD', KEYS[1], tonumber(ARGV[5]), ARGV[1])
redis.call('EXPIRE', KEYS[1], tonumber(ARGV[6]))
return 1
`)

var completeAgentLifecycleDispatchScript = redis.NewScript(`
local raw = redis.call('GET', KEYS[1])
if not raw then
    redis.call('ZREM', KEYS[2], ARGV[1])
    return 0
end
local dispatch = cjson.decode(raw)
if dispatch.runtime_id ~= ARGV[2] then
    return -1
end
redis.call('DEL', KEYS[1])
redis.call('ZREM', KEYS[2], ARGV[1])
return 1
`)

func redisScore(at time.Time) float64 { return float64(at.UnixNano()) }

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
		Score:  redisScore(now),
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

// HasPending counts only attempts whose delivery lease is due.
func (s *RedisAgentLifecycleDispatchStore) HasPending(ctx context.Context, runtimeID string) (bool, error) {
	cnt, err := s.rdb.ZCount(ctx, agentLifecycleDispatchPendingKey(runtimeID), "-inf", strconv.FormatInt(time.Now().UnixNano(), 10)).Result()
	if err != nil {
		return false, fmt.Errorf("zcount pending: %w", err)
	}
	return cnt > 0, nil
}

func (s *RedisAgentLifecycleDispatchStore) PopAllPending(ctx context.Context, runtimeID string) ([]*AgentLifecycleDispatch, error) {
	pendingKey := agentLifecycleDispatchPendingKey(runtimeID)

	now := time.Now()
	ids, err := s.rdb.ZRangeByScore(ctx, pendingKey, &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(now.UnixNano(), 10), Count: agentLifecycleDispatchBatchLimit,
	}).Result()
	if err != nil {
		return nil, fmt.Errorf("zrange due pending: %w", err)
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
		if !agentLifecycleDispatchReady(d, now) {
			s.rdb.ZRem(ctx, pendingKey, id)
			continue
		}

		now = time.Now()
		d.Status = AgentLifecycleDispatchDelivered
		d.DeliveredAt = &now
		d.UpdatedAt = now
		data, err := json.Marshal(d)
		if err != nil {
			return claimed, fmt.Errorf("marshal agent lifecycle dispatch: %w", err)
		}

		result, err := leaseAgentLifecycleDispatchScript.Run(
			ctx, s.rdb,
			[]string{pendingKey, agentLifecycleDispatchKey(id)},
			id, data, int(agentLifecycleDispatchStoreRetention.Seconds()),
			now.UnixNano(), now.Add(agentLifecycleDispatchDeliveryLease).UnixNano(),
			int((agentLifecycleDispatchStoreRetention * 2).Seconds()),
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

func (s *RedisAgentLifecycleDispatchStore) Complete(ctx context.Context, operationID, runtimeID string) error {
	result, err := completeAgentLifecycleDispatchScript.Run(
		ctx, s.rdb,
		[]string{agentLifecycleDispatchKey(operationID), agentLifecycleDispatchPendingKey(runtimeID)},
		operationID, runtimeID,
	).Int64()
	if err != nil {
		return fmt.Errorf("complete agent lifecycle dispatch: %w", err)
	}
	if result == -1 {
		return fmt.Errorf("agent lifecycle dispatch runtime mismatch: got %s", runtimeID)
	}
	return nil
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
