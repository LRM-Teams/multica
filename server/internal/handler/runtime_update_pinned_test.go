package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Task #81: a runtime pinned via MULTICA_PINNED_VERSION must never receive
// an automatic or queued update, on any of the three paths that can deliver
// one. Each test below covers exactly one of those paths.

func pinTestRuntime(t *testing.T, runtimeID, version string) {
	t.Helper()
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET pinned_version = $1 WHERE id = $2
	`, version, runtimeID); err != nil {
		t.Fatalf("pin test runtime: %v", err)
	}
}

// TestDaemonHeartbeat_SkipsPendingUpdateDeliveryWhenRuntimePinned covers
// gate 3: even an already-materialized daemon_runtime_update row (e.g. one
// created before the runtime was pinned) must not be popped and delivered
// in the heartbeat ack. Critically, the row must NOT be popped at all —
// popping claims it, which would silently drop it once this ack doesn't
// deliver it — so it must still be deliverable on a later heartbeat once
// the pin is lifted.
func TestDaemonHeartbeat_SkipsPendingUpdateDeliveryWhenRuntimePinned(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createRuntimeLocalSkillTestRuntime(t, testUserID)
	pinTestRuntime(t, runtimeID, "0.3.85")
	if _, err := testHandler.UpdateStore.Create(context.Background(), runtimeID, "v0.3.90"); err != nil {
		t.Fatalf("seed pending attempt: %v", err)
	}

	w := httptest.NewRecorder()
	req := newDaemonTokenRequest(http.MethodPost, "/api/daemon/heartbeat", map[string]any{
		"runtime_id": runtimeID,
	}, testWorkspaceID, "runtime-local-skills-daemon")
	testHandler.DaemonHeartbeat(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("DaemonHeartbeat: expected 200, got %d: %s", w.Code, w.Body.String())
	}

	var ack struct {
		PendingUpdate *struct {
			ID string `json:"id"`
		} `json:"pending_update"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ack); err != nil {
		t.Fatalf("decode heartbeat ack: %v", err)
	}
	if ack.PendingUpdate != nil {
		t.Fatalf("ack carried a pending_update for a pinned runtime: %+v", ack.PendingUpdate)
	}

	// The row must still be there, still pending — not silently consumed by
	// a PopPending call this heartbeat should never have made.
	still, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || still == nil || still.Status != UpdatePending {
		t.Fatalf("expected the attempt to remain pending (not popped), got %+v err=%v", still, err)
	}
}
