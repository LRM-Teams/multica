package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInMemoryRestartStore_HasPending(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryRestartStore()

	has, err := store.HasPending(ctx, "runtime-none")
	if err != nil {
		t.Fatalf("HasPending: %v", err)
	}
	if has {
		t.Fatal("HasPending = true before any Create")
	}

	if _, err := store.Create(ctx, "runtime-none"); err != nil {
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

// TestInMemoryRestartStore_PopPendingDelivers is the store-level proof of
// "create a request → the next heartbeat pop claims it" (task #43): the
// heartbeat handler calls exactly HasPending then PopPending against
// whichever RestartStore is wired in (in-memory here, Redis in production).
func TestInMemoryRestartStore_PopPendingDelivers(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryRestartStore()

	created, err := store.Create(ctx, "runtime-abc")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Status != RestartPending {
		t.Fatalf("Create status = %s, want %s", created.Status, RestartPending)
	}

	claimed, err := store.PopPending(ctx, "runtime-abc")
	if err != nil {
		t.Fatalf("PopPending: %v", err)
	}
	if claimed == nil {
		t.Fatal("expected PopPending to claim the pending request")
	}
	if claimed.ID != created.ID {
		t.Fatalf("claimed ID = %s, want %s", claimed.ID, created.ID)
	}
	if claimed.Status != RestartDelivered {
		t.Fatalf("claimed status = %s, want %s", claimed.Status, RestartDelivered)
	}
	if claimed.DeliveredAt == nil {
		t.Fatal("expected DeliveredAt to be set on PopPending")
	}

	// A second pop must find nothing left — the request is not
	// re-delivered on the next heartbeat.
	again, err := store.PopPending(ctx, "runtime-abc")
	if err != nil {
		t.Fatalf("second PopPending: %v", err)
	}
	if again != nil {
		t.Fatalf("expected no pending request left, got %+v", again)
	}

	has, err := store.HasPending(ctx, "runtime-abc")
	if err != nil {
		t.Fatalf("HasPending after pop: %v", err)
	}
	if has {
		t.Fatal("HasPending = true after the only pending request was delivered")
	}
}

func TestInMemoryRestartStore_PopPendingPicksOldest(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryRestartStore()

	first, err := store.Create(ctx, "runtime-multi")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	first.CreatedAt = first.CreatedAt.Add(-time.Minute)
	store.mu.Lock()
	store.requests[first.ID].CreatedAt = first.CreatedAt
	store.mu.Unlock()

	if _, err := store.Create(ctx, "runtime-multi"); err != nil {
		t.Fatalf("create second: %v", err)
	}

	claimed, err := store.PopPending(ctx, "runtime-multi")
	if err != nil {
		t.Fatalf("PopPending: %v", err)
	}
	if claimed == nil || claimed.ID != first.ID {
		t.Fatalf("expected the older request to be claimed first, got %+v", claimed)
	}
}

// TestInMemoryRestartStore_PendingTimesOut mirrors the update/model-list
// stores' escape hatch: a restart request nobody ever heartbeats for (e.g.
// the daemon really is offline) must not sit "pending" forever — the UI has
// to stop polling and tell the human the daemon looks unreachable, not that
// it's about to restart any second.
func TestInMemoryRestartStore_PendingTimesOut(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryRestartStore()

	req, err := store.Create(ctx, "runtime-stale")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	store.mu.Lock()
	store.requests[req.ID].CreatedAt = time.Now().Add(-(restartPendingTimeout + time.Second))
	store.mu.Unlock()

	got, err := store.Get(ctx, req.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got == nil {
		t.Fatal("expected stored request")
	}
	if got.Status != RestartTimeout {
		t.Fatalf("status = %s, want %s", got.Status, RestartTimeout)
	}
}

// TestRestartEndpointsRequireComputerOwner locks restart to the Computer
// owner only (Frank 2026-08-03; same canOwnRuntime gate as upgrade).
// Workspace admin who is not the owner gets 403 — FE hide is not enough.
func TestRestartEndpointsRequireComputerOwner(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()

	// Runtime is owned by the global fixture user (testUserID), matching
	// runtime_delete_permission_test.go's convention — avoids colliding
	// with createRuntimeLocalSkillTestMember's non-unique per-role name
	// when a role is needed more than once in the same test.
	daemonID := "restart-gate-" + uuid.NewString()
	var runtimeID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_runtime (
			workspace_id, daemon_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		)
		VALUES ($1, $2, $3, 'local', 'claude', 'offline',
		        'Restart Gate Computer', '{}'::jsonb, now())
		RETURNING id
	`, testWorkspaceID, daemonID, "Restart Gate "+uuid.NewString()).Scan(&runtimeID); err != nil {
		t.Fatalf("create runtime: %v", err)
	}
	// LRM-1570: ownership is machine-level via an active binding for the
	// runtime's daemon (the Computer owner is testUserID).
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO computer_workspace_bindings (
			daemon_id, workspace_id, user_id, execution_token_hash, active
		) VALUES ($1, $2, $3, 'restart-gate-test', TRUE)
	`, daemonID, testWorkspaceID, testUserID); err != nil {
		t.Fatalf("seed restart-gate owner binding: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM agent_runtime WHERE id = $1`, runtimeID)
		_, _ = testPool.Exec(context.Background(), `DELETE FROM computer_workspace_bindings WHERE daemon_id = $1 AND workspace_id = $2`, daemonID, testWorkspaceID)
	})

	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")
	adminID := createRuntimeLocalSkillTestMember(t, "admin")

	callers := []struct {
		name       string
		userID     string
		wantStatus int
	}{
		{name: "unrelated plain member", userID: plainMemberID, wantStatus: http.StatusForbidden},
		{name: "runtime owner", userID: testUserID, wantStatus: http.StatusOK},
		{name: "workspace admin non-owner", userID: adminID, wantStatus: http.StatusForbidden},
	}

	for _, caller := range callers {
		t.Run(caller.name, func(t *testing.T) {
			req := newRequestAsUser(caller.userID, http.MethodPost, "/api/runtimes/"+runtimeID+"/restart", nil)
			req = withURLParam(req, "runtimeId", runtimeID)
			w := httptest.NewRecorder()

			testHandler.InitiateRestart(w, req)

			if w.Code != caller.wantStatus {
				t.Fatalf("expected %d, got %d: %s", caller.wantStatus, w.Code, w.Body.String())
			}
		})
	}
}
