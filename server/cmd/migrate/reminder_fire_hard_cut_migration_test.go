package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestReminderFireHardCutMigrationPreservesLifecycleAndRetiresReceiptLinks(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("reminder_fire_hard_cut_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE channel_message (id UUID PRIMARY KEY, content TEXT NOT NULL);
		CREATE TABLE agent_reminder (
			id UUID PRIMARY KEY,
			fired_receipt_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL
		);
		CREATE INDEX idx_agent_reminder_fired_receipt
			ON agent_reminder(fired_receipt_message_id)
			WHERE fired_receipt_message_id IS NOT NULL;
		CREATE TABLE agent_reminder_occurrence (
			id UUID PRIMARY KEY,
			reminder_id UUID NOT NULL REFERENCES agent_reminder(id) ON DELETE CASCADE,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			cadence_scheduled_for TIMESTAMPTZ NOT NULL,
			due_at TIMESTAMPTZ NOT NULL,
			status TEXT NOT NULL,
			title_snapshot TEXT NOT NULL,
			receipt_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
			anchor_available BOOLEAN,
			claimed_at TIMESTAMPTZ,
			fired_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			CONSTRAINT agent_reminder_occurrence_reminder_id_cadence_scheduled_for_key
				UNIQUE (reminder_id, cadence_scheduled_for)
		);
		CREATE TABLE agent_reminder_lifecycle_event (
			id UUID PRIMARY KEY,
			reminder_id UUID NOT NULL REFERENCES agent_reminder(id) ON DELETE CASCADE,
			occurrence_id UUID REFERENCES agent_reminder_occurrence(id) ON DELETE SET NULL,
			event_type TEXT NOT NULL
		);

		INSERT INTO channel_message (id, content) VALUES
			('10000000-0000-0000-0000-000000000332', 'historical receipt one'),
			('20000000-0000-0000-0000-000000000332', 'historical receipt two');
		INSERT INTO agent_reminder (id, fired_receipt_message_id) VALUES
			('30000000-0000-0000-0000-000000000332', '20000000-0000-0000-0000-000000000332');
		INSERT INTO agent_reminder_occurrence (
			id, reminder_id, workspace_id, agent_id, cadence_scheduled_for, due_at,
			status, title_snapshot, receipt_message_id, anchor_available, claimed_at, fired_at, created_at
		) VALUES
			('40000000-0000-0000-0000-000000000332', '30000000-0000-0000-0000-000000000332',
			 '50000000-0000-0000-0000-000000000332', '60000000-0000-0000-0000-000000000332',
			 '2026-08-10T08:00:00Z', '2026-08-10T08:00:00Z', 'fired', 'first fire',
			 '10000000-0000-0000-0000-000000000332', true, '2026-08-10T08:00:00Z', '2026-08-10T08:00:01Z', '2026-08-10T08:00:00Z'),
			('70000000-0000-0000-0000-000000000332', '30000000-0000-0000-0000-000000000332',
			 '50000000-0000-0000-0000-000000000332', '60000000-0000-0000-0000-000000000332',
			 '2026-08-10T09:00:00Z', '2026-08-10T09:00:00Z', 'fired', 'second fire',
			 '20000000-0000-0000-0000-000000000332', true, '2026-08-10T09:00:00Z', '2026-08-10T09:00:01Z', '2026-08-10T09:00:00Z');
		INSERT INTO agent_reminder_lifecycle_event (id, reminder_id, occurrence_id, event_type) VALUES
			('80000000-0000-0000-0000-000000000332', '30000000-0000-0000-0000-000000000332', '40000000-0000-0000-0000-000000000332', 'fired'),
			('90000000-0000-0000-0000-000000000332', '30000000-0000-0000-0000-000000000332', '70000000-0000-0000-0000-000000000332', 'fired');
	`); err != nil {
		t.Fatal(err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "332_reminder_fire_hard_cut.up.sql"))

	var occurrences, lifecycles, messages, distinctVersions, positiveVersions int
	if err := conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM agent_reminder_occurrence),
		  (SELECT count(*) FROM agent_reminder_lifecycle_event),
		  (SELECT count(*) FROM channel_message),
		  (SELECT count(DISTINCT fire_version) FROM agent_reminder_occurrence),
		  (SELECT count(*) FROM agent_reminder_occurrence WHERE fire_version > 0)
	`).Scan(&occurrences, &lifecycles, &messages, &distinctVersions, &positiveVersions); err != nil {
		t.Fatal(err)
	}
	if occurrences != 2 || lifecycles != 2 || messages != 2 || distinctVersions != 2 || positiveVersions != 0 {
		t.Fatalf("up preserved occurrence/lifecycle/message=%d/%d/%d versions=%d positive=%d, want 2/2/2/2/0",
			occurrences, lifecycles, messages, distinctVersions, positiveVersions)
	}
	for _, retired := range []struct{ table, column string }{
		{table: "agent_reminder", column: "fired_receipt_message_id"},
		{table: "agent_reminder_occurrence", column: "receipt_message_id"},
		{table: "agent_reminder_occurrence", column: "anchor_available"},
	} {
		var count int
		if err := conn.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = $1 AND column_name = $2`, retired.table, retired.column).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired column %s.%s count=%d err=%v", retired.table, retired.column, count, err)
		}
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_reminder_occurrence (
			id, reminder_id, workspace_id, agent_id, fire_version, cadence_scheduled_for,
			due_at, status, title_snapshot, claimed_at, fired_at
		) VALUES (
			'a0000000-0000-0000-0000-000000000332', '30000000-0000-0000-0000-000000000332',
			'50000000-0000-0000-0000-000000000332', '60000000-0000-0000-0000-000000000332',
			1, '2026-08-10T10:00:00Z', '2026-08-10T10:00:00Z', 'fired', 'new fire',
			'2026-08-10T10:00:00Z', '2026-08-10T10:00:01Z'
		)`); err != nil {
		t.Fatalf("insert positive fire version: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_reminder_occurrence (
			id, reminder_id, workspace_id, agent_id, fire_version, cadence_scheduled_for,
			due_at, status, title_snapshot, claimed_at, fired_at
		) VALUES (
			'b0000000-0000-0000-0000-000000000332', '30000000-0000-0000-0000-000000000332',
			'50000000-0000-0000-0000-000000000332', '60000000-0000-0000-0000-000000000332',
			1, '2026-08-10T11:00:00Z', '2026-08-10T11:00:00Z', 'fired', 'duplicate fire',
			'2026-08-10T11:00:00Z', '2026-08-10T11:00:01Z'
		)`); err == nil {
		t.Fatal("duplicate (reminder_id, fire_version) was accepted")
	}
	if _, err := conn.Exec(ctx, `DELETE FROM agent_reminder_occurrence WHERE fire_version = 1`); err != nil {
		t.Fatal(err)
	}

	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "332_reminder_fire_hard_cut.down.sql"))
	var receiptColumns, historyRows int
	if err := conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM information_schema.columns WHERE table_schema = current_schema() AND column_name IN ('receipt_message_id', 'fired_receipt_message_id', 'anchor_available')),
		  (SELECT count(*) FROM agent_reminder_lifecycle_event)
	`).Scan(&receiptColumns, &historyRows); err != nil {
		t.Fatal(err)
	}
	if receiptColumns != 3 || historyRows != 2 {
		t.Fatalf("down restored receipt columns/history=%d/%d want 3/2", receiptColumns, historyRows)
	}
}
