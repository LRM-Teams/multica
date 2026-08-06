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

// Task #99: migration 107's original down.sql silently DELETEd any
// author_type='system' comment before re-narrowing the CHECK constraint —
// a rollback that "succeeds" by permanently destroying data with no
// warning. This test locks the replacement behavior: the down migration
// must refuse (RAISE EXCEPTION) when a system-authored comment exists, and
// must only proceed once none remain — matching every other down migration
// in this repo, which either preserves data or fails loudly, never both
// succeeds and silently deletes.
func TestCommentSystemAuthorMigration107RefusesToDropSystemComments(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("comment_system_author_migration_test_%d", time.Now().UnixNano())
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

	// Minimal schema: only what the up/down SQL under test actually
	// references (comment.author_type). The real table has many more
	// columns/FKs; none of them matter to this migration's constraint.
	if _, err := conn.Exec(ctx, `
		CREATE TABLE comment (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  author_type TEXT NOT NULL CHECK (author_type IN ('member', 'agent'))
		);
	`); err != nil {
		t.Fatalf("create minimal pre-107 comment table: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "107_comment_system_author.up.sql"))
	if err != nil {
		t.Fatalf("read migration 107 up: %v", err)
	}
	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "107_comment_system_author.down.sql"))
	if err != nil {
		t.Fatalf("read migration 107 down: %v", err)
	}

	// Apply up: widens the constraint to allow 'system'.
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 107 up: %v", err)
	}

	// Down with zero system-authored comments must succeed cleanly — an
	// empty table is not the scenario this migration needs to protect
	// against.
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down migration must succeed with no system comments present: %v", err)
	}
	var authorTypeCheckAllowsSystem bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM pg_constraint
		  WHERE conrelid = 'comment'::regclass AND conname = 'comment_author_type_check'
		    AND pg_get_constraintdef(oid) LIKE '%system%'
		)
	`).Scan(&authorTypeCheckAllowsSystem); err != nil {
		t.Fatalf("read narrowed constraint: %v", err)
	}
	if authorTypeCheckAllowsSystem {
		t.Fatal("after a clean down (no system comments), the constraint should be back to the narrow member/agent list")
	}

	// Re-apply up so we can seed a system comment and prove the refusal path.
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-apply migration 107 up: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO comment (author_type) VALUES ('system')
	`); err != nil {
		t.Fatalf("seed system comment: %v", err)
	}

	// Confirm-broken direction: down must now fail, atomically — the
	// constraint must remain exactly as it was (still permitting 'system'),
	// not left half-migrated. Run in an explicit transaction, matching how
	// cmd/migrate's runMigrations sends the whole file as one conn.Exec
	// (Postgres treats a semicolon-separated multi-statement string sent as
	// a single query as one implicit transaction) — a bare psql -f run
	// does NOT reproduce this and can leave the schema in a half-applied
	// state, which is not what happens for real.
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin down-attempt transaction: %v", err)
	}
	_, downErr := tx.Exec(ctx, string(downSQL))
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down migration must fail while a system-authored comment exists, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "author_type='system'") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down migration failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback down-attempt transaction: %v", err)
	}

	// The failed attempt must not have left anything half-applied: the
	// system comment must still be there, and the constraint must still
	// allow it.
	var systemCommentCount int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM comment WHERE author_type = 'system'`).Scan(&systemCommentCount); err != nil {
		t.Fatalf("count system comments after failed down: %v", err)
	}
	if systemCommentCount != 1 {
		t.Fatalf("system comment count after failed down = %d, want 1 (failed rollback must not touch data)", systemCommentCount)
	}
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM pg_constraint
		  WHERE conrelid = 'comment'::regclass AND conname = 'comment_author_type_check'
		    AND pg_get_constraintdef(oid) LIKE '%system%'
		)
	`).Scan(&authorTypeCheckAllowsSystem); err != nil {
		t.Fatalf("read constraint after failed down: %v", err)
	}
	if !authorTypeCheckAllowsSystem {
		t.Fatal("after a failed down, the constraint must still allow 'system' — it must not be left half-narrowed")
	}

	// Operator follows the error message's suggested recovery: delete the
	// system comments, then re-run down. Now it must succeed.
	if _, err := conn.Exec(ctx, `DELETE FROM comment WHERE author_type = 'system'`); err != nil {
		t.Fatalf("manual delete per the error message's suggested recovery: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down migration must succeed once no system comments remain: %v", err)
	}
}
