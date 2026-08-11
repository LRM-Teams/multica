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
	taskID := "30000000-0000-4000-8000-000000000003"
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
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'guard-positive', 1, 1)
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
		t.Fatalf("commit paired task insert: %v", err)
	}

	negativeTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin negative tx: %v", err)
	}
	orphanTaskID := "30000000-0000-4000-8000-000000000099"
	if _, err = negativeTx.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'guard-negative', 1, 1)
	`, orphanTaskID, workspaceID, sessionID); err != nil {
		negativeTx.Rollback(ctx)
		t.Fatalf("insert orphan task: %v", err)
	}
	if err = negativeTx.Commit(ctx); err == nil {
		t.Fatal("expected orphan task insert to fail reciprocal guard on commit")
	} else {
		negativeTx.Rollback(ctx)
	}

	if _, err = conn.Exec(ctx, `DELETE FROM workspace WHERE id = $1::uuid`, workspaceID); err != nil {
		t.Fatalf("workspace cascade delete: %v", err)
	}
	var remainingTasks int
	if err = conn.QueryRow(ctx, `SELECT count(*)::int FROM research_task`).Scan(&remainingTasks); err != nil || remainingTasks != 0 {
		t.Fatalf("tasks after cascade=%d err=%v", remainingTasks, err)
	}
	var remainingPassports int
	if err = conn.QueryRow(ctx, `SELECT count(*)::int FROM research_artifact_passport`).Scan(&remainingPassports); err != nil || remainingPassports != 0 {
		t.Fatalf("passports after cascade=%d err=%v", remainingPassports, err)
	}

	if _, err = conn.Exec(ctx, down320); err != nil {
		t.Fatalf("apply 320 down: %v", err)
	}
	if _, err = conn.Exec(ctx, up320); err != nil {
		t.Fatalf("reapply 320 up: %v", err)
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
