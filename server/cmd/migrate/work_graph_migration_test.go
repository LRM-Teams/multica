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
	"github.com/jackc/pgx/v5/pgconn"
)

func TestWorkGraphMigrationUpSeedDown(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("work_graph_migration_test_%d", time.Now().UnixNano())
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
		CREATE TABLE workspace (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE channel (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
		CREATE TABLE issue (
			id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE
		);
		CREATE TABLE agent_task_queue (id UUID PRIMARY KEY DEFAULT gen_random_uuid());
	`); err != nil {
		t.Fatalf("create migration prerequisites: %v", err)
	}

	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test file")
	}
	migrationsDir := filepath.Join(filepath.Dir(testFile), "..", "..", "migrations")
	upSQL, err := os.ReadFile(filepath.Join(migrationsDir, "170_wendy_work_graph.up.sql"))
	if err != nil {
		t.Fatalf("read up migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(upSQL)); err != nil {
		t.Fatalf("apply up migration: %v", err)
	}

	var workspaceID, firstIssueID, secondIssueID, firstNodeID, secondNodeID string
	if err := conn.QueryRow(ctx, `INSERT INTO workspace DEFAULT VALUES RETURNING id`).Scan(&workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO issue (workspace_id) VALUES ($1) RETURNING id`, workspaceID).Scan(&firstIssueID); err != nil {
		t.Fatalf("seed first issue: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO issue (workspace_id) VALUES ($1) RETURNING id`, workspaceID).Scan(&secondIssueID); err != nil {
		t.Fatalf("seed second issue: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		INSERT INTO work_node (workspace_id, kind, title, owner_type, status, linked_issue_id)
		VALUES ($1, 'issue', 'First issue', 'unassigned', 'active', $2)
		RETURNING id
	`, workspaceID, firstIssueID).Scan(&firstNodeID); err != nil {
		t.Fatalf("seed first work node: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		INSERT INTO work_node (workspace_id, kind, title, owner_type, status, linked_issue_id)
		VALUES ($1, 'issue', 'Second issue', 'unassigned', 'waiting', $2)
		RETURNING id
	`, workspaceID, secondIssueID).Scan(&secondNodeID); err != nil {
		t.Fatalf("seed second work node: %v", err)
	}
	_, err = conn.Exec(ctx, `
		INSERT INTO work_node (workspace_id, kind, title, owner_type, status, linked_issue_id)
		VALUES ($1, 'issue', 'Duplicate issue', 'unassigned', 'active', $2)
	`, workspaceID, firstIssueID)
	if err == nil {
		t.Fatal("duplicate issue-backed work node unexpectedly succeeded")
	}
	if pgErr, ok := err.(*pgconn.PgError); !ok || pgErr.Code != "23505" {
		t.Fatalf("duplicate issue-backed work node error = %v, want unique-violation", err)
	}

	if _, err := conn.Exec(ctx, `
		INSERT INTO work_edge (workspace_id, from_node_id, to_node_id, kind, status)
		VALUES ($1, $2, $3, 'waits_on', 'open')
	`, workspaceID, secondNodeID, firstNodeID); err != nil {
		t.Fatalf("seed open waits_on edge: %v", err)
	}
	var otherWorkspaceID, otherIssueID, otherNodeID string
	if err := conn.QueryRow(ctx, `INSERT INTO workspace DEFAULT VALUES RETURNING id`).Scan(&otherWorkspaceID); err != nil {
		t.Fatalf("seed other workspace: %v", err)
	}
	if err := conn.QueryRow(ctx, `INSERT INTO issue (workspace_id) VALUES ($1) RETURNING id`, otherWorkspaceID).Scan(&otherIssueID); err != nil {
		t.Fatalf("seed other issue: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		INSERT INTO work_node (workspace_id, kind, title, owner_type, status, linked_issue_id)
		VALUES ($1, 'issue', 'Other issue', 'unassigned', 'active', $2)
		RETURNING id
	`, otherWorkspaceID, otherIssueID).Scan(&otherNodeID); err != nil {
		t.Fatalf("seed other work node: %v", err)
	}
	_, err = conn.Exec(ctx, `
		INSERT INTO work_edge (workspace_id, from_node_id, to_node_id, kind, status)
		VALUES ($1, $2, $3, 'waits_on', 'open')
	`, workspaceID, secondNodeID, otherNodeID)
	if err == nil {
		t.Fatal("cross-workspace work edge unexpectedly succeeded")
	}
	if pgErr, ok := err.(*pgconn.PgError); !ok || pgErr.Code != "23503" {
		t.Fatalf("cross-workspace work edge error = %v, want foreign-key violation", err)
	}

	var handoffID string
	if err := conn.QueryRow(ctx, `
		INSERT INTO pending_handoff (
			workspace_id, urgency, reason_code, target_actor_type, target_actor_id, dedupe_key, status
		) VALUES ($1, 'fast', 'unlock', 'agent', gen_random_uuid(), 'unlock:test', 'pending')
		RETURNING id
	`, workspaceID).Scan(&handoffID); err != nil {
		t.Fatalf("seed pending handoff: %v", err)
	}

	rows, err := conn.Query(ctx, `
		WITH due AS (
			SELECT ph.id
			FROM pending_handoff ph
			WHERE ph.workspace_id = $1
			  AND ph.urgency = 'fast'
			  AND ph.reason_code = 'unlock'
			  AND ph.status = 'pending'
			  AND ph.not_before <= now()
			ORDER BY ph.not_before, ph.created_at
			LIMIT 10
			FOR UPDATE SKIP LOCKED
		)
		UPDATE pending_handoff handoff
		SET status = 'claimed',
		    claim_token = gen_random_uuid(),
		    claimed_at = now(),
		    updated_at = now()
		FROM due
		WHERE handoff.id = due.id
		RETURNING handoff.id, handoff.status
	`, workspaceID)
	if err != nil {
		t.Fatalf("claim due handoff: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("claim query returned no due handoff")
	}
	var claimedID, status string
	if err := rows.Scan(&claimedID, &status); err != nil {
		t.Fatalf("scan claimed handoff: %v", err)
	}
	if claimedID != handoffID || status != "claimed" {
		t.Fatalf("claim result = (%s, %s), want (%s, claimed)", claimedID, status, handoffID)
	}
	if rows.Next() {
		t.Fatal("claim query returned more than one handoff")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("claim rows: %v", err)
	}

	downSQL, err := os.ReadFile(filepath.Join(migrationsDir, "170_wendy_work_graph.down.sql"))
	if err != nil {
		t.Fatalf("read down migration: %v", err)
	}
	if _, err := conn.Exec(ctx, string(downSQL)); err != nil {
		t.Fatalf("apply down migration: %v", err)
	}
	for _, table := range []string{"work_node", "work_edge", "pending_handoff"} {
		var exists bool
		if err := conn.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			t.Fatalf("check %s after down migration: %v", table, err)
		}
		if exists {
			t.Errorf("table %s still exists after down migration", table)
		}
	}
}
