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
CREATE TABLE issue (id UUID PRIMARY KEY);
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
