package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/multica-ai/multica/server/internal/migrations"
)

func TestAgentWakeCleanCutoverMigrationPreservesLedgerAndReenqueuesActiveWork(t *testing.T) {
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
		if migrations.ExtractVersion(file) == "223_agent_wake_clean_cutover" {
			cutoverFile = file
			break
		}
		beforeCutover = append(beforeCutover, file)
	}
	if cutoverFile == "" {
		t.Fatal("223_agent_wake_clean_cutover migration not found")
	}
	downFiles, err := migrations.Files("down")
	if err != nil {
		t.Fatalf("list down migrations: %v", err)
	}
	var cutoverDownFile string
	for _, file := range downFiles {
		if migrations.ExtractVersion(file) == "223_agent_wake_clean_cutover" {
			cutoverDownFile = file
			break
		}
	}
	if cutoverDownFile == "" {
		t.Fatal("223_agent_wake_clean_cutover down migration not found")
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           beforeCutover,
		AdvisoryLockKey: int64(rand.Uint64()&0x7fffffffffffffff) | 1,
		Hooks:           preMigrationHooks,
	}); err != nil {
		t.Fatalf("apply migrations before 223: %v", err)
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO "user" (id, name, email)
		VALUES (
			'70000000-0000-4000-8000-000000000001',
			'Cutover Owner',
			'cutover-owner@example.test'
		);
		INSERT INTO workspace (id, name, slug, description, issue_prefix)
		VALUES (
			'70000000-0000-4000-8000-000000000002',
			'Cutover Workspace',
			'cutover-workspace',
			'Migration fixture',
			'CUT'
		);
		INSERT INTO member (workspace_id, user_id, role)
		VALUES (
			'70000000-0000-4000-8000-000000000002',
			'70000000-0000-4000-8000-000000000001',
			'owner'
		);
		INSERT INTO agent_runtime (
			id, workspace_id, daemon_id, name, runtime_mode, provider,
			status, device_info, metadata, last_seen_at
		) VALUES (
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000002',
			NULL,
			'Cutover Runtime',
			'cloud',
			'cutover_test',
			'online',
			'Migration fixture',
			'{}'::jsonb,
			now()
		), (
			'70000000-0000-4000-8000-000000000009',
			'70000000-0000-4000-8000-000000000002',
			NULL,
			'Historical Cutover Runtime',
			'cloud',
			'cutover_test',
			'offline',
			'Historical migration fixture',
			'{}'::jsonb,
			now()
		);
		INSERT INTO agent (
			id, workspace_id, name, description, runtime_mode, runtime_config,
			runtime_id, visibility, max_concurrent_tasks, owner_id
		) VALUES (
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000002',
			'cutover-agent',
			'',
			'cloud',
			'{}'::jsonb,
			'70000000-0000-4000-8000-000000000003',
			'workspace',
			1,
			'70000000-0000-4000-8000-000000000001'
		);
		INSERT INTO chat_session (
			id, workspace_id, agent_id, creator_id, runtime_id
		) VALUES (
			'70000000-0000-4000-8000-000000000008',
			'70000000-0000-4000-8000-000000000002',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000001',
			'70000000-0000-4000-8000-000000000003'
		);
		INSERT INTO agent_session (
			id, workspace_id, agent_id, runtime_id, chat_session_id, scope
		) VALUES (
			'70000000-0000-4000-8000-000000000010',
			'70000000-0000-4000-8000-000000000002',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000008',
			'dm'
		);

		INSERT INTO issue (
			id, workspace_id, title, status, priority, creator_type, creator_id,
			number, position
		)
		SELECT
			('70000000-0000-4000-8000-' || lpad(value::text, 12, '0'))::uuid,
			'70000000-0000-4000-8000-000000000002',
			'Cutover issue ' || value,
			'in_progress',
			'none',
			'member',
			'70000000-0000-4000-8000-000000000001',
			value,
			value
		FROM generate_series(101, 107) AS value;

		INSERT INTO agent_task_queue (
			id, agent_id, runtime_id, issue_id, chat_session_id, status, priority, created_at,
			dispatched_at, started_at, completed_at, result, error, context,
			failure_reason, wait_reason
		) VALUES
		(
			'70000000-0000-4000-8000-000000000201',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000101',
			'70000000-0000-4000-8000-000000000008',
			'queued', 1, '2026-07-24 00:01:00+00',
			NULL, NULL, NULL, NULL, NULL, '{"type":"quick_create"}'::jsonb,
			NULL, NULL
		),
		(
			'70000000-0000-4000-8000-000000000202',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000009',
			'70000000-0000-4000-8000-000000000102',
			NULL,
			'dispatched', 2, '2026-07-24 00:02:00+00',
			'2026-07-24 00:02:30+00', NULL, NULL, NULL, NULL, '{}'::jsonb,
			NULL, NULL
		),
		(
			'70000000-0000-4000-8000-000000000203',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000103',
			NULL,
			'running', 3, '2026-07-24 00:03:00+00',
			'2026-07-24 00:03:20+00', '2026-07-24 00:03:30+00', NULL,
			NULL, NULL, '{}'::jsonb, NULL, NULL
		),
		(
			'70000000-0000-4000-8000-000000000204',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000104',
			NULL,
			'waiting_local_directory', 4, '2026-07-24 00:04:00+00',
			'2026-07-24 00:04:20+00', '2026-07-24 00:04:30+00', NULL,
			NULL, NULL, '{}'::jsonb, NULL, 'waiting for worktree'
		),
		(
			'70000000-0000-4000-8000-000000000205',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000105',
			NULL,
			'completed', 5, '2026-07-24 00:05:00+00',
			'2026-07-24 00:05:20+00', '2026-07-24 00:05:30+00',
			'2026-07-24 00:06:00+00', '{"ok":true}'::jsonb, NULL, '{}'::jsonb,
			NULL, NULL
		),
		(
			'70000000-0000-4000-8000-000000000206',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000106',
			NULL,
			'failed', 6, '2026-07-24 00:06:00+00',
			'2026-07-24 00:06:20+00', '2026-07-24 00:06:30+00',
			'2026-07-24 00:07:00+00', NULL, 'provider failed', '{}'::jsonb,
			'provider_error', NULL
		),
		(
			'70000000-0000-4000-8000-000000000207',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000107',
			NULL,
			'cancelled', 7, '2026-07-24 00:07:00+00',
			NULL, NULL, '2026-07-24 00:08:00+00', NULL, NULL, '{}'::jsonb,
			'user_cancelled', NULL
		);
		INSERT INTO chat_message (chat_session_id, role, content)
		SELECT
			'70000000-0000-4000-8000-000000000008',
			'user',
			'cutover decoy ' || value
		FROM generate_series(1, 20000) AS value;
		INSERT INTO chat_message (
			id, chat_session_id, role, content, task_id, created_at
		) VALUES (
			'70000000-0000-4000-8000-000000000011',
			'70000000-0000-4000-8000-000000000008',
			'user',
			'cutover source message',
			'70000000-0000-4000-8000-000000000201',
			'2026-07-24 00:00:30+00'
		);

		INSERT INTO task_message (task_id, seq, type, content)
		VALUES (
			'70000000-0000-4000-8000-000000000205',
			1,
			'text',
			'stable task message'
		);
		INSERT INTO agent_task_progress_snapshot (task_id, summary, step, total)
		VALUES (
			'70000000-0000-4000-8000-000000000203',
			'running before cutover',
			1,
			3
		);
		INSERT INTO agent_activity_event (
			workspace_id, agent_id, runtime_id, task_id,
			event_kind, event_type, target_kind, message
		) VALUES (
			'70000000-0000-4000-8000-000000000002',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000206',
			'custom',
			'cutover_fixture',
			'issue',
			'stable activity fact'
		);
		INSERT INTO agent_execution (
			id, source_kind, source_event_id, source, workspace_id, runtime_id,
			agent_id, issue_id, execution_config, started_at, created_at
		) VALUES (
			'70000000-0000-4000-8000-000000000205',
			'queue',
			'70000000-0000-4000-8000-000000000205',
			'issue',
			'70000000-0000-4000-8000-000000000002',
			'70000000-0000-4000-8000-000000000003',
			'70000000-0000-4000-8000-000000000004',
			'70000000-0000-4000-8000-000000000105',
			'{}'::jsonb,
			'2026-07-24 00:05:30+00',
			'2026-07-24 00:05:00+00'
		);
		INSERT INTO agent_usage (
			execution_id, provider, model, input_tokens, output_tokens, source
		) VALUES (
			'70000000-0000-4000-8000-000000000205',
			'cutover-provider',
			'cutover-model',
			123,
			45,
			'issue'
		);
	`); err != nil {
		t.Fatalf("seed pre-223 data-bearing fixture: %v", err)
	}

	var legacyFKCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM pg_constraint
		WHERE contype = 'f'
		  AND confrelid = 'agent_task_queue'::regclass
	`).Scan(&legacyFKCount); err != nil {
		t.Fatalf("count queue foreign keys before cutover: %v", err)
	}
	if legacyFKCount == 0 {
		t.Fatal("fixture has no queue foreign keys to retarget")
	}

	cutoverLockKey := int64(rand.Uint64()&0x7fffffffffffffff) | 1
	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{cutoverFile},
		AdvisoryLockKey: cutoverLockKey,
	}); err != nil {
		t.Fatalf("apply migration 223: %v", err)
	}

	var (
		queueGone, allOriginalIDs, userMessageLookupIndexed bool
		total, pendingWake                                  int
		acked, suppressed                                   int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			to_regclass('agent_task_queue') IS NULL,
			count(*) = 7,
			to_regclass('idx_chat_message_task_user_created') IS NOT NULL,
			count(*),
			count(*) FILTER (WHERE status = 'pending' AND requires_wake),
			count(*) FILTER (WHERE status = 'acked'),
			count(*) FILTER (WHERE status = 'suppressed')
		FROM agent_inbox_event
		WHERE id BETWEEN
			'70000000-0000-4000-8000-000000000201'
			AND '70000000-0000-4000-8000-000000000207'
	`).Scan(
		&queueGone,
		&allOriginalIDs,
		&userMessageLookupIndexed,
		&total,
		&pendingWake,
		&acked,
		&suppressed,
	); err != nil {
		t.Fatalf("read migrated wake states: %v", err)
	}
	if !queueGone || !allOriginalIDs || !userMessageLookupIndexed ||
		total != 7 || pendingWake != 4 || acked != 2 || suppressed != 1 {
		t.Fatalf(
			"cutover states = queueGone:%t ids:%t userMessageIndex:%t total:%d pendingWake:%d acked:%d suppressed:%d",
			queueGone,
			allOriginalIDs,
			userMessageLookupIndexed,
			total,
			pendingWake,
			acked,
			suppressed,
		)
	}

	var terminalFacts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE (id, status, terminal_outcome) IN (
			('70000000-0000-4000-8000-000000000205', 'acked', 'completed'),
			('70000000-0000-4000-8000-000000000206', 'acked', 'failed'),
			('70000000-0000-4000-8000-000000000207', 'suppressed', 'cancelled')
		)
		  AND NOT requires_wake
	`).Scan(&terminalFacts); err != nil {
		t.Fatalf("read terminal cutover facts: %v", err)
	}
	if terminalFacts != 3 {
		t.Fatalf("terminal facts = %d, want 3 non-runnable facts", terminalFacts)
	}

	var sourceMessageID string
	if err := pool.QueryRow(ctx, `
		SELECT source_chat_message_id::text
		FROM agent_inbox_event
		WHERE id = '70000000-0000-4000-8000-000000000201'
	`).Scan(&sourceMessageID); err != nil {
		t.Fatalf("read migrated source message: %v", err)
	}
	if sourceMessageID != "70000000-0000-4000-8000-000000000011" {
		t.Fatalf("source message = %s, want cutover source message", sourceMessageID)
	}

	if _, err := pool.Exec(ctx, `ANALYZE chat_message`); err != nil {
		t.Fatalf("analyze chat messages for plan regression: %v", err)
	}
	planRows, err := pool.Query(ctx, `
		EXPLAIN (FORMAT TEXT)
		SELECT event.id, source_chat_message.id
		FROM agent_inbox_event event
		LEFT JOIN LATERAL (
		  SELECT message.id
		  FROM chat_message message
		  WHERE message.task_id = event.id
		    AND message.role = 'user'
		  ORDER BY message.created_at, message.id
		  LIMIT 1
		) source_chat_message ON TRUE
		WHERE event.id BETWEEN
			'70000000-0000-4000-8000-000000000201'
			AND '70000000-0000-4000-8000-000000000207'
	`)
	if err != nil {
		t.Fatalf("explain source-message lookup: %v", err)
	}
	var planLines []string
	for planRows.Next() {
		var line string
		if err := planRows.Scan(&line); err != nil {
			planRows.Close()
			t.Fatalf("scan source-message lookup plan: %v", err)
		}
		planLines = append(planLines, line)
	}
	if err := planRows.Err(); err != nil {
		planRows.Close()
		t.Fatalf("read source-message lookup plan: %v", err)
	}
	planRows.Close()
	plan := strings.Join(planLines, "\n")
	if !strings.Contains(plan, "idx_chat_message_task_user_created") {
		t.Fatalf("source-message lookup plan does not use the migration index:\n%s", plan)
	}

	var executionCount, usageCount, inputTokens, outputTokens int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM agent_execution
			 WHERE source_event_id BETWEEN
				'70000000-0000-4000-8000-000000000201'
				AND '70000000-0000-4000-8000-000000000207'),
			(SELECT count(*) FROM agent_usage
			 WHERE execution_id = '70000000-0000-4000-8000-000000000205'),
			(SELECT input_tokens FROM agent_usage
			 WHERE execution_id = '70000000-0000-4000-8000-000000000205'),
			(SELECT output_tokens FROM agent_usage
			 WHERE execution_id = '70000000-0000-4000-8000-000000000205')
	`).Scan(&executionCount, &usageCount, &inputTokens, &outputTokens); err != nil {
		t.Fatalf("read execution and usage ledger after cutover: %v", err)
	}
	if executionCount != 5 || usageCount != 1 || inputTokens != 123 || outputTokens != 45 {
		t.Fatalf(
			"ledger after cutover = executions:%d usage:%d input:%d output:%d",
			executionCount,
			usageCount,
			inputTokens,
			outputTokens,
		)
	}

	var retargetedFKs, preservedFacts, agentSessions, deliveries int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM pg_constraint
			 WHERE contype = 'f'
			   AND confrelid = 'agent_inbox_event'::regclass
			   AND obj_description(oid, 'pg_constraint') = 'agent_wake_clean_cutover_223'),
			(SELECT count(*) FROM task_message
			 WHERE task_id = '70000000-0000-4000-8000-000000000205')
			+
			(SELECT count(*) FROM agent_task_progress_snapshot
			 WHERE task_id = '70000000-0000-4000-8000-000000000203')
			+
			(SELECT count(*) FROM agent_activity_event
			 WHERE task_id = '70000000-0000-4000-8000-000000000206'),
			(SELECT count(*) FROM agent_session
			 WHERE agent_id = '70000000-0000-4000-8000-000000000004'
			   AND scope = 'agent'),
			(SELECT count(*) FROM agent_event_delivery
			 WHERE inbox_event_id BETWEEN
				'70000000-0000-4000-8000-000000000201'
				AND '70000000-0000-4000-8000-000000000207')
	`).Scan(&retargetedFKs, &preservedFacts, &agentSessions, &deliveries); err != nil {
		t.Fatalf("read retargeted dependencies: %v", err)
	}
	if retargetedFKs != legacyFKCount || preservedFacts != 3 || agentSessions != 1 || deliveries != 0 {
		t.Fatalf(
			"dependencies after cutover = foreignKeys:%d/%d facts:%d sessions:%d deliveries:%d",
			retargetedFKs,
			legacyFKCount,
			preservedFacts,
			agentSessions,
			deliveries,
		)
	}
	var wakeSessionRuntime, historicalEventRuntime string
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT runtime_id::text
			 FROM agent_session
			 WHERE agent_id = '70000000-0000-4000-8000-000000000004'
			   AND scope = 'agent'),
			(SELECT runtime_id::text
			 FROM agent_inbox_event
			 WHERE id = '70000000-0000-4000-8000-000000000202')
	`).Scan(&wakeSessionRuntime, &historicalEventRuntime); err != nil {
		t.Fatalf("read canonical and historical runtimes after cutover: %v", err)
	}
	if wakeSessionRuntime != "70000000-0000-4000-8000-000000000003" ||
		historicalEventRuntime != "70000000-0000-4000-8000-000000000009" {
		t.Fatalf("cutover runtimes = session:%s historical-event:%s", wakeSessionRuntime, historicalEventRuntime)
	}

	var legacyCatalogRefs int
	if err := pool.QueryRow(ctx, `
		WITH functions AS (
			SELECT proc.oid
			FROM pg_proc proc
			JOIN pg_namespace namespace ON namespace.oid = proc.pronamespace
			WHERE namespace.nspname = current_schema()
			  AND proc.prokind IN ('f', 'p')
		),
		legacy_refs AS (
			SELECT 'function:' || oid::text AS ref
			FROM functions
			WHERE pg_get_functiondef(oid) ILIKE '%agent_task_queue%'
			UNION ALL
			SELECT 'view:' || viewname
			FROM pg_views
			WHERE schemaname = current_schema()
			  AND definition ILIKE '%agent_task_queue%'
			UNION ALL
			SELECT 'trigger:' || trigger.oid::text
			FROM pg_trigger trigger
			WHERE NOT trigger.tgisinternal
			  AND pg_get_triggerdef(trigger.oid) ILIKE '%agent_task_queue%'
			UNION ALL
			SELECT 'constraint:' || constraint_row.oid::text
			FROM pg_constraint constraint_row
			WHERE pg_get_constraintdef(constraint_row.oid) ILIKE '%agent_task_queue%'
		)
		SELECT count(*) FROM legacy_refs
	`).Scan(&legacyCatalogRefs); err != nil {
		t.Fatalf("scan catalog for legacy queue references: %v", err)
	}
	if legacyCatalogRefs != 0 {
		t.Fatalf("legacy queue catalog references = %d, want 0", legacyCatalogRefs)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{cutoverFile},
		AdvisoryLockKey: cutoverLockKey,
	}); err != nil {
		t.Fatalf("rerun migration 223 through idempotent runner: %v", err)
	}
	var rerunCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE id BETWEEN
			'70000000-0000-4000-8000-000000000201'
			AND '70000000-0000-4000-8000-000000000207'
	`).Scan(&rerunCount); err != nil {
		t.Fatalf("count migrated events after rerun: %v", err)
	}
	if rerunCount != 7 {
		t.Fatalf("migrated events after rerun = %d, want 7", rerunCount)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "down",
		Files:           []string{cutoverDownFile},
		AdvisoryLockKey: cutoverLockKey,
	}); err != nil {
		t.Fatalf("roll back migration 223: %v", err)
	}
	var (
		queueRestored, copiedInboxRemoved, userMessageLookupIndexRemoved bool
		queueRows, queueFKs                                              int
		executionSource                                                  string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			to_regclass('agent_task_queue') IS NOT NULL,
			NOT EXISTS (
				SELECT 1
				FROM agent_inbox_event
				WHERE id BETWEEN
					'70000000-0000-4000-8000-000000000201'
					AND '70000000-0000-4000-8000-000000000207'
			),
			(SELECT count(*) FROM agent_task_queue
			 WHERE id BETWEEN
				'70000000-0000-4000-8000-000000000201'
				AND '70000000-0000-4000-8000-000000000207'),
			(SELECT count(*) FROM pg_constraint
			 WHERE contype = 'f'
			   AND confrelid = 'agent_task_queue'::regclass),
			(SELECT source_kind FROM agent_execution
			 WHERE id = '70000000-0000-4000-8000-000000000205'),
			to_regclass('idx_chat_message_task_user_created') IS NULL
	`).Scan(
		&queueRestored,
		&copiedInboxRemoved,
		&queueRows,
		&queueFKs,
		&executionSource,
		&userMessageLookupIndexRemoved,
	); err != nil {
		t.Fatalf("read rollback state: %v", err)
	}
	if !queueRestored || !copiedInboxRemoved || queueRows != 7 ||
		queueFKs != legacyFKCount || executionSource != "queue" ||
		!userMessageLookupIndexRemoved {
		t.Fatalf(
			"rollback = queue:%t inboxRemoved:%t rows:%d foreignKeys:%d/%d execution:%q userMessageIndexRemoved:%t",
			queueRestored,
			copiedInboxRemoved,
			queueRows,
			queueFKs,
			legacyFKCount,
			executionSource,
			userMessageLookupIndexRemoved,
		)
	}

	if err := runMigrations(ctx, pool, runOptions{
		Direction:       "up",
		Files:           []string{cutoverFile},
		AdvisoryLockKey: cutoverLockKey,
	}); err != nil {
		t.Fatalf("reapply migration 223 after rollback: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM agent_inbox_event
		WHERE id BETWEEN
			'70000000-0000-4000-8000-000000000201'
			AND '70000000-0000-4000-8000-000000000207'
	`).Scan(&rerunCount); err != nil {
		t.Fatalf("count events after rollback/reapply: %v", err)
	}
	if rerunCount != 7 {
		t.Fatalf("events after rollback/reapply = %d, want 7", rerunCount)
	}
}

func openAgentWakeCutoverDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()

	adminPool := openTestPool(t)
	databaseName := fmt.Sprintf("agent_wake_cutover_%d_%d", time.Now().UnixNano(), rand.Uint32())
	databaseIdentifier := pgx.Identifier{databaseName}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+databaseIdentifier); err != nil {
		t.Fatalf("create cutover database: %v", err)
	}

	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		databaseURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		t.Fatalf("parse database url: %v", err)
	}
	config.ConnConfig.Database = databaseName
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open cutover database: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping cutover database: %v", err)
	}

	t.Cleanup(func() {
		pool.Close()
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		_, _ = adminPool.Exec(cleanupCtx, `
			SELECT pg_terminate_backend(pid)
			FROM pg_stat_activity
			WHERE datname = $1
			  AND pid <> pg_backend_pid()
		`, databaseName)
		if _, err := adminPool.Exec(cleanupCtx, "DROP DATABASE IF EXISTS "+databaseIdentifier); err != nil {
			t.Logf("drop cutover database %s: %v", databaseName, err)
		}
	})

	return pool
}
