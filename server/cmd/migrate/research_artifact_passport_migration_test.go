package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

const researchArtifactPassportLegacySchema = `
CREATE TABLE workspace (id UUID PRIMARY KEY);
CREATE TABLE research_session (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_contract_revision (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE research_question (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1,
  plan_version INTEGER NOT NULL DEFAULT 1, client_key TEXT NOT NULL DEFAULT ''
);
CREATE TABLE research_task (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1,
  plan_version INTEGER NOT NULL DEFAULT 1, client_key TEXT NOT NULL DEFAULT ''
);
CREATE TABLE research_task_attempt (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL, task_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_source_snapshot (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_observation (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_claim (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1,
  plan_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE research_report (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1,
  plan_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE research_decision (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  decision_kind TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  goal_version INTEGER NOT NULL DEFAULT 1, plan_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE research_stage_eval (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_message (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_product_round_card (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_source (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_graph_node (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_graph_edge (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_claim_evidence (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  claim_id UUID NOT NULL, observation_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_run_event (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
`

func TestResearchArtifactPassportMigration318RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_passport_318_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Logf("drop schema %s: %v", schema, cleanupErr)
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	if _, err = conn.Exec(ctx, researchArtifactPassportLegacySchema); err != nil {
		t.Fatalf("create legacy research schema: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO workspace (id) VALUES ('10000000-0000-4000-8000-000000000001');
		INSERT INTO research_session (id, workspace_id) VALUES (
		  '20000000-0000-4000-8000-000000000001',
		  '10000000-0000-4000-8000-000000000001'
		);
	`); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	upSQL, downSQL := readMigrationPair(t, "318_research_artifact_passport")
	if _, err = conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("apply 318 up: %v", err)
	}

	var entityKindAllowed bool
	if err = conn.QueryRow(ctx, `SELECT research_artifact_entity_kind_allowed('task')`).Scan(&entityKindAllowed); err != nil || !entityKindAllowed {
		t.Fatalf("entity kind registry: allowed=%v err=%v", entityKindAllowed, err)
	}
	if err = conn.QueryRow(ctx, `SELECT research_artifact_entity_kind_allowed('hypothesis')`).Scan(&entityKindAllowed); err != nil || entityKindAllowed {
		t.Fatalf("unknown kind should fail closed: allowed=%v err=%v", entityKindAllowed, err)
	}

	var passportCount int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_passport WHERE entity_kind = 'run_session'
	`).Scan(&passportCount); err != nil || passportCount != 1 {
		t.Fatalf("run_session backfill count=%d err=%v", passportCount, err)
	}

	var immutableRejected bool
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_version (
		  workspace_id, session_id, artifact_id, version, content_hash, access_level
		) VALUES (
		  '10000000-0000-4000-8000-000000000001',
		  '20000000-0000-4000-8000-000000000001',
		  '20000000-0000-4000-8000-000000000001',
		  1, 'sha256:0000000000000000000000000000000000000000000000000000000000000000', 'raw'
		)
	`); err != nil {
		t.Fatalf("insert version: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		UPDATE research_artifact_version SET content_hash = 'sha256:1111111111111111111111111111111111111111111111111111111111111111'
		WHERE artifact_id = '20000000-0000-4000-8000-000000000001'
	`); err == nil {
		t.Fatal("expected version immutability guard")
	} else {
		immutableRejected = true
	}
	if !immutableRejected {
		t.Fatal("version immutability guard did not fire")
	}

	if _, err = conn.Exec(ctx, downSQL); err != nil {
		t.Fatalf("apply 318 down: %v", err)
	}
	if _, err = conn.Exec(ctx, upSQL); err != nil {
		t.Fatalf("reapply 318 up: %v", err)
	}
}

func TestResearchArtifactPassportMigration319RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_passport_319_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err = conn.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		if _, cleanupErr := pool.Exec(context.Background(), "DROP SCHEMA IF EXISTS "+quotedSchema+" CASCADE"); cleanupErr != nil {
			t.Logf("drop schema %s: %v", schema, cleanupErr)
		}
	})
	if _, err = conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); err != nil {
		t.Fatalf("set search path: %v", err)
	}
	if _, err = conn.Exec(ctx, researchArtifactPassportLegacySchema); err != nil {
		t.Fatalf("create legacy research schema: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO workspace (id) VALUES ('10000000-0000-4000-8000-000000000001');
		INSERT INTO research_session (id, workspace_id, created_at) VALUES (
		  '20000000-0000-4000-8000-000000000001',
		  '10000000-0000-4000-8000-000000000001',
		  '2026-01-01T00:00:00Z'
		);
		INSERT INTO research_contract_revision (id, workspace_id, session_id, created_at, goal_version)
		VALUES ('30000000-0000-4000-8000-000000000001', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '2026-01-01T00:00:01Z', 1);
		INSERT INTO research_question (id, workspace_id, session_id, created_at, goal_version, plan_version, client_key)
		VALUES ('30000000-0000-4000-8000-000000000002', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '2026-01-01T00:00:02Z', 1, 1, 'root');
		INSERT INTO research_task (id, workspace_id, session_id, created_at, goal_version, plan_version, client_key)
		VALUES ('30000000-0000-4000-8000-000000000003', '10000000-0000-4000-8000-000000000001', '20000000-0000-4000-8000-000000000001', '2026-01-01T00:00:03Z', 1, 1, 'plan:1');
	`); err != nil {
		t.Fatalf("seed fixture: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, down319 := readMigrationPair(t, "319_research_artifact_passport_backfill")
	if _, err = conn.Exec(ctx, up318); err != nil {
		t.Fatalf("apply 318 up: %v", err)
	}
	if _, err = conn.Exec(ctx, up319); err != nil {
		t.Fatalf("apply 319 up: %v", err)
	}

	var sqlHash string
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_migration_content_hash(
		  'task',
		  '10000000-0000-4000-8000-000000000001'::uuid,
		  '20000000-0000-4000-8000-000000000001'::uuid,
		  '30000000-0000-4000-8000-000000000003'::uuid
		)
	`).Scan(&sqlHash); err != nil {
		t.Fatalf("sql hash: %v", err)
	}
	wantHash := "sha256:8a4e1987763e42cfdf64e69bc319f1f0f77c671697299bc04317159c3b4a693b"
	if sqlHash != wantHash {
		t.Fatalf("sql hash=%q want=%q", sqlHash, wantHash)
	}

	for kind, want := range map[string]int{
		"run_session":       1,
		"contract_revision": 1,
		"question":          1,
		"task":              1,
	} {
		var passportCount int
		if err = conn.QueryRow(ctx, `
			SELECT count(*)::int FROM research_artifact_passport
			WHERE entity_kind = $1 AND current_version = 1
		`, kind).Scan(&passportCount); err != nil || passportCount != want {
			t.Fatalf("passport kind=%s count=%d want=%d err=%v", kind, passportCount, want, err)
		}
	}

	if _, err = conn.Exec(ctx, down319); err != nil {
		t.Fatalf("apply 319 down: %v", err)
	}
	var versionCount int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_version WHERE hash_origin = 'migration_recomputed'
	`).Scan(&versionCount); err != nil || versionCount != 0 {
		t.Fatalf("down migration versions=%d err=%v", versionCount, err)
	}
	if _, err = conn.Exec(ctx, up319); err != nil {
		t.Fatalf("reapply 319 up: %v", err)
	}
}
