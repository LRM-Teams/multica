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
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestChannelMessageAttachmentReferencesMigration224BackfillsReusableResources(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	schema := fmt.Sprintf("channel_message_attachment_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		conn.Release()
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "ROLLBACK")
		conn.Release()
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	if _, err := conn.Exec(ctx, pre224ChannelMessageAttachmentSchemaSQL); err != nil {
		t.Fatalf("create pre-224 schema: %v", err)
	}
	if _, err := conn.Exec(ctx, pre224ChannelMessageAttachmentFixtureSQL); err != nil {
		t.Fatalf("seed pre-224 attachment fixture: %v", err)
	}

	upSQL, downSQL := readChannelMessageAttachmentMigrationSQL(t)
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 224 up: %v", err)
	}
	assertMigration224ReusableReferences(t, ctx, conn)

	var singularColumnExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = 'attachment'
		    AND column_name = 'channel_message_id'
		)`).Scan(&singularColumnExists); err != nil {
		t.Fatalf("inspect legacy attachment column: %v", err)
	}
	if singularColumnExists {
		t.Fatal("attachment.channel_message_id still exists after migration 224")
	}

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply migration 224 down: %v", err)
	}
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("reapply migration 224 after rollback: %v", err)
	}
	assertMigration224ReusableReferences(t, ctx, conn)

	if _, err := conn.Exec(ctx, `DELETE FROM attachment WHERE id = '40000000-0000-4000-8000-000000000224'`); err != nil {
		t.Fatalf("delete referenced attachment resource: %v", err)
	}
	var referencesAfterDelete int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM channel_message_attachment`).Scan(&referencesAfterDelete); err != nil {
		t.Fatalf("count references after resource deletion: %v", err)
	}
	if referencesAfterDelete != 0 {
		t.Fatalf("references after resource deletion=%d, want 0", referencesAfterDelete)
	}
}

func assertMigration224ReusableReferences(t *testing.T, ctx context.Context, conn *pgxpool.Conn) {
	t.Helper()
	var references, messages int
	if err := conn.QueryRow(ctx, `
		SELECT count(*), count(DISTINCT channel_message_id)
		FROM channel_message_attachment
		WHERE attachment_id = '40000000-0000-4000-8000-000000000224'
	`).Scan(&references, &messages); err != nil {
		t.Fatalf("load reusable attachment references: %v", err)
	}
	if references != 2 || messages != 2 {
		t.Fatalf("reusable attachment references/messages=%d/%d, want 2/2", references, messages)
	}

	var inventedReferences int
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message_attachment
		WHERE attachment_id <> '40000000-0000-4000-8000-000000000224'
	`).Scan(&inventedReferences); err != nil {
		t.Fatalf("count invented attachment references: %v", err)
	}
	if inventedReferences != 0 {
		t.Fatalf("migration invented %d attachment reference(s)", inventedReferences)
	}
}

func readChannelMessageAttachmentMigrationSQL(t *testing.T) (string, string) {
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
	return read("224_channel_message_attachment_references.up.sql"), read("224_channel_message_attachment_references.down.sql")
}

const pre224ChannelMessageAttachmentSchemaSQL = `
CREATE TABLE channel_message (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  parts JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE attachment (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL,
  channel_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_attachment_channel_message
  ON attachment(channel_message_id)
  WHERE channel_message_id IS NOT NULL;
`

const pre224ChannelMessageAttachmentFixtureSQL = `
INSERT INTO channel_message (id, workspace_id, parts, created_at) VALUES
  (
    '20000000-0000-4000-8000-000000000224',
    '10000000-0000-4000-8000-000000000224',
    '[{"type":"attachment","attachment_id":"40000000-0000-4000-8000-000000000224"}]'::jsonb,
    '2026-07-24T09:08:00Z'
  ),
  (
    '30000000-0000-4000-8000-000000000224',
    '10000000-0000-4000-8000-000000000224',
    '[
      {"type":"voice","attachment_id":"40000000-0000-4000-8000-000000000224"},
      {"type":"attachment","attachment_id":"not-a-uuid"},
      {"type":"attachment","attachment_id":"50000000-0000-4000-8000-000000000224"}
    ]'::jsonb,
    '2026-07-24T09:17:00Z'
  );

INSERT INTO attachment (id, workspace_id, channel_message_id, created_at) VALUES (
  '40000000-0000-4000-8000-000000000224',
  '10000000-0000-4000-8000-000000000224',
  '20000000-0000-4000-8000-000000000224',
  '2026-07-24T09:07:00Z'
);
`
