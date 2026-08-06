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

// Task #101: migration 186's down.sql had two independent bugs.
//
//  1. It narrowed sandbox_job_type_check past 'create_template' and
//     'delete_template' — values 186 never added (they predate it, from
//     migrations 181/182) and has no business touching on rollback. This
//     happened because 186 was originally authored as migration 183, before
//     181/182 existed; when renumbered and merged after them, its up.sql got
//     patched (commit e0587aa9c) to stop erasing those values, but down.sql
//     was never updated to match.
//  2. It narrowed past 'clone' — a value 186 genuinely does introduce — via
//     an unconditional DELETE, silently destroying any real clone-type
//     sandbox_job row on rollback with no warning.
//
// This test locks both fixes: down must preserve create_template/
// delete_template unconditionally, and must refuse (not silently delete)
// when a real clone job exists.
func TestSandboxJobType186PreservesUnrelatedValuesAndRefusesToDropCloneJobs(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("sandbox_job_186_migration_test_%d", time.Now().UnixNano())
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

	// Minimal schema: what 186's up/down actually touch, plus the FK
	// targets sandbox_job requires. environment/environment_agent_sandbox
	// and channel/agent are not needed — 186's up.sql only references
	// channel/agent inside environment_agent_sandbox's own column
	// definitions (FKs it declares), which we don't need real rows for
	// since this test only exercises sandbox_job_type_check.
	if _, err := conn.Exec(ctx, `
		CREATE TABLE workspace (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE "user" (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_node (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_instance (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE channel (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE agent (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE agent_runtime (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE environment (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_job (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL REFERENCES workspace(id),
		  initiator_user_id UUID NOT NULL REFERENCES "user"(id),
		  node_id UUID NOT NULL REFERENCES sandbox_node(id),
		  instance_id UUID REFERENCES sandbox_instance(id),
		  type TEXT NOT NULL CHECK (type IN (
		    'create', 'stop', 'resume', 'delete', 'reconfigure',
		    'create_template', 'delete_template', 'exec', 'message'
		  )),
		  status TEXT NOT NULL DEFAULT 'queued',
		  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`); err != nil {
		t.Fatalf("create minimal pre-186 schema: %v", err)
	}

	var workspaceID, userID, nodeID string
	if err := conn.QueryRow(ctx, `INSERT INTO workspace DEFAULT VALUES RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO "user" DEFAULT VALUES RETURNING id`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO sandbox_node DEFAULT VALUES RETURNING id`).Scan(&nodeID); err != nil {
		t.Fatalf("seed sandbox_node: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "186_env_dispatch_message_channels.up.sql"))
	if err != nil {
		t.Fatalf("read migration 186 up: %v", err)
	}
	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "186_env_dispatch_message_channels.down.sql"))
	if err != nil {
		t.Fatalf("read migration 186 down: %v", err)
	}

	// Seed a pre-existing create_template row — a value 186 must never
	// touch, before applying up (186's up.sql itself must not erase it
	// either, matching the real e0587aa9c fix).
	if _, err := conn.Exec(ctx, `
		INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, type)
		VALUES ($1, $2, $3, 'create_template')
	`, workspaceID, userID, nodeID); err != nil {
		t.Fatalf("seed pre-existing create_template job: %v", err)
	}

	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 186 up: %v", err)
	}
	var createTemplateCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM sandbox_job WHERE type = 'create_template'`).Scan(&createTemplateCount); err != nil {
		t.Fatalf("count create_template jobs after up: %v", err)
	}
	if createTemplateCount != 1 {
		t.Fatalf("create_template job count after up = %d, want 1 (186's up must not erase pre-existing values)", createTemplateCount)
	}

	// Down with zero clone jobs must succeed, AND must preserve
	// create_template/delete_template in the narrowed constraint — 186
	// never added them, so it has no business removing them.
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down migration must succeed with no clone jobs present: %v", err)
	}
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM sandbox_job WHERE type = 'create_template'`).Scan(&createTemplateCount); err != nil {
		t.Fatalf("count create_template jobs after down: %v", err)
	}
	if createTemplateCount != 1 {
		t.Fatalf("create_template job count after down = %d, want 1 (down must not touch values it didn't add)", createTemplateCount)
	}
	var typeCheckAllowsCreateTemplate, typeCheckAllowsClone bool
	if err := conn.QueryRow(ctx, `
		SELECT pg_get_constraintdef(oid) LIKE '%create_template%', pg_get_constraintdef(oid) LIKE '%clone%'
		FROM pg_constraint WHERE conrelid = 'sandbox_job'::regclass AND conname = 'sandbox_job_type_check'
	`).Scan(&typeCheckAllowsCreateTemplate, &typeCheckAllowsClone); err != nil {
		t.Fatalf("read narrowed constraint: %v", err)
	}
	if !typeCheckAllowsCreateTemplate {
		t.Fatal("after down, the constraint must still allow create_template — 186 never added it, down has no business removing it")
	}
	if typeCheckAllowsClone {
		t.Fatal("after a clean down (no clone jobs), the constraint should no longer allow clone — 186 does own that value")
	}

	// Re-apply up so we can seed a real clone job and prove the refusal path.
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-apply migration 186 up: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, type)
		VALUES ($1, $2, $3, 'clone')
	`, workspaceID, userID, nodeID); err != nil {
		t.Fatalf("seed clone job: %v", err)
	}

	// Confirm-broken direction: down must now fail, atomically. Run in an
	// explicit transaction, matching how cmd/migrate's runMigrations sends
	// the whole file as one conn.Exec (Postgres treats a multi-statement
	// string sent as one query as an implicit transaction) — see task #99's
	// migration 107 test for why a bare psql -f run does not reproduce this.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin down-attempt transaction: %v", err)
	}
	_, downErr := tx.Exec(ctx, string(downSQL))
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down migration must fail while a clone job exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "type='clone'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down migration failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback down-attempt transaction: %v", err)
	}

	var cloneCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM sandbox_job WHERE type = 'clone'`).Scan(&cloneCount); err != nil {
		t.Fatalf("count clone jobs after failed down: %v", err)
	}
	if cloneCount != 1 {
		t.Fatalf("clone job count after failed down = %d, want 1 (failed rollback must not touch data)", cloneCount)
	}

	// Operator follows the error message's suggested recovery: delete the
	// clone jobs, then re-run down. Now it must succeed.
	if _, err := conn.Exec(ctx, `DELETE FROM sandbox_job WHERE type = 'clone'`); err != nil {
		t.Fatalf("manual delete per the error message's suggested recovery: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down migration must succeed once no clone jobs remain: %v", err)
	}
}
