package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAgentReminderDaemonSchedulerMigration210PreservesDefinitionsAcrossDownUp(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_reminder_daemon_scheduler_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			runtime_id UUID,
			archived_at TIMESTAMPTZ
		);
		CREATE TABLE agent_reminder (
			id UUID PRIMARY KEY,
			title TEXT NOT NULL,
			fire_at TIMESTAMPTZ NOT NULL,
			status TEXT NOT NULL,
			snooze_count INTEGER NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);
		INSERT INTO agent_reminder (id, title, fire_at, status, snooze_count, created_at, updated_at)
		VALUES
			('00000000-0000-0010-0000-000000000210', 'scheduled', '2026-07-22T09:00:00Z', 'scheduled', 2, '2026-07-22T08:00:00Z', '2026-07-22T08:30:00Z'),
			('00000000-0000-0011-0000-000000000210', 'fired', '2026-07-22T10:00:00Z', 'fired', 3, '2026-07-22T08:05:00Z', '2026-07-22T10:00:00Z')
	`); err != nil {
		t.Fatal(err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "210_agent_reminder_daemon_scheduler.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "210_agent_reminder_daemon_scheduler.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	assertDefinitions := func(label string) {
		t.Helper()
		var count int
		var minSnooze, maxSnooze int
		if err := conn.QueryRow(ctx, `SELECT count(*), min(snooze_count), max(snooze_count) FROM agent_reminder`).Scan(&count, &minSnooze, &maxSnooze); err != nil {
			t.Fatalf("%s read definitions: %v", label, err)
		}
		if count != 2 || minSnooze != 2 || maxSnooze != 3 {
			t.Fatalf("%s definitions changed: count=%d snooze=%d..%d", label, count, minSnooze, maxSnooze)
		}
	}

	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 210 up: %v", err)
	}
	assertDefinitions("up")
	var minVersion, maxVersion int64
	if err := conn.QueryRow(ctx, `SELECT min(version), max(version) FROM agent_reminder`).Scan(&minVersion, &maxVersion); err != nil || minVersion != 1 || maxVersion != 1 {
		t.Fatalf("initial versions=%d..%d err=%v", minVersion, maxVersion, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent_reminder SET version = 7 WHERE status = 'scheduled'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent (id, workspace_id, runtime_id)
		VALUES ('10000000-0000-0000-0000-000000000210', '20000000-0000-0000-0000-000000000210', '30000000-0000-0000-0000-000000000210');
		UPDATE agent SET runtime_id = '40000000-0000-0000-0000-000000000210' WHERE id = '10000000-0000-0000-0000-000000000210';
	`); err != nil {
		t.Fatalf("project runtime migration lifecycle: %v", err)
	}
	var oldEvent, newEvent string
	var oldGeneration, newGeneration int64
	if err := conn.QueryRow(ctx, `SELECT event_type, placement_generation FROM agent_reminder_daemon_owner_event WHERE runtime_id = '30000000-0000-0000-0000-000000000210' ORDER BY seq DESC LIMIT 1`).Scan(&oldEvent, &oldGeneration); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT event_type, placement_generation FROM agent_reminder_daemon_owner_event WHERE runtime_id = '40000000-0000-0000-0000-000000000210' ORDER BY seq DESC LIMIT 1`).Scan(&newEvent, &newGeneration); err != nil {
		t.Fatal(err)
	}
	if oldEvent != "stop" || newEvent != "start" || oldGeneration != newGeneration {
		t.Fatalf("runtime migration events old=%q/%d new=%q/%d", oldEvent, oldGeneration, newEvent, newGeneration)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent SET archived_at = now() WHERE id = '10000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}
	var archiveGeneration int64
	if err := conn.QueryRow(ctx, `SELECT event_type, placement_generation FROM agent_reminder_daemon_owner_event WHERE runtime_id = '40000000-0000-0000-0000-000000000210' ORDER BY seq DESC LIMIT 1`).Scan(&newEvent, &archiveGeneration); err != nil || newEvent != "stop" || archiveGeneration <= newGeneration {
		t.Fatalf("archive event=%q generation=%d err=%v", newEvent, archiveGeneration, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent SET archived_at = NULL WHERE id = '10000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}
	var restoreGeneration int64
	if err := conn.QueryRow(ctx, `SELECT event_type, placement_generation FROM agent_reminder_daemon_owner_event WHERE runtime_id = '40000000-0000-0000-0000-000000000210' ORDER BY seq DESC LIMIT 1`).Scan(&newEvent, &restoreGeneration); err != nil || newEvent != "start" || restoreGeneration <= archiveGeneration {
		t.Fatalf("restore event=%q generation=%d err=%v", newEvent, restoreGeneration, err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 210 down: %v", err)
	}
	assertDefinitions("down")
	var versionColumn *string
	if err := conn.QueryRow(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'agent_reminder' AND column_name = 'version'`).Scan(&versionColumn); err != pgx.ErrNoRows {
		t.Fatalf("version column survived down: value=%v err=%v", versionColumn, err)
	}
	var ownerEventTable *string
	if err := conn.QueryRow(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'agent_reminder_daemon_owner_event'`).Scan(&ownerEventTable); err != pgx.ErrNoRows {
		t.Fatalf("owner lifecycle table survived down: value=%v err=%v", ownerEventTable, err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("reapply migration 210 up: %v", err)
	}
	assertDefinitions("second up")
	if err := conn.QueryRow(ctx, `SELECT min(version), max(version) FROM agent_reminder`).Scan(&minVersion, &maxVersion); err != nil || minVersion != 1 || maxVersion != 1 {
		t.Fatalf("second-up versions=%d..%d err=%v", minVersion, maxVersion, err)
	}
}
