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
	"github.com/jackc/pgx/v5/pgxpool"
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
		CREATE TABLE workspace (id UUID PRIMARY KEY);
		CREATE TABLE agent_runtime (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			metadata JSONB NOT NULL DEFAULT '{}'::jsonb
		);
		CREATE TABLE agent (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			runtime_id UUID,
			archived_at TIMESTAMPTZ
		);
		CREATE TABLE agent_reminder (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			initiator_user_id UUID,
			title TEXT NOT NULL,
			fire_at TIMESTAMPTZ NOT NULL,
			status TEXT NOT NULL,
			fired_task_id UUID,
			snooze_count INTEGER NOT NULL,
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL,
			fired_at TIMESTAMPTZ,
			cadence TEXT,
			schedule_timezone TEXT,
			cadence_next_at TIMESTAMPTZ,
			current_occurrence_id UUID,
			terminal_reason TEXT
		);
		CREATE TABLE agent_reminder_occurrence (
			id UUID PRIMARY KEY,
			reminder_id UUID NOT NULL,
			status TEXT NOT NULL,
			fired_task_id UUID,
			receipt_message_id UUID,
			fired_at TIMESTAMPTZ,
			terminal_reason TEXT,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		CREATE TABLE agent_reminder_lifecycle_event (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			reminder_id UUID NOT NULL,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			occurrence_id UUID,
			event_type TEXT NOT NULL,
			actor_type TEXT NOT NULL,
			actor_id UUID,
			previous_fire_at TIMESTAMPTZ,
			next_fire_at TIMESTAMPTZ,
			title_snapshot TEXT NOT NULL,
			cadence_snapshot TEXT,
			timezone_snapshot TEXT,
			resulting_state TEXT NOT NULL,
			reason_code TEXT,
			details JSONB NOT NULL DEFAULT '{}'::jsonb,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
		INSERT INTO workspace (id) VALUES ('20000000-0000-0000-0000-000000000210');
		INSERT INTO agent_runtime (id, workspace_id, metadata) VALUES
			('30000000-0000-0000-0000-000000000210', '20000000-0000-0000-0000-000000000210', '{"capabilities":["reminder_versioned_cache_v1"]}'),
			('40000000-0000-0000-0000-000000000210', '20000000-0000-0000-0000-000000000210', '{"capabilities":["reminder_versioned_cache_v1"]}'),
			('50000000-0000-0000-0000-000000000210', '20000000-0000-0000-0000-000000000210', '{}');
		INSERT INTO agent (id, workspace_id, runtime_id)
		VALUES ('10000000-0000-0000-0000-000000000210', '20000000-0000-0000-0000-000000000210', '30000000-0000-0000-0000-000000000210');
		INSERT INTO agent_reminder (id, workspace_id, agent_id, title, fire_at, status, fired_task_id, snooze_count, created_at, updated_at, cadence, cadence_next_at, current_occurrence_id)
		VALUES
			('00000000-0000-0010-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'scheduled', '2026-07-22T09:00:00Z', 'scheduled', NULL, 1, '2026-07-22T08:00:00Z', '2026-07-22T08:30:00Z', NULL, NULL, NULL),
			('00000000-0000-0011-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'one shot delivered', '2026-07-22T10:00:00Z', 'firing', '70000000-0000-0000-0000-000000000210', 2, '2026-07-22T08:01:00Z', '2026-07-22T08:31:00Z', NULL, NULL, '60000000-0000-0000-0000-000000000211'),
			('00000000-0000-0012-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'recurring delivered', '2026-07-22T11:00:00Z', 'firing', '70000000-0000-0000-0000-000000000212', 3, '2026-07-22T08:02:00Z', '2026-07-22T08:32:00Z', 'every:1h', '2026-07-22T11:00:00Z', '60000000-0000-0000-0000-000000000212'),
			('00000000-0000-0013-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'unstarted retry', '2026-07-22T12:00:00Z', 'firing', NULL, 4, '2026-07-22T08:03:00Z', '2026-07-22T08:33:00Z', NULL, NULL, '60000000-0000-0000-0000-000000000213'),
			('00000000-0000-0014-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'partial receipt retry', '2026-07-22T13:00:00Z', 'firing', NULL, 5, '2026-07-22T08:04:00Z', '2026-07-22T08:34:00Z', 'every:2h', '2026-07-22T13:00:00Z', '60000000-0000-0000-0000-000000000214'),
			('00000000-0000-0015-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'already fired', '2026-07-22T14:00:00Z', 'fired', '70000000-0000-0000-0000-000000000215', 6, '2026-07-22T08:05:00Z', '2026-07-22T14:00:00Z', NULL, NULL, NULL);
		INSERT INTO agent_reminder_occurrence (id, reminder_id, status, fired_task_id, receipt_message_id) VALUES
			('60000000-0000-0000-0000-000000000211', '00000000-0000-0011-0000-000000000210', 'claimed', '70000000-0000-0000-0000-000000000210', '80000000-0000-0000-0000-000000000211'),
			('60000000-0000-0000-0000-000000000212', '00000000-0000-0012-0000-000000000210', 'claimed', '70000000-0000-0000-0000-000000000212', '80000000-0000-0000-0000-000000000212'),
			('60000000-0000-0000-0000-000000000213', '00000000-0000-0013-0000-000000000210', 'claimed', NULL, NULL),
			('60000000-0000-0000-0000-000000000214', '00000000-0000-0014-0000-000000000210', 'claimed', NULL, '80000000-0000-0000-0000-000000000214');
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
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent_reminder (id, workspace_id, agent_id, title, fire_at, status, fired_task_id, snooze_count, created_at, updated_at, cadence, schedule_timezone, cadence_next_at, current_occurrence_id)
		VALUES ('00000000-0000-0016-0000-000000000210', '20000000-0000-0000-0000-000000000210', '10000000-0000-0000-0000-000000000210', 'calendar recurring delivered', '2026-07-22T15:00:00Z', 'firing', '70000000-0000-0000-0000-000000000216', 7, '2026-07-22T08:06:00Z', '2026-07-22T08:36:00Z', 'daily@15:00', 'UTC', '2026-07-22T15:00:00Z', '60000000-0000-0000-0000-000000000216');
		INSERT INTO agent_reminder_occurrence (id, reminder_id, status, fired_task_id, receipt_message_id)
		VALUES ('60000000-0000-0000-0000-000000000216', '00000000-0000-0016-0000-000000000210', 'claimed', '70000000-0000-0000-0000-000000000216', '80000000-0000-0000-0000-000000000216');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err == nil || !strings.Contains(err.Error(), "reminder_cutover_recurring_requires_recovery") {
		t.Fatalf("calendar recurring cutover error = %v, want recovery preflight", err)
	}
	if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
		t.Fatalf("rollback failed recurring preflight: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		DELETE FROM agent_reminder_occurrence WHERE id = '60000000-0000-0000-0000-000000000216';
		DELETE FROM agent_reminder WHERE id = '00000000-0000-0016-0000-000000000210';
	`); err != nil {
		t.Fatal(err)
	}
	assertDefinitions := func(label string) {
		t.Helper()
		var count int
		var minSnooze, maxSnooze int
		if err := conn.QueryRow(ctx, `SELECT count(*), min(snooze_count), max(snooze_count) FROM agent_reminder`).Scan(&count, &minSnooze, &maxSnooze); err != nil {
			t.Fatalf("%s read definitions: %v", label, err)
		}
		if count != 6 || minSnooze != 1 || maxSnooze != 6 {
			t.Fatalf("%s definitions changed: count=%d snooze=%d..%d", label, count, minSnooze, maxSnooze)
		}
	}
	if _, err := conn.Exec(ctx, `UPDATE agent_runtime SET metadata = '{}'::jsonb WHERE id = '30000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err == nil || !strings.Contains(err.Error(), "daemon_outdated") {
		t.Fatalf("incapable owner preflight error = %v, want daemon_outdated", err)
	}
	if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
		t.Fatalf("rollback failed preflight: %v", err)
	}
	var preflightVersionColumn string
	if err := conn.QueryRow(ctx, `SELECT column_name FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'agent_reminder' AND column_name = 'version'`).Scan(&preflightVersionColumn); err != pgx.ErrNoRows {
		t.Fatalf("failed preflight mutated schema: column=%q err=%v", preflightVersionColumn, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent_runtime SET metadata = '{"capabilities":["reminder_versioned_cache_v1"]}'::jsonb WHERE id = '30000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}

	var migrationStartedAt time.Time
	if err := conn.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&migrationStartedAt); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 210 up: %v", err)
	}
	assertDefinitions("up")
	var minVersion, maxVersion int64
	if err := conn.QueryRow(ctx, `SELECT min(version), max(version) FROM agent_reminder`).Scan(&minVersion, &maxVersion); err != nil || minVersion != 1 || maxVersion != 2 {
		t.Fatalf("initial versions=%d..%d err=%v", minVersion, maxVersion, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent_reminder SET version = 7 WHERE status = 'scheduled'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent_runtime SET metadata = '{}'::jsonb WHERE id = '30000000-0000-0000-0000-000000000210'`); err == nil || !strings.Contains(err.Error(), "daemon_outdated") {
		t.Fatalf("active reminder capability downgrade error = %v, want daemon_outdated", err)
	}
	var retainedCapability bool
	if err := conn.QueryRow(ctx, `SELECT COALESCE((metadata->'capabilities') @> '["reminder_versioned_cache_v1"]'::jsonb, false) FROM agent_runtime WHERE id = '30000000-0000-0000-0000-000000000210'`).Scan(&retainedCapability); err != nil || !retainedCapability {
		t.Fatalf("rejected downgrade mutated registration capability=%v err=%v", retainedCapability, err)
	}
	moveConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer moveConn.Release()
	downgradeConn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer downgradeConn.Release()
	for _, testConn := range []*pgxpool.Conn{moveConn, downgradeConn} {
		if _, err := testConn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
			t.Fatal(err)
		}
	}

	// Move-first: the Agent trigger must hold a share lock on the capable target
	// runtime until placement commits. A concurrent metadata downgrade then
	// rechecks the committed active owner and fails without changing metadata.
	moveFirst, err := moveConn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := moveFirst.Exec(ctx, `UPDATE agent SET runtime_id = '40000000-0000-0000-0000-000000000210' WHERE id = '10000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}
	moveFirstDowngrade := make(chan error, 1)
	go func() {
		tx, err := downgradeConn.Begin(ctx)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE agent_runtime SET metadata = '{}'::jsonb WHERE id = '40000000-0000-0000-0000-000000000210'`)
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else if tx != nil {
			_ = tx.Rollback(ctx)
		}
		moveFirstDowngrade <- err
	}()
	select {
	case err := <-moveFirstDowngrade:
		t.Fatalf("move-first downgrade did not wait for target runtime share lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := moveFirst.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-moveFirstDowngrade; err == nil || !strings.Contains(err.Error(), "daemon_outdated") {
		t.Fatalf("move-first downgrade error=%v want daemon_outdated", err)
	}
	var placementAfterMoveFirst string
	if err := conn.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = '10000000-0000-0000-0000-000000000210'`).Scan(&placementAfterMoveFirst); err != nil || placementAfterMoveFirst != "40000000-0000-0000-0000-000000000210" {
		t.Fatalf("move-first placement=%q err=%v", placementAfterMoveFirst, err)
	}
	if err := conn.QueryRow(ctx, `SELECT COALESCE((metadata->'capabilities') @> '["reminder_versioned_cache_v1"]'::jsonb, false) FROM agent_runtime WHERE id = '40000000-0000-0000-0000-000000000210'`).Scan(&retainedCapability); err != nil || !retainedCapability {
		t.Fatalf("move-first rejected downgrade capability=%v err=%v", retainedCapability, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent SET runtime_id = '30000000-0000-0000-0000-000000000210' WHERE id = '10000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}

	// Downgrade-first: an exclusive metadata update held open on the target
	// runtime must make the Agent move wait. Once downgrade commits, the move
	// sees the incapable target and fails without changing placement.
	downgradeFirst, err := downgradeConn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := downgradeFirst.Exec(ctx, `UPDATE agent_runtime SET metadata = '{}'::jsonb WHERE id = '40000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}
	downgradeFirstMove := make(chan error, 1)
	go func() {
		tx, err := moveConn.Begin(ctx)
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE agent SET runtime_id = '40000000-0000-0000-0000-000000000210' WHERE id = '10000000-0000-0000-0000-000000000210'`)
		}
		if err == nil {
			err = tx.Commit(ctx)
		} else if tx != nil {
			_ = tx.Rollback(ctx)
		}
		downgradeFirstMove <- err
	}()
	select {
	case err := <-downgradeFirstMove:
		t.Fatalf("downgrade-first move did not wait for target runtime row: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := downgradeFirst.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-downgradeFirstMove; err == nil || !strings.Contains(err.Error(), "daemon_outdated") {
		t.Fatalf("downgrade-first move error=%v want daemon_outdated", err)
	}
	var placementAfterDowngradeFirst string
	if err := conn.QueryRow(ctx, `SELECT runtime_id::text FROM agent WHERE id = '10000000-0000-0000-0000-000000000210'`).Scan(&placementAfterDowngradeFirst); err != nil || placementAfterDowngradeFirst != "30000000-0000-0000-0000-000000000210" {
		t.Fatalf("downgrade-first placement=%q err=%v", placementAfterDowngradeFirst, err)
	}
	var downgradedCapability bool
	if err := conn.QueryRow(ctx, `SELECT COALESCE((metadata->'capabilities') @> '["reminder_versioned_cache_v1"]'::jsonb, false) FROM agent_runtime WHERE id = '40000000-0000-0000-0000-000000000210'`).Scan(&downgradedCapability); err != nil || downgradedCapability {
		t.Fatalf("downgrade-first capability=%v err=%v", downgradedCapability, err)
	}
	if _, err := conn.Exec(ctx, `UPDATE agent_runtime SET metadata = '{"capabilities":["reminder_versioned_cache_v1"]}'::jsonb WHERE id = '40000000-0000-0000-0000-000000000210'`); err != nil {
		t.Fatal(err)
	}
	var deliveredStatus, recurringStatus, retryStatus, partialStatus string
	var deliveredCurrent, recurringCurrent, retryCurrent, partialCurrent *string
	if err := conn.QueryRow(ctx, `SELECT status, current_occurrence_id::text FROM agent_reminder WHERE id = '00000000-0000-0011-0000-000000000210'`).Scan(&deliveredStatus, &deliveredCurrent); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT status, current_occurrence_id::text FROM agent_reminder WHERE id = '00000000-0000-0012-0000-000000000210'`).Scan(&recurringStatus, &recurringCurrent); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT status, current_occurrence_id::text FROM agent_reminder WHERE id = '00000000-0000-0013-0000-000000000210'`).Scan(&retryStatus, &retryCurrent); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `SELECT status, current_occurrence_id::text FROM agent_reminder WHERE id = '00000000-0000-0014-0000-000000000210'`).Scan(&partialStatus, &partialCurrent); err != nil {
		t.Fatal(err)
	}
	if deliveredStatus != "fired" || recurringStatus != "scheduled" || retryStatus != "scheduled" || partialStatus != "scheduled" || deliveredCurrent != nil || recurringCurrent != nil || retryCurrent != nil || partialCurrent != nil {
		t.Fatalf("cutover firing convergence statuses/current = %s/%v %s/%v %s/%v %s/%v", deliveredStatus, deliveredCurrent, recurringStatus, recurringCurrent, retryStatus, retryCurrent, partialStatus, partialCurrent)
	}
	var recurringFireAt, recurringCadenceNextAt time.Time
	if err := conn.QueryRow(ctx, `SELECT fire_at, cadence_next_at FROM agent_reminder WHERE id = '00000000-0000-0012-0000-000000000210'`).Scan(&recurringFireAt, &recurringCadenceNextAt); err != nil {
		t.Fatal(err)
	}
	if !recurringFireAt.Equal(recurringCadenceNextAt) || !recurringFireAt.After(migrationStartedAt) {
		t.Fatalf("delivered recurring next slot fire=%s cadence=%s migration_start=%s", recurringFireAt, recurringCadenceNextAt, migrationStartedAt)
	}
	seedSlot := time.Date(2026, 7, 22, 11, 0, 0, 0, time.UTC)
	if delta := recurringFireAt.Sub(seedSlot); delta <= 0 || delta%time.Hour != 0 {
		t.Fatalf("delivered recurring next slot delta = %s, want positive whole hours", delta)
	}
	var immediatelyDue int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agent_reminder WHERE id = '00000000-0000-0012-0000-000000000210' AND fire_at <= clock_timestamp()`).Scan(&immediatelyDue); err != nil || immediatelyDue != 0 {
		t.Fatalf("delivered recurring reconnect due count=%d err=%v", immediatelyDue, err)
	}
	var firedOccurrences, cancelledOccurrences int
	if err := conn.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status='fired'), count(*) FILTER (WHERE status='cancelled' AND terminal_reason='daemon_cutover_rearm') FROM agent_reminder_occurrence`).Scan(&firedOccurrences, &cancelledOccurrences); err != nil || firedOccurrences != 2 || cancelledOccurrences != 2 {
		t.Fatalf("cutover occurrence convergence fired/cancelled=%d/%d err=%v", firedOccurrences, cancelledOccurrences, err)
	}
	if _, err := conn.Exec(ctx, `
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
	if _, err := conn.Exec(ctx, `UPDATE agent SET runtime_id = '50000000-0000-0000-0000-000000000210' WHERE id = '10000000-0000-0000-0000-000000000210'`); err == nil || !strings.Contains(err.Error(), "daemon_outdated") {
		t.Fatalf("incapable runtime move error = %v, want daemon_outdated", err)
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
