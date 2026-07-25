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
