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

// TestInitiateUpdate_RejectsWhenRuntimePinned covers gate 1: the manual
// "click upgrade" path must reject immediately with a machine-readable code
// distinct from "update already in progress" — the frontend must be able to
// branch on this without matching the message text (Parker's requirement,
// task #81 thread).
func TestInitiateUpdate_RejectsWhenRuntimePinned(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	pinTestRuntime(t, runtimeID, "0.3.85")

	w := doInitiateUpdate(t, testUserID, runtimeID)
	if w.Code != http.StatusConflict {
		t.Fatalf("InitiateUpdate on pinned runtime = %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "runtime_pinned" {
		t.Fatalf("response code = %q, want %q: %s", body["code"], "runtime_pinned", w.Body.String())
	}
	// No intent should have been created — nothing left to materialize later
	// once the pin is lifted; the operator has to click again.
	if intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID); err != nil || intent != nil {
		t.Fatalf("expected no intent created for a pinned runtime, got %+v err=%v", intent, err)
	}
}

// TestInitiateUpdate_InFlightConflictHasStableCode locks in the sibling 409
// now carrying its own code (task #81 review comment: an existing 409 with
// no code would force the frontend back onto message-text matching for the
// one case this PR didn't touch).
func TestInitiateUpdate_InFlightConflictHasStableCode(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	if _, err := testHandler.UpdateStore.Create(context.Background(), runtimeID, "v0.3.90"); err != nil {
		t.Fatalf("seed in-flight attempt: %v", err)
	}

	w := doInitiateUpdate(t, testUserID, runtimeID)
	if w.Code != http.StatusConflict {
		t.Fatalf("InitiateUpdate with attempt in flight = %d, want 409: %s", w.Code, w.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["code"] != "update_already_in_progress" {
		t.Fatalf("response code = %q, want %q: %s", body["code"], "update_already_in_progress", w.Body.String())
	}
}

// TestMaybeMaterializeUpdateIntent_SkipsWhenRuntimePinned covers gate 2: an
// intent must stay live and un-materialized on a pinned runtime rather than
// being consumed into a daemon_runtime_update attempt that could never be
// delivered (task #81 thread: materialization has the real side effect of
// creating that row, so skipping only delivery would strand it).
func TestMaybeMaterializeUpdateIntent_SkipsWhenRuntimePinned(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_runtime_update_intent WHERE runtime_id = $1`, runtimeID)
	})

	initiated := decodeUpdateResponse(t, doInitiateUpdate(t, testUserID, runtimeID))
	if initiated.Status != UpdateQueued {
		t.Fatalf("initial status = %q, want queued", initiated.Status)
	}

	// Pin AFTER the intent already exists — the scenario gate 2 exists for.
	pinTestRuntime(t, runtimeID, "0.3.85")
	materializeUpdateIntentForRuntimeID(t, runtimeID)

	if attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID); err != nil || attempt != nil {
		t.Fatalf("expected no attempt materialized for a pinned runtime, got %+v err=%v", attempt, err)
	}
	intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || intent == nil || !intent.Live() {
		t.Fatalf("expected the intent to remain live (not consumed), got %+v err=%v", intent, err)
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
