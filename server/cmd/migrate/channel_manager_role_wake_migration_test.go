package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestChannelManagerRoleWake247RefusesChannelRoleChangedRows locks task
// #100's fix for migration 247: down.sql narrows agent_inbox_event.reason,
// dropping 'channel_role_changed' with no remap. That reason is its own
// durable wake ("The replacement wake is a normal durable agent inbox
// reason", per up.sql's own comment) with no equivalent among the
// remaining values — every other reason is triggered by chat content, not
// a membership/role change.
//
// Runs the real down.sql file directly (not a minimal reconstruction of
// up.sql's full migration, which touches channel_member/agent/several
// trigger functions unrelated to this guard) against tables in the
// post-247-up shape the down.sql itself expects: down.sql's own statements
// (ADD COLUMN/CREATE INDEX/DROP+ADD CONSTRAINT) are self-contained and
// don't require the full up.sql history to have actually run.
func TestChannelManagerRoleWake247RefusesChannelRoleChangedRows(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("channel_manager_role_wake_247_test_%d", time.Now().UnixNano())
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

	// Post-247-up shape: channel has no group_manager_agent_id (down.sql
	// re-adds it), agent_inbox_event's reason CHECK includes
	// 'channel_role_changed' (down.sql narrows it away).
	if _, err := conn.Exec(ctx, `
		CREATE TABLE channel (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE agent (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  managed_role TEXT
		);
		CREATE TABLE agent_inbox_event (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  reason TEXT NOT NULL
		    CHECK (reason IN (
		      'mention', 'dm', 'ambient', 'thread_reply', 'channel_message',
		      'collaboration_turn', 'collaboration_manager_fallback',
		      'channel_onboarding', 'issue', 'quick_create', 'autopilot',
		      'agent_radar', 'training', 'environment_dispatch',
		      'memory_curation', 'reminder', 'channel_role_changed'
		    ))
		);
	`); err != nil {
		t.Fatalf("create minimal post-247-up tables: %v", err)
	}

	downSQL := readMigration247Down(t)

	// Empty table: down must succeed.
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("down must succeed with no channel_role_changed rows: %v", err)
	}
	var columnExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.columns
		  WHERE table_schema = current_schema() AND table_name = 'channel'
		    AND column_name = 'group_manager_agent_id'
		)
	`).Scan(&columnExists); err != nil {
		t.Fatalf("check column after clean down: %v", err)
	}
	if !columnExists {
		t.Fatal("after clean down, channel.group_manager_agent_id must exist")
	}

	// Reset to post-247-up shape for the second phase: the first down.sql
	// call already added channel.group_manager_agent_id and narrowed
	// reason, and down.sql's ADD COLUMN has no IF NOT EXISTS guard.
	if _, err := conn.Exec(ctx, `
		ALTER TABLE channel DROP COLUMN group_manager_agent_id;
		ALTER TABLE agent_inbox_event DROP CONSTRAINT IF EXISTS agent_inbox_event_reason_check;
		ALTER TABLE agent_inbox_event ADD CONSTRAINT agent_inbox_event_reason_check
		  CHECK (reason IN (
		    'mention', 'dm', 'ambient', 'thread_reply', 'channel_message',
		    'collaboration_turn', 'collaboration_manager_fallback',
		    'channel_onboarding', 'issue', 'quick_create', 'autopilot',
		    'agent_radar', 'training', 'environment_dispatch',
		    'memory_curation', 'reminder', 'channel_role_changed'
		  ));
		INSERT INTO agent_inbox_event (reason) VALUES ('channel_role_changed');
	`); err != nil {
		t.Fatalf("reset to post-up shape and seed channel_role_changed row: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, downErr := tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must refuse while a channel_role_changed row exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "reason=''channel_role_changed''") &&
		!strings.Contains(downErr.Error(), "channel_role_changed") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agent_inbox_event WHERE reason = 'channel_role_changed'`).Scan(&count); err != nil {
		t.Fatalf("count after failed down: %v", err)
	}
	if count != 1 {
		t.Fatalf("row count after failed down = %d, want 1", count)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM agent_inbox_event WHERE reason = 'channel_role_changed'`); err != nil {
		t.Fatalf("manual delete per the guard's suggested recovery: %v", err)
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("down must succeed once no channel_role_changed rows remain: %v", err)
	}
}

func readMigration247Down(t *testing.T) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	down, err := os.ReadFile(filepath.Join(migrationsDir, "247_channel_manager_role_wake.down.sql"))
	if err != nil {
		t.Fatalf("read 247 down: %v", err)
	}
	return string(down)
}
