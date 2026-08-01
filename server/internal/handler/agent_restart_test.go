package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInMemoryAgentRestartStore_HasPending(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentRestartStore()

	has, err := store.HasPending(ctx, "runtime-none")
	if err != nil {
		t.Fatalf("HasPending: %v", err)
	}
	if has {
		t.Fatal("HasPending = true before any Create")
	}

	if _, err := store.Create(ctx, "agent-a", "runtime-none"); err != nil {
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

// TestInMemoryAgentRestartStore_PopAllPendingDeliversEveryAgentOnTheRuntime
// pins the reason PopAllPending exists instead of a single-item PopPending
// like RestartStore's: several agents can share one runtime/daemon, and a
// restart queued for each of them must all be delivered on the same
// heartbeat rather than trickling out one per cycle.
func TestInMemoryAgentRestartStore_PopAllPendingDeliversEveryAgentOnTheRuntime(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentRestartStore()

	first, err := store.Create(ctx, "agent-1", "runtime-shared")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	second, err := store.Create(ctx, "agent-2", "runtime-shared")
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	// A request for a different runtime must not be claimed alongside these.
	if _, err := store.Create(ctx, "agent-3", "runtime-other"); err != nil {
		t.Fatalf("create unrelated: %v", err)
	}

	claimed, err := store.PopAllPending(ctx, "runtime-shared")
	if err != nil {
		t.Fatalf("PopAllPending: %v", err)
	}
	if len(claimed) != 2 {
		t.Fatalf("claimed %d requests, want 2: %+v", len(claimed), claimed)
	}
	gotIDs := map[string]bool{claimed[0].AgentID: true, claimed[1].AgentID: true}
	if !gotIDs[first.AgentID] || !gotIDs[second.AgentID] {
		t.Fatalf("expected both agent-1 and agent-2 claimed, got %+v", claimed)
	}
	for _, req := range claimed {
		if req.Status != AgentRestartDelivered {
			t.Fatalf("claimed status = %s, want %s", req.Status, AgentRestartDelivered)
		}
		if req.DeliveredAt == nil {
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
		t.Fatalf("expected runtime-other's own request untouched, got %+v", stillPending)
	}
}

func TestInMemoryAgentRestartStore_PendingTimesOut(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryAgentRestartStore()

	req, err := store.Create(ctx, "agent-stale", "runtime-stale")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.mu.Lock()
	store.requests[req.ID].CreatedAt = time.Now().Add(-(agentRestartPendingTimeout + time.Second))
	store.mu.Unlock()

	got, err := store.Get(ctx, req.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected stored request")
	}
	if got.Status != AgentRestartTimeout {
		t.Fatalf("status = %s, want %s", got.Status, AgentRestartTimeout)
	}
}

// TestAgentRestartEndpointsRequireAgentOwnerOrAdmin locks the permission bar
// at canManageAgent's (same bar as agent management generally): a plain
// workspace member unrelated to the agent is forbidden; the agent's own
// owner and a workspace admin/owner are both allowed.
func TestAgentRestartEndpointsRequireAgentOwnerOrAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Reuses the global fixture user as agent owner (like
	// TestRestartEndpointsRequireRuntimeOwnerOrAdmin does for runtime owner)
	// rather than calling createRuntimeLocalSkillTestMember(t, "member")
	// twice in one test — its user "name" is only unique per role, so two
	// "member" calls in the same test collide on the name unique constraint.
	ownerID := testUserID
	var agentID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent (
			workspace_id, name, description, runtime_mode, runtime_config, runtime_id, max_concurrent_tasks, owner_id, instructions, custom_env, custom_args
		, model) VALUES ($1, 'agent-restart-gate-test', '', 'cloud', '{}'::jsonb, $2, 1, $3, '', '{}'::jsonb, '[]'::jsonb, 'composer-1.5')
		RETURNING id
	`, testWorkspaceID, handlerTestRuntimeID(t), ownerID).Scan(&agentID); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent WHERE id = $1`, agentID)
	})

	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")
	adminID := createRuntimeLocalSkillTestMember(t, "admin")

	callers := []struct {
		name       string
		userID     string
		wantStatus int
	}{
		{name: "unrelated plain member", userID: plainMemberID, wantStatus: http.StatusForbidden},
		{name: "agent owner", userID: ownerID, wantStatus: http.StatusOK},
		{name: "workspace admin", userID: adminID, wantStatus: http.StatusOK},
	}

	for _, caller := range callers {
		t.Run(caller.name, func(t *testing.T) {
			req := newRequestAsUser(caller.userID, http.MethodPost, "/api/agents/"+agentID+"/restart", nil)
			req = withURLParam(req, "id", agentID)
			w := httptest.NewRecorder()

			testHandler.InitiateAgentRestart(w, req)

			if w.Code != caller.wantStatus {
				t.Fatalf("expected %d, got %d: %s", caller.wantStatus, w.Code, w.Body.String())
			}
		})
	}
}

// TestAgentRestartEndpointsRejectMalformedAgentID guards the same class of
// bug TestRuntimeHandlersRejectMalformedRuntimeID exists for: a malformed
// path param must 400, not panic or fall through to a nil-UUID query.
func TestAgentRestartEndpointsRejectMalformedAgentID(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	req := newRequestAsUser(uuid.NewString(), http.MethodPost, "/api/agents/not-a-uuid/restart", nil)
	req = withURLParam(req, "id", "not-a-uuid")
	w := httptest.NewRecorder()

	testHandler.InitiateAgentRestart(w, req)

	if w.Code != http.StatusBadRequest && w.Code != http.StatusNotFound {
		t.Fatalf("expected 400 or 404 for malformed agent id, got %d: %s", w.Code, w.Body.String())
	}
}
