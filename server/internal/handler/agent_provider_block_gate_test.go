package handler

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

func TestInboxFailureRetryable_StickyQuotaNotRetryable(t *testing.T) {
	t.Parallel()
	err := `429: {"code":"1310","message":"已达到 7 天使用上限，2026-08-03 13:52:38 后可继续使用。"}`
	if inboxFailureRetryable(err, string(taskfailure.ReasonAgentProviderCapacityOrRateLimit), false) {
		t.Fatal("sticky quota must set retryable=false")
	}
	if !inboxFailureRetryable("API Error: 429 Too Many Requests", string(taskfailure.ReasonAgentProviderCapacityOrRateLimit), false) {
		t.Fatal("transient capacity 429 must stay retryable")
	}
	if inboxFailureRetryable("anything", "", true) {
		t.Fatal("already-replied must not be retryable")
	}
}

// TestLeaseAgentInbox_SkipsProviderQuotaLockAndResumesAfterClear is task #92
// bidirectional acceptance: while locked, lease returns no rows and the wake
// stays pending (not terminalized); after clear, the same wake is leasable.
func TestLeaseAgentInbox_SkipsProviderQuotaLockAndResumesAfterClear(t *testing.T) {
	if testHandler == nil {
		t.Skip("database not available")
	}
	ctx := context.Background()
	agentID, runtimeID := createAgentHealthFixture(t, "online", time.Now(), time.Now())

	var sessionID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_session (workspace_id, agent_id, runtime_id, scope, status)
		VALUES ($1, $2, $3, 'direct_chat', 'active')
		RETURNING id
	`, testWorkspaceID, agentID, runtimeID).Scan(&sessionID); err != nil {
		t.Fatalf("create agent session: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_session WHERE id = $1`, sessionID)
	})

	var eventID string
	if err := testPool.QueryRow(ctx, `
		INSERT INTO agent_inbox_event (
		  workspace_id, agent_session_id, runtime_id, agent_id,
		  reason, requires_wake, status, priority, seq_from, seq_to
		)
		VALUES ($1, $2, $3, $4, 'dm', true, 'pending', 10, 1, 1)
		RETURNING id
	`, testWorkspaceID, sessionID, runtimeID, agentID).Scan(&eventID); err != nil {
		t.Fatalf("create inbox wake: %v", err)
	}
	t.Cleanup(func() {
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_event_delivery WHERE inbox_event_id = $1`, eventID)
		_, _ = testPool.Exec(ctx, `DELETE FROM agent_inbox_event WHERE id = $1`, eventID)
	})

	runtime, err := testHandler.Queries.GetAgentRuntime(ctx, parseUUID(runtimeID))
	if err != nil {
		t.Fatalf("load runtime: %v", err)
	}

	// Lock with unknown until (NULL) — still blocked.
	if err := testHandler.Queries.MarkAgentProviderBlocked(ctx, db.MarkAgentProviderBlockedParams{
		ID:                   parseUUID(agentID),
		ProviderBlockedUntil: pgtype.Timestamptz{},
		ProviderBlockDetail:  "quota lock for #92 gate test",
	}); err != nil {
		t.Fatalf("mark provider blocked: %v", err)
	}
	t.Cleanup(func() {
		_ = testHandler.Queries.ClearAgentProviderBlocked(ctx, parseUUID(agentID))
	})

	if _, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("lease while locked: err=%v, want no rows", err)
	}
	var status string
	if err := testPool.QueryRow(ctx, `SELECT status FROM agent_inbox_event WHERE id = $1`, eventID).Scan(&status); err != nil {
		t.Fatalf("read event status: %v", err)
	}
	if status != "pending" {
		t.Fatalf("event status = %q, want pending (must not terminalize while locked)", status)
	}

	if err := testHandler.Queries.ClearAgentProviderBlocked(ctx, parseUUID(agentID)); err != nil {
		t.Fatalf("clear provider block: %v", err)
	}

	delivery, err := testHandler.leaseAgentInboxEventForRuntime(ctx, runtime)
	if err != nil {
		t.Fatalf("lease after unlock: %v", err)
	}
	if uuidToString(delivery.InboxEventID) != eventID {
		t.Fatalf("leased event = %s, want %s", uuidToString(delivery.InboxEventID), eventID)
	}
}
