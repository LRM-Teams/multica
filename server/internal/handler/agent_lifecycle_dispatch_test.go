package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInMemoryAgentLifecycleDispatchStore_HasPending(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentLifecycleDispatchStore(nil)

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
	store := NewInMemoryAgentLifecycleDispatchStore(nil)

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

// A heartbeat response is not a delivery acknowledgement: the server can
// claim the dispatch and then lose the HTTP/WS response before the daemon sees
// it. Keep offering the same operation after a short lease until the daemon's
// terminal result explicitly completes the dispatch.
func TestInMemoryAgentLifecycleDispatchStore_RedeliversClaimLostWithHeartbeatResponse(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentLifecycleDispatchStore(nil)

	if _, err := store.Create(ctx, "op-lost", "agent-a", "runtime-a", "workspace-a", "restart"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := store.PopAllPending(ctx, "runtime-a")
	if err != nil || len(first) != 1 {
		t.Fatalf("first PopAllPending = %+v, %v; want one dispatch", first, err)
	}

	store.mu.Lock()
	old := time.Now().Add(-(agentLifecycleDispatchDeliveryLease + time.Second))
	store.dispatches["op-lost"].DeliveredAt = &old
	store.mu.Unlock()

	has, err := store.HasPending(ctx, "runtime-a")
	if err != nil || !has {
		t.Fatalf("HasPending after expired delivery lease = %v, %v; want true", has, err)
	}
	redelivered, err := store.PopAllPending(ctx, "runtime-a")
	if err != nil || len(redelivered) != 1 || redelivered[0].OperationID != "op-lost" {
		t.Fatalf("redelivery = %+v, %v; want op-lost", redelivered, err)
	}

	if err := store.Complete(ctx, "op-lost", "runtime-a"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	has, err = store.HasPending(ctx, "runtime-a")
	if err != nil || has {
		t.Fatalf("HasPending after Complete = %v, %v; want false", has, err)
	}
}

func TestInMemoryAgentLifecycleDispatchStore_PendingTimesOut(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentLifecycleDispatchStore(nil)

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

// TestAgentLifecycleDispatchTimeoutFailsTheOperation pins the fix Parker/Alice
// asked for in #52 review: a dispatch whose claimed heartbeat response is
// lost, followed by a daemon that never returns, must not leave its
// agent_lifecycle_operation row stuck at "running" forever — that permanently
// overlays the agent's health as "restarting" with no way out.
//
// After the first claim, only SweepTimedOut (the heartbeat-independent
// trigger) is exercised: the runtime can disappear before any later heartbeat.
func TestAgentLifecycleDispatchTimeoutFailsTheOperation(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	agentID, runtimeID := createAgentLifecycleFixture(t, true)
	idempotencyKey := uuid.NewString()
	create := invokeCreateAgentLifecycle(t, agentID, idempotencyKey, agentLifecycleRestart)
	if create.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", create.Code, create.Body.String())
	}
	var operation AgentLifecycleOperation
	if err := json.Unmarshal(create.Body.Bytes(), &operation); err != nil {
		t.Fatalf("decode operation: %v", err)
	}

	// Build a dispatch store wired to the real test DB (mirrors production
	// wiring) instead of reusing testHandler.AgentLifecycleDispatchStore, so
	// this test controls CreatedAt directly without racing the real store's
	// 120s timeout window.
	store := NewInMemoryAgentLifecycleDispatchStore(testPool)
	ctx := context.Background()
	if _, err := store.Create(ctx, operation.ID, agentID, runtimeID, testWorkspaceID, string(agentLifecycleRestart)); err != nil {
		t.Fatalf("create dispatch: %v", err)
	}
	// Claim once to model the exact lost-heartbeat-response window: the server
	// prepared the dispatch in an ack, but the daemon never received it.
	if claimed, err := store.PopAllPending(ctx, runtimeID); err != nil || len(claimed) != 1 {
		t.Fatalf("initial claim = %+v, %v; want one delivered dispatch", claimed, err)
	}
	store.mu.Lock()
	store.dispatches[operation.ID].CreatedAt = time.Now().Add(-(agentLifecycleDispatchPendingTimeout + time.Second))
	store.mu.Unlock()

	swept, err := store.SweepTimedOut(ctx)
	if err != nil {
		t.Fatalf("SweepTimedOut: %v", err)
	}
	if swept != 1 {
		t.Fatalf("SweepTimedOut swept %d, want 1", swept)
	}

	got, err := getAgentLifecycleOperation(ctx, testPool, parseUUID(agentID), parseUUID(operation.ID))
	if err != nil {
		t.Fatalf("reload operation: %v", err)
	}
	if got == nil {
		t.Fatal("operation not found")
	}
	if got.Status != agentLifecycleFailed {
		t.Fatalf("operation status = %q, want %q (dispatch timeout must fail the operation, not leave it stuck)", got.Status, agentLifecycleFailed)
	}
	if got.FinishedAt == nil {
		t.Fatal("expected finished_at to be set")
	}

	// The health overlay must clear once the operation is failed — otherwise
	// the agent still shows "restarting" even though the operation resolved.
	healthRec := httptest.NewRecorder()
	healthReq := withURLParam(
		newRequestAs(testUserID, http.MethodGet, "/api/agents/"+agentID+"/health", nil),
		"id", agentID,
	)
	testHandler.GetAgentHealth(healthRec, healthReq)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("health status=%d body=%s", healthRec.Code, healthRec.Body.String())
	}
	var health AgentHealthResponse
	if err := json.Unmarshal(healthRec.Body.Bytes(), &health); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	if health.Summary.State == "restarting" {
		t.Fatalf("health still shows restarting after the operation failed: %+v", health.Summary)
	}
}
