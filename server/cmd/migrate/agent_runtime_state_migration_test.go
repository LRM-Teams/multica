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
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAgentRuntimeStateMigration218BackfillsCurrentPairWithoutMutatingLegacyResume(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_runtime_state_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE agent_runtime (
			id UUID PRIMARY KEY,
			name TEXT NOT NULL
		);
		CREATE TABLE agent (
			id UUID PRIMARY KEY,
			runtime_id UUID NOT NULL REFERENCES agent_runtime(id),
			name TEXT NOT NULL
		);
		CREATE TABLE chat_session (
			id UUID PRIMARY KEY,
			agent_id UUID NOT NULL REFERENCES agent(id),
			runtime_id UUID NOT NULL REFERENCES agent_runtime(id),
			session_id TEXT,
			work_dir TEXT,
			updated_at TIMESTAMPTZ NOT NULL
		);
		CREATE TABLE agent_task_queue (
			id UUID PRIMARY KEY,
			agent_id UUID NOT NULL REFERENCES agent(id),
			runtime_id UUID NOT NULL REFERENCES agent_runtime(id),
			issue_id UUID,
			chat_session_id UUID REFERENCES chat_session(id),
			session_id TEXT,
			work_dir TEXT,
			status TEXT NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		);

		INSERT INTO agent_runtime (id, name)
		VALUES ('10000000-0000-4000-8000-000000000218', 'current runtime');
		INSERT INTO agent (id, runtime_id, name)
		VALUES (
			'20000000-0000-4000-8000-000000000218',
			'10000000-0000-4000-8000-000000000218',
			'existing agent'
		);
		INSERT INTO chat_session (
			id, agent_id, runtime_id, session_id, work_dir, updated_at
		) VALUES (
			'30000000-0000-4000-8000-000000000218',
			'20000000-0000-4000-8000-000000000218',
			'10000000-0000-4000-8000-000000000218',
			'legacy-chat-session',
			'/legacy/chat',
			'2026-07-23 00:00:00+00'
		);
		INSERT INTO agent_task_queue (
			id, agent_id, runtime_id, issue_id, chat_session_id,
			session_id, work_dir, status, updated_at
		) VALUES
		(
			'40000000-0000-4000-8000-000000000218',
			'20000000-0000-4000-8000-000000000218',
			'10000000-0000-4000-8000-000000000218',
			NULL,
			'30000000-0000-4000-8000-000000000218',
			'legacy-chat-task-session',
			'/legacy/chat-task',
			'completed',
			'2026-07-23 00:01:00+00'
		),
		(
			'50000000-0000-4000-8000-000000000218',
			'20000000-0000-4000-8000-000000000218',
			'10000000-0000-4000-8000-000000000218',
			'60000000-0000-4000-8000-000000000218',
			NULL,
			'legacy-issue-session',
			'/legacy/issue',
			'completed',
			'2026-07-23 00:02:00+00'
		);
	`); err != nil {
		t.Fatalf("create pre-218 fixture: %v", err)
	}

	var chatBefore, tasksBefore string
	if err := conn.QueryRow(ctx, `
		SELECT to_jsonb(chat_session.*)::text
		FROM chat_session
		WHERE id = '30000000-0000-4000-8000-000000000218'
	`).Scan(&chatBefore); err != nil {
		t.Fatalf("capture legacy chat resume row: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT jsonb_agg(to_jsonb(agent_task_queue.*) ORDER BY id)::text
		FROM agent_task_queue
		WHERE id IN (
			'40000000-0000-4000-8000-000000000218',
			'50000000-0000-4000-8000-000000000218'
		)
	`).Scan(&tasksBefore); err != nil {
		t.Fatalf("capture legacy chat/issue task rows: %v", err)
	}

	upSQL := readAgentRuntimeStateMigrationSQL(t, "218_agent_runtime_state.up.sql")
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 218 up: %v", err)
	}

	var (
		providerSessionID         pgtype.Text
		workDir                   pgtype.Text
		providerConfigFingerprint pgtype.Text
		generation                int64
		lastTurnID                pgtype.UUID
		noticeReason              pgtype.Text
		legacyArchivedAt          pgtype.Timestamptz
	)
	if err := conn.QueryRow(ctx, `
		SELECT
			provider_session_id,
			work_dir,
			provider_config_fingerprint,
			generation,
			last_turn_id,
			fresh_session_notice_reason,
			legacy_resume_archived_at
		FROM agent_runtime_state
		WHERE agent_id = '20000000-0000-4000-8000-000000000218'
		  AND runtime_id = '10000000-0000-4000-8000-000000000218'
	`).Scan(
		&providerSessionID,
		&workDir,
		&providerConfigFingerprint,
		&generation,
		&lastTurnID,
		&noticeReason,
		&legacyArchivedAt,
	); err != nil {
		t.Fatalf("read migration A canonical row: %v", err)
	}
	if providerSessionID.Valid ||
		workDir.Valid ||
		providerConfigFingerprint.Valid ||
		generation != 1 ||
		lastTurnID.Valid ||
		!noticeReason.Valid || noticeReason.String != "cutover" ||
		legacyArchivedAt.Valid {
		t.Fatalf(
			"migration A canonical row = session:%#v workdir:%#v fingerprint:%#v generation:%d turn:%#v notice:%#v archive:%#v",
			providerSessionID,
			workDir,
			providerConfigFingerprint,
			generation,
			lastTurnID,
			noticeReason,
			legacyArchivedAt,
		)
	}

	var rowCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM agent_runtime_state`).Scan(&rowCount); err != nil {
		t.Fatalf("count migration A canonical rows: %v", err)
	}
	if rowCount != 1 {
		t.Fatalf("migration A canonical rows = %d, want 1 current agent/runtime pair", rowCount)
	}

	var chatAfter, tasksAfter string
	if err := conn.QueryRow(ctx, `
		SELECT to_jsonb(chat_session.*)::text
		FROM chat_session
		WHERE id = '30000000-0000-4000-8000-000000000218'
	`).Scan(&chatAfter); err != nil {
		t.Fatalf("read legacy chat resume row after migration: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		SELECT jsonb_agg(to_jsonb(agent_task_queue.*) ORDER BY id)::text
		FROM agent_task_queue
		WHERE id IN (
			'40000000-0000-4000-8000-000000000218',
			'50000000-0000-4000-8000-000000000218'
		)
	`).Scan(&tasksAfter); err != nil {
		t.Fatalf("read legacy chat/issue task rows after migration: %v", err)
	}
	if chatAfter != chatBefore {
		t.Fatalf("migration 218 mutated legacy chat resume evidence:\nbefore=%s\nafter=%s", chatBefore, chatAfter)
	}
	if tasksAfter != tasksBefore {
		t.Fatalf("migration 218 mutated legacy chat/issue task resume evidence:\nbefore=%s\nafter=%s", tasksBefore, tasksAfter)
	}
}

func readAgentRuntimeStateMigrationSQL(t *testing.T, name string) string {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate migration test file")
	}
	body, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "..", "..", "migrations", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(body)
}
