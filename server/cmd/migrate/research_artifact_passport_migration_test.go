package main

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const researchArtifactPassportLegacySchema = `
CREATE TABLE workspace (id UUID PRIMARY KEY);
CREATE TABLE research_session (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_contract_revision (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1
);
CREATE TABLE research_question (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1,
  plan_version INTEGER NOT NULL DEFAULT 1, client_key TEXT NOT NULL DEFAULT ''
);
CREATE TABLE research_task (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), goal_version INTEGER NOT NULL DEFAULT 1,
  plan_version INTEGER NOT NULL DEFAULT 1, client_key TEXT NOT NULL DEFAULT ''
);
CREATE TABLE research_task_attempt (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL, task_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE research_source_snapshot (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  verification_status TEXT NOT NULL DEFAULT 'pending'
);
CREATE TABLE research_observation (
  id UUID PRIMARY KEY, workspace_id UUID NOT NULL, session_id UUID NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  verification_status TEXT NOT NULL DEFAULT 'pending'
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
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  verification_status TEXT NOT NULL DEFAULT 'pending'
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
	for _, futureKind := range []string{"hypothesis", "branch", "insight", "inquiry_edge"} {
		if err = conn.QueryRow(ctx, `SELECT research_artifact_entity_kind_allowed($1)`, futureKind).Scan(&entityKindAllowed); err != nil || entityKindAllowed {
			t.Fatalf("future kind %q should fail closed: allowed=%v err=%v", futureKind, entityKindAllowed, err)
		}
	}
	var fabricatedFutureRows int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int
		FROM research_artifact_passport
		WHERE entity_kind IN ('hypothesis', 'branch', 'insight', 'inquiry_edge')
	`).Scan(&fabricatedFutureRows); err != nil || fabricatedFutureRows != 0 {
		t.Fatalf("fabricated E-N passport rows=%d err=%v", fabricatedFutureRows, err)
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

func TestResearchArtifactReciprocalGuards320RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_reciprocal_320_test_%d", time.Now().UnixNano())
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
	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, down320 := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	for _, upSQL := range []string{up318, up319, up320} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	commitOrForce := func(t *testing.T, tx pgx.Tx, immediate bool) error {
		t.Helper()
		if immediate {
			if _, constraintErr := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); constraintErr != nil {
				return constraintErr
			}
		}
		return tx.Commit(ctx)
	}
	assertConstraint := func(t *testing.T, got error, want string) {
		t.Helper()
		pgErr, ok := got.(*pgconn.PgError)
		if !ok || pgErr.ConstraintName != want {
			t.Fatalf("constraint error=%v want=%s", got, want)
		}
	}

	modes := []struct {
		name      string
		immediate bool
		suffix    string
	}{
		{name: "immediate", immediate: true, suffix: "003"},
		{name: "ordinary_commit", immediate: false, suffix: "004"},
	}
	for _, mode := range modes {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			pairedTaskID := "30000000-0000-4000-8000-000000000" + mode.suffix
			pairedTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin paired tx: %v", beginErr)
			}
			defer pairedTx.Rollback(ctx)
			if _, execErr := pairedTx.Exec(ctx, `
				INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
				VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
				ON CONFLICT (workspace_id, session_id) DO NOTHING
			`, workspaceID, sessionID); execErr != nil {
				t.Fatalf("insert policy state: %v", execErr)
			}
			if _, execErr := pairedTx.Exec(ctx, `
				INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
				VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 1, 1)
			`, pairedTaskID, workspaceID, sessionID, "guard-positive-"+mode.name); execErr != nil {
				t.Fatalf("insert paired task: %v", execErr)
			}
			if _, execErr := pairedTx.Exec(ctx, `
				SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
			`, workspaceID, sessionID, pairedTaskID); execErr != nil {
				t.Fatalf("register paired task passport: %v", execErr)
			}
			if commitErr := commitOrForce(t, pairedTx, mode.immediate); commitErr != nil {
				t.Fatalf("commit paired task and passport: %v", commitErr)
			}

			domainOnlyTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin domain-only tx: %v", beginErr)
			}
			defer domainOnlyTx.Rollback(ctx)
			if _, execErr := domainOnlyTx.Exec(ctx, `
				INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
				VALUES ('30000000-0000-4000-8000-000000000099'::uuid, $1::uuid, $2::uuid, $3, 1, 1)
			`, workspaceID, sessionID, "guard-domain-only-"+mode.name); execErr != nil {
				t.Fatalf("insert domain-only task: %v", execErr)
			}
			assertConstraint(t, commitOrForce(t, domainOnlyTx, mode.immediate), "research_task_artifact_passport_guard")
			_ = domainOnlyTx.Rollback(ctx)

			passportOnlyTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin passport-only tx: %v", beginErr)
			}
			defer passportOnlyTx.Rollback(ctx)
			if _, execErr := passportOnlyTx.Exec(ctx, `
				SELECT research_artifact_backfill_registered(
				  $1::uuid, $2::uuid, '30000000-0000-4000-8000-000000000098'::uuid,
				  'task', now(), 1, 1
				)
			`, workspaceID, sessionID); execErr != nil {
				t.Fatalf("insert passport-only task registration: %v", execErr)
			}
			assertConstraint(t, commitOrForce(t, passportOnlyTx, mode.immediate), "research_artifact_passport_class_guard")
		})
	}

	if _, err = conn.Exec(ctx, `DELETE FROM research_session WHERE id = $1::uuid`, sessionID); err != nil {
		t.Fatalf("session cascade delete: %v", err)
	}
	var remainingTasks int
	if err = conn.QueryRow(ctx, `SELECT count(*)::int FROM research_task`).Scan(&remainingTasks); err != nil || remainingTasks != 0 {
		t.Fatalf("tasks after cascade=%d err=%v", remainingTasks, err)
	}
	var remainingPassports int
	if err = conn.QueryRow(ctx, `SELECT count(*)::int FROM research_artifact_passport`).Scan(&remainingPassports); err != nil || remainingPassports != 0 {
		t.Fatalf("passports after cascade=%d err=%v", remainingPassports, err)
	}
	if _, err = conn.Exec(ctx, `DELETE FROM workspace WHERE id = $1::uuid`, workspaceID); err != nil {
		t.Fatalf("delete empty workspace: %v", err)
	}

	if _, err = conn.Exec(ctx, down320); err != nil {
		t.Fatalf("apply 320 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up320); err != nil {
		t.Fatalf("reapply 320 up: %v", err)
	}
}

