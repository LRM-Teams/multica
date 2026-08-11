package main

import (
	"context"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestAgentReminderOrdinaryIdentityMigrationPreservesOnlyAgentReminders(t *testing.T) {
	pool := openAgentWakeCutoverDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	files, err := migrations.Files("up")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	var beforeCutover []string
	var upFile string
	for _, file := range files {
		if migrations.ExtractVersion(file) == "331_agent_reminder_ordinary_identity" {
			upFile = file
			break
		}
		beforeCutover = append(beforeCutover, file)
	}
	if upFile == "" {
		t.Fatal("331_agent_reminder_ordinary_identity migration not found")
	}
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           beforeCutover,
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 331: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES ('78000000-0000-4000-8000-000000000001', 'Reminder Owner', 'ordinary-reminder-owner@example.test');
		INSERT INTO workspace (id, name, slug, description, issue_prefix)
		VALUES (
			'78000000-0000-4000-8000-000000000002', 'Ordinary Reminder Workspace',
			'ordinary-reminder-workspace', 'Migration fixture', 'ORR'
		);
		INSERT INTO member (workspace_id, user_id, role)
		VALUES (
			'78000000-0000-4000-8000-000000000002',
			'78000000-0000-4000-8000-000000000001', 'owner'
		);
		INSERT INTO agent_runtime (
			id, workspace_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		) VALUES (
			'78000000-0000-4000-8000-000000000003',
			'78000000-0000-4000-8000-000000000002', 'Ordinary Reminder Runtime',
			'cloud', 'migration_test', 'online', 'Migration fixture', '{}'::jsonb, now()
		);
		INSERT INTO agent (
			id, workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, max_concurrent_tasks, owner_id
		) VALUES (
			'78000000-0000-4000-8000-000000000004',
			'78000000-0000-4000-8000-000000000002', 'ordinary-reminder-agent', '',
			'cloud', '{}'::jsonb, '78000000-0000-4000-8000-000000000003',
			1, '78000000-0000-4000-8000-000000000001'
		);
		INSERT INTO channel (id, workspace_id, name, created_by, kind)
		VALUES (
			'78000000-0000-4000-8000-000000000005',
			'78000000-0000-4000-8000-000000000002', 'ordinary-reminder-channel',
			'78000000-0000-4000-8000-000000000001', 'group'
		);
		INSERT INTO agent_reminder (
			id, workspace_id, agent_id, initiator_user_id, title,
			anchor_channel_id, fire_at, status,
			origin_kind, managed_kind, origin_key, managed_backoff_step
		) VALUES
		(
			'78000000-0000-4000-8000-000000000006',
			'78000000-0000-4000-8000-000000000002',
			'78000000-0000-4000-8000-000000000004',
			'78000000-0000-4000-8000-000000000001', 'ordinary Reminder',
			'78000000-0000-4000-8000-000000000005', now() + interval '1 hour',
			'scheduled', 'agent', NULL, NULL, 0
		),
		(
			'78000000-0000-4000-8000-000000000007',
			'78000000-0000-4000-8000-000000000002',
			'78000000-0000-4000-8000-000000000004',
			'78000000-0000-4000-8000-000000000001', 'obsolete managed identity',
			'78000000-0000-4000-8000-000000000005', now() + interval '1 hour',
			'scheduled', 'group_manager_auto', 'patrol', 'patrol:obsolete', 3
		);
	`); err != nil {
		t.Fatalf("seed Reminder identity fixture: %v", err)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{upFile},
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}); err != nil {
		t.Fatalf("apply migration 331: %v", err)
	}

	var ordinaryTitle, ordinaryStatus string
	var ordinaryCount, obsoleteCount, retiredColumnCount int
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT title FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000006'),
		  (SELECT status FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000006'),
		  (SELECT count(*) FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000006'),
		  (SELECT count(*) FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000007'),
		  (SELECT count(*) FROM information_schema.columns
		   WHERE table_schema = 'public' AND table_name = 'agent_reminder'
		     AND column_name IN ('origin_kind', 'managed_kind', 'origin_key', 'managed_backoff_step'))
	`).Scan(&ordinaryTitle, &ordinaryStatus, &ordinaryCount, &obsoleteCount, &retiredColumnCount); err != nil {
		t.Fatalf("read migrated Reminder identity: %v", err)
	}
	if ordinaryTitle != "ordinary Reminder" || ordinaryStatus != "scheduled" || ordinaryCount != 1 {
		t.Fatalf("ordinary Reminder changed title=%q status=%q count=%d", ordinaryTitle, ordinaryStatus, ordinaryCount)
	}
	if obsoleteCount != 0 || retiredColumnCount != 0 {
		t.Fatalf("obsolete Reminder identity remains row=%d columns=%d", obsoleteCount, retiredColumnCount)
	}

	downFiles, err := migrations.Files("down")
	if err != nil {
		t.Fatalf("list down migrations: %v", err)
	}
	var downFile string
	for _, file := range downFiles {
		if migrations.ExtractVersion(file) == "331_agent_reminder_ordinary_identity" {
			downFile = file
			break
		}
	}
	if downFile == "" {
		t.Fatal("331_agent_reminder_ordinary_identity down migration not found")
	}
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "down",
		Files:           []string{downFile},
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}); err != nil {
		t.Fatalf("rollback migration 331: %v", err)
	}
	var originKind string
	var managedKind, originKey *string
	var backoff int16
	if err := pool.QueryRow(ctx, `
		SELECT origin_kind, managed_kind, origin_key, managed_backoff_step
		FROM agent_reminder
		WHERE id = '78000000-0000-4000-8000-000000000006'
	`).Scan(&originKind, &managedKind, &originKey, &backoff); err != nil {
		t.Fatalf("read rolled-back ordinary Reminder: %v", err)
	}
	if originKind != "agent" || managedKind != nil || originKey != nil || backoff != 0 {
		t.Fatalf("rollback identity origin=%q managed=%v key=%v backoff=%d", originKind, managedKind, originKey, backoff)
	}
}
