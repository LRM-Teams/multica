package handler

import (
	"context"
	"testing"
	"time"
)

func TestInMemoryAgentLifecycleDispatchStore_HasPending(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentLifecycleDispatchStore()

	has, err := store.HasPending(ctx, "runtime-none")
	if err != nil {
		t.Fatalf("HasPending: %v", err)
	}
	if has {
		t.Fatal("HasPending = true before any Create")
	}

	if _, err := store.Create(ctx, "op-1", "agent-a", "runtime-none", "workspace-a", "restart"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	has, err = store.HasPending(ctx, "runtime-none")
	if err != nil {
		t.Fatalf("HasPending after create: %v", err)
	}
	if !has {
		t.Fatal("HasPending = false after Create")
	}
}

// TestInMemoryAgentLifecycleDispatchStore_PopAllPendingDeliversEveryAgentOnTheRuntime
// pins the reason PopAllPending exists instead of a single-item pop: several
// agents can share one runtime/daemon, and dispatches queued for each of
// them must all be delivered on the same heartbeat rather than trickling out
// one per cycle.
func TestInMemoryAgentLifecycleDispatchStore_PopAllPendingDeliversEveryAgentOnTheRuntime(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentLifecycleDispatchStore()

	if _, err := store.Create(ctx, "op-1", "agent-1", "runtime-shared", "workspace-a", "restart"); err != nil {
		t.Fatalf("create first: %v", err)
	}
	if _, err := store.Create(ctx, "op-2", "agent-2", "runtime-shared", "workspace-a", "reset_session_restart"); err != nil {
		t.Fatalf("create second: %v", err)
	}
	// A dispatch for a different runtime must not be claimed alongside these.
	if _, err := store.Create(ctx, "op-3", "agent-3", "runtime-other", "workspace-a", "restart"); err != nil {
		t.Fatalf("create unrelated: %v", err)
	}

	claimed, err := store.PopAllPending(ctx, "runtime-shared")
	if err != nil {
		t.Fatalf("PopAllPending: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d dispatches, want 2: %+v", len(claimed), claimed)
	}
	gotOps := map[string]bool{claimed[0].OperationID: true, claimed[1].OperationID: true}
	if !gotOps["op-1"] || !gotOps["op-2"] {
		t.Fatalf("expected op-1 and op-2 claimed, got %+v", claimed)
	}
	for _, d := range claimed {
		if d.Status != AgentLifecycleDispatchDelivered {
			t.Fatalf("claimed status = %s, want %s", d.Status, AgentLifecycleDispatchDelivered)
		}
		if d.DeliveredAt == nil {
			t.Fatal("expected DeliveredAt to be set")
		}
	}

	again, err := store.PopAllPending(ctx, "runtime-shared")
	if err != nil {
		t.Fatalf("second PopAllPending: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("expected nothing left pending on runtime-shared, got %+v", again)
	}

	stillPending, err := store.PopAllPending(ctx, "runtime-other")
	if err != nil {
		t.Fatalf("PopAllPending runtime-other: %v", err)
	}
	if len(stillPending) != 1 {
		t.Fatalf("expected runtime-other's own dispatch untouched, got %+v", stillPending)
	}
}

func TestInMemoryAgentLifecycleDispatchStore_PendingTimesOut(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentLifecycleDispatchStore()

	if _, err := store.Create(ctx, "op-stale", "agent-stale", "runtime-stale", "workspace-a", "restart"); err != nil {
		t.Fatalf("create: %v", err)
	}
	store.mu.Lock()
	store.dispatches["op-stale"].CreatedAt = time.Now().Add(-(agentLifecycleDispatchPendingTimeout + time.Second))
	store.mu.Unlock()

	has, err := store.HasPending(ctx, "runtime-stale")
	if err != nil {
		t.Fatalf("HasPending: %v", err)
	}
	if has {
		t.Fatal("expected timed-out dispatch to no longer count as pending")
	}
}
