package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestAgentWakeCleanCutover223RefusesTerminalOutcomeRows locks task #100's
// fix for migration 223: down.sql narrows agent_inbox_event.terminal_outcome
// to drop 'completed'/'cancelled' with no remap. The narrowing is safe for
// rows this same down.sql relocates into the reconstructed agent_task_queue
// (any reason in the queue-copy list: dm/channel_message/issue/quick_create/
// autopilot/agent_radar/training/environment_dispatch/memory_curation/
// reminder — see the DELETE at the top of the file) — those never reach the
// guard, since they're already gone from agent_inbox_event by the time it
// runs. The guard exists for terminal rows that arose independently of that
// reconstruction: reason values the queue never covered (mention/dm-thread/
// ambient/collaboration_turn/collaboration_manager_fallback/
// channel_onboarding), which only exist because of real usage after 223's
// cutover, not because of the migration itself.
func TestAgentWakeCleanCutover223RefusesTerminalOutcomeRows(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_wake_223_outcome_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search path: %v", err)
	}

	// Minimal reconstruction of just what the guard's own SELECT/DELETE
	// touch: agent_inbox_event with the columns and CHECK the guard
	// exercises. Not the full 223 up.sql schema — that's already covered
	// end-to-end by TestAgentWakeCleanCutoverMigrationPreservesLedgerAndReenqueuesActiveWork,
	// which proves real migrated data never trips this guard. This test is
	// narrowly about the guard's own SQL: does it fire on the exact rows it
	// claims to protect.
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent_inbox_event (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  reason TEXT NOT NULL,
		  terminal_outcome TEXT,
		  CONSTRAINT agent_inbox_event_terminal_outcome_check
		    CHECK (terminal_outcome IN (
		      'replied', 'no_reply', 'held', 'failed', 'sent', 'skipped',
		      'expired', 'completed', 'cancelled'
		    ))
		);
	`); err != nil {
		t.Fatalf("create minimal agent_inbox_event: %v", err)
	}

	guardSQL := `
DO $$
DECLARE
    affected_count integer;
BEGIN
    SELECT count(*) INTO affected_count
      FROM agent_inbox_event WHERE terminal_outcome IN ('completed', 'cancelled');
    IF affected_count > 0 THEN
        RAISE EXCEPTION 'migration 223 down cannot proceed: % row(s) in agent_inbox_event have terminal_outcome in (''completed'', ''cancelled''). There is no safe value to remap them to under the narrower terminal_outcome list this migration is reverting to — none of the remaining values mean "finished successfully" or "was cancelled". If you accept permanently losing this outcome history, run: DELETE FROM agent_inbox_event WHERE terminal_outcome IN (''completed'', ''cancelled''); -- then re-run this down migration.', affected_count;
    END IF;
END $$;
`

	// No terminal rows at all: guard must be a clean no-op.
	if _, err := conn.Exec(ctx, guardSQL); err != nil {
		t.Fatalf("guard must succeed with no completed/cancelled rows: %v", err)
	}

	// A row with reason='mention' — outside the queue-copy list, so it
	// would still be sitting in agent_inbox_event when the real down.sql
	// reaches this guard. Must refuse.
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_inbox_event (reason, terminal_outcome)
		VALUES ('mention', 'cancelled')
	`); err != nil {
		t.Fatalf("seed mention/cancelled row: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, guardErr := tx.Exec(ctx, guardSQL)
	if guardErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("guard must refuse while a mention/cancelled row exists, it succeeded instead")
	}
	if !strings.Contains(guardErr.Error(), "terminal_outcome in ('completed', 'cancelled')") {
		_ = tx.Rollback(ctx)
		t.Fatalf("guard failed for the wrong reason: %v", guardErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM agent_inbox_event WHERE terminal_outcome IN ('completed', 'cancelled')
	`).Scan(&count); err != nil {
		t.Fatalf("count after failed guard: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after failed guard = %d, want 1", count)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM agent_inbox_event WHERE terminal_outcome IN ('completed', 'cancelled')`); err != nil {
		t.Fatalf("manual delete per the guard's suggested recovery: %v", err)
	}
	if _, err := conn.Exec(ctx, guardSQL); err != nil {
		t.Fatalf("guard must succeed once no completed/cancelled rows remain: %v", err)
	}
}