func TestResearchArtifactAppendOnlyCascadeGuards335RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_cascade_335_test_%d", time.Now().UnixNano())
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
	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	taskID := "30000000-0000-4000-8000-000000000001"
	attemptID := "40000000-0000-4000-8000-000000000001"
	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)
	`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		ALTER TABLE research_task_attempt
		  ADD CONSTRAINT research_task_attempt_session_cascade_test_fkey
		  FOREIGN KEY (session_id) REFERENCES research_session(id) ON DELETE CASCADE
	`); err != nil {
		t.Fatalf("add producer session cascade: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id)
		VALUES ($1::uuid, $2::uuid, $3::uuid)
	`, taskID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed producer task: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_attempt (id, workspace_id, session_id, task_id)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)
	`, attemptID, workspaceID, sessionID, taskID); err != nil {
		t.Fatalf("seed producer attempt: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up335, down335 := readMigrationPair(t, "335_research_artifact_append_only_cascade_guards")
	for _, upSQL := range []string{up318, up319} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision,
		  old_lifecycle_status, new_lifecycle_status, eligibility_reason
		) VALUES (
		  $1::uuid, $2::uuid, 1, 'lifecycle', $3::uuid,
		  1, 2, 'registered', 'accepted', 'append-only fixture'
		)
	`, workspaceID, sessionID, attemptID); err != nil {
		t.Fatalf("seed append-only mutation: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_lifecycle_event (
		  workspace_id, session_id, artifact_id, old_status, new_status,
		  old_eligibility_revision, new_eligibility_revision, policy_watermark, reason
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'registered', 'accepted',
		  1, 2, 1, 'append-only fixture'
		)
	`, workspaceID, sessionID, attemptID); err != nil {
		t.Fatalf("seed append-only lifecycle event: %v", err)
	}
	for _, upSQL := range []string{up320, up335} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	var manifestCascade bool
	if err = conn.QueryRow(ctx, `
		SELECT confdeltype = 'c'
		FROM pg_constraint
		WHERE conrelid = 'research_artifact_input_reference'::regclass
		  AND conname = 'research_artifact_input_reference_manifest_fkey'
	`).Scan(&manifestCascade); err != nil || !manifestCascade {
		t.Fatalf("manifest input-reference cascade=%v err=%v", manifestCascade, err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_version (
		  workspace_id, session_id, artifact_id, version, schema_name, schema_version,
		  canonicalization_version, content_hash, access_level, hash_origin,
		  produced_by_task_id, produced_by_attempt_id
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 2, 'attempt', 'legacy-v1',
		  'research-artifact-c14n-v1', $4, 'raw', 'production', $5::uuid, $3::uuid
		)
	`, workspaceID, sessionID, attemptID,
		"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", taskID); err != nil {
		t.Fatalf("seed produced artifact version: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		UPDATE research_artifact_passport
		SET current_version = 2
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, attemptID); err != nil {
		t.Fatalf("advance produced artifact version: %v", err)
	}
	if _, err = conn.Exec(ctx, `DELETE FROM research_artifact_version WHERE session_id = $1::uuid`, sessionID); err == nil {
		t.Fatal("expected direct version delete to remain rejected")
	}
	appendOnlyMutations := []struct {
		name string
		sql  string
	}{
		{
			name: "policy mutation update",
			sql:  `UPDATE research_artifact_policy_mutation SET eligibility_reason = 'tampered' WHERE session_id = $1::uuid`,
		},
		{
			name: "policy mutation delete",
			sql:  `DELETE FROM research_artifact_policy_mutation WHERE session_id = $1::uuid`,
		},
		{
			name: "lifecycle event update",
			sql:  `UPDATE research_artifact_lifecycle_event SET reason = 'tampered' WHERE session_id = $1::uuid`,
		},
		{
			name: "lifecycle event delete",
			sql:  `DELETE FROM research_artifact_lifecycle_event WHERE session_id = $1::uuid`,
		},
	}
	for _, mutation := range appendOnlyMutations {
		if _, err = conn.Exec(ctx, mutation.sql, sessionID); err == nil {
			t.Fatalf("expected direct %s to be rejected", mutation.name)
		}
	}
	directDeleteTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin direct producer delete: %v", err)
	}
	if _, err = directDeleteTx.Exec(ctx, `DELETE FROM research_task_attempt WHERE id = $1::uuid`, attemptID); err != nil {
		_ = directDeleteTx.Rollback(ctx)
	} else if err = directDeleteTx.Commit(ctx); err == nil {
		t.Fatal("expected direct producer delete to remain rejected")
	}
	if _, err = conn.Exec(ctx, `DELETE FROM workspace WHERE id = $1::uuid`, workspaceID); err != nil {
		t.Fatalf("workspace cascade delete: %v", err)
	}
	var remainingVersions, remainingMutations, remainingLifecycleEvents int
	if err = conn.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_version),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation),
		  (SELECT count(*)::int FROM research_artifact_lifecycle_event)
	`).Scan(&remainingVersions, &remainingMutations, &remainingLifecycleEvents); err != nil {
		t.Fatalf("count append-only rows after workspace cascade: %v", err)
	}
	if remainingVersions != 0 || remainingMutations != 0 || remainingLifecycleEvents != 0 {
		t.Fatalf("append-only rows after cascade: versions=%d mutations=%d lifecycle_events=%d",
			remainingVersions, remainingMutations, remainingLifecycleEvents)
	}

	if _, err = conn.Exec(ctx, down335); err != nil {
		t.Fatalf("apply 335 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up335); err != nil {
		t.Fatalf("reapply 335 up: %v", err)
	}
}

func TestResearchArtifactPolicyCouplingGuards321RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_policy_321_test_%d", time.Now().UnixNano())
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

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	snapshotID := "30000000-0000-4000-8000-000000000010"
	userID := "40000000-0000-4000-8000-000000000001"
	grantID := "50000000-0000-4000-8000-000000000001"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, down321 := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	for _, upSQL := range []string{up318, up319, up320, up321} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	positiveTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin positive tx: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("insert policy state: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		INSERT INTO research_source_snapshot (id, workspace_id, session_id, verification_status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'pending')
	`, snapshotID, workspaceID, sessionID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("insert snapshot: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'source_snapshot', now(), NULL, NULL)
	`, workspaceID, sessionID, snapshotID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("register snapshot passport: %v", err)
	}
	var watermark int64
	if err = positiveTx.QueryRow(ctx, `
		SELECT research_artifact_reserve_policy_watermark($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("reserve watermark: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET eligibility_revision = 2
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, snapshotID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("bump passport revision: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision
		) VALUES ($1::uuid, $2::uuid, $3, 'verification', $4::uuid, 1, 2)
	`, workspaceID, sessionID, watermark, snapshotID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("insert verification mutation: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		UPDATE research_source_snapshot
		SET verification_status = 'verified'
		WHERE id = $1::uuid
	`, snapshotID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("verify snapshot: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("set constraints immediate: %v", err)
	}
	if err = positiveTx.Commit(ctx); err != nil {
		t.Fatalf("commit verification coupling tx: %v", err)
	}

	grantTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin grant tx: %v", err)
	}
	if err = grantTx.QueryRow(ctx, `
		SELECT research_artifact_reserve_policy_watermark($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		grantTx.Rollback(ctx)
		t.Fatalf("reserve grant watermark: %v", err)
	}
	if _, err = grantTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_grant (
		  id, workspace_id, session_id, principal_kind, principal_id, purpose,
		  normal_clearance, revision, status
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'user', $4::uuid, 'ordinary', 'raw', 1, 'active'
		)
	`, grantID, workspaceID, sessionID, userID); err != nil {
		grantTx.Rollback(ctx)
		t.Fatalf("insert grant: %v", err)
	}
	if _, err = grantTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
		  old_grant_revision, new_grant_revision, new_grant_status
		) VALUES ($1::uuid, $2::uuid, $3, 'grant_create', $4::uuid, 0, 1, 'active')
	`, workspaceID, sessionID, watermark, grantID); err != nil {
		grantTx.Rollback(ctx)
		t.Fatalf("insert grant_create mutation: %v", err)
	}
	if _, err = grantTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		grantTx.Rollback(ctx)
		t.Fatalf("set grant constraints immediate: %v", err)
	}
	if err = grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit grant coupling tx: %v", err)
	}

	negativeTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin negative tx: %v", err)
	}
	if _, err = negativeTx.Exec(ctx, `
		UPDATE research_source_snapshot SET verification_status = 'rejected' WHERE id = $1::uuid
	`, snapshotID); err != nil {
		negativeTx.Rollback(ctx)
		t.Fatalf("update snapshot without mutation: %v", err)
	}
	if err = negativeTx.Commit(ctx); err == nil {
		t.Fatal("expected verification update without policy mutation to fail on commit")
	} else {
		negativeTx.Rollback(ctx)
	}

	if _, err = conn.Exec(ctx, down321); err != nil {
		t.Fatalf("apply 321 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up321); err != nil {
		t.Fatalf("reapply 321 up: %v", err)
	}
}

func TestResearchArtifactPolicyLedgerGuards322RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_policy_322_test_%d", time.Now().UnixNano())
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

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	taskID := "30000000-0000-4000-8000-000000000003"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, down322 := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	for _, upSQL := range []string{up318, up319, up320, up321, up322} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	positiveTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin positive tx: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("insert policy state: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'ledger-positive', 1, 1)
	`, taskID, workspaceID, sessionID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("insert task: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
	`, workspaceID, sessionID, taskID); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("register task passport: %v", err)
	}
	if _, err = positiveTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		positiveTx.Rollback(ctx)
		t.Fatalf("set constraints immediate: %v", err)
	}
	if err = positiveTx.Commit(ctx); err != nil {
		t.Fatalf("commit ledger coupling tx: %v", err)
	}

	var mutationCount int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_policy_mutation
		WHERE artifact_id = $1::uuid AND mutation_kind = 'artifact_create'
	`, taskID).Scan(&mutationCount); err != nil || mutationCount != 1 {
		t.Fatalf("artifact_create mutations=%d err=%v", mutationCount, err)
	}

	for index, mode := range artifactConstraintModes {
		t.Run("artifact_create_one_sided/"+mode.name, func(t *testing.T) {
			negativeTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin negative tx: %v", beginErr)
			}
			defer negativeTx.Rollback(ctx)
			orphanID := fmt.Sprintf("30000000-0000-4000-8000-%012d", 90+index)
			if _, execErr := negativeTx.Exec(ctx, `
				INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
				VALUES ($1::uuid, $2::uuid, $3::uuid, $4, 1, 1);
				INSERT INTO research_artifact_passport (
				  id, workspace_id, session_id, entity_kind, eligibility_revision,
				  lifecycle_status, provenance_completeness, registered_at
				) VALUES (
				  $1::uuid, $2::uuid, $3::uuid, 'task', 1, 'registered', 'partial', now()
				);
			`, pgx.QueryExecModeSimpleProtocol, orphanID, workspaceID, sessionID, "ledger-orphan-"+mode.name); execErr != nil {
				t.Fatalf("insert paired domain/passport without ledger: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, negativeTx, mode.immediate),
				"research_artifact_passport_to_policy_mutation_guard",
			)
		})

		t.Run("eligibility_passport_only/"+mode.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			if _, execErr := tx.Exec(ctx, `
				UPDATE research_artifact_passport
				SET eligibility_revision=2
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
			`, workspaceID, sessionID, taskID); execErr != nil {
				t.Fatalf("update passport without eligibility ledger: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, tx, mode.immediate),
				"research_artifact_passport_to_policy_mutation_guard",
			)
		})

		t.Run("eligibility_ledger_only/"+mode.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			var mutationWatermark int64
			if queryErr := tx.QueryRow(ctx, `SELECT research_artifact_reserve_policy_watermark($1::uuid,$2::uuid)`, workspaceID, sessionID).Scan(&mutationWatermark); queryErr != nil {
				t.Fatal(queryErr)
			}
			if _, execErr := tx.Exec(ctx, `
				INSERT INTO research_artifact_policy_mutation (
				  workspace_id,session_id,watermark,mutation_kind,artifact_id,
				  old_eligibility_revision,new_eligibility_revision
				) VALUES ($1::uuid,$2::uuid,$4,'eligibility',$3::uuid,1,2)
			`, workspaceID, sessionID, taskID, mutationWatermark); execErr != nil {
				t.Fatalf("insert eligibility ledger without passport change: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, tx, mode.immediate),
				"research_artifact_policy_mutation_to_passport_guard",
			)
		})
	}

	if _, err = conn.Exec(ctx, down322); err != nil {
		t.Fatalf("apply 322 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up322); err != nil {
		t.Fatalf("reapply 322 up: %v", err)
	}
}

const researchArtifactIntegrityTestDDL = `
CREATE TABLE IF NOT EXISTS agent (id UUID PRIMARY KEY);
INSERT INTO agent (id) VALUES ('60000000-0000-4000-8000-000000000001') ON CONFLICT DO NOTHING;
ALTER TABLE research_task_attempt
  ADD COLUMN IF NOT EXISTS assigned_agent_id UUID NOT NULL DEFAULT '60000000-0000-4000-8000-000000000001',
  ADD COLUMN IF NOT EXISTS execution_adapter TEXT NOT NULL DEFAULT 'agent_inbox',
  ADD COLUMN IF NOT EXISTS provider TEXT NOT NULL DEFAULT 'openai',
  ADD COLUMN IF NOT EXISTS model TEXT NOT NULL DEFAULT 'composer-1.5',
  ADD COLUMN IF NOT EXISTS client_request_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS result_hash TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS result JSONB NOT NULL DEFAULT '{}'::jsonb;
