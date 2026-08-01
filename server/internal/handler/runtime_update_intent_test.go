package handler

import (
	"context"
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
