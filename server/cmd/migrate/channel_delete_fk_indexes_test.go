package main

import (
	"context"
	"testing"
	"time"
)

func TestChannelDeleteFKIndexesCoverCascadeClosureAndAreIdempotent(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// The hook may already have run through `migrate up`; two more passes prove
	// it is safe for interrupted/retried deploys and leaves no duplicate work.
	for i := 0; i < 2; i++ {
		if err := runChannelDeleteFKIndexesHook(ctx, pool); err != nil {
			t.Fatalf("run channel-delete index hook pass %d: %v", i+1, err)
		}
	}

	// Start at channel, recursively include every relation reached through
	// ON DELETE CASCADE, then require a supporting child index for every FK
	// that PostgreSQL must enforce while deleting any row in that closure.
	// Without them a group delete degrades into one sequential scan per
	// cascaded message — the "delete channel → 500 after 30s" timeout.
	rows, err := pool.Query(ctx, `
		WITH RECURSIVE cascade_relation(rel) AS (
			SELECT 'channel'::regclass::oid
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
		t.Fatalf("inspect channel-delete foreign-key indexes: %v", err)
	}
	defer rows.Close()

	var missing []string
	for rows.Next() {
		var parentTable, childTable, constraintName string
		if err := rows.Scan(&parentTable, &childTable, &constraintName); err != nil {
			t.Fatalf("scan missing channel-delete index: %v", err)
		}
		missing = append(missing, parentTable+" -> "+childTable+"."+constraintName)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate missing channel-delete indexes: %v", err)
	}
	if len(missing) > 0 {
		t.Fatalf("channel delete cascade closure still lacks supporting indexes: %v", missing)
	}

	// chat_message is the pathological case: 1.6 GB on the test workspace, one
	// sequential scan per deleted channel_message without this index.
	var chatIndexReady bool
	if err := pool.QueryRow(ctx, `
		SELECT i.indisvalid AND i.indisready
		FROM pg_index i
		WHERE i.indexrelid = 'idx_chat_message_channel_thread_root_message_id'::regclass
	`).Scan(&chatIndexReady); err != nil {
		t.Fatalf("inspect chat_message thread-root index: %v", err)
	}
	if !chatIndexReady {
		t.Fatal("idx_chat_message_channel_thread_root_message_id is not valid and ready")
	}
}
