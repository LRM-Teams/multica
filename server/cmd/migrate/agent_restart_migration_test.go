package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestAgentRestartMigration384FailsClosedLegacyActiveOperations(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("agent_restart_384_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE agent_lifecycle_operation (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  status TEXT NOT NULL CHECK (status IN ('scheduled', 'running', 'succeeded', 'failed')),
		  step TEXT NOT NULL DEFAULT '',
		  reason_code TEXT NOT NULL DEFAULT '',
		  started_at TIMESTAMPTZ,
		  finished_at TIMESTAMPTZ,
		  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		  CHECK (
		    (status = 'scheduled' AND started_at IS NULL AND finished_at IS NULL)
		    OR (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
		    OR (status IN ('succeeded', 'failed') AND started_at IS NOT NULL AND finished_at IS NOT NULL)
		  )
		);
		INSERT INTO agent_lifecycle_operation (status) VALUES ('scheduled');
		INSERT INTO agent_lifecycle_operation (status, started_at) VALUES ('running', now());
		INSERT INTO agent_lifecycle_operation (status, started_at, finished_at) VALUES ('succeeded', now(), now());
	`); err != nil {
		t.Fatalf("create pre-384 fixture: %v", err)
	}

	upSQL, _ := readMigrationPair(t, "384_agent_restart_contract_cutover")
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 384: %v", err)
	}

	rows, err := conn.Query(ctx, `
		SELECT status, step, reason_code, started_at IS NOT NULL, finished_at IS NOT NULL
		FROM agent_lifecycle_operation
		ORDER BY finished_at NULLS LAST, id
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	failed := 0
	succeeded := 0
	for rows.Next() {
		var status, step, reason string
		var started, finished bool
		if err := rows.Scan(&status, &step, &reason, &started, &finished); err != nil {
			t.Fatal(err)
		}
		switch status {
		case "failed":
			failed++
			if step != "migration" || reason != "agent_restart_contract_upgraded_retry" || !started || !finished {
				t.Fatalf("migrated active row = %q/%q started=%v finished=%v", step, reason, started, finished)
			}
		case "succeeded":
			succeeded++
		default:
			t.Fatalf("legacy active status survived migration: %q", status)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if failed != 2 || succeeded != 1 {
		t.Fatalf("migration outcomes failed=%d succeeded=%d, want 2/1", failed, succeeded)
	}
}
