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
	"github.com/jackc/pgx/v5/pgxpool"
)

// setupSandboxJobMigrationTest is shared setup for the 143/181/182 down
// tests: creates an isolated schema, the minimal pre-migration
// sandbox_job/sandbox_instance schema, and FK-target rows, then hands back
// the SAME connection (callers must use it — including for the
// confirm-broken tx.Begin — not acquire a fresh one, which would default
// back to the public search_path and silently test the wrong schema) plus
// the seeded workspace/user/node IDs and a helper to read migration SQL
// files.
func setupSandboxJobMigrationTest(t *testing.T) (ctx context.Context, conn *pgxpool.Conn, connExec func(string, ...any) error, connQueryCount func(string, ...any) int, workspaceID, userID, nodeID string, readMigration func(name string) string) {
	t.Helper()
	pool := openTestPool(t)
	baseCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	conn, err := pool.Acquire(baseCtx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	t.Cleanup(conn.Release)

	schema := fmt.Sprintf("sandbox_job_migration_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := conn.Exec(baseCtx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); err != nil {
			t.Logf("drop schema %s: %v", schema, err)
		}
	})
	if _, err := conn.Exec(baseCtx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search path: %v", err)
	}

	if _, err := conn.Exec(baseCtx, `
		CREATE TABLE workspace (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE "user" (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_node (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE sandbox_instance (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  node_id UUID NOT NULL REFERENCES sandbox_node(id),
		  status TEXT NOT NULL DEFAULT 'pending'
		    CHECK (status IN ('pending', 'creating', 'running', 'failed', 'stopping', 'stopped', 'resuming'))
		);
		CREATE TABLE sandbox_job (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL REFERENCES workspace(id),
		  initiator_user_id UUID NOT NULL REFERENCES "user"(id),
		  node_id UUID NOT NULL REFERENCES sandbox_node(id),
		  instance_id UUID REFERENCES sandbox_instance(id),
		  type TEXT NOT NULL CHECK (type IN ('create', 'stop', 'resume', 'delete', 'exec', 'message')),
		  status TEXT NOT NULL DEFAULT 'queued',
		  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		);
	`); err != nil {
		t.Fatalf("create minimal pre-143 schema: %v", err)
	}

	var ws, usr, node string
	if err := conn.QueryRow(baseCtx, `INSERT INTO workspace DEFAULT VALUES RETURNING id`).Scan(&ws); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := conn.QueryRow(baseCtx, `INSERT INTO "user" DEFAULT VALUES RETURNING id`).Scan(&usr); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := conn.QueryRow(baseCtx, `INSERT INTO sandbox_node DEFAULT VALUES RETURNING id`).Scan(&node); err != nil {
		t.Fatalf("seed sandbox_node: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")

	return baseCtx,
		conn,
		func(sql string, args ...any) error {
			_, err := conn.Exec(baseCtx, sql, args...)
			return err
		},
		func(sql string, args ...any) int {
			var n int
			if err := conn.QueryRow(baseCtx, sql, args...).Scan(&n); err != nil {
				t.Fatalf("count query %q: %v", sql, err)
			}
			return n
		},
		ws, usr, node,
		func(name string) string {
			b, err := os.ReadFile(filepath.Join(migrationsDir, name))
			if err != nil {
				t.Fatalf("read migration %s: %v", name, err)
			}
			return string(b)
		}
}