`

func TestResearchArtifactIntegrityGuards323RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_integrity_323_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifactIntegrityTestDDL); err != nil {
		t.Fatalf("extend attempt schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	taskID := "30000000-0000-4000-8000-000000000003"
	otherTaskID := "30000000-0000-4000-8000-000000000099"
	attemptID := "30000000-0000-4000-8000-000000000004"
	resultID := "30000000-0000-4000-8000-000000000005"
	agentID := "60000000-0000-4000-8000-000000000001"
	resultHash := "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	requestID := "result-request-323"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'integrity-task', 1, 1)
	`, taskID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'integrity-other-task', 1, 1)
	`, otherTaskID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed other task: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_attempt (
		  id, workspace_id, session_id, task_id, assigned_agent_id,
		  execution_adapter, provider, model, client_request_id, result_hash, result
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
		  'agent_inbox', 'openai', 'composer-1.5', $6, $7, '{}'::jsonb
		)
	`, attemptID, workspaceID, sessionID, taskID, agentID, requestID, resultHash); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, down323 := readMigrationPair(t, "323_research_artifact_integrity_guards")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	producerTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin producer tx: %v", err)
	}
	if _, err = producerTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID); err != nil {
		producerTx.Rollback(ctx)
		t.Fatalf("insert policy state: %v", err)
	}
	if _, err = producerTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
	`, workspaceID, sessionID, taskID); err != nil {
		producerTx.Rollback(ctx)
		t.Fatalf("register task passport: %v", err)
	}
	if _, err = producerTx.Exec(ctx, `
		INSERT INTO research_artifact_version (
		  workspace_id, session_id, artifact_id, version,
		  schema_name, schema_version, canonicalization_version,
		  content_hash, access_level, goal_version, plan_version, hash_origin,
		  produced_by_task_id, produced_by_attempt_id, produced_by_agent_id,
		  model, provider, execution_adapter
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 2,
		  'task', 'legacy-v1', 'research-artifact-c14n-v1',
		  'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 'raw', 1, 1, 'production',
		  $4::uuid, $5::uuid, $6::uuid,
		  'composer-1.5', 'openai', 'agent_inbox'
		)
	`, workspaceID, sessionID, taskID, taskID, attemptID, agentID); err != nil {
		producerTx.Rollback(ctx)
		t.Fatalf("insert version with producer facts: %v", err)
	}
	if _, err = producerTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		producerTx.Rollback(ctx)
		t.Fatalf("set producer constraints immediate: %v", err)
	}
	if err = producerTx.Commit(ctx); err != nil {
		t.Fatalf("commit producer guard tx: %v", err)
	}

	projectionTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin projection tx: %v", err)
	}
	if _, err = projectionTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'result_artifact', now(), NULL, NULL)
	`, workspaceID, sessionID, resultID); err != nil {
		projectionTx.Rollback(ctx)
		t.Fatalf("register result passport: %v", err)
	}
	if _, err = projectionTx.Exec(ctx, `
		INSERT INTO research_result_artifact (
		  id, workspace_id, session_id, attempt_id, client_request_id, content_hash, result
		) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5, $6, '{}'::jsonb)
	`, resultID, workspaceID, sessionID, attemptID, requestID, resultHash); err != nil {
		projectionTx.Rollback(ctx)
		t.Fatalf("insert result artifact: %v", err)
	}
	if _, err = projectionTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		projectionTx.Rollback(ctx)
		t.Fatalf("set projection constraints immediate: %v", err)
	}
	if err = projectionTx.Commit(ctx); err != nil {
		t.Fatalf("commit projection guard tx: %v", err)
	}

	for _, mode := range artifactConstraintModes {
		t.Run("producer_attempt_task_mismatch/"+mode.name, func(t *testing.T) {
			badProducerTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin bad producer tx: %v", beginErr)
			}
			defer badProducerTx.Rollback(ctx)
			if _, execErr := badProducerTx.Exec(ctx, `
				INSERT INTO research_artifact_version (
				  workspace_id, session_id, artifact_id, version,
				  schema_name, schema_version, canonicalization_version,
				  content_hash, access_level, goal_version, plan_version, hash_origin,
				  produced_by_task_id, produced_by_attempt_id
				) VALUES (
				  $1::uuid, $2::uuid, $3::uuid, 3,
				  'task', 'legacy-v1', 'research-artifact-c14n-v1',
				  'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', 'raw', 1, 1, 'production',
				  $4::uuid, $5::uuid
				)
			`, workspaceID, sessionID, taskID, otherTaskID, attemptID); execErr != nil {
				t.Fatalf("insert mismatched producer version: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, badProducerTx, mode.immediate),
				"research_artifact_version_producer_guard",
			)
		})

		t.Run("result_attempt_projection_mismatch/"+mode.name, func(t *testing.T) {
			badProjectionTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin bad projection tx: %v", beginErr)
			}
			defer badProjectionTx.Rollback(ctx)
			if _, execErr := badProjectionTx.Exec(ctx, `
				UPDATE research_result_artifact
				SET content_hash = 'sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd'
				WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
			`, workspaceID, sessionID, resultID); execErr != nil {
				t.Fatalf("update mismatched result projection: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, badProjectionTx, mode.immediate),
				"research_result_attempt_projection_guard",
			)
		})
	}

	if _, err = conn.Exec(ctx, down323); err != nil {
		t.Fatalf("apply 323 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up323); err != nil {
		t.Fatalf("reapply 323 up: %v", err)
	}
}

func TestResearchArtifactLinkPolicyGuards324RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_link_policy_324_test_%d", time.Now().UnixNano())
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

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	supersededTaskID := "30000000-0000-4000-8000-000000000003"
	successorTaskID := "30000000-0000-4000-8000-000000000004"
	lifecycleTaskID := "30000000-0000-4000-8000-000000000005"
	decisionID := "40000000-0000-4000-8000-000000000010"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_decision (id, workspace_id, session_id, decision_kind)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'evaluation')
	`, decisionID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed decision: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up324, down324 := readMigrationPair(t, "324_research_artifact_link_policy_guards")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323, up324} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	supersessionTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin supersession tx: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("insert policy state: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES
		  ($1::uuid, $3::uuid, $4::uuid, 'superseded-task', 1, 1),
		  ($2::uuid, $3::uuid, $4::uuid, 'successor-task', 1, 1)
	`, supersededTaskID, successorTaskID, workspaceID, sessionID); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("insert tasks: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
	`, workspaceID, sessionID, supersededTaskID); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("register superseded passport: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
	`, workspaceID, sessionID, successorTaskID); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("register successor passport: %v", err)
	}
	if err = supersessionTx.Commit(ctx); err != nil {
		t.Fatalf("commit supersession seed tx: %v", err)
	}

	var supersededVersionID, successorVersionID string
	if err = conn.QueryRow(ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid AND version = 1
	`, workspaceID, sessionID, supersededTaskID).Scan(&supersededVersionID); err != nil {
		t.Fatalf("load superseded version id: %v", err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid AND version = 1
	`, workspaceID, sessionID, successorTaskID).Scan(&successorVersionID); err != nil {
		t.Fatalf("load successor version id: %v", err)
	}

	supersessionTx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin supersession guard tx: %v", err)
	}
	var watermark int64
	if err = supersessionTx.QueryRow(ctx, `
		SELECT research_artifact_reserve_policy_watermark($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("reserve supersession watermark: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET eligibility_revision = 2, lifecycle_status = 'superseded'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, supersededTaskID); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("bump superseded passport: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision
		) VALUES ($1::uuid, $2::uuid, $3, 'supersession', $4::uuid, 1, 2)
	`, workspaceID, sessionID, watermark, supersededTaskID); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("insert supersession mutation: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `
		INSERT INTO research_artifact_supersession (
		  workspace_id, session_id, successor_version_id, superseded_version_id,
		  superseded_artifact_id, reason, decision_id, policy_watermark,
		  old_eligibility_revision, new_eligibility_revision
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid,
		  'superseded by successor', $6::uuid, $7, 1, 2
		)
	`, workspaceID, sessionID, successorVersionID, supersededVersionID, supersededTaskID, decisionID, watermark); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("insert supersession edge: %v", err)
	}
	if _, err = supersessionTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		supersessionTx.Rollback(ctx)
		t.Fatalf("set supersession constraints immediate: %v", err)
	}
	if err = supersessionTx.Commit(ctx); err != nil {
		t.Fatalf("commit supersession guard tx: %v", err)
	}

	lifecycleTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lifecycle seed tx: %v", err)
	}
	if _, err = lifecycleTx.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'lifecycle-task', 1, 1)
	`, lifecycleTaskID, workspaceID, sessionID); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("insert lifecycle task: %v", err)
	}
	if _, err = lifecycleTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
	`, workspaceID, sessionID, lifecycleTaskID); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("register lifecycle passport: %v", err)
	}
	if err = lifecycleTx.Commit(ctx); err != nil {
		t.Fatalf("commit lifecycle seed tx: %v", err)
	}

	lifecycleTx, err = conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lifecycle guard tx: %v", err)
	}
	if err = lifecycleTx.QueryRow(ctx, `
		SELECT research_artifact_reserve_policy_watermark($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("reserve lifecycle watermark: %v", err)
	}
	if _, err = lifecycleTx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET eligibility_revision = 2, lifecycle_status = 'accepted'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, lifecycleTaskID); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("bump lifecycle passport: %v", err)
	}
	if _, err = lifecycleTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision,
		  old_lifecycle_status, new_lifecycle_status
		) VALUES ($1::uuid, $2::uuid, $3, 'lifecycle', $4::uuid, 1, 2, 'registered', 'accepted')
	`, workspaceID, sessionID, watermark, lifecycleTaskID); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("insert lifecycle mutation: %v", err)
	}
	if _, err = lifecycleTx.Exec(ctx, `
		INSERT INTO research_artifact_lifecycle_event (
		  workspace_id, session_id, artifact_id,
		  old_status, new_status, old_eligibility_revision, new_eligibility_revision,
		  policy_watermark, reason
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'registered', 'accepted', 1, 2, $4, 'accepted for dispatch')
	`, workspaceID, sessionID, lifecycleTaskID, watermark); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("insert lifecycle event: %v", err)
	}
	if _, err = lifecycleTx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
		lifecycleTx.Rollback(ctx)
		t.Fatalf("set lifecycle constraints immediate: %v", err)
	}
	if err = lifecycleTx.Commit(ctx); err != nil {
		t.Fatalf("commit lifecycle guard tx: %v", err)
	}

	orphanSupersessionTaskID := "30000000-0000-4000-8000-000000000006"
	orphanLifecycleTaskID := "30000000-0000-4000-8000-000000000007"
	orphanSeedTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin one-sided fixture seed: %v", err)
	}
	defer orphanSeedTx.Rollback(ctx)
	if _, err = orphanSeedTx.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES
		  ($1::uuid, $3::uuid, $4::uuid, 'orphan-supersession-task', 1, 1),
		  ($2::uuid, $3::uuid, $4::uuid, 'orphan-lifecycle-task', 1, 1)
	`, orphanSupersessionTaskID, orphanLifecycleTaskID, workspaceID, sessionID); err != nil {
		t.Fatalf("insert one-sided fixture tasks: %v", err)
	}
	for _, artifactID := range []string{orphanSupersessionTaskID, orphanLifecycleTaskID} {
		if _, err = orphanSeedTx.Exec(ctx, `
			SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
		`, workspaceID, sessionID, artifactID); err != nil {
			t.Fatalf("register one-sided fixture passport: %v", err)
		}
	}
	if err = orphanSeedTx.Commit(ctx); err != nil {
		t.Fatalf("commit one-sided fixture seed: %v", err)
	}
	var orphanSupersededVersionID string
	if err = conn.QueryRow(ctx, `
		SELECT id::text FROM research_artifact_version
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid AND version=1
	`, workspaceID, sessionID, orphanSupersessionTaskID).Scan(&orphanSupersededVersionID); err != nil {
		t.Fatalf("load one-sided supersession version: %v", err)
	}

	for _, mode := range artifactConstraintModes {
		t.Run("supersession_edge_only/"+mode.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			if _, execErr := tx.Exec(ctx, `
				INSERT INTO research_artifact_supersession (
				  workspace_id, session_id, successor_version_id, superseded_version_id,
				  superseded_artifact_id, reason, decision_id, policy_watermark,
				  old_eligibility_revision, new_eligibility_revision
				) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,'one-sided',$6::uuid,999,1,2)
			`, workspaceID, sessionID, successorVersionID, orphanSupersededVersionID, orphanSupersessionTaskID, decisionID); execErr != nil {
				t.Fatalf("insert edge-only supersession: %v", execErr)
			}
			assertArtifactConstraintOneOf(t,
				commitOrForceArtifactConstraints(ctx, tx, mode.immediate),
				"research_artifact_supersession_policy_mutation_fkey",
				"research_artifact_supersession_to_policy_guard",
			)
		})

		t.Run("supersession_ledger_only/"+mode.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			var mutationWatermark int64
			if queryErr := tx.QueryRow(ctx, `SELECT research_artifact_reserve_policy_watermark($1::uuid,$2::uuid)`, workspaceID, sessionID).Scan(&mutationWatermark); queryErr != nil {
				t.Fatal(queryErr)
			}
			if _, execErr := tx.Exec(ctx, `
				UPDATE research_artifact_passport SET eligibility_revision=2,lifecycle_status='superseded'
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid;
				INSERT INTO research_artifact_policy_mutation (
				  workspace_id,session_id,watermark,mutation_kind,artifact_id,
				  old_eligibility_revision,new_eligibility_revision
				) VALUES ($1::uuid,$2::uuid,$4,'supersession',$3::uuid,1,2);
			`, pgx.QueryExecModeSimpleProtocol, workspaceID, sessionID, orphanSupersessionTaskID, mutationWatermark); execErr != nil {
				t.Fatalf("insert ledger-only supersession: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, tx, mode.immediate),
				"research_artifact_policy_mutation_to_supersession_guard",
			)
		})

		t.Run("lifecycle_event_only/"+mode.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			if _, execErr := tx.Exec(ctx, `
				INSERT INTO research_artifact_lifecycle_event (
				  workspace_id,session_id,artifact_id,old_status,new_status,
				  old_eligibility_revision,new_eligibility_revision,policy_watermark,reason
				) VALUES ($1::uuid,$2::uuid,$3::uuid,'registered','accepted',1,2,999,'one-sided')
			`, workspaceID, sessionID, orphanLifecycleTaskID); execErr != nil {
				t.Fatalf("insert event-only lifecycle: %v", execErr)
			}
			assertArtifactConstraintOneOf(t,
				commitOrForceArtifactConstraints(ctx, tx, mode.immediate),
				"research_artifact_lifecycle_event_policy_mutation_fkey",
				"research_artifact_lifecycle_event_to_policy_guard",
			)
		})

		t.Run("lifecycle_ledger_only/"+mode.name, func(t *testing.T) {
			tx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatal(beginErr)
			}
			defer tx.Rollback(ctx)
			var mutationWatermark int64
			if queryErr := tx.QueryRow(ctx, `SELECT research_artifact_reserve_policy_watermark($1::uuid,$2::uuid)`, workspaceID, sessionID).Scan(&mutationWatermark); queryErr != nil {
				t.Fatal(queryErr)
			}
			if _, execErr := tx.Exec(ctx, `
				UPDATE research_artifact_passport SET eligibility_revision=2,lifecycle_status='accepted'
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid;
				INSERT INTO research_artifact_policy_mutation (
				  workspace_id,session_id,watermark,mutation_kind,artifact_id,
				  old_eligibility_revision,new_eligibility_revision,old_lifecycle_status,new_lifecycle_status
				) VALUES ($1::uuid,$2::uuid,$4,'lifecycle',$3::uuid,1,2,'registered','accepted');
			`, pgx.QueryExecModeSimpleProtocol, workspaceID, sessionID, orphanLifecycleTaskID, mutationWatermark); execErr != nil {
				t.Fatalf("insert ledger-only lifecycle: %v", execErr)
			}
			assertArtifactConstraint(t,
				commitOrForceArtifactConstraints(ctx, tx, mode.immediate),
				"research_artifact_policy_mutation_to_lifecycle_event_guard",
			)
		})
	}

	if _, err = conn.Exec(ctx, down324); err != nil {
		t.Fatalf("apply 324 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up324); err != nil {
		t.Fatalf("reapply 324 up: %v", err)
	}
}

