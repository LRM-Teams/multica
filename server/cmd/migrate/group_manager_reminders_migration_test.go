package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestGroupManagerRemindersMigration221DiscardsPendingAndBackfillsPatrol(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, schema := createPre221ManagedReminderSchema(t, ctx, pool)
	defer conn.Release()
	seedPre221ManagedReminderRows(t, ctx, conn, "pending")

	upSQL := readManagedReminderMigrationSQL(t, "221_group_manager_reminders.up.sql")
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 221 up: %v", err)
	}

	var pendingExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('pending_handoff') IS NOT NULL`).Scan(&pendingExists); err != nil {
		t.Fatal(err)
	}
	if pendingExists {
		t.Fatal("pending_handoff still exists after migration 221")
	}
	var total, patrols int
	if err := conn.QueryRow(ctx, `
		SELECT
		  count(*),
		  count(*) FILTER (WHERE managed_kind = 'patrol')
		FROM agent_reminder
		WHERE origin_kind = 'group_manager_auto'
	`).Scan(&total, &patrols); err != nil {
		t.Fatal(err)
	}
	if total != 1 || patrols != 1 {
		t.Fatalf("managed reminders total/patrol = %d/%d, want 1/1", total, patrols)
	}
	var cadenceIsNull bool
	var initialDelaySeconds int64
	if err := conn.QueryRow(ctx, `
		SELECT cadence IS NULL, extract(epoch FROM (fire_at - now()))::bigint
		FROM agent_reminder
		WHERE managed_kind = 'patrol'
	`).Scan(&cadenceIsNull, &initialDelaySeconds); err != nil {
		t.Fatal(err)
	}
	initialDelay := time.Duration(initialDelaySeconds) * time.Second
	if !cadenceIsNull || initialDelay < 14*time.Minute || initialDelay > 16*time.Minute {
		t.Fatalf("patrol cadence_null=%v initial_delay=%s, want adaptive one-shot near 15m", cadenceIsNull, initialDelay)
	}

	if _, err := conn.Exec(ctx, `
		DELETE FROM channel_member
		WHERE channel_id = '50000000-0000-4000-8000-000000000219'
		  AND member_type = 'agent'
		  AND member_id = '30000000-0000-4000-8000-000000000219'
	`); err != nil {
		t.Fatalf("delete manager membership: %v", err)
	}
	var cancelled, lifecycle int
	if err := conn.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE status = 'cancelled'),
		  (
		    SELECT count(*)
		    FROM agent_reminder_lifecycle_event
		    WHERE reason_code = 'group_manager_removed'
		  )
		FROM agent_reminder
		WHERE origin_kind = 'group_manager_auto'
	`).Scan(&cancelled, &lifecycle); err != nil {
		t.Fatal(err)
	}
	if cancelled != 1 || lifecycle != 1 {
		t.Fatalf("membership teardown cancelled/lifecycle = %d/%d, want 1/1", cancelled, lifecycle)
	}

	t.Logf("migration 221 fixture schema %s converted and lifecycle-cancelled cleanly", schema)
}

func TestGroupManagerRemindersMigration221RejectsClaimedLegacyRows(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _ := createPre221ManagedReminderSchema(t, ctx, pool)
	defer conn.Release()
	seedPre221ManagedReminderRows(t, ctx, conn, "claimed")

	_, err := conn.Exec(ctx, readManagedReminderMigrationSQL(t, "221_group_manager_reminders.up.sql"))
	if err == nil {
		t.Fatal("migration 221 unexpectedly accepted a claimed legacy handoff")
	}
	var pgErr *pgconn.PgError
	if !strings.Contains(err.Error(), "group_manager_handoff_cutover_claimed_rows_require_audit") ||
		!errors.As(err, &pgErr) || pgErr.Code != "P0001" {
		t.Fatalf("claimed cutover error = %v, want P0001 group_manager_handoff_cutover_claimed_rows_require_audit", err)
	}
	if _, rollbackErr := conn.Exec(ctx, "ROLLBACK"); rollbackErr != nil {
		t.Fatalf("rollback failed migration: %v", rollbackErr)
	}
	var pendingExists, originColumnExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('pending_handoff') IS NOT NULL`).Scan(&pendingExists); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = 'agent_reminder'
		    AND column_name = 'origin_kind'
		)
	`).Scan(&originColumnExists); err != nil {
		t.Fatal(err)
	}
	if !pendingExists || originColumnExists {
		t.Fatalf("failed migration was not atomic: pending_exists=%v origin_column_exists=%v", pendingExists, originColumnExists)
	}
}

