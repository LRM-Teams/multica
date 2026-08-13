package handler

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// Production uses the Redis adapter, so pin the same lost-heartbeat-response
// recovery across two server replicas instead of relying only on the
// in-memory adapter.
func TestRedisAgentLifecycleDispatchStore_RedeliversUntilCompleteAcrossInstances(t *testing.T) {
	rdb := newRedisTestClient(t)
	ctx := context.Background()
	nodeA := NewRedisAgentLifecycleDispatchStore(rdb, nil)
	nodeB := NewRedisAgentLifecycleDispatchStore(rdb, nil)

	created, err := nodeA.Create(ctx, "op-lost", "agent-a", "runtime-a", "workspace-a", "restart")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := nodeB.PopAllPending(ctx, "runtime-a")
	if err != nil || len(first) != 1 {
		t.Fatalf("first PopAllPending = %+v, %v; want one dispatch", first, err)
	}

	// Expire the delivery lease without sleeping through a production-sized
	// interval. Both the durable record and its due-score are part of the
	// Redis adapter's observable persisted state.
	old := time.Now().Add(-(agentLifecycleDispatchDeliveryLease + time.Second))
	created.Status = AgentLifecycleDispatchDelivered
	created.DeliveredAt = &old
	created.UpdatedAt = old
	body, err := json.Marshal(created)
	if err != nil {
		t.Fatalf("marshal stale dispatch: %v", err)
	}
	if err := rdb.Set(ctx, agentLifecycleDispatchKey(created.OperationID), body, agentLifecycleDispatchStoreRetention).Err(); err != nil {
		t.Fatalf("persist stale dispatch: %v", err)
	}
	if err := rdb.ZAdd(ctx, agentLifecycleDispatchPendingKey(created.RuntimeID), redis.Z{Score: redisScore(old), Member: created.OperationID}).Err(); err != nil {
		t.Fatalf("schedule stale dispatch: %v", err)
	}

	has, err := nodeA.HasPending(ctx, "runtime-a")
	if err != nil || !has {
		t.Fatalf("HasPending after expired delivery lease = %v, %v; want true", has, err)
	}
	redelivered, err := nodeA.PopAllPending(ctx, "runtime-a")
	if err != nil || len(redelivered) != 1 || redelivered[0].OperationID != "op-lost" {
		t.Fatalf("redelivery = %+v, %v; want op-lost", redelivered, err)
	}

	if err := nodeB.Complete(ctx, "op-lost", "runtime-other"); err == nil {
		t.Fatal("Complete with a different runtime succeeded")
	}
	has, err = nodeA.HasPending(ctx, "runtime-a")
	if err != nil || has {
		t.Fatalf("dispatch became immediately due after rejected Complete = %v, %v; want false until lease expiry", has, err)
	}

	if err := nodeB.Complete(ctx, "op-lost", "runtime-a"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	has, err = nodeA.HasPending(ctx, "runtime-a")
	if err != nil || has {
		t.Fatalf("HasPending after Complete = %v, %v; want false", has, err)
	}
}
