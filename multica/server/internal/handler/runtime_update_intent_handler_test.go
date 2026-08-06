package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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

// materializeUpdateIntentForRuntimeID fetches the runtime row and calls
// maybeMaterializeUpdateIntent — most of this file's tests only have the ID
// handy (createUpdateIntentTestRuntime returns a string), but the pin-check
// added in task #81 needs the full row.
func materializeUpdateIntentForRuntimeID(t *testing.T, runtimeID string) {
	t.Helper()
	rt, err := testHandler.Queries.GetAgentRuntime(context.Background(), parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("get runtime for materialize: %v", err)
	}
	testHandler.maybeMaterializeUpdateIntent(context.Background(), rt)
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
	materializeUpdateIntentForRuntimeID(t, runtimeID)

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

	materializeUpdateIntentForRuntimeID(t, runtimeID)

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
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	first, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || first == nil {
		t.Fatalf("expected a materialized attempt: %+v err=%v", first, err)
	}

	// A second heartbeat before the attempt resolves must not create another
	// one — that would violate daemon_runtime_update's one-active-per-runtime
	// invariant and would be a duplicate delivery to the daemon.
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	second, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || second == nil || second.ID != first.ID {
		t.Fatalf("expected the same attempt, got first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestMaybeMaterializeUpdateIntent_RetriesAfterAttemptFailsOnceBackoffElapses(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)

	materializeUpdateIntentForRuntimeID(t, runtimeID)
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

	// The heartbeat that observes this failure folds it into backoff — it
	// must NOT immediately materialize a fresh attempt on the same call
	// (that would defeat the whole point of backoff: a machine heartbeating
	// every 15s would get hammered exactly as before).
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	stillFirst, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || stillFirst == nil || stillFirst.ID != first.ID {
		t.Fatalf("must not create a new attempt before backoff elapses, got %+v err=%v", stillFirst, err)
	}
	intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || intent == nil || !intent.Live() || intent.ConsecutiveFailures != 1 {
		t.Fatalf("intent should be live with ConsecutiveFailures=1 after one failure: %+v err=%v", intent, err)
	}

	// A heartbeat arriving again before the backoff elapses (very plausible
	// at a 15s interval) must not double-count the same failed attempt.
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	unchanged, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || unchanged == nil || unchanged.ConsecutiveFailures != 1 {
		t.Fatalf("re-observing the same failed attempt must not increment again: %+v err=%v", unchanged, err)
	}

	// Once backoff has elapsed (simulated here — real time would be the
	// updateIntentRetryBackoff(1) = 1 minute this test isn't going to sleep
	// for), the next heartbeat retries automatically.
	if _, err := testPool.Exec(context.Background(), `
		UPDATE daemon_runtime_update_intent SET next_retry_at = now() - interval '1 second' WHERE runtime_id = $1
	`, runtimeID); err != nil {
		t.Fatalf("simulate elapsed backoff: %v", err)
	}
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	second, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || second == nil || second.ID == first.ID || second.Status != UpdatePending {
		t.Fatalf("expected a fresh retried attempt once backoff elapsed, got first=%+v second=%+v err=%v", first, second, err)
	}
}

func TestMaybeMaterializeUpdateIntent_NoHeartbeatNeverCountsAsFailure(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	// Parker's non-negotiable, 2026-08-02: a runtime that's simply asleep —
	// never heartbeating — must not consume any of its retry budget. This
	// function is ONLY ever called from the heartbeat handler, so "no
	// heartbeat" means this function literally never runs for that runtime.
	// This test locks that structural property in explicitly rather than
	// leaving it as an inference from where the call site happens to live.
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)

	// Simulate "days pass, this runtime never heartbeats" by simply not
	// calling maybeMaterializeUpdateIntent at all — the passage of time
	// itself must not degrade the intent.
	if _, err := testPool.Exec(context.Background(), `
		UPDATE daemon_runtime_update_intent SET created_at = now() - interval '10 days' WHERE runtime_id = $1
	`, runtimeID); err != nil {
		t.Fatalf("simulate elapsed time with no heartbeat: %v", err)
	}

	intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || intent == nil {
		t.Fatalf("Get: %+v err=%v", intent, err)
	}
	if intent.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d after 10 days with zero heartbeats, want 0 — a sleeping runtime must never be charged a failure", intent.ConsecutiveFailures)
	}
	if intent.GivenUpAt != nil {
		t.Fatalf("a runtime that's simply asleep must never be marked given up: %+v", intent)
	}
	if !intent.Live() {
		t.Fatalf("intent should still be live: %+v", intent)
	}
}