const researchArtifactDiagnosticTestDDL = `
ALTER TABLE research_message
  ADD COLUMN IF NOT EXISTS meta JSONB NOT NULL DEFAULT '{}'::jsonb;
`

func TestResearchArtifactMigrationDiagnostics325RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_diagnostic_325_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifactDiagnosticTestDDL); err != nil {
		t.Fatalf("extend message schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	otherSessionID := "20000000-0000-4000-8000-000000000002"
	validNodeID := "30000000-0000-4000-8000-000000000010"
	crossScopeNodeID := "30000000-0000-4000-8000-000000000011"
	anchorMessageID := "30000000-0000-4000-8000-000000000020"
	validMessageID := "30000000-0000-4000-8000-000000000030"
	brokenMessageID := "30000000-0000-4000-8000-000000000031"
	crossScopeMessageID := "30000000-0000-4000-8000-000000000032"
	missingNodeID := "30000000-0000-4000-8000-000000000099"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	for _, seed := range []struct {
		id string
	}{
		{sessionID},
		{otherSessionID},
	} {
		if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, seed.id, workspaceID); err != nil {
			t.Fatalf("seed session %s: %v", seed.id, err)
		}
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_graph_node (id, workspace_id, session_id)
		VALUES ($1::uuid, $3::uuid, $4::uuid), ($2::uuid, $3::uuid, $5::uuid)
	`, validNodeID, crossScopeNodeID, workspaceID, sessionID, otherSessionID); err != nil {
		t.Fatalf("seed graph nodes: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_message (id, workspace_id, session_id)
		VALUES ($1::uuid, $2::uuid, $3::uuid)
	`, anchorMessageID, workspaceID, otherSessionID); err != nil {
		t.Fatalf("seed anchor message: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_message (id, workspace_id, session_id, meta)
		VALUES
		  ($1::uuid, $4::uuid, $5::uuid, $6::jsonb),
		  ($2::uuid, $4::uuid, $5::uuid, $7::jsonb),
		  ($3::uuid, $4::uuid, $5::uuid, $8::jsonb)
	`, validMessageID, brokenMessageID, crossScopeMessageID, workspaceID, sessionID,
		fmt.Sprintf(`{"match_decision":{"matched_node_ids":["%s"],"decisions":[{"node_id":"%s","action":"continue"}]}}`, validNodeID, validNodeID),
		fmt.Sprintf(`{"match_decision":{"matched_node_ids":["%s"],"decisions":[{"node_id":"%s","action":"continue"}]}}`, missingNodeID, missingNodeID),
		fmt.Sprintf(`{"match_decision":{"utterance_id":"%s","matched_node_ids":["%s"],"decisions":[{"node_id":"%s","action":"continue"}]}}`, anchorMessageID, crossScopeNodeID, crossScopeNodeID),
	); err != nil {
		t.Fatalf("seed messages: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up324, _ := readMigrationPair(t, "324_research_artifact_link_policy_guards")
	up325, down325 := readMigrationPair(t, "325_research_artifact_migration_diagnostics")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323, up324, up325} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	var diagnosticCount int
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_scan_session_message_migration_diagnostics($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&diagnosticCount); err != nil {
		t.Fatalf("scan session diagnostics: %v", err)
	}
	if diagnosticCount < 3 {
		t.Fatalf("diagnostic count=%d want at least 3", diagnosticCount)
	}

	var brokenReason string
	if err = conn.QueryRow(ctx, `
		SELECT reason_code FROM research_artifact_migration_diagnostic
		WHERE owner_id = $1::uuid AND field_path = '/meta/match_decision/matched_node_ids/0'
	`, brokenMessageID).Scan(&brokenReason); err != nil || brokenReason != "unresolved_reference" {
		t.Fatalf("broken reason=%q err=%v", brokenReason, err)
	}

	var crossScopeReason string
	if err = conn.QueryRow(ctx, `
		SELECT reason_code FROM research_artifact_migration_diagnostic
		WHERE owner_id = $1::uuid AND field_path = '/meta/match_decision/utterance_id'
	`, crossScopeMessageID).Scan(&crossScopeReason); err != nil || crossScopeReason != "cross_scope_reference" {
		t.Fatalf("cross-scope reason=%q err=%v", crossScopeReason, err)
	}

	var validCount int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_migration_diagnostic
		WHERE owner_id = $1::uuid
	`, validMessageID).Scan(&validCount); err != nil || validCount != 0 {
		t.Fatalf("valid message diagnostics=%d err=%v", validCount, err)
	}

	if _, err = conn.Exec(ctx, `
		UPDATE research_message
		SET meta = $2::jsonb
		WHERE workspace_id = $3::uuid AND session_id = $4::uuid AND id = $1::uuid
	`, brokenMessageID,
		fmt.Sprintf(`{"match_decision":{"matched_node_ids":["%s"],"decisions":[{"node_id":"%s","action":"continue"}]}}`, validNodeID, validNodeID),
		workspaceID, sessionID,
	); err != nil {
		t.Fatalf("repair broken message: %v", err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_scan_research_message_migration_diagnostics($1::uuid, $2::uuid, $3::uuid)
	`, workspaceID, sessionID, brokenMessageID).Scan(&diagnosticCount); err != nil {
		t.Fatalf("rescan repaired message: %v", err)
	}
	if diagnosticCount != 0 {
		t.Fatalf("repaired message diagnostics=%d want 0", diagnosticCount)
	}

	if _, err = conn.Exec(ctx, down325); err != nil {
		t.Fatalf("apply 325 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up325); err != nil {
		t.Fatalf("reapply 325 up: %v", err)
	}
}

const researchArtifactScopedFKTestDDL = `
CREATE TABLE IF NOT EXISTS research_task_dependency (
  task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  depends_on_task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (task_id, depends_on_task_id),
  CHECK (task_id <> depends_on_task_id)
);
ALTER TABLE research_question
  ADD COLUMN IF NOT EXISTS parent_question_id UUID REFERENCES research_question(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS created_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS answer_claim_id UUID REFERENCES research_claim(id) ON DELETE SET NULL;
ALTER TABLE research_task
  ADD COLUMN IF NOT EXISTS question_id UUID REFERENCES research_question(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS parent_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL;
ALTER TABLE research_source_snapshot
  ADD COLUMN IF NOT EXISTS produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL;
ALTER TABLE research_observation
  ADD COLUMN IF NOT EXISTS source_snapshot_id UUID REFERENCES research_source_snapshot(id) ON DELETE CASCADE,
  ADD COLUMN IF NOT EXISTS produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL;
ALTER TABLE research_claim
  ADD COLUMN IF NOT EXISTS produced_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL;
ALTER TABLE research_claim_evidence
  ADD COLUMN IF NOT EXISTS verified_by_task_id UUID REFERENCES research_task(id) ON DELETE SET NULL;
ALTER TABLE research_source
  ADD COLUMN IF NOT EXISTS source_snapshot_id UUID REFERENCES research_source_snapshot(id) ON DELETE SET NULL;
ALTER TABLE research_task_attempt
  ADD CONSTRAINT research_task_attempt_task_id_fkey
  FOREIGN KEY (task_id) REFERENCES research_task(id) ON DELETE CASCADE;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_claim_id_fkey
  FOREIGN KEY (claim_id) REFERENCES research_claim(id) ON DELETE CASCADE;
ALTER TABLE research_claim_evidence
  ADD CONSTRAINT research_claim_evidence_observation_id_fkey
  FOREIGN KEY (observation_id) REFERENCES research_observation(id) ON DELETE CASCADE;
CREATE TABLE IF NOT EXISTS research_report_claim (
  report_id UUID NOT NULL REFERENCES research_report(id) ON DELETE CASCADE,
  claim_id UUID NOT NULL REFERENCES research_claim(id) ON DELETE CASCADE,
  section_id TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (report_id, claim_id, section_id)
);
ALTER TABLE research_graph_edge
  ADD COLUMN IF NOT EXISTS from_node_id UUID,
  ADD COLUMN IF NOT EXISTS to_node_id UUID;
`

func TestResearchArtifactScopedRelationshipFKs326RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_scoped_fk_326_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifactScopedFKTestDDL); err != nil {
		t.Fatalf("extend scoped fk schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	otherWorkspaceID := "10000000-0000-4000-8000-000000000002"
	sessionID := "20000000-0000-4000-8000-000000000001"
	otherSessionID := "20000000-0000-4000-8000-000000000002"
	otherWorkspaceSessionID := "20000000-0000-4000-8000-000000000003"
	localTaskID := "30000000-0000-4000-8000-000000000003"
	localDependsTaskID := "30000000-0000-4000-8000-000000000004"
	foreignTaskID := "30000000-0000-4000-8000-000000000005"
	otherWorkspaceTaskID := "30000000-0000-4000-8000-000000000006"
	localQuestionID := "31000000-0000-4000-8000-000000000001"
	localParentQuestionID := "31000000-0000-4000-8000-000000000002"
	foreignQuestionID := "31000000-0000-4000-8000-000000000003"
	otherWorkspaceQuestionID := "31000000-0000-4000-8000-000000000004"
	localClaimID := "32000000-0000-4000-8000-000000000001"
	foreignClaimID := "32000000-0000-4000-8000-000000000002"
	otherWorkspaceClaimID := "32000000-0000-4000-8000-000000000003"
	localSnapshotID := "33000000-0000-4000-8000-000000000001"
	foreignSnapshotID := "33000000-0000-4000-8000-000000000002"
	otherWorkspaceSnapshotID := "33000000-0000-4000-8000-000000000003"
	localObservationID := "34000000-0000-4000-8000-000000000001"
	foreignObservationID := "34000000-0000-4000-8000-000000000002"
	otherWorkspaceObservationID := "34000000-0000-4000-8000-000000000003"
	localEvidenceID := "35000000-0000-4000-8000-000000000001"
	localSourceID := "36000000-0000-4000-8000-000000000001"
	localAttemptID := "37000000-0000-4000-8000-000000000001"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid),($2::uuid)`, workspaceID, otherWorkspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	for _, id := range []string{sessionID, otherSessionID} {
		if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, id, workspaceID); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id,workspace_id) VALUES ($1::uuid,$2::uuid)`, otherWorkspaceSessionID, otherWorkspaceID); err != nil {
		t.Fatalf("seed other workspace session: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES
		  ($1::uuid, $5::uuid, $6::uuid, 'local-task', 1, 1),
		  ($2::uuid, $5::uuid, $6::uuid, 'local-depends', 1, 1),
		  ($3::uuid, $5::uuid, $7::uuid, 'foreign-task', 1, 1),
		  ($4::uuid, $8::uuid, $9::uuid, 'other-workspace-task', 1, 1)
	`, localTaskID, localDependsTaskID, foreignTaskID, otherWorkspaceTaskID,
		workspaceID, sessionID, otherSessionID, otherWorkspaceID, otherWorkspaceSessionID); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_question (id,workspace_id,session_id,client_key,goal_version,plan_version) VALUES
		 ($1::uuid,$5::uuid,$6::uuid,'local-question',1,1),
		 ($2::uuid,$5::uuid,$6::uuid,'local-parent-question',1,1),
		 ($3::uuid,$5::uuid,$7::uuid,'foreign-question',1,1),
		 ($4::uuid,$8::uuid,$9::uuid,'other-workspace-question',1,1)
	`, localQuestionID, localParentQuestionID, foreignQuestionID, otherWorkspaceQuestionID,
		workspaceID, sessionID, otherSessionID, otherWorkspaceID, otherWorkspaceSessionID); err != nil {
		t.Fatalf("seed questions: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_claim (id,workspace_id,session_id,goal_version,plan_version) VALUES
		 ($1::uuid,$4::uuid,$5::uuid,1,1),($2::uuid,$4::uuid,$6::uuid,1,1),($3::uuid,$7::uuid,$8::uuid,1,1)
	`, localClaimID, foreignClaimID, otherWorkspaceClaimID, workspaceID, sessionID,
		otherSessionID, otherWorkspaceID, otherWorkspaceSessionID); err != nil {
		t.Fatalf("seed claims: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_source_snapshot (id,workspace_id,session_id) VALUES
		 ($1::uuid,$4::uuid,$5::uuid),($2::uuid,$4::uuid,$6::uuid),($3::uuid,$7::uuid,$8::uuid)
	`, localSnapshotID, foreignSnapshotID, otherWorkspaceSnapshotID, workspaceID, sessionID,
		otherSessionID, otherWorkspaceID, otherWorkspaceSessionID); err != nil {
		t.Fatalf("seed snapshots: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_observation (id,workspace_id,session_id) VALUES
		 ($1::uuid,$4::uuid,$5::uuid),($2::uuid,$4::uuid,$6::uuid),($3::uuid,$7::uuid,$8::uuid)
	`, localObservationID, foreignObservationID, otherWorkspaceObservationID, workspaceID, sessionID,
		otherSessionID, otherWorkspaceID, otherWorkspaceSessionID); err != nil {
		t.Fatalf("seed observations: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_source (id,workspace_id,session_id) VALUES ($1::uuid,$2::uuid,$3::uuid)`,
		localSourceID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed source: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_claim_evidence (id,workspace_id,session_id,claim_id,observation_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid)
	`, localEvidenceID, workspaceID, sessionID, localClaimID, localObservationID); err != nil {
		t.Fatalf("seed evidence: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_attempt (id,workspace_id,session_id,task_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid)
	`, localAttemptID, workspaceID, sessionID, localTaskID); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up324, _ := readMigrationPair(t, "324_research_artifact_link_policy_guards")
	up325, _ := readMigrationPair(t, "325_research_artifact_migration_diagnostics")
	up326, down326 := readMigrationPair(t, "326_research_artifact_scoped_relationship_fks")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323, up324, up325, up326} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_dependency (workspace_id, session_id, task_id, depends_on_task_id)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)
	`, workspaceID, sessionID, localTaskID, localDependsTaskID); err != nil {
		t.Fatalf("insert same-scope dependency: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		UPDATE research_task
		SET parent_task_id = $4::uuid
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, localTaskID, localDependsTaskID); err != nil {
		t.Fatalf("set same-scope parent task: %v", err)
	}
	positiveRelationships := []struct {
		name  string
		query string
		args  []any
	}{
		{"task question", `UPDATE research_task SET question_id=$1::uuid WHERE id=$2::uuid`, []any{localQuestionID, localTaskID}},
		{"question links", `UPDATE research_question SET parent_question_id=$1::uuid,created_by_task_id=$2::uuid,answer_claim_id=$3::uuid WHERE id=$4::uuid`, []any{localParentQuestionID, localTaskID, localClaimID, localQuestionID}},
		{"snapshot producer", `UPDATE research_source_snapshot SET produced_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{localTaskID, localSnapshotID}},
		{"observation links", `UPDATE research_observation SET source_snapshot_id=$1::uuid,produced_by_task_id=$2::uuid WHERE id=$3::uuid`, []any{localSnapshotID, localTaskID, localObservationID}},
		{"claim producer", `UPDATE research_claim SET produced_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{localTaskID, localClaimID}},
		{"evidence verifier", `UPDATE research_claim_evidence SET verified_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{localTaskID, localEvidenceID}},
		{"source snapshot", `UPDATE research_source SET source_snapshot_id=$1::uuid WHERE id=$2::uuid`, []any{localSnapshotID, localSourceID}},
	}
	for _, relationship := range positiveRelationships {
		if _, err = conn.Exec(ctx, relationship.query, relationship.args...); err != nil {
			t.Fatalf("set same-scope %s: %v", relationship.name, err)
		}
	}

	if _, err = conn.Exec(ctx, `
		UPDATE research_task
		SET parent_task_id = $4::uuid
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, localTaskID, foreignTaskID); err == nil {
		t.Fatal("expected cross-session parent_task_id update to fail")
	}
	if _, err = conn.Exec(ctx, `UPDATE research_task SET parent_task_id=$1::uuid WHERE id=$2::uuid`, otherWorkspaceTaskID, localTaskID); err == nil {
		t.Fatal("expected cross-workspace parent_task_id update to fail")
	}

	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_dependency (workspace_id, session_id, task_id, depends_on_task_id)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)
	`, workspaceID, sessionID, localTaskID, foreignTaskID); err == nil {
		t.Fatal("expected cross-session dependency insert to fail")
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_dependency (workspace_id,session_id,task_id,depends_on_task_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid)
	`, workspaceID, sessionID, localTaskID, otherWorkspaceTaskID); err == nil {
		t.Fatal("expected cross-workspace dependency insert to fail")
	}

	negativeRelationships := []struct {
		name               string
		query              string
		crossSessionArgs   []any
		crossWorkspaceArgs []any
	}{
		{"attempt task", `UPDATE research_task_attempt SET task_id=$1::uuid WHERE id=$2::uuid`, []any{foreignTaskID, localAttemptID}, []any{otherWorkspaceTaskID, localAttemptID}},
		{"task question", `UPDATE research_task SET question_id=$1::uuid WHERE id=$2::uuid`, []any{foreignQuestionID, localTaskID}, []any{otherWorkspaceQuestionID, localTaskID}},
		{"question parent", `UPDATE research_question SET parent_question_id=$1::uuid WHERE id=$2::uuid`, []any{foreignQuestionID, localQuestionID}, []any{otherWorkspaceQuestionID, localQuestionID}},
		{"question creator", `UPDATE research_question SET created_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{foreignTaskID, localQuestionID}, []any{otherWorkspaceTaskID, localQuestionID}},
		{"question answer", `UPDATE research_question SET answer_claim_id=$1::uuid WHERE id=$2::uuid`, []any{foreignClaimID, localQuestionID}, []any{otherWorkspaceClaimID, localQuestionID}},
		{"snapshot producer", `UPDATE research_source_snapshot SET produced_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{foreignTaskID, localSnapshotID}, []any{otherWorkspaceTaskID, localSnapshotID}},
		{"observation source", `UPDATE research_observation SET source_snapshot_id=$1::uuid WHERE id=$2::uuid`, []any{foreignSnapshotID, localObservationID}, []any{otherWorkspaceSnapshotID, localObservationID}},
		{"observation producer", `UPDATE research_observation SET produced_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{foreignTaskID, localObservationID}, []any{otherWorkspaceTaskID, localObservationID}},
		{"claim producer", `UPDATE research_claim SET produced_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{foreignTaskID, localClaimID}, []any{otherWorkspaceTaskID, localClaimID}},
		{"evidence claim", `UPDATE research_claim_evidence SET claim_id=$1::uuid WHERE id=$2::uuid`, []any{foreignClaimID, localEvidenceID}, []any{otherWorkspaceClaimID, localEvidenceID}},
		{"evidence observation", `UPDATE research_claim_evidence SET observation_id=$1::uuid WHERE id=$2::uuid`, []any{foreignObservationID, localEvidenceID}, []any{otherWorkspaceObservationID, localEvidenceID}},
		{"evidence verifier", `UPDATE research_claim_evidence SET verified_by_task_id=$1::uuid WHERE id=$2::uuid`, []any{foreignTaskID, localEvidenceID}, []any{otherWorkspaceTaskID, localEvidenceID}},
		{"legacy source snapshot", `UPDATE research_source SET source_snapshot_id=$1::uuid WHERE id=$2::uuid`, []any{foreignSnapshotID, localSourceID}, []any{otherWorkspaceSnapshotID, localSourceID}},
	}
	for _, relationship := range negativeRelationships {
		for _, boundary := range []struct {
			name string
			args []any
		}{{"cross-session", relationship.crossSessionArgs}, {"cross-workspace", relationship.crossWorkspaceArgs}} {
			t.Run(relationship.name+"/"+boundary.name, func(t *testing.T) {
				if _, updateErr := conn.Exec(ctx, relationship.query, boundary.args...); updateErr == nil {
					t.Fatalf("expected %s %s relationship to fail", boundary.name, relationship.name)
				}
			})
		}
	}

	if _, err = conn.Exec(ctx, down326); err != nil {
		t.Fatalf("apply 326 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up326); err != nil {
		t.Fatalf("reapply 326 up: %v", err)
	}
}

func TestResearchArtifactCanonicalizationRegistry327RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_c14n_327_test_%d", time.Now().UnixNano())
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

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up324, _ := readMigrationPair(t, "324_research_artifact_link_policy_guards")
	up325, _ := readMigrationPair(t, "325_research_artifact_migration_diagnostics")
	up327, down327 := readMigrationPair(t, "327_research_artifact_canonicalization_registry")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323, up324, up325, up327} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	var allowed bool
	if err = conn.QueryRow(ctx, `SELECT research_artifact_canonicalization_version_allowed('research-artifact-c14n-v1')`).Scan(&allowed); err != nil || !allowed {
		t.Fatalf("canonicalization version allowed=%v err=%v", allowed, err)
	}
	if err = conn.QueryRow(ctx, `SELECT research_artifact_canonicalization_version_allowed('bogus-c14n')`).Scan(&allowed); err != nil || allowed {
		t.Fatalf("unknown canonicalization version should fail closed: allowed=%v err=%v", allowed, err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_schema_family_allowed('run_session', 'legacy-v1', 'research-artifact-c14n-v1')
	`).Scan(&allowed); err != nil || !allowed {
		t.Fatalf("run_session schema family allowed=%v err=%v", allowed, err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_schema_family_allowed('run_session', 'v2', 'research-artifact-c14n-v1')
	`).Scan(&allowed); err != nil || allowed {
		t.Fatalf("unknown schema version should fail closed: allowed=%v err=%v", allowed, err)
	}

	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_version (
		  workspace_id, session_id, artifact_id, version,
		  schema_name, schema_version, canonicalization_version,
		  content_hash, access_level
		) VALUES (
		  $1::uuid, $2::uuid, $2::uuid, 2,
		  'hypothesis', 'legacy-v1', 'research-artifact-c14n-v1',
		  'sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc', 'raw'
		)
	`, workspaceID, sessionID); err == nil {
		t.Fatal("expected unknown schema family insert to fail")
	}

	if _, err = conn.Exec(ctx, down327); err != nil {
		t.Fatalf("apply 327 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up327); err != nil {
		t.Fatalf("reapply 327 up: %v", err)
	}
}

const researchArtifact328TestDDL = `
CREATE TABLE IF NOT EXISTS research_report_claim (
  report_id UUID NOT NULL REFERENCES research_report(id) ON DELETE CASCADE,
  claim_id UUID NOT NULL REFERENCES research_claim(id) ON DELETE CASCADE,
  section_id TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (report_id, claim_id, section_id)
);
ALTER TABLE research_graph_edge
  ADD COLUMN IF NOT EXISTS from_node_id UUID,
  ADD COLUMN IF NOT EXISTS to_node_id UUID;
ALTER TABLE research_decision
  ADD COLUMN IF NOT EXISTS actor_type TEXT NOT NULL DEFAULT 'system',
  ADD COLUMN IF NOT EXISTS inputs JSONB NOT NULL DEFAULT '{}'::jsonb;
ALTER TABLE research_run_event
  ADD COLUMN IF NOT EXISTS payload JSONB NOT NULL DEFAULT '{}'::jsonb;
`

const researchArtifact329TestDDL = `
ALTER TABLE research_session
  ADD COLUMN IF NOT EXISTS orchestrator_version TEXT NOT NULL DEFAULT '';
ALTER TABLE research_task_attempt
  ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'succeeded',
  ADD COLUMN IF NOT EXISTS client_request_id TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS result_hash TEXT NOT NULL DEFAULT '',
  ADD COLUMN IF NOT EXISTS result JSONB NOT NULL DEFAULT '{}'::jsonb,
  ADD COLUMN IF NOT EXISTS result_submitted_at TIMESTAMPTZ;
`

func TestResearchArtifactPassportDCompletion328RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_d328_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifact328TestDDL); err != nil {
		t.Fatalf("extend 328 schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	otherSessionID := "20000000-0000-4000-8000-000000000002"
	taskID := "30000000-0000-4000-8000-000000000003"
	decisionID := "30000000-0000-4000-8000-000000000010"
	eventID := "30000000-0000-4000-8000-000000000011"
	foreignTaskID := "30000000-0000-4000-8000-000000000099"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	for _, id := range []string{sessionID, otherSessionID} {
		if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, id, workspaceID); err != nil {
			t.Fatalf("seed session %s: %v", id, err)
		}
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'local-task', 1, 1), ($4::uuid, $2::uuid, $5::uuid, 'foreign-task', 1, 1)
	`, taskID, workspaceID, sessionID, foreignTaskID, otherSessionID); err != nil {
		t.Fatalf("seed tasks: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	for _, upSQL := range []string{up318, up319} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	if _, err = conn.Exec(ctx, `
		INSERT INTO research_decision (
		  id, workspace_id, session_id, decision_kind, actor_type,
		  goal_version, plan_version, inputs
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'research_method', 'system', 1, 1, $4::jsonb)
	`, decisionID, workspaceID, sessionID, fmt.Sprintf(`{"task_id":"%s"}`, foreignTaskID)); err != nil {
		t.Fatalf("insert decision: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_run_event (id,workspace_id,session_id,payload)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::jsonb)
	`, eventID, workspaceID, sessionID, fmt.Sprintf(`{"task_id":"%s"}`, foreignTaskID)); err != nil {
		t.Fatalf("insert run event: %v", err)
	}

	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up324, _ := readMigrationPair(t, "324_research_artifact_link_policy_guards")
	up325, _ := readMigrationPair(t, "325_research_artifact_migration_diagnostics")
	up327, _ := readMigrationPair(t, "327_research_artifact_canonicalization_registry")
	for _, upSQL := range []string{up320, up321, up322, up323, up324, up325, up327} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	up328, down328 := readMigrationPair(t, "328_research_artifact_passport_d_completion")
	if _, err = conn.Exec(ctx, up328); err != nil {
		t.Fatalf("apply migration: %v", err)
	}

	var diagnosticCount int
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_scan_research_decision_migration_diagnostics($1::uuid, $2::uuid, $3::uuid)
	`, workspaceID, sessionID, decisionID).Scan(&diagnosticCount); err != nil {
		t.Fatalf("scan decision diagnostics: %v", err)
	}
	if diagnosticCount != 1 {
		t.Fatalf("decision diagnostic count=%d want 1", diagnosticCount)
	}
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_scan_research_run_event_migration_diagnostics($1::uuid,$2::uuid,$3::uuid)
	`, workspaceID, sessionID, eventID).Scan(&diagnosticCount); err != nil {
		t.Fatalf("scan run event diagnostics: %v", err)
	}
	if diagnosticCount != 1 {
		t.Fatalf("run event diagnostic count=%d want 1", diagnosticCount)
	}

	type diagnosticFact struct {
		ownerKind    string
		fieldPath    string
		expectedKind string
		reference    string
		reason       string
	}
	rows, err := conn.Query(ctx, `
		SELECT owner_kind,field_path,expected_target_kind,reference_value,reason_code
		FROM research_artifact_migration_diagnostic
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND owner_id IN ($3::uuid,$4::uuid)
		ORDER BY owner_kind
	`, workspaceID, sessionID, decisionID, eventID)
	if err != nil {
		t.Fatalf("query diagnostic facts: %v", err)
	}
	var facts []diagnosticFact
	for rows.Next() {
		var fact diagnosticFact
		if err = rows.Scan(&fact.ownerKind, &fact.fieldPath, &fact.expectedKind, &fact.reference, &fact.reason); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		facts = append(facts, fact)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantFacts := []diagnosticFact{
		{ownerKind: "method_decision", fieldPath: "/inputs/task_id", expectedKind: "task", reference: foreignTaskID, reason: "cross_scope_reference"},
		{ownerKind: "run_event", fieldPath: "/payload/task_id", expectedKind: "task", reference: foreignTaskID, reason: "cross_scope_reference"},
	}
	if !reflect.DeepEqual(facts, wantFacts) {
		t.Fatalf("diagnostic facts=%+v want=%+v", facts, wantFacts)
	}

	if _, err = conn.Exec(ctx, `UPDATE research_decision SET inputs=jsonb_build_object('task_id',$1::text) WHERE id=$2::uuid`, taskID, decisionID); err != nil {
		t.Fatalf("repair decision input: %v", err)
	}
	if _, err = conn.Exec(ctx, `UPDATE research_run_event SET payload=jsonb_build_object('task_id',$1::text) WHERE id=$2::uuid`, taskID, eventID); err != nil {
		t.Fatalf("repair event payload: %v", err)
	}
	if err = conn.QueryRow(ctx, `SELECT research_artifact_scan_research_decision_migration_diagnostics($1::uuid,$2::uuid,$3::uuid)`, workspaceID, sessionID, decisionID).Scan(&diagnosticCount); err != nil || diagnosticCount != 0 {
		t.Fatalf("rescan repaired decision diagnostics=%d err=%v", diagnosticCount, err)
	}
	if err = conn.QueryRow(ctx, `SELECT research_artifact_scan_research_run_event_migration_diagnostics($1::uuid,$2::uuid,$3::uuid)`, workspaceID, sessionID, eventID).Scan(&diagnosticCount); err != nil || diagnosticCount != 0 {
		t.Fatalf("rescan repaired event diagnostics=%d err=%v", diagnosticCount, err)
	}
	var remainingDiagnostics int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_migration_diagnostic
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND owner_id IN ($3::uuid,$4::uuid)
	`, workspaceID, sessionID, decisionID, eventID).Scan(&remainingDiagnostics); err != nil {
		t.Fatal(err)
	}
	if remainingDiagnostics != 0 {
		t.Fatalf("repaired owners retain %d diagnostics", remainingDiagnostics)
	}

	var enabled bool
	if err = conn.QueryRow(ctx, `SELECT artifact_passport_enabled FROM research_session WHERE id = $1::uuid`, sessionID).Scan(&enabled); err != nil || enabled {
		t.Fatalf("default artifact_passport_enabled=%v err=%v", enabled, err)
	}

	if _, err = conn.Exec(ctx, down328); err != nil {
		t.Fatalf("apply 328 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up328); err != nil {
		t.Fatalf("reapply 328 up: %v", err)
	}
}

func TestResearchReportRelationshipDiagnostics351RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_report_diagnostic_351_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifact328TestDDL+`
		ALTER TABLE research_report ADD COLUMN structured JSONB NOT NULL DEFAULT '{}'::jsonb;
	`); err != nil {
		t.Fatalf("extend report schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	otherSessionID := "20000000-0000-4000-8000-000000000002"
	reportID := "30000000-0000-4000-8000-000000000010"
	claimID := "30000000-0000-4000-8000-000000000011"
	localSourceID := "30000000-0000-4000-8000-000000000012"
	foreignSourceID := "30000000-0000-4000-8000-000000000013"
	brokenStructured := fmt.Sprintf(`{
		"schema_version":1,
		"outline":[{"id":"section-a","children":["section-b","missing-section"]},{"id":"section-b","children":["section-a"]}],
		"sections":[{"id":"section-a","citation_ids":["missing-citation"]},{"id":"section-a","citation_ids":[]},{"id":"section-b","citation_ids":[]}],
		"citations":[{"id":"citation-a","source_id":"missing-structured-source"}],
		"sources":[{"source_id":"%s"},{"source_id":"not-a-uuid"}]
	}`, foreignSourceID)
	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed report workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_session (id, workspace_id) VALUES
		  ($1::uuid, $2::uuid), ($3::uuid, $2::uuid)
	`, sessionID, workspaceID, otherSessionID); err != nil {
		t.Fatalf("seed report sessions: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_source (id, workspace_id, session_id) VALUES
		  ($1::uuid, $2::uuid, $3::uuid), ($4::uuid, $2::uuid, $5::uuid)
	`, localSourceID, workspaceID, sessionID, foreignSourceID, otherSessionID); err != nil {
		t.Fatalf("seed report sources: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_claim (id, workspace_id, session_id) VALUES ($1::uuid, $2::uuid, $3::uuid)
	`, claimID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed report claim: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_report (id, workspace_id, session_id, structured)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::jsonb)
	`, reportID, workspaceID, sessionID, brokenStructured); err != nil {
		t.Fatalf("seed report row: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_report_claim (report_id, claim_id, section_id)
		VALUES ($1::uuid, $2::uuid, 'missing-claim-section')
	`, reportID, claimID); err != nil {
		t.Fatalf("seed report claim link: %v", err)
	}

	versions := []string{
		"318_research_artifact_passport",
		"319_research_artifact_passport_backfill",
		"320_research_artifact_reciprocal_guards",
		"321_research_artifact_policy_coupling_guards",
		"322_research_artifact_policy_ledger_guards",
		"323_research_artifact_integrity_guards",
		"324_research_artifact_link_policy_guards",
		"325_research_artifact_migration_diagnostics",
		"327_research_artifact_canonicalization_registry",
		"328_research_artifact_passport_d_completion",
	}
	for _, version := range versions {
		upSQL, _ := readMigrationPair(t, version)
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply %s: %v", version, err)
		}
	}
	up351, down351 := readMigrationPair(t, "351_research_report_relationship_diagnostics")
	if _, err = conn.Exec(ctx, up351); err != nil {
		t.Fatalf("apply 351: %v", err)
	}

	var diagnosticCount int
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_scan_research_report_migration_diagnostics($1::uuid, $2::uuid, $3::uuid)
	`, workspaceID, sessionID, reportID).Scan(&diagnosticCount); err != nil {
		t.Fatalf("scan broken report: %v", err)
	}
	if diagnosticCount < 6 {
		t.Fatalf("broken report diagnostics=%d want at least 6", diagnosticCount)
	}
	for fieldPath, wantReason := range map[string]string{
		"/structured/sections/0/id":                "duplicate_local_key",
		"/structured/outline/0/id":                 "ambiguous_local_key",
		"/structured/outline/0/children/1":         "dangling_local_key",
		"/structured/outline":                      "cyclic_local_reference",
		"/structured/citations/0/source_id":        "dangling_local_key",
		"/structured/sources/1/source_id":          "malformed_uuid",
		"/structured/sources/0/source_id":          "cross_scope_reference",
		"/report_claim/" + claimID + "/section_id": "dangling_local_key",
	} {
		var reason string
		if err = conn.QueryRow(ctx, `
			SELECT reason_code FROM research_artifact_migration_diagnostic
			WHERE owner_kind = 'report_revision' AND owner_id = $1::uuid AND field_path = $2
		`, reportID, fieldPath).Scan(&reason); err != nil || reason != wantReason {
			t.Fatalf("diagnostic %s reason=%q want=%q err=%v", fieldPath, reason, wantReason, err)
		}
	}

	validStructured := fmt.Sprintf(`{
		"schema_version":1,
		"outline":[{"id":"section-a","children":[]}],
		"sections":[{"id":"section-a","citation_ids":["citation-a"]}],
		"citations":[{"id":"citation-a","source_id":"%s"}],
		"sources":[{"source_id":"%s"}]
	}`, localSourceID, localSourceID)
	if _, err = conn.Exec(ctx, `
		UPDATE research_report SET structured = $4::jsonb
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, reportID, validStructured); err != nil {
		t.Fatalf("repair report structured: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		UPDATE research_report_claim SET section_id = 'section-a'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND report_id = $3::uuid
	`, workspaceID, sessionID, reportID); err != nil {
		t.Fatalf("repair report claim section: %v", err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT research_artifact_scan_research_report_migration_diagnostics($1::uuid, $2::uuid, $3::uuid)
	`, workspaceID, sessionID, reportID).Scan(&diagnosticCount); err != nil {
		t.Fatalf("rescan repaired report: %v", err)
	}
	if diagnosticCount != 0 {
		t.Fatalf("repaired report diagnostics=%d want 0", diagnosticCount)
	}

	if _, err = conn.Exec(ctx, down351); err != nil {
		t.Fatalf("apply 351 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up351); err != nil {
		t.Fatalf("reapply 351 up: %v", err)
	}
}

func TestResearchResultArtifactBackfill329RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_d329_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifact329TestDDL); err != nil {
		t.Fatalf("extend 329 schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	taskID := "30000000-0000-4000-8000-000000000003"
	attemptID := "40000000-0000-4000-8000-000000000004"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'local-task', 1, 1)
	`, taskID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_attempt (
		  id, workspace_id, session_id, task_id, status, client_request_id, result_hash, result
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'succeeded', 'req-1',
		  'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
		  '{"schema_version":1,"client_request_id":"req-1","summary":"ok"}'::jsonb
		)
	`, attemptID, workspaceID, sessionID, taskID); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up329, down329 := readMigrationPair(t, "329_research_result_artifact_backfill")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323, up329} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	var resultCount int
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_result_artifact WHERE attempt_id = $1::uuid
	`, attemptID).Scan(&resultCount); err != nil || resultCount != 1 {
		t.Fatalf("result artifact count=%d err=%v", resultCount, err)
	}
	var storedHash, artifactHash, hashOrigin string
	if err = conn.QueryRow(ctx, `
		SELECT a.result_hash, ra.content_hash, av.hash_origin
		FROM research_task_attempt a
		JOIN research_result_artifact ra
		  ON ra.workspace_id = a.workspace_id
		 AND ra.session_id = a.session_id
		 AND ra.attempt_id = a.id
		JOIN research_artifact_version av
		  ON av.workspace_id = ra.workspace_id
		 AND av.session_id = ra.session_id
		 AND av.artifact_id = ra.id
		 AND av.version = 1
		WHERE a.id = $1::uuid
	`, attemptID).Scan(&storedHash, &artifactHash, &hashOrigin); err != nil || artifactHash != storedHash || hashOrigin != "legacy_stored" {
		t.Fatalf("result artifact hash=%q stored attempt hash=%q origin=%q err=%v", artifactHash, storedHash, hashOrigin, err)
	}

	if _, err = conn.Exec(ctx, down329); err != nil {
		t.Fatalf("apply 329 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up329); err != nil {
		t.Fatalf("reapply 329 up: %v", err)
	}
}

const researchArtifact330TestDDL = `
CREATE TABLE IF NOT EXISTS research_dispatch_outbox (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  session_id UUID NOT NULL REFERENCES research_session(id) ON DELETE CASCADE,
  task_id UUID NOT NULL REFERENCES research_task(id) ON DELETE CASCADE,
  attempt_id UUID NOT NULL REFERENCES research_task_attempt(id) ON DELETE CASCADE,
  dispatch_key TEXT NOT NULL,
  request_payload JSONB NOT NULL DEFAULT '{}'::jsonb,
  request_hash TEXT NOT NULL DEFAULT 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
  status TEXT NOT NULL DEFAULT 'pending',
  UNIQUE (attempt_id)
);
`

func TestResearchDispatchManifestBinding330RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_d330_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, researchArtifact330TestDDL); err != nil {
		t.Fatalf("extend 330 schema: %v", err)
	}
	if _, err = conn.Exec(ctx, researchArtifact328TestDDL); err != nil {
		t.Fatalf("extend 328 schema: %v", err)
	}

	workspaceID := "10000000-0000-4000-8000-000000000001"
	sessionID := "20000000-0000-4000-8000-000000000001"
	taskID := "30000000-0000-4000-8000-000000000003"
	attemptID := "40000000-0000-4000-8000-000000000004"

	if _, err = conn.Exec(ctx, `INSERT INTO workspace (id) VALUES ($1::uuid)`, workspaceID); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
	if _, err = conn.Exec(ctx, `INSERT INTO research_session (id, workspace_id) VALUES ($1::uuid, $2::uuid)`, sessionID, workspaceID); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'task', 1, 1)
	`, taskID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_task_attempt (id, workspace_id, session_id, task_id)
		VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)
	`, attemptID, workspaceID, sessionID, taskID); err != nil {
		t.Fatalf("seed attempt: %v", err)
	}

	up318, _ := readMigrationPair(t, "318_research_artifact_passport")
	up319, _ := readMigrationPair(t, "319_research_artifact_passport_backfill")
	up320, _ := readMigrationPair(t, "320_research_artifact_reciprocal_guards")
	up321, _ := readMigrationPair(t, "321_research_artifact_policy_coupling_guards")
	up322, _ := readMigrationPair(t, "322_research_artifact_policy_ledger_guards")
	up323, _ := readMigrationPair(t, "323_research_artifact_integrity_guards")
	up324, _ := readMigrationPair(t, "324_research_artifact_link_policy_guards")
	up325, _ := readMigrationPair(t, "325_research_artifact_migration_diagnostics")
	up327, _ := readMigrationPair(t, "327_research_artifact_canonicalization_registry")
	up328, _ := readMigrationPair(t, "328_research_artifact_passport_d_completion")
	for _, upSQL := range []string{up318, up319, up320, up321, up322, up323, up324, up325, up327, up328} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}

	up330, down330 := readMigrationPair(t, "330_research_dispatch_manifest_binding")
	if _, err = conn.Exec(ctx, up330); err != nil {
		t.Fatalf("apply 330 up: %v", err)
	}

	var manifestColumnExists bool
	if err = conn.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM information_schema.columns
		  WHERE table_schema = current_schema()
		    AND table_name = 'research_dispatch_outbox'
		    AND column_name = 'manifest_id'
		)
	`).Scan(&manifestColumnExists); err != nil {
		t.Fatalf("check manifest_id column: %v", err)
	}
	if !manifestColumnExists {
		t.Fatal("expected research_dispatch_outbox.manifest_id column")
	}

	if _, err = conn.Exec(ctx, `
		INSERT INTO research_dispatch_outbox (
		  workspace_id, session_id, task_id, attempt_id, dispatch_key,
		  request_payload, request_hash, manifest_hash
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'dispatch-key',
		  '{}'::jsonb, 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
		  'not-a-hash'
		)
	`, workspaceID, sessionID, taskID, attemptID); err == nil {
		t.Fatal("expected manifest_hash check constraint violation")
	}

	if _, err = conn.Exec(ctx, down330); err != nil {
		t.Fatalf("apply 330 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up330); err != nil {
		t.Fatalf("reapply 330 up: %v", err)
	}
}

