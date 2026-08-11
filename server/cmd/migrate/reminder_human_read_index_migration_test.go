package main

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestReminderHumanReadIndexMigrationSupportsAgentHistoryCursor(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("reminder_human_read_index_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE") })
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent_reminder (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			status TEXT NOT NULL,
			fire_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE agent_reminder_occurrence (
			id UUID PRIMARY KEY,
			workspace_id UUID NOT NULL,
			agent_id UUID NOT NULL,
			status TEXT NOT NULL,
			fired_at TIMESTAMPTZ
		)`); err != nil {
		t.Fatal(err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "334_reminder_human_read_index.up.sql"))

	var indexDef string
	if err := conn.QueryRow(ctx, `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname = 'idx_agent_reminder_occurrence_human_history'`).Scan(&indexDef); err != nil {
		t.Fatalf("load human history index: %v", err)
	}
	for _, required := range []string{"workspace_id", "agent_id", "fired_at DESC", "id DESC", "status = 'fired'"} {
		if !strings.Contains(indexDef, required) {
			t.Fatalf("index definition %q missing %q", indexDef, required)
		}
	}
	if err := conn.QueryRow(ctx, `
		SELECT indexdef
		FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname = 'idx_agent_reminder_human_upcoming'`).Scan(&indexDef); err != nil {
		t.Fatalf("load human upcoming index: %v", err)
	}
	for _, required := range []string{"workspace_id", "agent_id", "fire_at", "id", "status = ANY", "'scheduled'", "'firing'"} {
		if !strings.Contains(indexDef, required) {
			t.Fatalf("upcoming index definition %q missing %q", indexDef, required)
		}
	}

	applyMigrationFile(t, ctx, conn, filepath.Join(migrationsDir, "334_reminder_human_read_index.down.sql"))
	var remaining int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM pg_indexes
		WHERE schemaname = current_schema()
		  AND indexname IN (
			'idx_agent_reminder_occurrence_human_history',
			'idx_agent_reminder_human_upcoming'
		  )`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("down migration left %d human Reminder indexes behind", remaining)
	}
}