func TestMaybeMaterializeUpdateIntent_GivenUpStopsRetryingAndGetUpdateReflectsIt(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	initiated := decodeUpdateResponse(t, doInitiateUpdate(t, testUserID, runtimeID))

	for i := 0; i < updateIntentMaxConsecutiveFailures; i++ {
		materializeUpdateIntentForRuntimeID(t, runtimeID)
		attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
		if err != nil || attempt == nil {
			t.Fatalf("round %d: expected a materialized attempt: %+v err=%v", i, attempt, err)
		}
		if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
			t.Fatalf("round %d: pop pending: %v", i, err)
		}
		if err := testHandler.UpdateStore.Fail(context.Background(), attempt.ID, "rename failed: Access is denied"); err != nil {
			t.Fatalf("round %d: fail attempt: %v", i, err)
		}
		materializeUpdateIntentForRuntimeID(t, runtimeID) // folds the failure in
		if _, err := testPool.Exec(context.Background(), `
			UPDATE daemon_runtime_update_intent SET next_retry_at = now() - interval '1 second' WHERE runtime_id = $1
		`, runtimeID); err != nil {
			t.Fatalf("round %d: simulate elapsed backoff: %v", i, err)
		}
	}

	intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || intent == nil || intent.GivenUpAt == nil {
		t.Fatalf("expected the intent to have given up after %d consecutive failures: %+v err=%v", updateIntentMaxConsecutiveFailures, intent, err)
	}

	// One more heartbeat must not create yet another attempt — the whole
	// point of giving up is to stop.
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	final, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || final == nil || updateRequestBlocksNewRequest(final.Status) {
		t.Fatalf("no new attempt should be in flight after giving up: %+v err=%v", final, err)
	}

	// Parker's rule, 2026-08-02: the polling surface must not keep saying
	// "queued" once the system has stopped trying — that would be a UI lie.
	//
	// The raw last attempt (status=failed, error="rename failed: Access is
	// denied") is still well within UpdateStore's retention window here —
	// Alice's catch, 2026-08-02: GetUpdate's ordering originally let that
	// still-fresh attempt mask the given-up state entirely, so the message
	// must be provably the *synthesized* given-up response, not just
	// "any failed status" (which the raw attempt alone would also satisfy).
	got := decodeUpdateResponse(t, doGetUpdate(t, testUserID, runtimeID, initiated.ID))
	if got.Status == UpdateQueued {
		t.Fatalf("GetUpdate must not report queued once given up, got %+v", got)
	}
	if got.Status != UpdateFailed {
		t.Fatalf("expected a terminal failed status once given up, got %q", got.Status)
	}
	// Parker's bar, restated explicitly, 2026-08-02: the message must answer
	// both "will this fix itself" (no — say so) and "what do I do" /"why did
	// it fail" (the real underlying error) — either half missing fails.
	if !strings.Contains(got.Error, "gave up") {
		t.Fatalf("error should say it gave up (won't retry itself), got %q", got.Error)
	}
	if !strings.Contains(got.Error, "rename failed: Access is denied") {
		t.Fatalf("error should include the real last-attempt failure reason, got %q", got.Error)
	}
}

func TestInitiateUpdate_AfterGivenUpResetsAndRetriesImmediately(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	withFakeRuntimeReleaseSource(t, "v9.9.9")
	runtimeID := createUpdateIntentTestRuntime(t, testUserID)
	doInitiateUpdate(t, testUserID, runtimeID)

	for i := 0; i < updateIntentMaxConsecutiveFailures; i++ {
		materializeUpdateIntentForRuntimeID(t, runtimeID)
		attempt, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
		if err != nil || attempt == nil {
			t.Fatalf("round %d: expected a materialized attempt: %+v err=%v", i, attempt, err)
		}
		if _, err := testHandler.UpdateStore.PopPending(context.Background(), runtimeID); err != nil {
			t.Fatalf("round %d: pop pending: %v", i, err)
		}
		if err := testHandler.UpdateStore.Fail(context.Background(), attempt.ID, "rename failed"); err != nil {
			t.Fatalf("round %d: fail attempt: %v", i, err)
		}
		materializeUpdateIntentForRuntimeID(t, runtimeID)
		if _, err := testPool.Exec(context.Background(), `
			UPDATE daemon_runtime_update_intent SET next_retry_at = now() - interval '1 second' WHERE runtime_id = $1
		`, runtimeID); err != nil {
			t.Fatalf("round %d: simulate elapsed backoff: %v", i, err)
		}
	}
	givenUp, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || givenUp == nil || givenUp.Live() {
		t.Fatalf("precondition: expected the intent to have given up: %+v err=%v", givenUp, err)
	}

	// Parker's requirement, 2026-08-02: this is very plausibly the exact
	// machine an admin most wants to fix (persistent failure = a real
	// problem) — it must not be permanently stuck just because the system
	// gave up automatically. Clicking Update again is the only recovery path
	// required (no separate "un-give-up" action).
	w := doInitiateUpdate(t, testUserID, runtimeID)
	if w.Code != http.StatusOK {
		t.Fatalf("InitiateUpdate after giving up = %d: %s", w.Code, w.Body.String())
	}
	got := decodeUpdateResponse(t, w)
	if got.Status != UpdateQueued {
		t.Fatalf("re-request after giving up should queue fresh, got status=%q", got.Status)
	}

	reset, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID)
	if err != nil || reset == nil || !reset.Live() || reset.ConsecutiveFailures != 0 {
		t.Fatalf("re-created intent should be live with failures reset: %+v err=%v", reset, err)
	}

	// And materialization actually resumes — not just the bookkeeping.
	materializeUpdateIntentForRuntimeID(t, runtimeID)
	fresh, err := testHandler.UpdateStore.LatestForRuntime(context.Background(), runtimeID)
	if err != nil || fresh == nil || fresh.Status != UpdatePending {
		t.Fatalf("expected a fresh attempt after re-requesting past a given-up intent: %+v err=%v", fresh, err)
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

	materializeUpdateIntentForRuntimeID(t, runtimeID)

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

	materializeUpdateIntentForRuntimeID(t, runtimeID)
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

	materializeUpdateIntentForRuntimeID(t, runtimeID)

	if intent, err := testHandler.UpdateIntentStore.Get(context.Background(), runtimeID); err != nil || intent != nil {
		t.Fatalf("fulfilled intent should be deleted, got %+v err=%v", intent, err)
	}
}
