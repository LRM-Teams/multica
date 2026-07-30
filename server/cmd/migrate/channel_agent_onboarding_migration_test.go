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

func TestChannelAgentOnboardingMigration207DoesNotFloodExistingRosterAndIsReversible(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}

	schema := fmt.Sprintf("channel_agent_onboarding_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "ROLLBACK")
		conn.Release()
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}

	if _, err := conn.Exec(ctx, channelAgentOnboardingLegacySchema); err != nil {
		t.Fatalf("create pre-onboarding schema: %v", err)
	}
	if _, err := conn.Exec(ctx, channelAgentOnboardingLegacyFixture); err != nil {
		t.Fatalf("seed pre-onboarding roster: %v", err)
	}

	upSQL, downSQL := readChannelAgentOnboardingMigrationSQL(t)
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 207 up: %v", err)
	}
	assertChannelAgentOnboardingExistingRosterNoFlood(t, ctx, conn, 0)
	assertChannelAgentOnboardingFutureInsert(t, ctx, conn,
		"40000000-0000-0000-0000-000000000002",
		"50000000-0000-0000-0000-000000000001",
		1,
	)

	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply migration 207 down: %v", err)
	}
	for _, column := range []string{"generation_id", "added_by", "join_source"} {
		assertMigrationColumnExists(t, ctx, conn, "channel_member", column, false)
	}
	assertMigrationColumnExists(t, ctx, conn, "channel_message", "membership_generation_id", false)
	var onboardingTableExists bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass(current_schema() || '.channel_agent_onboarding') IS NOT NULL`).Scan(&onboardingTableExists); err != nil {
		t.Fatalf("check onboarding table after down: %v", err)
	}
	if onboardingTableExists {
		t.Fatal("channel_agent_onboarding still exists after down")
	}

	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("reapply migration 207 up: %v", err)
	}
	// The first future-generation audit row intentionally survives down/up,
	// but neither existing membership is retroactively given an onboarding.
	assertChannelAgentOnboardingExistingRosterNoFlood(t, ctx, conn, 1)
	assertChannelAgentOnboardingFutureInsert(t, ctx, conn,
		"40000000-0000-0000-0000-000000000003",
		"50000000-0000-0000-0000-000000000001",
		2,
	)
}

func readChannelAgentOnboardingMigrationSQL(t *testing.T) (string, string) {
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
	return read("207_channel_agent_onboarding.up.sql"), read("207_channel_agent_onboarding.down.sql")
}

func assertChannelAgentOnboardingExistingRosterNoFlood(t *testing.T, ctx context.Context, conn *pgxpool.Conn, wantSystemRows int) {
	t.Helper()
	var missingGenerations, systemRows, onboardingRows int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM channel_member WHERE generation_id IS NULL`).Scan(&missingGenerations); err != nil {
		t.Fatalf("count missing membership generations: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT count(*)
		FROM channel_message
		WHERE author_type = 'system'
		  AND parts->0->>'event' = 'channel_member_added'`).Scan(&systemRows); err != nil {
		t.Fatalf("count membership system messages: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM channel_agent_onboarding`).Scan(&onboardingRows); err != nil {
		t.Fatalf("count onboarding rows: %v", err)
	}
	if missingGenerations != 0 || systemRows != wantSystemRows || onboardingRows != 0 {
		t.Fatalf("post-up existing roster = missing_generations:%d system_rows:%d onboarding_rows:%d, want 0/%d/0", missingGenerations, systemRows, onboardingRows, wantSystemRows)
	}
}

func assertChannelAgentOnboardingFutureInsert(t *testing.T, ctx context.Context, conn *pgxpool.Conn, channelID, agentID string, wantSystemRows int) {
	t.Helper()
	var generationID string
	if err := conn.QueryRow(ctx, `
		INSERT INTO channel_member (
		  channel_id, workspace_id, member_type, member_id, join_source, added_by
		)
		VALUES ($1, '10000000-0000-0000-0000-000000000001', 'agent', $2, 'manual',
		        '20000000-0000-0000-0000-000000000001')
		RETURNING generation_id::text`, channelID, agentID).Scan(&generationID); err != nil {
		t.Fatalf("insert future agent membership: %v", err)
	}

	var systemRows, onboardingRows int
	var systemGeneration, onboardingGeneration, onboardingSystemID, messageID string
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM channel_message
		WHERE author_type = 'system'
		  AND parts->0->>'event' = 'channel_member_added'`).Scan(&systemRows); err != nil {
		t.Fatalf("count future membership system rows: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT message.id::text,
		       message.membership_generation_id::text,
		       onboarding.system_message_id::text,
		       onboarding.membership_generation_id::text,
		       count(*) OVER ()
		FROM channel_message message
		JOIN channel_agent_onboarding onboarding
		  ON onboarding.system_message_id = message.id
		WHERE message.membership_generation_id = $1`, generationID).Scan(
		&messageID, &systemGeneration, &onboardingSystemID, &onboardingGeneration, &onboardingRows,
	); err != nil {
		t.Fatalf("load future generation ledger: %v", err)
	}
	if systemRows != wantSystemRows || onboardingRows != 1 || systemGeneration != generationID || onboardingGeneration != generationID || onboardingSystemID != messageID {
		t.Fatalf("future generation ledger = system_rows:%d onboarding_rows:%d message:%s/%s onboarding:%s/%s, want %d/1 and identical generation/message ids", systemRows, onboardingRows, messageID, systemGeneration, onboardingSystemID, onboardingGeneration, wantSystemRows)
	}
}