// TestSandboxJob143RefusesToDropReconfigureJobs locks task #101's fix for
// migration 143: the sibling sandbox_instance.status narrowing in the same
// file already remaps correctly; sandbox_job.type='reconfigure' used to be
// silently DELETEd instead. Must now refuse instead.
func TestSandboxJob143RefusesToDropReconfigureJobs(t *testing.T) {
	ctx, conn, exec, count, ws, usr, node, readMigration := setupSandboxJobMigrationTest(t)

	upSQL := readMigration("143_sandbox_reconfigure.up.sql")
	downSQL := readMigration("143_sandbox_reconfigure.down.sql")

	if err := exec(upSQL); err != nil {
		t.Fatalf("apply 143 up: %v", err)
	}
	if err := exec(downSQL); err != nil {
		t.Fatalf("down must succeed with no reconfigure jobs: %v", err)
	}

	if err := exec(upSQL); err != nil {
		t.Fatalf("re-apply 143 up: %v", err)
	}
	if err := exec(`INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, type) VALUES ($1, $2, $3, 'reconfigure')`, ws, usr, node); err != nil {
		t.Fatalf("seed reconfigure job: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, downErr := tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must fail while a reconfigure job exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "type='reconfigure'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := count(`SELECT count(*) FROM sandbox_job WHERE type = 'reconfigure'`); got != 1 {
		t.Fatalf("reconfigure job count after failed down = %d, want 1", got)
	}

	if err := exec(`DELETE FROM sandbox_job WHERE type = 'reconfigure'`); err != nil {
		t.Fatalf("manual delete: %v", err)
	}
	if err := exec(downSQL); err != nil {
		t.Fatalf("down must succeed once no reconfigure jobs remain: %v", err)
	}
}

// TestSandboxJob181RefusesToDropCreateTemplateJobs mirrors 143's test for
// migration 181's 'create_template' DELETE.
func TestSandboxJob181RefusesToDropCreateTemplateJobs(t *testing.T) {
	ctx, conn, exec, count, ws, usr, node, readMigration := setupSandboxJobMigrationTest(t)

	// 181 widens sandbox_instance_status_check and sandbox_job_type_check
	// further than 143 alone; apply 143's up first so 181's ALTER
	// statements have the expected starting point.
	if err := exec(readMigration("143_sandbox_reconfigure.up.sql")); err != nil {
		t.Fatalf("apply 143 up (prerequisite): %v", err)
	}

	upSQL := readMigration("181_sandbox_create_template.up.sql")
	downSQL := readMigration("181_sandbox_create_template.down.sql")

	if err := exec(upSQL); err != nil {
		t.Fatalf("apply 181 up: %v", err)
	}
	if err := exec(downSQL); err != nil {
		t.Fatalf("down must succeed with no create_template jobs: %v", err)
	}

	if err := exec(upSQL); err != nil {
		t.Fatalf("re-apply 181 up: %v", err)
	}
	if err := exec(`INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, type) VALUES ($1, $2, $3, 'create_template')`, ws, usr, node); err != nil {
		t.Fatalf("seed create_template job: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	_, downErr := tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must fail while a create_template job exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "type='create_template'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	if got := count(`SELECT count(*) FROM sandbox_job WHERE type = 'create_template'`); got != 1 {
		t.Fatalf("create_template job count after failed down = %d, want 1", got)
	}

	if err := exec(`DELETE FROM sandbox_job WHERE type = 'create_template'`); err != nil {
		t.Fatalf("manual delete: %v", err)
	}
	if err := exec(downSQL); err != nil {
		t.Fatalf("down must succeed once no create_template jobs remain: %v", err)
	}
}

// TestSandboxJob182RefusesToDropDeleteTemplateJobsAndNullInstanceRows locks
// BOTH of 182's fixes: the 'delete_template' type-narrowing DELETE, and the
// separate, column-nullability-driven `WHERE instance_id IS NULL` DELETE
// that isn't scoped to any particular job type.
func TestSandboxJob182RefusesToDropDeleteTemplateJobsAndNullInstanceRows(t *testing.T) {
	ctx, conn, exec, count, ws, usr, node, readMigration := setupSandboxJobMigrationTest(t)

	if err := exec(readMigration("143_sandbox_reconfigure.up.sql")); err != nil {
		t.Fatalf("apply 143 up (prerequisite): %v", err)
	}
	if err := exec(readMigration("181_sandbox_create_template.up.sql")); err != nil {
		t.Fatalf("apply 181 up (prerequisite): %v", err)
	}

	upSQL := readMigration("182_sandbox_snapshot.up.sql")
	downSQL := readMigration("182_sandbox_snapshot.down.sql")

	if err := exec(upSQL); err != nil {
		t.Fatalf("apply 182 up: %v", err)
	}
	if err := exec(downSQL); err != nil {
		t.Fatalf("down must succeed with no delete_template/null-instance jobs: %v", err)
	}

	// Case A: a delete_template-typed row (also happens to be the realistic
	// case for a null instance_id, per sandbox.go's own comment — "may be
	// null if source instance was deleted"). Confirms the type-scoped guard.
	if err := exec(upSQL); err != nil {
		t.Fatalf("re-apply 182 up (case A): %v", err)
	}
	if err := exec(`INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, type, instance_id) VALUES ($1, $2, $3, 'delete_template', NULL)`, ws, usr, node); err != nil {
		t.Fatalf("seed delete_template job: %v", err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin (case A): %v", err)
	}
	_, downErr := tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must fail while a delete_template job exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "type='delete_template'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason (case A): %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback (case A): %v", err)
	}
	if got := count(`SELECT count(*) FROM sandbox_job WHERE type = 'delete_template'`); got != 1 {
		t.Fatalf("delete_template job count after failed down = %d, want 1", got)
	}
	if err := exec(`DELETE FROM sandbox_job WHERE type = 'delete_template'`); err != nil {
		t.Fatalf("manual delete (case A): %v", err)
	}

	// Case B: a DIFFERENT job type with a null instance_id — proves the
	// second guard is genuinely type-independent, not just re-detecting
	// delete_template under another name. 'create' normally has a non-null
	// instance_id in production, but the column itself allows NULL, and
	// this guard must catch it regardless of type.
	if err := exec(`INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, type, instance_id) VALUES ($1, $2, $3, 'create', NULL)`, ws, usr, node); err != nil {
		t.Fatalf("seed null-instance non-delete_template job: %v", err)
	}
	tx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin (case B): %v", err)
	}
	_, downErr = tx.Exec(ctx, downSQL)
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down must fail while ANY job has a null instance_id, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "null instance_id") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down failed for the wrong reason (case B): %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback (case B): %v", err)
	}
	if got := count(`SELECT count(*) FROM sandbox_job WHERE instance_id IS NULL`); got != 1 {
		t.Fatalf("null-instance job count after failed down = %d, want 1", got)
	}

	if err := exec(`DELETE FROM sandbox_job WHERE instance_id IS NULL`); err != nil {
		t.Fatalf("manual delete (case B): %v", err)
	}
	if err := exec(downSQL); err != nil {
		t.Fatalf("down must succeed once no offending rows remain: %v", err)
	}
}
