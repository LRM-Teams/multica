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

func TestChannelVoiceSynthesisMigration214QueuesOnlyPendingAgentVoice(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("channel_voice_synthesis_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE channel (
		  id UUID PRIMARY KEY,
		  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE
		);
		CREATE TABLE channel_message (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
		  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
		  author_type TEXT NOT NULL,
		  parts JSONB NOT NULL DEFAULT '[]'::jsonb
		);
		INSERT INTO workspace (id) VALUES ('10000000-0000-0000-0000-000000000001');
		INSERT INTO channel (id, workspace_id)
		VALUES (
		  '20000000-0000-0000-0000-000000000001',
		  '10000000-0000-0000-0000-000000000001'
		);
	`); err != nil {
		t.Fatalf("create pre-214 schema: %v", err)
	}

	upSQL, downSQL := readChannelVoiceSynthesisMigrationSQL(t)
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 214 up: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO channel_message (workspace_id, channel_id, author_type, parts)
		VALUES
		  (
		    '10000000-0000-0000-0000-000000000001',
		    '20000000-0000-0000-0000-000000000001',
		    'agent',
		    '[{"type":"text","text":"hello"},{"type":"voice","synthesis_status":"pending"}]'
		  ),
		  (
		    '10000000-0000-0000-0000-000000000001',
		    '20000000-0000-0000-0000-000000000001',
		    'agent',
		    '[{"type":"text","text":"done"},{"type":"voice","synthesis_status":"completed"}]'
		  ),
		  (
		    '10000000-0000-0000-0000-000000000001',
		    '20000000-0000-0000-0000-000000000001',
		    'user',
		    '[{"type":"voice","synthesis_status":"pending"}]'
		  );
	`); err != nil {
		t.Fatalf("insert post-214 messages: %v", err)
	}
	var jobs int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM channel_voice_synthesis`).Scan(&jobs); err != nil {
		t.Fatalf("count synthesis jobs: %v", err)
	}
	if jobs != 1 {
		t.Fatalf("synthesis jobs = %d, want 1", jobs)
	}

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply migration 214 down: %v", err)
	}
	var queueExists bool
	if err := conn.QueryRow(ctx, `
		SELECT to_regclass(current_schema() || '.channel_voice_synthesis') IS NOT NULL
	`).Scan(&queueExists); err != nil {
		t.Fatalf("check synthesis queue after down: %v", err)
	}
	if queueExists {
		t.Fatal("channel_voice_synthesis still exists after down")
	}
}

func readChannelVoiceSynthesisMigrationSQL(t *testing.T) (string, string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	read := func(name string) string {
		body, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(body)
	}
	return read("214_channel_voice_synthesis.up.sql"), read("214_channel_voice_synthesis.down.sql")
}