func TestGroupManagerRemindersMigration221DownRestoresEmptyLegacyTable(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _ := createPre221ManagedReminderSchema(t, ctx, pool)
	defer conn.Release()
	seedPre221ManagedReminderRows(t, ctx, conn, "pending")

	if _, err := conn.Exec(ctx, readManagedReminderMigrationSQL(t, "221_group_manager_reminders.up.sql")); err != nil {
		t.Fatalf("apply migration 221 up: %v", err)
	}
	if _, err := conn.Exec(ctx, readManagedReminderMigrationSQL(t, "221_group_manager_reminders.down.sql")); err != nil {
		t.Fatalf("apply migration 221 down: %v", err)
	}

	var pendingCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM pending_handoff`).Scan(&pendingCount); err != nil {
		t.Fatal(err)
	}
	if pendingCount != 0 {
		t.Fatalf("restored pending handoff count=%d, want 0", pendingCount)
	}
	var managedColumnsExist bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = 'agent_reminder'
		    AND column_name IN ('origin_kind', 'managed_kind', 'origin_key')
		)
	`).Scan(&managedColumnsExist); err != nil {
		t.Fatal(err)
	}
	if managedColumnsExist {
		t.Fatal("managed reminder columns still exist after migration 221 down")
	}
	var reminderCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agent_reminder`).Scan(&reminderCount); err != nil {
		t.Fatal(err)
	}
	if reminderCount != 0 {
		t.Fatalf("managed reminders after down = %d, want 0", reminderCount)
	}
}

func TestGroupManagerPatrolIntervalsMigration222UsesIssueProgressAndDormancy(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, _ := createPre221ManagedReminderSchema(t, ctx, pool)
	defer conn.Release()
	seedPre221ManagedReminderRows(t, ctx, conn, "pending")

	if _, err := conn.Exec(ctx, readManagedReminderMigrationSQL(t, "221_group_manager_reminders.up.sql")); err != nil {
		t.Fatalf("apply migration 221 up: %v", err)
	}

	var dormantReminderID string
	if err := conn.QueryRow(ctx, `
		UPDATE agent_reminder
		SET fire_at = now() + interval '24 hours'
		WHERE origin_kind = 'group_manager_auto'
		RETURNING id
	`).Scan(&dormantReminderID); err != nil {
		t.Fatalf("prepare dormant schedule: %v", err)
	}

	const adaptiveReminderID = "91000000-0000-4000-8000-000000000222"
	const ordinaryReminderID = "92000000-0000-4000-8000-000000000222"
	const activeIssueID = "71000000-0000-4000-8000-000000000222"
	if _, err := conn.Exec(ctx, `
		INSERT INTO agent (
		  id, workspace_id, runtime_id, managed_role
		) VALUES (
		  '31000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219',
		  '60000000-0000-4000-8000-000000000219',
		  'group_manager'
		);
		INSERT INTO channel (
		  id, workspace_id, kind, created_by, group_manager_agent_id
		) VALUES (
		  '51000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219',
		  'group',
		  '20000000-0000-4000-8000-000000000219',
		  '31000000-0000-4000-8000-000000000222'
		);
		INSERT INTO channel_message (
		  id, channel_id, workspace_id
		) VALUES (
		  '81000000-0000-4000-8000-000000000222',
		  '51000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219'
		);
		INSERT INTO issue (
		  id, workspace_id, status
		) VALUES (
		  '71000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219',
		  'in_progress'
		);
		INSERT INTO issue_source_message (
		  issue_id, message_id, channel_id, workspace_id
		) VALUES (
		  '71000000-0000-4000-8000-000000000222',
		  '81000000-0000-4000-8000-000000000222',
		  '51000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219'
		);
		INSERT INTO agent_reminder (
		  id, workspace_id, agent_id, initiator_user_id, title,
		  anchor_channel_id, fire_at, origin_kind, managed_kind, origin_key
		) VALUES (
		  '91000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219',
		  '31000000-0000-4000-8000-000000000222',
		  '20000000-0000-4000-8000-000000000219',
		  'adaptive patrol',
		  '51000000-0000-4000-8000-000000000222',
		  now() + interval '24 hours',
		  'group_manager_auto', 'patrol',
		  'patrol:51000000-0000-4000-8000-000000000222'
		);
		INSERT INTO agent_reminder_lifecycle_event (
		  reminder_id, workspace_id, agent_id, event_type, actor_type, actor_id,
		  next_fire_at, title_snapshot, resulting_state, reason_code
		) VALUES (
		  '91000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219',
		  '31000000-0000-4000-8000-000000000222',
		  'snoozed', 'agent',
		  '31000000-0000-4000-8000-000000000222',
		  now() + interval '24 hours',
		  'adaptive patrol', 'scheduled',
		  'patrol_replanned_by_natural_language'
		);

		INSERT INTO agent_reminder (
		  id, workspace_id, agent_id, initiator_user_id, title,
		  anchor_channel_id, fire_at
		) VALUES (
		  '92000000-0000-4000-8000-000000000222',
		  '10000000-0000-4000-8000-000000000219',
		  '31000000-0000-4000-8000-000000000222',
		  '20000000-0000-4000-8000-000000000219',
		  'ordinary reminder',
		  '51000000-0000-4000-8000-000000000222',
		  now() + interval '24 hours'
		)
	`); err != nil {
		t.Fatalf("seed pre-222 schedules: %v", err)
	}

	if _, err := conn.Exec(ctx, readManagedReminderMigrationSQL(t, "222_group_manager_patrol_intervals.up.sql")); err != nil {
		t.Fatalf("apply migration 222 up: %v", err)
	}
	if _, err := conn.Exec(ctx, readManagedReminderMigrationSQL(t, "222_group_manager_patrol_intervals.up.sql")); err != nil {
		t.Fatalf("reapply migration 222 up: %v", err)
	}

	var dormantStatus, adaptiveStatus, ordinaryStatus string
	var adaptiveSeconds, ordinarySeconds int64
	var adaptiveStep int16
	if err := conn.QueryRow(ctx, `
		SELECT
		  (SELECT status FROM agent_reminder WHERE id = $1),
		  (SELECT status FROM agent_reminder WHERE id = $2),
		  (SELECT managed_backoff_step FROM agent_reminder WHERE id = $2),
		  extract(epoch FROM ((SELECT fire_at FROM agent_reminder WHERE id = $2) - now()))::bigint,
		  (SELECT status FROM agent_reminder WHERE id = $3),
		  extract(epoch FROM ((SELECT fire_at FROM agent_reminder WHERE id = $3) - now()))::bigint
	`, dormantReminderID, adaptiveReminderID, ordinaryReminderID).Scan(
		&dormantStatus, &adaptiveStatus, &adaptiveStep, &adaptiveSeconds,
		&ordinaryStatus, &ordinarySeconds,
	); err != nil {
		t.Fatal(err)
	}
	assertDelayNear := func(name string, got int64, want time.Duration) {
		t.Helper()
		wantSeconds := int64(want / time.Second)
		if got < wantSeconds-60 || got > wantSeconds+60 {
			t.Fatalf("%s delay=%ds, want near %s", name, got, want)
		}
	}
	if dormantStatus != "fired" {
		t.Fatalf("no-active-issue patrol status=%s, want fired/dormant", dormantStatus)
	}
	if adaptiveStatus != "scheduled" || adaptiveStep != 0 {
		t.Fatalf("active-issue patrol status/step=%s/%d, want scheduled/0", adaptiveStatus, adaptiveStep)
	}
	assertDelayNear("adaptive", adaptiveSeconds, 15*time.Minute)
	if ordinaryStatus != "scheduled" {
		t.Fatalf("ordinary reminder status=%s, want scheduled", ordinaryStatus)
	}
	assertDelayNear("ordinary", ordinarySeconds, 24*time.Hour)

	var dormantEvents, adaptiveEvents, ordinaryEvents int
	if err := conn.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE reminder_id = $1 AND reason_code = 'patrol_no_active_issue_dormant'),
		  count(*) FILTER (WHERE reminder_id = $2 AND reason_code = 'patrol_issue_progress_policy_migrated'),
		  count(*) FILTER (WHERE reminder_id = $3)
		FROM agent_reminder_lifecycle_event
	`, dormantReminderID, adaptiveReminderID, ordinaryReminderID).Scan(
		&dormantEvents, &adaptiveEvents, &ordinaryEvents,
	); err != nil {
		t.Fatal(err)
	}
	if dormantEvents != 1 || adaptiveEvents != 1 || ordinaryEvents != 0 {
		t.Fatalf("migration lifecycle dormant/adaptive/ordinary=%d/%d/%d, want 1/1/0",
			dormantEvents, adaptiveEvents, ordinaryEvents)
	}

	if _, err := conn.Exec(ctx, `
		UPDATE agent_reminder
		SET fire_at = now() + interval '1 hour',
		    fired_at = '2026-07-24T08:00:00Z',
		    managed_backoff_step = 3
		WHERE id = $1
	`, adaptiveReminderID); err != nil {
		t.Fatalf("prepare progressed patrol: %v", err)
	}
	var progressEventsBefore int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_reminder_lifecycle_event
		WHERE reminder_id = $1
		  AND reason_code = 'patrol_issue_progress_reset'
	`, adaptiveReminderID).Scan(&progressEventsBefore); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		UPDATE issue
		SET status = status,
		    assignee_type = assignee_type,
		    assignee_id = assignee_id,
		    project_id = project_id
		WHERE id = $1
	`, activeIssueID); err != nil {
		t.Fatalf("execute canonical no-op issue update: %v", err)
	}
	var unchangedSeconds int64
	var progressEventsAfter int
	if err := conn.QueryRow(ctx, `
		SELECT reminder.managed_backoff_step,
		       extract(epoch FROM (reminder.fire_at - now()))::bigint,
		       (
		         SELECT count(*)
		         FROM agent_reminder_lifecycle_event lifecycle
		         WHERE lifecycle.reminder_id = reminder.id
		           AND lifecycle.reason_code = 'patrol_issue_progress_reset'
		       )
		FROM agent_reminder reminder
		WHERE reminder.id = $1
	`, adaptiveReminderID).Scan(&adaptiveStep, &unchangedSeconds, &progressEventsAfter); err != nil {
		t.Fatal(err)
	}
	if adaptiveStep != 3 {
		t.Fatalf("no-op issue update backoff step=%d, want 3", adaptiveStep)
	}
	assertDelayNear("no-op issue update", unchangedSeconds, time.Hour)
	if progressEventsAfter != progressEventsBefore {
		t.Fatalf("no-op issue update progress events=%d, want unchanged %d", progressEventsAfter, progressEventsBefore)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO comment (issue_id, content) VALUES ($1, 'real issue progress')
	`, activeIssueID); err != nil {
		t.Fatalf("trigger issue-comment progress reset: %v", err)
	}
	var resetSeconds int64
	var lastFireAt time.Time
	if err := conn.QueryRow(ctx, `
		SELECT managed_backoff_step,
		       extract(epoch FROM (fire_at - now()))::bigint,
		       fired_at
		FROM agent_reminder WHERE id = $1
	`, adaptiveReminderID).Scan(&adaptiveStep, &resetSeconds, &lastFireAt); err != nil {
		t.Fatal(err)
	}
	if adaptiveStep != 0 {
		t.Fatalf("comment progress backoff step=%d, want 0", adaptiveStep)
	}
	if want := time.Date(2026, time.July, 24, 8, 0, 0, 0, time.UTC); !lastFireAt.Equal(want) {
		t.Fatalf("comment progress last fire=%s, want preserved %s", lastFireAt, want)
	}
	assertDelayNear("comment progress reset", resetSeconds, 15*time.Minute)

	const oldProjectID = "61000000-0000-4000-8000-000000000222"
	const newProjectID = "62000000-0000-4000-8000-000000000222"
	if _, err := conn.Exec(ctx, `
		UPDATE channel SET project_id = $2 WHERE id = $1
	`, "51000000-0000-4000-8000-000000000222", oldProjectID); err != nil {
		t.Fatalf("bind managed group to old project scope: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		UPDATE issue SET project_id = $2 WHERE id = $1
	`, activeIssueID, oldProjectID); err != nil {
		t.Fatalf("bind active issue to old project scope: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		DELETE FROM issue_source_message WHERE issue_id = $1
	`, activeIssueID); err != nil {
		t.Fatalf("remove source scope before project move: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		UPDATE issue SET project_id = $2 WHERE id = $1
	`, activeIssueID, newProjectID); err != nil {
		t.Fatalf("move active issue away from managed project scope: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT status FROM agent_reminder WHERE id = $1
	`, adaptiveReminderID).Scan(&adaptiveStatus); err != nil {
		t.Fatal(err)
	}
	if adaptiveStatus != "fired" {
		t.Fatalf("patrol after issue project move status=%s, want fired/dormant", adaptiveStatus)
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO issue_source_message (
		  issue_id, message_id, channel_id, workspace_id
		) VALUES ($1, $2, $3, $4)
	`, activeIssueID,
		"81000000-0000-4000-8000-000000000222",
		"51000000-0000-4000-8000-000000000222",
		"10000000-0000-4000-8000-000000000219",
	); err != nil {
		t.Fatalf("restore source scope after project move: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT status FROM agent_reminder WHERE id = $1
	`, adaptiveReminderID).Scan(&adaptiveStatus); err != nil {
		t.Fatal(err)
	}
	if adaptiveStatus != "scheduled" {
		t.Fatalf("patrol after restored source scope status=%s, want scheduled", adaptiveStatus)
	}

	if _, err := conn.Exec(ctx, `
		UPDATE issue SET status = 'done' WHERE id = $1
	`, activeIssueID); err != nil {
		t.Fatalf("terminalize active issue: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT status FROM agent_reminder WHERE id = $1
	`, adaptiveReminderID).Scan(&adaptiveStatus); err != nil {
		t.Fatal(err)
	}
	if adaptiveStatus != "fired" {
		t.Fatalf("patrol after final active issue status=%s, want fired/dormant", adaptiveStatus)
	}
}

func createPre221ManagedReminderSchema(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
) (*pgxpool.Conn, string) {
	t.Helper()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	schema := fmt.Sprintf("group_manager_reminder_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		conn.Release()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		conn.Release()
		t.Fatalf("set search path: %v", err)
	}
	if _, err := conn.Exec(ctx, pre221ManagedReminderSchemaSQL); err != nil {
		conn.Release()
		t.Fatalf("create pre-221 schema: %v", err)
	}
	return conn, schema
}

func seedPre221ManagedReminderRows(t *testing.T, ctx context.Context, conn *pgxpool.Conn, status string) {
	t.Helper()
	if _, err := conn.Exec(ctx, `
		INSERT INTO workspace (id) VALUES ('10000000-0000-4000-8000-000000000219');
		INSERT INTO "user" (id) VALUES ('20000000-0000-4000-8000-000000000219');
		INSERT INTO agent_runtime (id, workspace_id, metadata)
		VALUES (
		  '60000000-0000-4000-8000-000000000219',
		  '10000000-0000-4000-8000-000000000219',
		  '{"capabilities":["reminder_versioned_cache_v1"]}'::jsonb
		);
		INSERT INTO agent (
		  id, workspace_id, runtime_id, managed_role
		) VALUES (
		  '30000000-0000-4000-8000-000000000219',
		  '10000000-0000-4000-8000-000000000219',
		  '60000000-0000-4000-8000-000000000219',
		  'group_manager'
		);
		INSERT INTO channel (
		  id, workspace_id, kind, created_by, group_manager_agent_id
		) VALUES (
		  '50000000-0000-4000-8000-000000000219',
		  '10000000-0000-4000-8000-000000000219',
		  'group',
		  '20000000-0000-4000-8000-000000000219',
		  '30000000-0000-4000-8000-000000000219'
		);
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id
		) VALUES (
		  '50000000-0000-4000-8000-000000000219',
		  '10000000-0000-4000-8000-000000000219',
		  'agent',
		  '30000000-0000-4000-8000-000000000219'
		);
		INSERT INTO channel_message (
		  id, channel_id, workspace_id
		) VALUES (
		  '80000000-0000-4000-8000-000000000219',
		  '50000000-0000-4000-8000-000000000219',
		  '10000000-0000-4000-8000-000000000219'
		)
	`); err != nil {
		t.Fatalf("seed pre-219 rows: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO pending_handoff (
		  workspace_id, urgency, reason_code, target_actor_type,
		  target_actor_id, related_node_ids, channel_id, dedupe_key, status,
		  claimed_at
		) VALUES (
		  '10000000-0000-4000-8000-000000000219',
		  'fast',
		  'start_work',
		  'agent',
		  '40000000-0000-4000-8000-000000000219',
		  ARRAY['70000000-0000-4000-8000-000000000219'::uuid],
		  '50000000-0000-4000-8000-000000000219',
		  'start_work:migration-219',
		  $1,
		  CASE WHEN $1 = 'claimed' THEN now() ELSE NULL END
		)
	`, status); err != nil {
		t.Fatalf("seed pre-219 rows: %v", err)
	}
}

func readManagedReminderMigrationSQL(t *testing.T, name string) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test file")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "..", "..", "migrations", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}

const pre221ManagedReminderSchemaSQL = `
CREATE TABLE workspace (id UUID PRIMARY KEY);
CREATE TABLE "user" (id UUID PRIMARY KEY);
CREATE TABLE issue (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  project_id UUID,
  status TEXT NOT NULL DEFAULT 'todo',
  assignee_type TEXT,
  assignee_id UUID
);
CREATE TABLE agent_runtime (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE TABLE agent (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  runtime_id UUID,
  archived_at TIMESTAMPTZ,
  managed_role TEXT
);
CREATE TABLE channel (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  kind TEXT NOT NULL,
  created_by UUID NOT NULL,
  project_id UUID,
  group_manager_agent_id UUID,
  archived_at TIMESTAMPTZ
);
CREATE TABLE channel_member (
  channel_id UUID NOT NULL,
  workspace_id UUID NOT NULL,
  member_type TEXT NOT NULL,
  member_id UUID NOT NULL
);
CREATE TABLE channel_message (
  id UUID PRIMARY KEY,
  channel_id UUID NOT NULL,
  workspace_id UUID NOT NULL,
  deleted_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE issue_source_message (
  issue_id UUID NOT NULL,
  message_id UUID NOT NULL,
  channel_id UUID NOT NULL,
  workspace_id UUID NOT NULL
);
CREATE TABLE comment (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  issue_id UUID NOT NULL,
  content TEXT NOT NULL
);
CREATE TABLE agent_task_queue (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  issue_id UUID,
  status TEXT NOT NULL
);
CREATE TABLE agent_reminder (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  agent_id UUID NOT NULL,
  initiator_user_id UUID,
  title TEXT NOT NULL,
  anchor_channel_id UUID NOT NULL,
  anchor_message_id UUID,
  anchor_thread_root_message_id UUID,
  fire_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'scheduled',
  fired_task_id UUID,
  snooze_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  fired_at TIMESTAMPTZ,
  cadence TEXT,
  schedule_timezone TEXT,
  cadence_next_at TIMESTAMPTZ,
  current_occurrence_id UUID,
  terminal_reason TEXT,
  version BIGINT NOT NULL DEFAULT 1
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
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT agent_reminder_lifecycle_event_actor_type_check
    CHECK (actor_type IN ('agent', 'system'))
);
CREATE TABLE pending_handoff (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL,
  urgency TEXT NOT NULL,
  reason_code TEXT NOT NULL,
  target_actor_type TEXT NOT NULL,
  target_actor_id UUID NOT NULL,
  related_node_ids UUID[] NOT NULL DEFAULT '{}',
  channel_id UUID,
  issue_id UUID,
  dedupe_key TEXT NOT NULL,
  not_before TIMESTAMPTZ NOT NULL DEFAULT now(),
  status TEXT NOT NULL,
  claim_token UUID,
  claimed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`
