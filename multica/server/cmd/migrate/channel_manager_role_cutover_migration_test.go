package main

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestChannelManagerRoleCutoverMigrationRetiresLegacyState(t *testing.T) {
	pool := openAgentWakeCutoverDatabase(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()

	files, err := migrations.Files("up")
	if err != nil {
		t.Fatalf("list migrations: %v", err)
	}
	var beforeCutover []string
	var cutoverFile string
	for _, file := range files {
		if migrations.ExtractVersion(file) == "247_channel_manager_role_wake" {
			cutoverFile = file
			break
		}
		beforeCutover = append(beforeCutover, file)
	}
	if cutoverFile == "" {
		t.Fatal("247_channel_manager_role_wake migration not found")
	}
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           beforeCutover,
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 247: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES (
			'71000000-0000-4000-8000-000000000001',
			'Manager Cutover Owner',
			'manager-cutover-owner@example.test'
		);
		INSERT INTO workspace (id, name, slug, description, issue_prefix)
		VALUES (
			'71000000-0000-4000-8000-000000000002',
			'Manager Cutover Workspace',
			'manager-cutover-workspace',
			'Migration fixture',
			'MGR'
		);
		INSERT INTO member (workspace_id, user_id, role)
		VALUES (
			'71000000-0000-4000-8000-000000000002',
			'71000000-0000-4000-8000-000000000001',
			'owner'
		);
		INSERT INTO agent_runtime (
			id, workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at
		) VALUES (
			'71000000-0000-4000-8000-000000000003',
			'71000000-0000-4000-8000-000000000002',
			NULL,
			'Manager Cutover Runtime',
			'cloud',
			'cutover_test',
			'online',
			'Migration fixture',
			'{}'::jsonb,
			now()
		);
		INSERT INTO agent (
			id, workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id, managed_role
		) VALUES (
			'71000000-0000-4000-8000-000000000004',
			'71000000-0000-4000-8000-000000000002',
			'manager-cutover-agent',
			'',
			'cloud',
			'{}'::jsonb,
			'71000000-0000-4000-8000-000000000003',
			'workspace',
			1,
			'71000000-0000-4000-8000-000000000001',
			'group_manager'
		);
		INSERT INTO channel (
			id, workspace_id, name, created_by, kind, group_manager_agent_id
		) VALUES (
			'71000000-0000-4000-8000-000000000005',
			'71000000-0000-4000-8000-000000000002',
			'manager-cutover-channel',
			'71000000-0000-4000-8000-000000000001',
			'group',
			'71000000-0000-4000-8000-000000000004'
		);
		INSERT INTO channel (
			id, workspace_id, name, created_by, kind, group_manager_agent_id
		) VALUES (
			'71000000-0000-4000-8000-000000000007',
			'71000000-0000-4000-8000-000000000002',
			'manager-cutover-orphan-channel',
			'71000000-0000-4000-8000-000000000001',
			'group',
			'71000000-0000-4000-8000-000000000004'
		);
		INSERT INTO channel_member (
			channel_id, workspace_id, member_type, member_id, role
		) VALUES (
			'71000000-0000-4000-8000-000000000005',
			'71000000-0000-4000-8000-000000000002',
			'agent',
			'71000000-0000-4000-8000-000000000004',
			'member'
		);
		INSERT INTO agent_reminder (
			id, workspace_id, agent_id, initiator_user_id, title,
			anchor_channel_id, fire_at, origin_kind, managed_kind, origin_key
		) VALUES (
			'71000000-0000-4000-8000-000000000006',
			'71000000-0000-4000-8000-000000000002',
			'71000000-0000-4000-8000-000000000004',
			'71000000-0000-4000-8000-000000000001',
			'legacy managed patrol',
			'71000000-0000-4000-8000-000000000005',
			now() + interval '15 minutes',
			'group_manager_auto',
			'patrol',
			'patrol:manager-cutover'
		);
	`); err != nil {
		t.Fatalf("seed legacy manager state: %v", err)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{cutoverFile},
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
	}); err != nil {
		t.Fatalf("apply migration 247: %v", err)
	}

	var role string
	if err := pool.QueryRow(ctx, `
		SELECT role
		FROM channel_member
		WHERE channel_id = '71000000-0000-4000-8000-000000000005'
		  AND member_type = 'agent'
		  AND member_id = '71000000-0000-4000-8000-000000000004'
	`).Scan(&role); err != nil {
		t.Fatalf("read migrated channel role: %v", err)
	}
	if role != "manager" {
		t.Fatalf("migrated channel role = %q, want manager", role)
	}

	var orphanMemberships, orphanOnboardingRows, migrationWakeEvents int
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)
		   FROM channel_member
		   WHERE channel_id = '71000000-0000-4000-8000-000000000007'
		     AND member_type = 'agent'
		     AND member_id = '71000000-0000-4000-8000-000000000004'),
		  (SELECT count(*)
		   FROM channel_agent_onboarding
		   WHERE channel_id = '71000000-0000-4000-8000-000000000007'
		     AND agent_id = '71000000-0000-4000-8000-000000000004'),
		  (SELECT count(*)
		   FROM agent_inbox_event
		   WHERE agent_id = '71000000-0000-4000-8000-000000000004'
		     AND reason IN ('channel_onboarding', 'channel_role_changed'))
	`).Scan(&orphanMemberships, &orphanOnboardingRows, &migrationWakeEvents); err != nil {
		t.Fatalf("read orphan binding cutover state: %v", err)
	}
	if orphanMemberships != 0 || orphanOnboardingRows != 0 || migrationWakeEvents != 0 {
		t.Fatalf(
			"orphan binding manufactured state memberships=%d onboarding=%d wakeEvents=%d",
			orphanMemberships,
			orphanOnboardingRows,
			migrationWakeEvents,
		)
	}

	var managedRoleIsNull, legacyReminderRemoved bool
	var singletonColumnRemoved, ambientTableRemoved bool
	var reasonConstraint string
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT managed_role IS NULL FROM agent
		   WHERE id = '71000000-0000-4000-8000-000000000004'),
		  NOT EXISTS (
		    SELECT 1 FROM agent_reminder
		    WHERE id = '71000000-0000-4000-8000-000000000006'
		  ),
		  NOT EXISTS (
		    SELECT 1 FROM information_schema.columns
		    WHERE table_schema = 'public'
		      AND table_name = 'channel'
		      AND column_name = 'group_manager_agent_id'
		  ),
		  to_regclass('public.wendy_channel_ambient') IS NULL,
		  pg_get_constraintdef(oid)
		FROM pg_constraint
		WHERE conname = 'agent_inbox_event_reason_check'
	`).Scan(
		&managedRoleIsNull,
		&legacyReminderRemoved,
		&singletonColumnRemoved,
		&ambientTableRemoved,
		&reasonConstraint,
	); err != nil {
		t.Fatalf("read cutover state: %v", err)
	}
	if !managedRoleIsNull || !legacyReminderRemoved || !singletonColumnRemoved || !ambientTableRemoved {
		t.Fatalf(
			"cutover state managedRoleNull=%t reminderRemoved=%t singletonColumnRemoved=%t ambientTableRemoved=%t",
			managedRoleIsNull,
			legacyReminderRemoved,
			singletonColumnRemoved,
			ambientTableRemoved,
		)
	}
	if !containsAll(reasonConstraint, "channel_role_changed") {
		t.Fatalf("agent inbox reason constraint missing channel_role_changed: %s", reasonConstraint)
	}

	if _, err := pool.Exec(ctx, `
		SELECT ensure_system_general_channel(
			'71000000-0000-4000-8000-000000000002',
			'71000000-0000-4000-8000-000000000001'
		)
	`); err != nil {
		t.Fatalf("system general function still references retired column: %v", err)
	}
}

func containsAll(value string, needles ...string) bool {
	for _, needle := range needles {
		if !strings.Contains(value, needle) {
			return false
		}
	}
	return true
}
