package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestEnvDispatchDerivedAgents198RemapsSafeStatusesAndRefusesDeleted locks
// task #100's fix for migration 198: down.sql narrows
// environment_agent_sandbox.status, dropping 6 values this migration
// introduced. Four have a safe remap target in the pre-198 vocabulary
// (credential_ready/sandbox_creating/runtime_waiting/agent_creating all
// collapse to 'provisioning'; failed_retryable collapses to 'failed') —
// those must be silently, safely remapped, not refused. 'deleted' has no
// equivalent (pre-198's 'deleting' means in-progress, not finished) and
// must refuse loudly instead.
func TestEnvDispatchDerivedAgents198RemapsSafeStatusesAndRefusesDeleted(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("env_dispatch_198_migration_test_%d", time.Now().UnixNano())
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

	// Minimal pre-198 reconstruction: just the columns/constraint 198's
	// up.sql actually touches, without the FK-heavy real schema (env/
	// channel/agent) that isn't relevant to the status-narrowing behavior
	// under test. agent needs (workspace_id, id) for the self-referencing
	// FK 198's up.sql adds.
	if _, err := conn.Exec(ctx, `
		CREATE TABLE agent (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL DEFAULT gen_random_uuid(),
		  UNIQUE (workspace_id, id)
		);
		CREATE TABLE environment_agent_sandbox (
		  env_id UUID NOT NULL DEFAULT gen_random_uuid(),
		  agent_id UUID NOT NULL REFERENCES agent(id),
		  status TEXT NOT NULL DEFAULT 'pending'
		    CHECK (status IN ('pending', 'provisioning', 'ready', 'failed', 'deleting')),
		  PRIMARY KEY (env_id, agent_id)
		);
	`); err != nil {
		t.Fatalf("create minimal pre-198 tables: %v", err)
	}

	var agentID string
	if err := conn.QueryRow(ctx, `INSERT INTO agent DEFAULT VALUES RETURNING id`).Scan(&agentID); err != nil {
		t.Fatalf("seed agent: %v", err)
	}

	upSQL, downSQL := readMigrationPair198(t)

	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply migration 198 up: %v", err)
	}

	// Seed one row per removed status value, including the four with a
	// safe remap and the one without.
	seedStatuses := []string{
		"credential_ready", "sandbox_creating", "runtime_waiting",
		"agent_creating", "failed_retryable",
	}
	for _, status := range seedStatuses {
		if _, err := conn.Exec(ctx, `
			INSERT INTO environment_agent_sandbox (agent_id, status) VALUES ($1, $2)
		`, agentID, status); err != nil {
			t.Fatalf("seed %s row: %v", status, err)
		}
	}

	// Down must succeed and remap all five safely.
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("down must succeed with only safely-remappable statuses: %v", err)
	}

	rows, err := conn.Query(ctx, `SELECT status, count(*) FROM environment_agent_sandbox GROUP BY status ORDER BY status`)
	if err != nil {
		t.Fatalf("query remapped statuses: %v", err)
	}
	got := map[string]int{}
	for rows.Next() {
		var status string
		var n int
		if err := rows.Scan(&status, &n); err != nil {
			rows.Close()
			t.Fatalf("scan: %v", err)
		}
		got[status] = n
	}
	rows.Close()
	if got["provisioning"] != 4 {
		t.Fatalf("provisioning count = %d, want 4 (credential_ready/sandbox_creating/runtime_waiting/agent_creating remapped)", got["provisioning"])
	}
	if got["failed"] != 1 {
		t.Fatalf("failed count = %d, want 1 (failed_retryable remapped)", got["failed"])
	}

	// Re-apply up, seed a 'deleted' row, confirm down refuses.
	if _, err := conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("re-apply migration 198 up: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (agent_id, status) VALUES ($1, 'deleted')
	`, agentID); err != nil {
		t.Fatalf("seed deleted row: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, downErr := tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must fail while a deleted row exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "status='deleted'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM environment_agent_sandbox WHERE status = 'deleted'`).Scan(&count); err != nil {
		t.Fatalf("count deleted rows after failed down: %v", err)
	}
	if count != 1 {
		t.Fatalf("deleted row count after failed down = %d, want 1", count)
	}

	if _, err := conn.Exec(ctx, `DELETE FROM environment_agent_sandbox WHERE status = 'deleted'`); err != nil {
		t.Fatalf("manual delete per the guard's suggested recovery: %v", err)
	}
	if _, err := conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("down must succeed once no deleted rows remain: %v", err)
	}
}

func readMigrationPair198(t *testing.T) (upSQL, downSQL string) {
	t.Helper()
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	up, err := os.ReadFile(filepath.Join(migrationsDir, "198_env_dispatch_derived_agents.up.sql"))
	if err != nil {
		t.Fatalf("read 198 up: %v", err)
	}
	down, err := os.ReadFile(filepath.Join(migrationsDir, "198_env_dispatch_derived_agents.down.sql"))
	if err != nil {
		t.Fatalf("read 198 down: %v", err)
	}
	return string(up), string(down)
}
