package main

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestGroupManagerOpenLoopPatrolMigrationRemovesIssueMachineAndRearmsDormant(t *testing.T) {
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
		if migrations.ExtractVersion(file) == "228_group_manager_open_loop_patrol" {
			cutoverFile = file
			break
		}
		beforeCutover = append(beforeCutover, file)
	}
	if cutoverFile == "" {
		t.Fatal("228_group_manager_open_loop_patrol migration not found")
	}
	downFiles, err := migrations.Files("down")
	if err != nil {
		t.Fatalf("list down migrations: %v", err)
	}
	var cutoverDownFile string
	for _, file := range downFiles {
		if migrations.ExtractVersion(file) == "228_group_manager_open_loop_patrol" {
			cutoverDownFile = file
			break
		}
	}
	if cutoverDownFile == "" {
		t.Fatal("228_group_manager_open_loop_patrol down migration not found")
	}

	lockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           beforeCutover,
		AdvisoryLockKey: lockKey,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 228: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES (
		  '78000000-0000-4000-8000-000000000001',
		  'Open Loop Owner',
		  'open-loop-owner@example.test'
		);
		INSERT INTO workspace (id, name, slug, description, issue_prefix)
		VALUES (
		  '78000000-0000-4000-8000-000000000002',
		  'Open Loop Workspace',
		  'open-loop-workspace',
		  'Migration fixture',
		  'LOOP'
		);
		INSERT INTO member (workspace_id, user_id, role)
		VALUES (
		  '78000000-0000-4000-8000-000000000002',
		  '78000000-0000-4000-8000-000000000001',
		  'owner'
		);
		INSERT INTO agent_runtime (
		  id, workspace_id, name, runtime_mode, provider, status,
		  device_info, metadata, last_seen_at
		) VALUES (
		  '78000000-0000-4000-8000-000000000003',
		  '78000000-0000-4000-8000-000000000002',
		  'Open Loop Runtime',
		  'cloud',
		  'migration_test',
		  'online',
		  'Migration fixture',
		  '{}'::jsonb,
		  now()
		);
		INSERT INTO agent (
		  id, workspace_id, name, display_name, description, runtime_mode,
		  runtime_config, runtime_id, visibility, max_concurrent_tasks,
		  owner_id, managed_role
		) VALUES (
		  '78000000-0000-4000-8000-000000000004',
		  '78000000-0000-4000-8000-000000000002',
		  'open_loop_manager',
		  'Open Loop Manager',
		  '',
		  'cloud',
		  '{}'::jsonb,
		  '78000000-0000-4000-8000-000000000003',
		  'workspace',
		  1,
		  '78000000-0000-4000-8000-000000000001',
		  'group_manager'
		);
		INSERT INTO channel (
		  id, workspace_id, name, kind, created_by, group_manager_agent_id
		) VALUES
		(
		  '78000000-0000-4000-8000-000000000005',
		  '78000000-0000-4000-8000-000000000002',
		  'open-loop-scheduled',
		  'group',
		  '78000000-0000-4000-8000-000000000001',
		  '78000000-0000-4000-8000-000000000004'
		),
		(
		  '78000000-0000-4000-8000-000000000008',
		  '78000000-0000-4000-8000-000000000002',
		  'open-loop-dormant',
		  'group',
		  '78000000-0000-4000-8000-000000000001',
		  '78000000-0000-4000-8000-000000000004'
		);
		INSERT INTO agent_reminder (
		  id, workspace_id, agent_id, initiator_user_id, title,
		  anchor_channel_id, fire_at, status, origin_kind, managed_kind,
		  origin_key
		) VALUES
		(
		  '78000000-0000-4000-8000-000000000006',
		  '78000000-0000-4000-8000-000000000002',
		  '78000000-0000-4000-8000-000000000004',
		  '78000000-0000-4000-8000-000000000001',
		  'scheduled patrol',
		  '78000000-0000-4000-8000-000000000005',
		  now() + interval '47 minutes',
		  'scheduled',
		  'group_manager_auto',
		  'patrol',
		  'patrol:scheduled'
		),
		(
		  '78000000-0000-4000-8000-000000000007',
		  '78000000-0000-4000-8000-000000000002',
		  '78000000-0000-4000-8000-000000000004',
		  '78000000-0000-4000-8000-000000000001',
		  'dormant patrol',
		  '78000000-0000-4000-8000-000000000008',
		  now() - interval '1 hour',
		  'fired',
		  'group_manager_auto',
		  'patrol',
		  'patrol:dormant'
		);
	`); err != nil {
		t.Fatalf("seed open-loop migration fixture: %v", err)
	}

	var scheduledBefore time.Time
	if err := pool.QueryRow(ctx, `
		SELECT fire_at
		FROM agent_reminder
		WHERE id = '78000000-0000-4000-8000-000000000006'
	`).Scan(&scheduledBefore); err != nil {
		t.Fatal(err)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{cutoverFile},
		AdvisoryLockKey: lockKey,
	}); err != nil {
		t.Fatalf("apply migration 228: %v", err)
	}

	var triggerCount, functionCount, markerCount, indexCount int
	if err := pool.QueryRow(ctx, `
		SELECT
		  (
		    SELECT count(*)
		    FROM pg_trigger
		    WHERE NOT tgisinternal
		      AND tgname IN (
		        'issue_group_manager_patrol_progress_trigger',
		        'comment_group_manager_patrol_progress_trigger',
		        'task_group_manager_patrol_progress_trigger',
		        'issue_source_group_manager_patrol_scope_trigger',
		        'channel_project_group_manager_patrol_scope_trigger'
		      )
		  ),
		  (
		    SELECT count(*)
		    FROM pg_proc
		    WHERE proname IN (
		      'group_manager_patrol_channel_has_active_issue',
		      'refresh_group_manager_patrol_for_channel',
		      'refresh_group_manager_patrol_for_issue',
		      'refresh_group_manager_patrol_for_issue_row',
		      'refresh_group_manager_patrol_from_issue_child',
		      'refresh_group_manager_patrol_from_source',
		      'refresh_group_manager_patrol_from_channel_project'
		    )
		  ),
		  (
		    SELECT count(*)
		    FROM agent_reminder_lifecycle_event
		    WHERE reason_code = 'patrol_open_loop_policy_migrated'
		  ),
		  (
		    SELECT count(*)
		    FROM pg_indexes
		    WHERE indexname IN (
		      'idx_channel_message_agent_outbound_recent',
		      'idx_agent_reminder_group_manager_dormant_patrol'
		    )
		  )
	`).Scan(&triggerCount, &functionCount, &markerCount, &indexCount); err != nil {
		t.Fatal(err)
	}
	if triggerCount != 0 || functionCount != 0 || markerCount != 2 || indexCount != 2 {
		t.Fatalf("cutover catalog triggers=%d functions=%d markers=%d indexes=%d, want 0/0/2/2",
			triggerCount, functionCount, markerCount, indexCount)
	}
	var dormantIndexDefinition, dormantIndexPredicate string
	if err := pool.QueryRow(ctx, `
		SELECT pg_get_indexdef(index.indexrelid), pg_get_expr(index.indpred, index.indrelid)
		FROM pg_index index
		JOIN pg_class class ON class.oid = index.indexrelid
		WHERE class.relname = 'idx_agent_reminder_group_manager_dormant_patrol'
	`).Scan(&dormantIndexDefinition, &dormantIndexPredicate); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"workspace_id", "anchor_channel_id", "id"} {
		if !strings.Contains(dormantIndexDefinition, want) {
			t.Fatalf("dormant patrol index definition missing %q: %s", want, dormantIndexDefinition)
		}
	}
	for _, want := range []string{"group_manager_auto", "patrol", "fired"} {
		if !strings.Contains(dormantIndexPredicate, want) {
			t.Fatalf("dormant patrol index predicate missing %q: %s", want, dormantIndexPredicate)
		}
	}
	perfTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer perfTx.Rollback(ctx)
	if _, err := perfTx.Exec(ctx, `
		INSERT INTO agent_reminder (
		  workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
		  fire_at, status, origin_kind, managed_kind, origin_key
		)
		SELECT
		  '78000000-0000-4000-8000-000000000002',
		  '78000000-0000-4000-8000-000000000004',
		  '78000000-0000-4000-8000-000000000001',
		  'cancelled decoy ' || series,
		  '78000000-0000-4000-8000-000000000008',
		  now() - interval '1 day',
		  'cancelled',
		  'group_manager_auto',
		  'patrol',
		  'patrol:cancelled-decoy:' || series
		FROM generate_series(1, 2000) series;

		INSERT INTO agent_reminder (
		  workspace_id, agent_id, initiator_user_id, title, anchor_channel_id,
		  fire_at, status, origin_kind, managed_kind, origin_key
		) VALUES (
		  '78000000-0000-4000-8000-000000000002',
		  '78000000-0000-4000-8000-000000000004',
		  '78000000-0000-4000-8000-000000000001',
		  'dormant explain target',
		  '78000000-0000-4000-8000-000000000008',
		  now() - interval '1 hour',
		  'fired',
		  'group_manager_auto',
		  'patrol',
		  'patrol:explain-target'
		);
		ANALYZE agent_reminder;
	`); err != nil {
		t.Fatalf("seed dormant patrol explain volume: %v", err)
	}
	planRows, err := perfTx.Query(ctx, `
		EXPLAIN (COSTS OFF)
		SELECT reminder.id
		FROM agent_reminder reminder
		JOIN channel ch
		  ON ch.id = reminder.anchor_channel_id
		 AND ch.workspace_id = reminder.workspace_id
		 AND ch.kind = 'group'
		 AND ch.archived_at IS NULL
		 AND ch.group_manager_agent_id = reminder.agent_id
		WHERE reminder.workspace_id = '78000000-0000-4000-8000-000000000002'
		  AND reminder.anchor_channel_id = '78000000-0000-4000-8000-000000000008'
		  AND reminder.origin_kind = 'group_manager_auto'
		  AND reminder.managed_kind = 'patrol'
		  AND reminder.status = 'fired'
		FOR UPDATE OF reminder
	`)
	if err != nil {
		t.Fatal(err)
	}
	var planLines []string
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			planRows.Close()
			t.Fatal(err)
		}
		planLines = append(planLines, line)
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		t.Fatal(err)
	}
	planRows.Close()
	plan := strings.Join(planLines, "\n")
	t.Logf("dormant patrol hot-path plan:\n%s", plan)
	if !strings.Contains(plan, "idx_agent_reminder_group_manager_dormant_patrol") ||
		strings.Contains(plan, "Seq Scan on agent_reminder") {
		t.Fatalf("dormant patrol hot-path plan did not use partial index:\n%s", plan)
	}
	if err := perfTx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}

	var scheduledAfter, dormantAfter time.Time
	var scheduledStatus, dormantStatus string
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT status FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000006'),
		  (SELECT fire_at FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000006'),
		  (SELECT status FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000007'),
		  (SELECT fire_at FROM agent_reminder WHERE id = '78000000-0000-4000-8000-000000000007')
	`).Scan(&scheduledStatus, &scheduledAfter, &dormantStatus, &dormantAfter); err != nil {
		t.Fatal(err)
	}
	if scheduledStatus != "scheduled" || !scheduledAfter.Equal(scheduledBefore) ||
		dormantStatus != "scheduled" ||
		time.Until(dormantAfter) < 14*time.Minute ||
		time.Until(dormantAfter) > 16*time.Minute {
		t.Fatalf("cutover timers scheduled=%s/%s dormant=%s/%s",
			scheduledStatus, scheduledAfter, dormantStatus, dormantAfter)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "down",
		Files:           []string{cutoverDownFile},
		AdvisoryLockKey: lockKey,
	}); err != nil {
		t.Fatalf("rollback migration 228: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT
		  (
		    SELECT count(*)
		    FROM pg_trigger
		    WHERE NOT tgisinternal
		      AND tgname IN (
		        'issue_group_manager_patrol_progress_trigger',
		        'comment_group_manager_patrol_progress_trigger',
		        'task_group_manager_patrol_progress_trigger',
		        'issue_source_group_manager_patrol_scope_trigger',
		        'channel_project_group_manager_patrol_scope_trigger'
		      )
		  ),
		  (
		    SELECT count(*)
		    FROM pg_indexes
		    WHERE indexname IN (
		      'idx_channel_message_agent_outbound_recent',
		      'idx_agent_reminder_group_manager_dormant_patrol'
		    )
		  )
	`).Scan(&triggerCount, &indexCount); err != nil {
		t.Fatal(err)
	}
	if triggerCount != 5 || indexCount != 0 {
		t.Fatalf("rollback restored triggers/indexes=%d/%d, want 5/0", triggerCount, indexCount)
	}
}