func assertMigrationColumnExists(t *testing.T, ctx context.Context, conn *pgxpool.Conn, tableName, columnName string, want bool) {
	t.Helper()
	var got bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = $1
		    AND column_name = $2
		)`, tableName, columnName).Scan(&got); err != nil {
		t.Fatalf("check column %s.%s: %v", tableName, columnName, err)
	}
	if got != want {
		t.Fatalf("column %s.%s exists=%v, want %v", tableName, columnName, got, want)
	}
}

const channelAgentOnboardingLegacySchema = `
CREATE TABLE "user" (
  id UUID PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL,
  display_name TEXT
);
CREATE TABLE workspace (
  id UUID PRIMARY KEY
);
CREATE TABLE agent (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  display_name TEXT,
  archived_at TIMESTAMPTZ
);
CREATE TABLE channel (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  kind TEXT NOT NULL,
  system_key TEXT,
  archived_at TIMESTAMPTZ
);
CREATE TABLE channel_member (
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  member_type TEXT NOT NULL,
  member_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, member_type, member_id)
);
CREATE TABLE channel_message (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  author_type TEXT NOT NULL,
  author_name TEXT NOT NULL,
  content TEXT NOT NULL,
  parts JSONB NOT NULL DEFAULT '[]'::jsonb,
  source TEXT NOT NULL
);
CREATE TABLE agent_inbox_event (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  reason TEXT NOT NULL,
  status TEXT NOT NULL DEFAULT 'pending',
  terminal_outcome TEXT,
  retryable BOOLEAN NOT NULL DEFAULT TRUE,
  terminal_at TIMESTAMPTZ,
  last_error TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT agent_inbox_event_reason_check CHECK (reason IN (
    'mention', 'dm', 'ambient', 'thread_reply', 'channel_message',
    'collaboration_turn', 'collaboration_manager_fallback'
  )),
  CONSTRAINT agent_inbox_event_terminal_outcome_check
    CHECK (terminal_outcome IN ('replied', 'no_reply', 'held', 'failed'))
);
CREATE TABLE agent_event_delivery (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  inbox_event_id UUID NOT NULL REFERENCES agent_inbox_event(id) ON DELETE CASCADE,
  status TEXT NOT NULL,
  last_error TEXT,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE chat_message (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  task_id UUID REFERENCES agent_inbox_event(id) ON DELETE CASCADE
);
`

const channelAgentOnboardingLegacyFixture = `
INSERT INTO workspace (id) VALUES ('10000000-0000-0000-0000-000000000001');
INSERT INTO "user" (id, name, email, display_name)
VALUES ('20000000-0000-0000-0000-000000000001', 'owner', 'owner@example.test', 'Owner');
INSERT INTO agent (id, workspace_id, name, display_name)
VALUES ('50000000-0000-0000-0000-000000000001',
        '10000000-0000-0000-0000-000000000001', 'fixture-agent', 'Fixture Agent');
INSERT INTO channel (id, workspace_id, kind, system_key)
VALUES
  ('40000000-0000-0000-0000-000000000001', '10000000-0000-0000-0000-000000000001', 'group', NULL),
  ('40000000-0000-0000-0000-000000000002', '10000000-0000-0000-0000-000000000001', 'group', NULL),
  ('40000000-0000-0000-0000-000000000003', '10000000-0000-0000-0000-000000000001', 'group', NULL);
INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
VALUES ('40000000-0000-0000-0000-000000000001',
        '10000000-0000-0000-0000-000000000001', 'agent',
        '50000000-0000-0000-0000-000000000001');
`