func TestResearchArtifactLateManifestPolicyMigrationsRoundTrip(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_late_policy_test_%d", time.Now().UnixNano())
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
		t.Fatalf("create legacy schema: %v", err)
	}
	if _, err = conn.Exec(ctx, researchArtifactScopedFKTestDDL); err != nil {
		t.Fatalf("create scoped relationship fixture: %v", err)
	}

	prerequisites := []string{
		"318_research_artifact_passport",
		"319_research_artifact_passport_backfill",
		"320_research_artifact_reciprocal_guards",
		"321_research_artifact_policy_coupling_guards",
		"322_research_artifact_policy_ledger_guards",
		"323_research_artifact_integrity_guards",
		"324_research_artifact_link_policy_guards",
		"325_research_artifact_migration_diagnostics",
		"326_research_artifact_scoped_relationship_fks",
		"327_research_artifact_canonicalization_registry",
		"328_research_artifact_passport_d_completion",
	}
	for _, name := range prerequisites {
		upSQL, _ := readMigrationPair(t, name)
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply prerequisite %s: %v", name, err)
		}
	}

	up346, down346 := readMigrationPair(t, "346_research_manifest_policy_grants")
	up347, down347 := readMigrationPair(t, "347_research_manifest_gate_snapshot")
	up349, down349 := readMigrationPair(t, "349_research_evaluation_manifest_grant_guard")
	apply := func(label string) {
		t.Helper()
		if _, pathErr := conn.Exec(ctx, "SET search_path TO "+quotedSchema); pathErr != nil {
			t.Fatalf("%s set isolated search_path: %v", label, pathErr)
		}
		for _, migration := range []struct {
			name string
			sql  string
		}{{"346", up346}, {"347", up347}, {"349", up349}} {
			if _, applyErr := conn.Exec(ctx, migration.sql); applyErr != nil {
				t.Fatalf("%s apply %s: %v", label, migration.name, applyErr)
			}
		}
		if _, pathErr := conn.Exec(ctx, "SET search_path TO "+quotedSchema+", public"); pathErr != nil {
			t.Fatalf("%s restore search_path: %v", label, pathErr)
		}
	}
	assertUp := func(label string) {
		t.Helper()
		var constraints, columns, triggers int
		var functionDefinition string
		if queryErr := conn.QueryRow(ctx, `
			SELECT count(*)::int FROM pg_constraint
			WHERE conrelid='research_artifact_context_manifest'::regclass
			  AND conname IN (
			    'research_artifact_context_manifest_normal_grant_pair_check',
			    'research_artifact_context_manifest_evaluation_grant_pair_check',
			    'research_artifact_context_manifest_normal_grant_fkey',
			    'research_artifact_context_manifest_evaluation_grant_fkey'
			  )
		`).Scan(&constraints); queryErr != nil {
			t.Fatalf("%s query constraints: %v", label, queryErr)
		}
		if queryErr := conn.QueryRow(ctx, `
			SELECT count(*)::int FROM information_schema.columns
			WHERE table_schema=current_schema() AND table_name='research_artifact_context_manifest'
			  AND column_name IN ('gate_snapshot_bytes','gate_snapshot_hash')
		`).Scan(&columns); queryErr != nil {
			t.Fatalf("%s query columns: %v", label, queryErr)
		}
		if queryErr := conn.QueryRow(ctx, `
			SELECT count(*)::int FROM pg_trigger
			WHERE tgrelid='research_artifact_context_manifest'::regclass
			  AND tgname='research_artifact_context_manifest_grant_guard' AND NOT tgisinternal
		`).Scan(&triggers); queryErr != nil {
			t.Fatalf("%s query trigger: %v", label, queryErr)
		}
		if queryErr := conn.QueryRow(ctx, `
			SELECT pg_get_functiondef(pg_proc.oid)
			FROM pg_proc
			JOIN pg_namespace ON pg_namespace.oid = pg_proc.pronamespace
			WHERE pg_namespace.nspname = current_schema()
			  AND pg_proc.proname = 'research_artifact_context_manifest_grant_guard_fn'
		`).Scan(&functionDefinition); queryErr != nil {
			t.Fatalf("%s query function: %v", label, queryErr)
		}
		for _, required := range []string{
			"NEW.purpose = 'evaluation'",
			"NEW.purpose NOT IN ('task_execution','evaluation')",
			"NEW.evaluation_grant_id IS NULL",
		} {
			if !strings.Contains(functionDefinition, required) {
				t.Fatalf("%s strict grant function missing %q", label, required)
			}
		}
		if constraints != 4 || columns != 2 || triggers != 1 {
			t.Fatalf("%s constraints=%d columns=%d triggers=%d want 4/2/1", label, constraints, columns, triggers)
		}
	}

	apply("first")
	assertUp("first")
	for _, migration := range []struct {
		name string
		sql  string
	}{{"349", down349}, {"347", down347}, {"346", down346}} {
		if _, err = conn.Exec(ctx, migration.sql); err != nil {
			t.Fatalf("apply %s down: %v", migration.name, err)
		}
	}
	var constraints, columns, triggers, functions int
	if err = conn.QueryRow(ctx, `
		SELECT
		 (SELECT count(*)::int FROM pg_constraint WHERE conrelid='research_artifact_context_manifest'::regclass
		   AND conname LIKE 'research_artifact_context_manifest_%grant%'),
		 (SELECT count(*)::int FROM information_schema.columns WHERE table_schema=current_schema()
		   AND table_name='research_artifact_context_manifest' AND column_name IN ('gate_snapshot_bytes','gate_snapshot_hash')),
		 (SELECT count(*)::int FROM pg_trigger WHERE tgrelid='research_artifact_context_manifest'::regclass
		   AND tgname='research_artifact_context_manifest_grant_guard' AND NOT tgisinternal),
		 (SELECT count(*)::int FROM pg_proc JOIN pg_namespace ON pg_namespace.oid=pg_proc.pronamespace
		   WHERE pg_namespace.nspname=current_schema() AND proname='research_artifact_context_manifest_grant_guard_fn')
	`).Scan(&constraints, &columns, &triggers, &functions); err != nil {
		t.Fatalf("query down state: %v", err)
	}
	if constraints != 0 || columns != 0 || triggers != 0 || functions != 0 {
		t.Fatalf("down state constraints=%d columns=%d triggers=%d functions=%d want zero", constraints, columns, triggers, functions)
	}
	apply("reapply")
	assertUp("reapply")
}

