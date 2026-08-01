package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// fakeRuntimeReleaseSource lets tests control what "latest release" resolves
// to without hitting the network — production always uses
// CachedRuntimeReleaseSource, which testHandler.RuntimeReleaseSource is nil
// by default specifically to keep the suite from calling out to GitHub.
type fakeRuntimeReleaseSource struct {
	release *RuntimeRelease
	err     error
}

func (f *fakeRuntimeReleaseSource) Latest(ctx context.Context) (*RuntimeRelease, error) {
	return f.release, f.err
}

// withFakeRuntimeReleaseSource swaps in a fake release source for the
// duration of the test and restores nil afterward.
func withFakeRuntimeReleaseSource(t *testing.T, tag string) {
	t.Helper()
	testHandler.RuntimeReleaseSource = &fakeRuntimeReleaseSource{release: &RuntimeRelease{TagName: tag}}
	t.Cleanup(func() { testHandler.RuntimeReleaseSource = nil })
}

func createUpdateIntentTestRuntime(t *testing.T, ownerID string) string {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	runtimeID := newPostgresUpdateTestRuntime(t)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE agent_runtime SET owner_id = $1 WHERE id = $2
	`, ownerID, runtimeID); err != nil {
		t.Fatalf("set runtime owner: %v", err)
	}
	return runtimeID
}

func doInitiateUpdate(t *testing.T, userID, runtimeID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAsUser(userID, http.MethodPost, "/api/runtimes/"+runtimeID+"/update", nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	w := httptest.NewRecorder()
	testHandler.InitiateUpdate(w, req)
	return w
}

func doGetUpdate(t *testing.T, userID, runtimeID, updateID string) *httptest.ResponseRecorder {
	t.Helper()
	req := newRequestAsUser(userID, http.MethodGet, "/api/runtimes/"+runtimeID+"/update/"+updateID, nil)
	req = withURLParams(req, "runtimeId", runtimeID, "updateId", updateID)
	w := httptest.NewRecorder()
	testHandler.GetUpdate(w, req)
	return w
}

func decodeUpdateResponse(t *testing.T, w *httptest.ResponseRecorder) UpdateRequest {
	t.Helper()
	var got UpdateRequest
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode response %s: %v", w.Body.String(), err)
	}
	return got
}

func TestInitiateUpdate_CreatesQueuedIntentNotImmediateAttempt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	t.Cleanup(func() {
		_, _ = testPool.Exec(context.Background(), `DELETE FROM daemon_runtime_update_intent WHERE runtime_id = $1`, runtimeID)
	})

	w := doInitiateUpdate(t, testUserID, runtimeID)
	if w.Code != http.StatusOK {
		t.Fatalf("InitiateUpdate = %d: %s", w.Code, w.Body.String())
	}
	got := decodeUpdateResponse(t, w)
	if got.Status != UpdateQueued {
		t.Fatalf("status = %q, want %q", got.Status, UpdateQueued)
	}
	if got.ID != intentUpdateID(runtimeID) {
		t.Fatalf("id = %q, want %q", got.ID, intentUpdateID(runtimeID))
	}

	// The actual point of this design: no daemon_runtime_update row exists
	// yet — delivery is deferred to the next heartbeat, not created here.
	if attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID); err != nil || attempt != nil {
		t.Fatalf("expected no attempt yet, got %+v err=%v", attempt, err)
	}
	if intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID); err != nil || intent == nil || !intent.Live() {
		t.Fatalf("expected a live intent, got %+v err=%v", intent, err)
	}
}

func TestInitiateUpdate_RejectsWhenAttemptAlreadyInFlight(t *testing.T) {
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
	// Must not have created an intent either — the existing attempt is the
	// thing to watch, an intent alongside it would be redundant/confusing.
	if intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID); err != nil || intent != nil {
		t.Fatalf("expected no intent created while an attempt is in flight, got %+v err=%v", intent, err)
	}
}

func TestInitiateUpdate_RequiresRuntimeOwnerOrAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")

	w := doInitiateUpdate(t, plainMemberID, runtimeID)
	if w.Code != http.StatusForbidden {
		t.Fatalf("InitiateUpdate from unrelated member = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestGetUpdate_QueuedIntentSurfacesMaterializedAttempt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)

	initiated := decodeUpdateResponse(t, doInitiateUpdate(t, testUserID, runtimeID))
	if initiated.Status != UpdateQueued {
		t.Fatalf("initial status = %q, want queued", initiated.Status)
	}

	stillQueued := decodeUpdateResponse(t, doGetUpdate(t, testUserID, runtimeID, initiated.ID))
	if stillQueued.Status != UpdateQueued {
		t.Fatalf("status before materialization = %q, want queued", stillQueued.Status)
	}

	// Simulate a heartbeat arriving — this is the actual fix: reachability
	// materializes the intent into a real attempt, no admin re-click needed.
	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)

	// Polling the SAME id InitiateUpdate handed back must now show the real,
	// delivered attempt — the frontend never needs to learn a new ID.
	delivered := decodeUpdateResponse(t, doGetUpdate(t, testUserID, runtimeID, initiated.ID))
	if delivered.Status != UpdatePending {
		t.Fatalf("status after materialization = %q, want pending: %+v", delivered.Status, delivered)
	}
	if delivered.TargetVersion != "v9.9.9" {
		t.Fatalf("materialized target_version = %q, want v9.9.9 (resolved at delivery time)", delivered.TargetVersion)
	}
	if delivered.ID == intentUpdateID(runtimeID) {
		t.Fatalf("delivered attempt should have a real attempt ID, not the synthetic intent ID")
	}
}

func TestGetUpdate_CancelledIntentIsNotFound(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	initiated := decodeUpdateResponse(t, doInitiateUpdate(t, testUserID, runtimeID))

	req := newRequestAsUser(testUserID, http.MethodDelete, "/api/runtimes/"+runtimeID+"/update-intent", nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	w := httptest.NewRecorder()
	testHandler.CancelUpdateIntent(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("CancelUpdateIntent = %d: %s", w.Code, w.Body.String())
	}

	got := doGetUpdate(t, testUserID, runtimeID, initiated.ID)
	if got.Code != http.StatusNotFound {
		t.Fatalf("GetUpdate after cancel = %d, want 404: %s", got.Code, got.Body.String())
	}
}

func TestCancelUpdateIntent_RequiresRuntimeOwnerOrAdmin(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)
	plainMemberID := createRuntimeLocalSkillTestMember(t, "member")

	req := newRequestAsUser(plainMemberID, http.MethodDelete, "/api/runtimes/"+runtimeID+"/update-intent", nil)
	req = withURLParam(req, "runtimeId", runtimeID)
	w := httptest.NewRecorder()
	testHandler.CancelUpdateIntent(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("CancelUpdateIntent from unrelated member = %d, want 403: %s", w.Code, w.Body.String())
	}
}

func TestMaybeMaterializeUpdateIntent_NoIntentIsNoop(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)

	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)

	if attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID); err != nil || attempt != nil {
		t.Fatalf("expected no attempt without an intent, got %+v err=%v", attempt, err)
	}
}

func TestMaybeMaterializeUpdateIntent_DoesNotDoubleCreateWhileAttemptInFlight(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)

	// First heartbeat materializes.
	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)
	first, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || first == nil {
		t.Fatalf("expected a materialized attempt: %+v err=%v", first, err)
	}

	// A second heartbeat before the attempt resolves must not create another
	// one — that would violate daemon_runtime_update's one-active-per-runtime
	// invariant and would be a duplicate delivery to the daemon.
	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)
	second, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || second == nil || second.ID != first.ID {
		t.Fatalf("expected the same attempt, got first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestMaybeMaterializeUpdateIntent_RetriesAfterAttemptFails(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)

	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)
	first, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || first == nil {
		t.Fatalf("expected a materialized attempt: %+v err=%v", first, err)
	}
	if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
		t.Fatalf("pop pending: %v", err)
	}
	if err := testHandler.UpdateStore.Fail(context.Background(), first.ID, "download_failed"); err != nil {
		t.Fatalf("fail attempt: %v", err)
	}

	// Intent must still be live — a failed delivery attempt is exactly what
	// this mechanism exists to survive without requiring a manual re-click.
	intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || intent == nil || !intent.Live() {
		t.Fatalf("intent should still be live after a failed attempt: %+v err=%v", intent, err)
	}

	// Next heartbeat retries automatically.
	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)
	second, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || second == nil || second.ID == first.ID || second.Status != UpdatePending {
		t.Fatalf("expected a fresh retried attempt, got first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestMaybeMaterializeUpdateIntent_ExpiredIntentIsMarkedNotDeleted(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	if _, err := testHandler.UpdateIntentStore.Create(context.Background(), runtimeID, testMemberIDForIntent(t), time.Hour); err != nil {
		t.Fatalf("create intent: %v", err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE daemon_runtime_update_intent SET expires_at = now() - interval '1 second' WHERE runtime_id = $1
	`, runtimeID); err != nil {
		t.Fatalf("back-date expiry: %v", err)
	}

	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)

	if attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID); err != nil || attempt != nil {
		t.Fatalf("expired intent must not materialize an attempt, got %+v err=%v", attempt, err)
	}
	intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || intent == nil {
		// Never silently deleted — this is the row a human would look at to
		// see "this update request aged out without ever being delivered".
		t.Fatalf("expired intent row should remain visible, got %+v err=%v", intent, err)
	}
	if intent.ExpiredAt == nil {
		t.Fatalf("expired intent should have ExpiredAt set: %+v", intent)
	}
}

func TestMaybeMaterializeUpdateIntent_FulfilledIntentIsDeleted(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)

	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)
	attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || attempt == nil {
		t.Fatalf("expected a materialized attempt: %+v err=%v", attempt, err)
	}
	if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
		t.Fatalf("pop pending: %v", err)
	}
	if err := testHandler.UpdateStore.Complete(context.Background(), attempt.ID, "done"); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}

	testHandler.maybeMaterializeUpdateIntent(context.Background(), runtimeID)

	if intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID); err != nil || intent != nil {
		t.Fatalf("fulfilled intent should be deleted, got %+v err=%v", intent, err)
	}
}
