package handler

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestPostgresUpdateIntentStore_CreateGetCancel(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if intent, err := store.Get(ctx, runtimeID); err != nil || intent != nil {
		t.Fatalf("Get before Create = %+v, err=%v, want nil,nil", intent, err)
	}

	created, err := store.Create(ctx, runtimeID, memberID, time.Hour)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.RuntimeID != runtimeID || created.CreatedBy != memberID {
		t.Fatalf("created intent = %+v", created)
	}
	if !created.Live() {
		t.Fatalf("freshly created intent should be live: %+v", created)
	}

	fetched, err := store.Get(ctx, runtimeID)
	if err != nil || fetched == nil || fetched.RuntimeID != runtimeID {
		t.Fatalf("Get after Create = %+v, err=%v", fetched, err)
	}

	if err := store.Cancel(ctx, runtimeID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	cancelled, err := store.Get(ctx, runtimeID)
	if err != nil || cancelled == nil || cancelled.CancelledAt == nil || cancelled.Live() {
		t.Fatalf("Get after Cancel = %+v, err=%v, want cancelled and not live", cancelled, err)
	}
}

func TestPostgresUpdateIntentStore_CreateReplacesExistingIntent(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	first, err := store.Create(ctx, runtimeID, memberID, time.Hour)
	if err != nil {
		t.Fatalf("create first: %v", err)
	}
	if err := store.Cancel(ctx, runtimeID); err != nil {
		t.Fatalf("cancel first: %v", err)
	}

	// A fresh Create must supersede a cancelled intent (last-write-wins,
	// re-request path) — this is how an admin recovers from an accidental
	// cancel or an expired intent without needing a separate "un-cancel".
	second, err := store.Create(ctx, runtimeID, memberID, time.Hour)
	if err != nil {
		t.Fatalf("create second: %v", err)
	}
	if !second.Live() {
		t.Fatalf("re-created intent should be live: %+v", second)
	}
	if !second.CreatedAt.After(first.CreatedAt) && second.CreatedAt != first.CreatedAt {
		t.Fatalf("second.CreatedAt = %v should not be before first.CreatedAt = %v", second.CreatedAt, first.CreatedAt)
	}
}

func TestPostgresUpdateIntentStore_MarkExpiredOnlyAffectsPastExpiry(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if _, err := store.Create(ctx, runtimeID, memberID, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Not yet expired — MarkExpired must be a no-op.
	if err := store.MarkExpired(ctx, runtimeID); err != nil {
		t.Fatalf("MarkExpired (not yet due): %v", err)
	}
	stillLive, err := store.Get(ctx, runtimeID)
	if err != nil || stillLive == nil || !stillLive.Live() {
		t.Fatalf("intent should still be live before its expiry: %+v, err=%v", stillLive, err)
	}

	// Force it into the past directly (Create's ttl param is what a real caller
	// would use; back-dating here isolates MarkExpired's own boundary check).
	if _, err := testPool.Exec(ctx, `
		UPDATE daemon_runtime_update_intent SET expires_at = now() - interval '1 second' WHERE runtime_id = $1
	`, runtimeID); err != nil {
		t.Fatalf("back-date expiry: %v", err)
	}
	if err := store.MarkExpired(ctx, runtimeID); err != nil {
		t.Fatalf("MarkExpired (due): %v", err)
	}
	expired, err := store.Get(ctx, runtimeID)
	if err != nil || expired == nil || expired.ExpiredAt == nil || expired.Live() {
		t.Fatalf("intent should be expired and not live: %+v, err=%v", expired, err)
	}
	// Never deleted — visible, per the "no silent disappearance" rule.
	if expired.RuntimeID != runtimeID {
		t.Fatalf("expired intent row should remain readable: %+v", expired)
	}
}

func TestPostgresUpdateIntentStore_Delete(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if _, err := store.Create(ctx, runtimeID, memberID, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.Delete(ctx, runtimeID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if intent, err := store.Get(ctx, runtimeID); err != nil || intent != nil {
		t.Fatalf("Get after Delete = %+v, err=%v, want nil,nil", intent, err)
	}
}

func TestUpdateIntentRetryBackoff_ExponentialCappedAt6h(t *testing.T) {
	cases := []struct {
		failures int
		want     time.Duration
	}{
		{0, 0},
		{1, time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 16 * time.Minute},
		{6, 32 * time.Minute},
		{7, 64 * time.Minute},
		{8, 128 * time.Minute},
		{9, 256 * time.Minute}, // 4h16m — still under the 6h cap
		{10, 6 * time.Hour},    // 512min would exceed the cap
		{20, 6 * time.Hour},    // stays capped, doesn't keep growing
	}
	for _, c := range cases {
		if got := updateIntentRetryBackoff(c.failures); got != c.want {
			t.Errorf("updateIntentRetryBackoff(%d) = %v, want %v", c.failures, got, c.want)
		}
	}
}

func TestPostgresUpdateIntentStore_RecordFailure_IncrementsAndBacksOff(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if _, err := store.Create(ctx, runtimeID, memberID, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}

	before := time.Now()
	if err := store.RecordFailure(ctx, runtimeID, "attempt-1"); err != nil {
		t.Fatalf("RecordFailure: %v", err)
	}
	after, err := store.Get(ctx, runtimeID)
	if err != nil || after == nil {
		t.Fatalf("Get after RecordFailure: %+v, err=%v", after, err)
	}
	if after.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1", after.ConsecutiveFailures)
	}
	if after.LastFailedAttemptID != "attempt-1" {
		t.Fatalf("LastFailedAttemptID = %q, want attempt-1", after.LastFailedAttemptID)
	}
	if !after.NextRetryAt.After(before) {
		t.Fatalf("NextRetryAt = %v should be pushed into the future (after %v)", after.NextRetryAt, before)
	}
	if after.DueForRetry(time.Now()) {
		t.Fatalf("should not be due for retry immediately after a failure, backoff = %v", after.NextRetryAt.Sub(before))
	}
	if !after.Live() {
		t.Fatalf("one failure should not give up: %+v", after)
	}
}

func TestPostgresUpdateIntentStore_RecordFailure_IdempotentPerAttemptID(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if _, err := store.Create(ctx, runtimeID, memberID, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := store.RecordFailure(ctx, runtimeID, "attempt-1"); err != nil {
		t.Fatalf("RecordFailure first: %v", err)
	}
	// A heartbeat that observes the SAME terminal attempt again (e.g. before
	// NextRetryAt) must not double-count it — otherwise a machine that
	// heartbeats every 15s while backing off for a full 6h would rack up
	// ~1440 phantom "failures" from re-observing one real failure.
	if err := store.RecordFailure(ctx, runtimeID, "attempt-1"); err != nil {
		t.Fatalf("RecordFailure repeat: %v", err)
	}
	intent, err := store.Get(ctx, runtimeID)
	if err != nil || intent == nil || intent.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures after repeat observation = %+v, err=%v, want 1", intent, err)
	}

	// A genuinely different attempt ID (the actual retry landed and failed
	// again) must still count.
	if err := store.RecordFailure(ctx, runtimeID, "attempt-2"); err != nil {
		t.Fatalf("RecordFailure second distinct attempt: %v", err)
	}
	intent, err = store.Get(ctx, runtimeID)
	if err != nil || intent == nil || intent.ConsecutiveFailures != 2 {
		t.Fatalf("ConsecutiveFailures after second distinct failure = %+v, err=%v, want 2", intent, err)
	}
}

func TestPostgresUpdateIntentStore_RecordFailure_GivesUpAtCapAndStopsBeingLive(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if _, err := store.Create(ctx, runtimeID, memberID, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < updateIntentMaxConsecutiveFailures; i++ {
		if err := store.RecordFailure(ctx, runtimeID, fmt.Sprintf("attempt-%d", i)); err != nil {
			t.Fatalf("RecordFailure #%d: %v", i, err)
		}
	}
	intent, err := store.Get(ctx, runtimeID)
	if err != nil || intent == nil {
		t.Fatalf("Get after reaching cap: %+v, err=%v", intent, err)
	}
	if intent.ConsecutiveFailures != updateIntentMaxConsecutiveFailures {
		t.Fatalf("ConsecutiveFailures = %d, want %d", intent.ConsecutiveFailures, updateIntentMaxConsecutiveFailures)
	}
	if intent.GivenUpAt == nil {
		t.Fatalf("expected GivenUpAt to be set after reaching the cap: %+v", intent)
	}
	if intent.Live() {
		t.Fatalf("a given-up intent must not be live: %+v", intent)
	}

	// Never silently deleted — same visibility rule as expiry.
	if intent.RuntimeID != runtimeID {
		t.Fatalf("given-up intent row should remain readable: %+v", intent)
	}

	// And it must stop accepting further failure recordings (nothing left to
	// retry, so nothing left to fail).
	if err := store.RecordFailure(ctx, runtimeID, "attempt-after-giving-up"); err != nil {
		t.Fatalf("RecordFailure after giving up: %v", err)
	}
	unchanged, err := store.Get(ctx, runtimeID)
	if err != nil || unchanged == nil || unchanged.ConsecutiveFailures != updateIntentMaxConsecutiveFailures {
		t.Fatalf("ConsecutiveFailures should not grow past the cap once given up: %+v, err=%v", unchanged, err)
	}
}

func TestPostgresUpdateIntentStore_CreateAfterGivenUpResetsFailureBookkeeping(t *testing.T) {
	runtimeID := newPostgresUpdateTestRuntime(t)
	memberID := testMemberIDForIntent(t)
	ctx := context.Background()
	store := NewPostgresUpdateIntentStore(testPool)

	if _, err := store.Create(ctx, runtimeID, memberID, time.Hour); err != nil {
		t.Fatalf("create: %v", err)
	}
	for i := 0; i < updateIntentMaxConsecutiveFailures; i++ {
		if err := store.RecordFailure(ctx, runtimeID, fmt.Sprintf("attempt-%d", i)); err != nil {
			t.Fatalf("RecordFailure #%d: %v", i, err)
		}
	}
	givenUp, err := store.Get(ctx, runtimeID)
	if err != nil || givenUp == nil || givenUp.Live() {
		t.Fatalf("precondition: intent should be given up: %+v, err=%v", givenUp, err)
	}

	// Parker's explicit requirement, 2026-08-02: a given-up runtime — very
	// plausibly the exact machine an admin most wants to fix — must not be
	// permanently stuck. Clicking Update again must fully reset it, not
	// require a separate "un-give-up" action.
	recreated, err := store.Create(ctx, runtimeID, memberID, time.Hour)
	if err != nil {
		t.Fatalf("re-create after giving up: %v", err)
	}
	if !recreated.Live() {
		t.Fatalf("re-created intent should be live: %+v", recreated)
	}
	if recreated.ConsecutiveFailures != 0 {
		t.Fatalf("re-created intent should reset ConsecutiveFailures, got %d", recreated.ConsecutiveFailures)
	}
	if recreated.GivenUpAt != nil {
		t.Fatalf("re-created intent should clear GivenUpAt, got %v", recreated.GivenUpAt)
	}
	if !recreated.DueForRetry(time.Now()) {
		t.Fatalf("re-created intent should be immediately eligible, NextRetryAt = %v", recreated.NextRetryAt)
	}
}

func testMemberIDForIntent(t *testing.T) string {
	t.Helper()
	if testPool == nil {
		t.Skip("database not available")
	}
	var memberID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM member WHERE workspace_id = $1 AND user_id = $2
	`, testWorkspaceID, testUserID).Scan(&memberID); err != nil {
		t.Fatalf("look up test member id: %v", err)
	}
	return memberID
}
