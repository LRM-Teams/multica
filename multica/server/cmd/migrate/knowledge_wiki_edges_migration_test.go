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

// LRM-1000 / Frank An convention (tasks #99/#101): migration 268 down must not
// silently DELETE user-authored context/decision knowledge items. Refuse loudly
// when those rows exist; succeed only when none remain. Guard runs before DROP
// TABLE so a bare psql -f path also refuses before destructive steps.
func TestKnowledgeWikiEdgesMigration268RefusesWikiKindRows(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("knowledge_wiki_edges_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE workspace (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid()
		);
		CREATE TABLE team_knowledge_item (
		  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
		  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
		  kind TEXT NOT NULL,
		  title TEXT NOT NULL DEFAULT '',
		  content TEXT NOT NULL DEFAULT '',
		  CONSTRAINT team_knowledge_item_kind_check
		    CHECK (kind IN ('memory', 'pattern', 'skill', 'policy', 'troubleshooting'))
		);
	`); err != nil {
		t.Fatalf("create minimal pre-268 tables: %v", err)
	}

	wsID := "00000000-0000-0000-0000-000000000268"
	if _, err := conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1)`, wsID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "268_knowledge_wiki_edges.up.sql"))
	if err != nil {
		t.Fatalf("read migration 268 up: %v", err)
	}
	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "268_knowledge_wiki_edges.down.sql"))
	if err != nil {
		t.Fatalf("read migration 268 down: %v", err)
	}

	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply migration 268 up: %v", err)
	}

	// Empty wiki kinds: down must succeed.
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down migration must succeed with no context/decision rows: %v", err)
	}
	var edgeExists bool
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.tables
		  WHERE table_schema = current_schema() AND table_name = 'team_knowledge_edge'
		)
	`).Scan(&edgeExists); err != nil {
		t.Fatalf("check edge table after clean down: %v", err)
	}
	if edgeExists {
		t.Fatal("after clean down, team_knowledge_edge must be gone")
	}

	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("re-apply migration 268 up: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		INSERT INTO team_knowledge_item (workspace_id, kind, title, content)
		VALUES ($1, 'context', 'wiki page', 'body')
	`, wsID); err != nil {
		t.Fatalf("seed context knowledge item: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin down-attempt transaction: %v", err)
	}
	_, downErr := tx.Exec(ctx, string(downSQL))
	if downErr == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("down migration must fail while context/decision rows exist, it succeeded instead")
	}
	if !strings.Contains(downErr.Error(), "migration 268 down cannot proceed") ||
		!strings.Contains(downErr.Error(), "context") ||
		!strings.Contains(downErr.Error(), "decision") {
		_ = tx.Rollback(ctx)
		t.Fatalf("down migration failed for the wrong reason: %v", downErr)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback down-attempt transaction: %v", err)
	}

	var wikiCount int
	if err := conn.QueryRow(ctx, `
		SELECT count(*) FROM team_knowledge_item WHERE kind IN ('context', 'decision')
	`).Scan(&wikiCount); err != nil {
		t.Fatalf("count wiki rows after failed down: %v", err)
	}
	if wikiCount != 1 {
		t.Fatalf("wiki row count after failed down = %d, want 1", wikiCount)
	}
	if err := conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.tables
		  WHERE table_schema = current_schema() AND table_name = 'team_knowledge_edge'
		)
	`).Scan(&edgeExists); err != nil {
		t.Fatalf("check edge table after failed down: %v", err)
	}
	if !edgeExists {
		t.Fatal("after a failed down, team_knowledge_edge must still exist (guard before DROP)")
	}

	if _, err := conn.Exec(ctx, `
		DELETE FROM team_knowledge_item WHERE kind IN ('context', 'decision')
	`); err != nil {
		t.Fatalf("manual delete per the error message's suggested recovery: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("down migration must succeed once no wiki-kind rows remain: %v", err)
	}
}