func TestResearchArtifactManifestOmissionReasons333RoundTrips(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_d333_test_%d", time.Now().UnixNano())
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
	if _, err = conn.Exec(ctx, `
		CREATE FUNCTION research_artifact_context_omission_reason_allowed(reason TEXT)
		RETURNS BOOLEAN LANGUAGE sql IMMUTABLE AS $$
		  SELECT reason IN ('access_denied', 'stale', 'superseded', 'duplicate', 'token_budget', 'irrelevant');
		$$
	`); err != nil {
		t.Fatalf("create legacy omission reason function: %v", err)
	}

	up333, down333 := readMigrationPair(t, "333_research_artifact_omission_reasons")
	if _, err = conn.Exec(ctx, up333); err != nil {
		t.Fatalf("apply 333 up: %v", err)
	}
	var allowed bool
	if err = conn.QueryRow(ctx, `SELECT research_artifact_context_omission_reason_allowed('evaluation_compartment')`).Scan(&allowed); err != nil || !allowed {
		t.Fatalf("evaluation_compartment allowed=%v err=%v", allowed, err)
	}
	if err = conn.QueryRow(ctx, `SELECT research_artifact_context_omission_reason_allowed('lifecycle')`).Scan(&allowed); err != nil || !allowed {
		t.Fatalf("lifecycle allowed=%v err=%v", allowed, err)
	}
	if _, err = conn.Exec(ctx, down333); err != nil {
		t.Fatalf("apply 333 down: %v", err)
	}
	if err = conn.QueryRow(ctx, `SELECT research_artifact_context_omission_reason_allowed('evaluation_compartment')`).Scan(&allowed); err != nil || allowed {
		t.Fatalf("evaluation_compartment allowed after down=%v err=%v", allowed, err)
	}
}
