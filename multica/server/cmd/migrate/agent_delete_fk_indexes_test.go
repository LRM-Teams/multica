package main

import (
	"context"
	"testing"
	"time"
)

func TestAgentDeleteFKIndexesCoverEveryReferenceAndAreIdempotent(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// The hook may already have run through `migrate up`; two more passes prove
	// it is safe for interrupted/retried deploys and leaves no duplicate work.
	for i := 0; i < 2; i++ {
		if err := runAgentDeleteFKIndexesHook(ctx, pool); err != nil {
			t.Fatalf("run agent-delete index hook pass %d: %v", i+1, err)
		}
	}

	rows, err := pool.Query(ctx, `
		SELECT conrelid::regclass::text, conname
		FROM pg_constraint
		WHERE contype = 'f'
		  AND confrelid = 'agent'::regclass
		  AND NOT EXISTS (
		      SELECT 1
		      FROM pg_index i
		      WHERE i.indrelid = conrelid
		        AND i.indisvalid
		        AND i.indisready
		        AND (i.indkey::smallint[])[0:cardinality(conkey)-1] = conkey
		  )
		ORDER BY 1, 2
	`)
	if err != nil {
		t.Fatalf("inspect agent foreign-key indexes: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var tableName, constraintName string
		if err := rows.Scan(&tableName, &constraintName); err != nil {
			t.Fatalf("scan missing agent foreign-key index: %v", err)
		}
		missing = append(missing, tableName+"."+constraintName)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate missing agent foreign-key indexes: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("agent hard-delete still lacks supporting indexes: %v", missing)
	}

	var runtimeIndexReady bool
	if err := pool.QueryRow(ctx, `
		SELECT i.indisvalid AND i.indisready
		FROM pg_index i
		WHERE i.indexrelid = 'idx_agent_inbox_event_runtime'::regclass
	`).Scan(&runtimeIndexReady); err != nil {
		t.Fatalf("inspect runtime snapshot index: %v", err)
	}
	if !runtimeIndexReady {
		t.Fatal("idx_agent_inbox_event_runtime is not valid and ready")
	}
}

func TestAgentDeleteCascadeFKIndexesCoverRecursiveDeleteClosureAndAreIdempotent(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	for i := 0; i < 2; i++ {
		if err := runAgentDeleteCascadeFKIndexesHook(ctx, pool); err != nil {
			t.Fatalf("run agent-delete cascade index hook pass %d: %v", i+1, err)
		}
	}

	// Start at agent, recursively include every relation reached through
	// ON DELETE CASCADE, then require a supporting child index for every FK
	// that PostgreSQL must enforce while deleting any row in that closure.
	// This catches second-order edges such as:
	// agent -> agent_inbox_event -> agent_inbox_event.parent_task_id.
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE cascade_relation(rel) AS (
			SELECT 'agent'::regclass::oid
			UNION
			SELECT constraint_row.conrelid
			FROM pg_constraint constraint_row
			JOIN cascade_relation parent
			  ON constraint_row.confrelid = parent.rel
			WHERE constraint_row.contype = 'f'
			  AND constraint_row.confdeltype = 'c'
		)
		SELECT constraint_row.confrelid::regclass::text,
		       constraint_row.conrelid::regclass::text,
		       constraint_row.conname
		FROM pg_constraint constraint_row
		WHERE constraint_row.contype = 'f'
		  AND constraint_row.confrelid IN (SELECT rel FROM cascade_relation)
		  AND NOT EXISTS (
		      SELECT 1
		      FROM pg_index index_row
		      WHERE index_row.indrelid = constraint_row.conrelid
		        AND index_row.indisvalid
		        AND index_row.indisready
		        AND (index_row.indkey::smallint[])[0:cardinality(constraint_row.conkey)-1]
		            = constraint_row.conkey
		  )
		ORDER BY 1, 2, 3
	`)
	if err != nil {
		t.Fatalf("inspect recursive agent-delete foreign-key indexes: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var parentTable, childTable, constraintName string
		if err := rows.Scan(&parentTable, &childTable, &constraintName); err != nil {
			t.Fatalf("scan recursive missing index: %v", err)
		}
		missing = append(missing, parentTable+" -> "+childTable+"."+constraintName)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate recursive missing indexes: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("agent hard-delete cascade closure still lacks supporting indexes: %v", missing)
	}

	var parentIndexReady bool
	if err := pool.QueryRow(ctx, `
		SELECT i.indisvalid AND i.indisready
		FROM pg_index i
		WHERE i.indexrelid = 'idx_agent_inbox_event_parent_task'::regclass
	`).Scan(&parentIndexReady); err != nil {
		t.Fatalf("inspect parent-task index: %v", err)
	}
	if !parentIndexReady {
		t.Fatal("idx_agent_inbox_event_parent_task is not valid and ready")
	}
}
