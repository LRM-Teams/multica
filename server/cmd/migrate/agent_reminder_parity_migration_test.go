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

func TestAgentReminderParityMigration208PreservesFourV1StatesAcrossDownUp(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_reminder_parity_migration_test_%d", time.Now().UnixNano())
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

	if _, err := conn.Exec(ctx, `
		CREATE TABLE workspace (id UUID PRIMARY KEY);
		CREATE TABLE agent (id UUID PRIMARY KEY, workspace_id UUID NOT NULL REFERENCES workspace(id));
		CREATE TABLE channel (id UUID PRIMARY KEY, workspace_id UUID NOT NULL REFERENCES workspace(id));
		CREATE TABLE channel_message (id UUID PRIMARY KEY);
		CREATE TABLE agent_task_queue (id UUID PRIMARY KEY);
		CREATE TABLE agent_reminder (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
		  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
		  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
		  anchor_channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
		  anchor_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
		  anchor_thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
		  fire_at TIMESTAMPTZ NOT NULL,
		  status TEXT NOT NULL DEFAULT 'scheduled' CHECK (status IN ('scheduled', 'firing', 'fired', 'cancelled')),
		  fired_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
		  snooze_count INTEGER NOT NULL DEFAULT 0,
		  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  fired_at TIMESTAMPTZ
		);
		CREATE INDEX idx_agent_reminder_due ON agent_reminder(fire_at) WHERE status = 'scheduled';
		CREATE INDEX idx_agent_reminder_agent_active ON agent_reminder(workspace_id, agent_id) WHERE status = 'scheduled';
	`); err != nil {
		t.Fatalf("create exact V1 reminder prerequisites: %v", err)
	}

	const (
		workspaceID = "00000000-0000-0000-0000-000000000208"
		agentID     = "00000000-0000-0000-0001-000000000208"
		channelID   = "00000000-0000-0000-0002-000000000208"
		rootID      = "00000000-0000-0000-0003-000000000208"
		replyID     = "00000000-0000-0000-0004-000000000208"
		taskID      = "00000000-0000-0000-0005-000000000208"
	)
	if _, err := conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1)`, workspaceID); err != nil {
		t.Fatalf("seed V1 dependencies: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO agent (id, workspace_id) VALUES ($1, $2)`, agentID, workspaceID); err != nil {
		t.Fatalf("seed V1 agent: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO channel (id, workspace_id) VALUES ($1, $2)`, channelID, workspaceID); err != nil {
		t.Fatalf("seed V1 channel: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO channel_message (id) VALUES ($1), ($2)`, rootID, replyID); err != nil {
		t.Fatalf("seed V1 messages: %v", err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO agent_task_queue (id) VALUES ($1)`, taskID); err != nil {
		t.Fatalf("seed V1 task: %v", err)
	}

	type v1Row struct {
		id, title, status string
		fireAt, createdAt time.Time
		updatedAt         time.Time
		firedAt           *time.Time
		firedTaskID       *string
		snoozeCount       int
	}
	base := time.Date(2026, 7, 22, 1, 2, 3, 456000000, time.UTC)
	rows := []v1Row{
		{id: "00000000-0000-0010-0000-000000000208", title: "scheduled", status: "scheduled", fireAt: base.Add(time.Hour), createdAt: base, updatedAt: base.Add(time.Minute), snoozeCount: 0},
		{id: "00000000-0000-0011-0000-000000000208", title: "firing without task", status: "firing", fireAt: base.Add(2 * time.Hour), createdAt: base.Add(time.Minute), updatedAt: base.Add(2 * time.Minute), snoozeCount: 1},
		{id: "00000000-0000-0012-0000-000000000208", title: "firing with task", status: "firing", fireAt: base.Add(3 * time.Hour), createdAt: base.Add(2 * time.Minute), updatedAt: base.Add(3 * time.Minute), firedTaskID: stringPointer(taskID), snoozeCount: 2},
		{id: "00000000-0000-0013-0000-000000000208", title: "fired", status: "fired", fireAt: base.Add(4 * time.Hour), createdAt: base.Add(3 * time.Minute), updatedAt: base.Add(4 * time.Minute), firedAt: timePointer(base.Add(5 * time.Minute)), firedTaskID: stringPointer(taskID), snoozeCount: 3},
		{id: "00000000-0000-0014-0000-000000000208", title: "cancelled", status: "cancelled", fireAt: base.Add(5 * time.Hour), createdAt: base.Add(4 * time.Minute), updatedAt: base.Add(5 * time.Minute), firedAt: timePointer(base.Add(6 * time.Minute)), snoozeCount: 4},
	}
	for _, row := range rows {
		if _, err := conn.Exec(ctx, `
			INSERT INTO agent_reminder (
			  id, workspace_id, agent_id, title, anchor_channel_id,
			  anchor_message_id, anchor_thread_root_message_id, fire_at,
			  status, fired_task_id, snooze_count, created_at, updated_at, fired_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
			row.id, workspaceID, agentID, row.title, channelID, replyID, rootID,
			row.fireAt, row.status, row.firedTaskID, row.snoozeCount, row.createdAt, row.updatedAt, row.firedAt); err != nil {
			t.Fatalf("seed V1 reminder %s: %v", row.status, err)
		}
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "208_agent_reminder_parity.up.sql"))
	if err != nil {
		t.Fatalf("read migration 208 up: %v", err)
	}
	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "208_agent_reminder_parity.down.sql"))
	if err != nil {
		t.Fatalf("read migration 208 down: %v", err)
	}
	assertRowsPreserved := func(label string) {
		t.Helper()
		for _, want := range rows {
			var gotTitle, gotStatus, gotChannelID, gotMessageID, gotRootID string
			var gotFireAt, gotCreatedAt, gotUpdatedAt time.Time
			var gotFiredAt *time.Time
			var gotFiredTaskID *string
			var gotSnoozeCount int
			if err := conn.QueryRow(ctx, `
				SELECT title, status, anchor_channel_id::text, anchor_message_id::text,
				       anchor_thread_root_message_id::text, fire_at, fired_task_id::text,
				       snooze_count, created_at, updated_at, fired_at
				FROM agent_reminder WHERE id = $1`, want.id).Scan(
				&gotTitle, &gotStatus, &gotChannelID, &gotMessageID, &gotRootID,
				&gotFireAt, &gotFiredTaskID, &gotSnoozeCount, &gotCreatedAt, &gotUpdatedAt, &gotFiredAt); err != nil {
				t.Fatalf("%s read preserved reminder %s: %v", label, want.id, err)
			}
			if gotTitle != want.title || gotStatus != want.status || gotChannelID != channelID || gotMessageID != replyID || gotRootID != rootID ||
				!gotFireAt.Equal(want.fireAt) || gotSnoozeCount != want.snoozeCount || !gotCreatedAt.Equal(want.createdAt) || !gotUpdatedAt.Equal(want.updatedAt) ||
				!equalOptionalTime(gotFiredAt, want.firedAt) || !equalOptionalString(gotFiredTaskID, want.firedTaskID) {
				t.Fatalf("%s reminder %s changed: title/status=%q/%q channel/message/root=%s/%s/%s fire=%s task=%v snooze=%d created/updated/fired=%s/%s/%v",
					label, want.id, gotTitle, gotStatus, gotChannelID, gotMessageID, gotRootID, gotFireAt, gotFiredTaskID, gotSnoozeCount, gotCreatedAt, gotUpdatedAt, gotFiredAt)
			}
		}
	}

	applyUp := func(label string) {
		t.Helper()
		if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
			t.Fatalf("%s apply migration 208 up: %v", label, err)
		}
		assertRowsPreserved(label)
		var definitions, scheduledEvents, occurrences, firedEvents, claimedCurrent int
		if err := conn.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM agent_reminder),
			  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE event_type = 'scheduled'),
			  (SELECT count(*) FROM agent_reminder_occurrence),
			  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE event_type = 'fired'),
			  (SELECT count(*) FROM agent_reminder WHERE current_occurrence_id IS NOT NULL)
		`).Scan(&definitions, &scheduledEvents, &occurrences, &firedEvents, &claimedCurrent); err != nil {
			t.Fatalf("%s read V2 ledger counts: %v", label, err)
		}
		if definitions != 5 || scheduledEvents != 5 || occurrences != 3 || firedEvents != 2 || claimedCurrent != 1 {
			t.Fatalf("%s V2 ledger counts definitions/scheduled/occurrences/fired/current=%d/%d/%d/%d/%d", label, definitions, scheduledEvents, occurrences, firedEvents, claimedCurrent)
		}
		var cancelledOccurrences, cancelledFiredEvents, cancelledCurrent int
		if err := conn.QueryRow(ctx, `
			SELECT
			  (SELECT count(*) FROM agent_reminder_occurrence WHERE reminder_id = $1),
			  (SELECT count(*) FROM agent_reminder_lifecycle_event WHERE reminder_id = $1 AND event_type = 'fired'),
			  (SELECT count(*) FROM agent_reminder WHERE id = $1 AND current_occurrence_id IS NOT NULL)
		`, rows[4].id).Scan(&cancelledOccurrences, &cancelledFiredEvents, &cancelledCurrent); err != nil {
			t.Fatalf("%s read cancelled V2 ledger state: %v", label, err)
		}
		if cancelledOccurrences != 0 || cancelledFiredEvents != 0 || cancelledCurrent != 0 {
			t.Fatalf("%s cancelled reminder generated occurrence/fired/current=%d/%d/%d, want 0/0/0", label, cancelledOccurrences, cancelledFiredEvents, cancelledCurrent)
		}
	}

	applyUp("first")
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply migration 208 down: %v", err)
	}
	assertRowsPreserved("down")
	var occurrenceTable, lifecycleTable *string
	if err := conn.QueryRow(ctx, `SELECT to_regclass('agent_reminder_occurrence')::text, to_regclass('agent_reminder_lifecycle_event')::text`).Scan(&occurrenceTable, &lifecycleTable); err != nil {
		t.Fatalf("read down table state: %v", err)
	}
	if occurrenceTable != nil || lifecycleTable != nil {
		t.Fatalf("down left V2 ledger tables occurrence=%v lifecycle=%v", occurrenceTable, lifecycleTable)
	}
	applyUp("second")
}

func stringPointer(value string) *string { return &value }

func timePointer(value time.Time) *time.Time { return &value }

func equalOptionalString(left, right *string) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && *left == *right)
}

func equalOptionalTime(left, right *time.Time) bool {
	return (left == nil && right == nil) || (left != nil && right != nil && left.Equal(*right))
}
