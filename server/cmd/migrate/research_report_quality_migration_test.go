package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

func TestResearchReportQualityMigration276RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_report_quality_276_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Logf("drop schema %s: %v", schema, cleanupErr)
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		CREATE TABLE research_task (id UUID PRIMARY KEY);
		CREATE TABLE research_task_attempt (id UUID PRIMARY KEY);
		CREATE TABLE research_report (
		  id UUID PRIMARY KEY,
		  session_id UUID NOT NULL,
		  revision INT NOT NULL
		);
		CREATE TABLE research_report_claim (
		  report_id UUID NOT NULL REFERENCES research_report(id) ON DELETE CASCADE,
		  claim_id UUID NOT NULL,
		  section_id TEXT NOT NULL,
		  PRIMARY KEY (report_id, claim_id, section_id)
		);
	`); err != nil {
		t.Fatalf("create pre-275 schema: %v", err)
	}

	upSQL, downSQL := readMigrationPair(t, "276_research_report_quality")
	if _, err = conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 276 up: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task VALUES ('10000000-0000-4000-8000-000000000001');
		INSERT INTO research_task_attempt VALUES ('10000000-0000-4000-8000-000000000002');
		INSERT INTO research_report (
		  id, session_id, revision, produced_by_task_id, produced_by_attempt_id, author_agent_id
		) VALUES (
		  '10000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000004', 1,
		  '10000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000002',
		  '10000000-0000-4000-8000-000000000005'
		);
		INSERT INTO research_report_claim (report_id, claim_id, section_id, anchor_quote)
		VALUES (
		  '10000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000006',
		  'finding', 'Exact report prose anchor'
		);
		DELETE FROM research_task WHERE id = '10000000-0000-4000-8000-000000000001';
		DELETE FROM research_task_attempt WHERE id = '10000000-0000-4000-8000-000000000002';
	`); err != nil {
		t.Fatalf("exercise 276 attribution: %v", err)
	}
	var taskID, attemptID *string
	var authorID, anchor string
	if err = conn.QueryRow(ctx, `
		SELECT produced_by_task_id::text, produced_by_attempt_id::text, author_agent_id::text
		FROM research_report
	`).Scan(&taskID, &attemptID, &authorID); err != nil {
		t.Fatalf("read report attribution: %v", err)
	}
	if taskID != nil || attemptID != nil || authorID != "10000000-0000-4000-8000-000000000005" {
		t.Fatalf("unexpected attribution task=%v attempt=%v author=%q", taskID, attemptID, authorID)
	}
	if err = conn.QueryRow(ctx, `SELECT anchor_quote FROM research_report_claim`).Scan(&anchor); err != nil || anchor != "Exact report prose anchor" {
		t.Fatalf("anchor=%q err=%v", anchor, err)
	}

	if _, err = conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply 276 down: %v", err)
	}
	if _, err = conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("re-apply 276 up: %v", err)
	}
	var attributionColumns, anchorColumns int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_report'
		  AND column_name IN ('produced_by_task_id', 'produced_by_attempt_id', 'author_agent_id')
	`).Scan(&attributionColumns); err != nil {
		t.Fatalf("inspect attribution columns: %v", err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM information_schema.columns
		WHERE table_schema = current_schema() AND table_name = 'research_report_claim'
		  AND column_name = 'anchor_quote'
	`).Scan(&anchorColumns); err != nil {
		t.Fatalf("inspect anchor column: %v", err)
	}
	if attributionColumns != 3 || anchorColumns != 1 {
		t.Fatalf("round-trip columns attribution=%d anchor=%d", attributionColumns, anchorColumns)
	}
}
