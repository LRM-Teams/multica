package main

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestChannelMemberNotifyLevelMigration250BackfillsAndRejectsDefaultLiteral(t *testing.T) {
	pool := openAgentWakeCutoverDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	upFiles, err := migrations.Files("up")
	if err != nil {
		t.Fatalf("list up migrations: %v", err)
	}
	var before250 []string
	var up250 string
	for _, file := range upFiles {
		if migrations.ExtractVersion(file) == "250_channel_member_notify_level" {
			up250 = file
			break
		}
		before250 = append(before250, file)
	}
	if up250 == "" {
		t.Fatal("250_channel_member_notify_level up migration not found")
	}
	downFiles, err := migrations.Files("down")
	if err != nil {
		t.Fatalf("list down migrations: %v", err)
	}
	var down250 string
	for _, file := range downFiles {
		if migrations.ExtractVersion(file) == "250_channel_member_notify_level" {
			down250 = file
			break
		}
	}
	if down250 == "" {
		t.Fatal("250_channel_member_notify_level down migration not found")
	}

	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           before250,
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 250: %v", err)
	}

	if _, err := pool.Exec(ctx, channelMemberNotifyLevelLegacyFixture); err != nil {
		t.Fatalf("seed pre-250 notify-level fixture: %v", err)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{up250},
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migration 250 up: %v", err)
	}

	assertNotifyLevel(t, ctx, pool, "79000000-0000-4000-8000-000000000004", "79000000-0000-4000-8000-000000000001", "mentions")
	assertNotifyLevel(t, ctx, pool, "79000000-0000-4000-8000-000000000004", "79000000-0000-4000-8000-000000000005", "")

	if _, err := pool.Exec(ctx, `
		UPDATE channel_member
		SET notify_level = 'default'
		WHERE channel_id = '79000000-0000-4000-8000-000000000004'
		  AND member_id = '79000000-0000-4000-8000-000000000005'`); err == nil {
		t.Fatal("CHECK should reject literal 'default' in notify_level")
	}
	if _, err := pool.Exec(ctx, `
		UPDATE channel_member
		SET notify_level = 'loud'
		WHERE channel_id = '79000000-0000-4000-8000-000000000004'
		  AND member_id = '79000000-0000-4000-8000-000000000005'`); err == nil {
		t.Fatal("CHECK should reject invalid notify_level")
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "down",
		Files:           []string{down250},
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migration 250 down: %v", err)
	}
	var colExists bool
	if err := pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.columns
		  WHERE table_name = 'channel_member' AND column_name = 'notify_level'
		)`).Scan(&colExists); err != nil {
		t.Fatalf("check notify_level column after down: %v", err)
	}
	if colExists {
		t.Fatal("notify_level column still present after down")
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{up250},
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("re-apply migration 250 up: %v", err)
	}
	// muted_at still set on owner after down/up → backfill again
	assertNotifyLevel(t, ctx, pool, "79000000-0000-4000-8000-000000000004", "79000000-0000-4000-8000-000000000001", "mentions")
}

func assertNotifyLevel(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID, memberID, want string) {
	t.Helper()
	var level pgtype.Text
	if err := pool.QueryRow(ctx, `
		SELECT notify_level
		FROM channel_member
		WHERE channel_id = $1 AND member_type = 'user' AND member_id = $2`,
		channelID, memberID).Scan(&level); err != nil {
		t.Fatalf("load notify_level: %v", err)
	}
	got := ""
	if level.Valid {
		got = level.String
	}
	if got != want {
		t.Fatalf("notify_level = %q, want %q", got, want)
	}
}

const channelMemberNotifyLevelLegacyFixture = `
INSERT INTO "user" (id, name, email)
VALUES
  ('79000000-0000-4000-8000-000000000001', 'Notify Level Owner', 'notify-level-owner@example.test'),
  ('79000000-0000-4000-8000-000000000005', 'Notify Level Member', 'notify-level-member@example.test');
INSERT INTO workspace (id, name, slug, description, issue_prefix)
VALUES (
  '79000000-0000-4000-8000-000000000002',
  'Notify Level Workspace',
  'notify-level-workspace',
  'Migration fixture',
  'NL'
);
INSERT INTO member (workspace_id, user_id, role)
VALUES
  ('79000000-0000-4000-8000-000000000002', '79000000-0000-4000-8000-000000000001', 'owner'),
  ('79000000-0000-4000-8000-000000000002', '79000000-0000-4000-8000-000000000005', 'member');
INSERT INTO channel (id, workspace_id, name, kind, created_by)
VALUES (
  '79000000-0000-4000-8000-000000000004',
  '79000000-0000-4000-8000-000000000002',
  'notify-level-channel',
  'group',
  '79000000-0000-4000-8000-000000000001'
);
-- Channel create may already insert the owner membership via trigger; mute that row.
UPDATE channel_member
SET muted_at = now(),
    role = 'owner',
    added_by_type = 'user',
    added_by_id = '79000000-0000-4000-8000-000000000001'
WHERE channel_id = '79000000-0000-4000-8000-000000000004'
  AND member_type = 'user'
  AND member_id = '79000000-0000-4000-8000-000000000001';
INSERT INTO channel_member (
  channel_id, workspace_id, member_type, member_id, role, muted_at,
  added_by_type, added_by_id, join_source
)
SELECT
  '79000000-0000-4000-8000-000000000004',
  '79000000-0000-4000-8000-000000000002',
  'user',
  '79000000-0000-4000-8000-000000000001',
  'owner',
  now(),
  'user',
  '79000000-0000-4000-8000-000000000001',
  'manual'
WHERE NOT EXISTS (
  SELECT 1 FROM channel_member
  WHERE channel_id = '79000000-0000-4000-8000-000000000004'
    AND member_type = 'user'
    AND member_id = '79000000-0000-4000-8000-000000000001'
);
INSERT INTO channel_member (
  channel_id, workspace_id, member_type, member_id, role, muted_at,
  added_by_type, added_by_id, join_source
) VALUES (
  '79000000-0000-4000-8000-000000000004',
  '79000000-0000-4000-8000-000000000002',
  'user',
  '79000000-0000-4000-8000-000000000005',
  'member',
  NULL,
  'user',
  '79000000-0000-4000-8000-000000000001',
  'manual'
);
`
