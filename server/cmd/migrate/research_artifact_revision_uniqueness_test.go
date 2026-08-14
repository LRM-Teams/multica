package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResearchArtifactPolicyRevisionUniquenessIgnoresWatermark(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_revision_uniqueness_%d", time.Now().UnixNano())
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
	grantID := "50000000-0000-4000-8000-000000000001"
	userID := "60000000-0000-4000-8000-000000000001"
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
	for _, upSQL := range []string{up318, up319, up320, up321, up322} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO UPDATE SET watermark = 0
	`, workspaceID, sessionID); err != nil {
		t.Fatalf("seed policy state: %v", err)
	}

	artifactTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin artifact seed tx: %v", err)
	}
	defer artifactTx.Rollback(ctx)
	if _, err = artifactTx.Exec(ctx, `
		INSERT INTO research_task (id, workspace_id, session_id, client_key, goal_version, plan_version)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'revision-uniqueness', 1, 1)
	`, taskID, workspaceID, sessionID); err != nil {
		t.Fatalf("insert task: %v", err)
	}
	if _, err = artifactTx.Exec(ctx, `
		SELECT research_artifact_backfill_registered($1::uuid, $2::uuid, $3::uuid, 'task', now(), 1, 1)
	`, workspaceID, sessionID, taskID); err != nil {
		t.Fatalf("register task passport: %v", err)
	}
	if err = artifactTx.Commit(ctx); err != nil {
		t.Fatalf("commit artifact seed: %v", err)
	}

	assertUniqueViolation := func(t *testing.T, got error, want string) {
		t.Helper()
		pgErr, ok := got.(*pgconn.PgError)
		if !ok || pgErr.Code != "23505" || pgErr.ConstraintName != want {
			t.Fatalf("unique error=%v want constraint=%s", got, want)
		}
	}
	reserveWatermark := func(t *testing.T, tx pgx.Tx) int64 {
		t.Helper()
		var watermark int64
		if queryErr := tx.QueryRow(ctx, `
			SELECT research_artifact_reserve_policy_watermark($1::uuid, $2::uuid)
		`, workspaceID, sessionID).Scan(&watermark); queryErr != nil {
			t.Fatalf("reserve policy watermark: %v", queryErr)
		}
		return watermark
	}

	duplicateArtifactTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin duplicate artifact tx: %v", err)
	}
	defer duplicateArtifactTx.Rollback(ctx)
	duplicateArtifactWatermark := reserveWatermark(t, duplicateArtifactTx)
	_, err = duplicateArtifactTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision
		) VALUES ($1::uuid, $2::uuid, $3, 'artifact_create', $4::uuid, 0, 1)
	`, workspaceID, sessionID, duplicateArtifactWatermark, taskID)
	assertUniqueViolation(t, err, "research_artifact_policy_mutation_artifact_revision_uidx")
	if err = duplicateArtifactTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback duplicate artifact tx: %v", err)
	}

	grantTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin grant seed tx: %v", err)
	}
	defer grantTx.Rollback(ctx)
	grantWatermark := reserveWatermark(t, grantTx)
	if _, err = grantTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_grant (
		  id, workspace_id, session_id, principal_kind, principal_id, purpose,
		  normal_clearance, revision, status
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'user', $4::uuid, 'ordinary', 'raw', 1, 'active')
	`, grantID, workspaceID, sessionID, userID); err != nil {
		t.Fatalf("insert policy grant: %v", err)
	}
	if _, err = grantTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
		  old_grant_revision, new_grant_revision, new_grant_status
		) VALUES ($1::uuid, $2::uuid, $3, 'grant_create', $4::uuid, 0, 1, 'active')
	`, workspaceID, sessionID, grantWatermark, grantID); err != nil {
		t.Fatalf("insert grant mutation: %v", err)
	}
	if err = grantTx.Commit(ctx); err != nil {
		t.Fatalf("commit grant seed: %v", err)
	}

	duplicateGrantTx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin duplicate grant tx: %v", err)
	}
	defer duplicateGrantTx.Rollback(ctx)
	duplicateGrantWatermark := reserveWatermark(t, duplicateGrantTx)
	_, err = duplicateGrantTx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
		  old_grant_revision, new_grant_revision, new_grant_status
		) VALUES ($1::uuid, $2::uuid, $3, 'grant_create', $4::uuid, 0, 1, 'active')
	`, workspaceID, sessionID, duplicateGrantWatermark, grantID)
	assertUniqueViolation(t, err, "research_artifact_policy_mutation_grant_revision_uidx")
	if err = duplicateGrantTx.Rollback(ctx); err != nil {
		t.Fatalf("rollback duplicate grant tx: %v", err)
	}

	var finalWatermark int64
	var artifactMutations, grantMutations int
	if err = conn.QueryRow(ctx, `
		SELECT watermark FROM research_artifact_policy_state
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, workspaceID, sessionID).Scan(&finalWatermark); err != nil {
		t.Fatalf("read final watermark: %v", err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_policy_mutation
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND artifact_id = $3::uuid AND new_eligibility_revision = 1
	`, workspaceID, sessionID, taskID).Scan(&artifactMutations); err != nil {
		t.Fatalf("count artifact mutations: %v", err)
	}
	if err = conn.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_policy_mutation
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND policy_grant_id = $3::uuid AND new_grant_revision = 1
	`, workspaceID, sessionID, grantID).Scan(&grantMutations); err != nil {
		t.Fatalf("count grant mutations: %v", err)
	}
	if finalWatermark != grantWatermark || artifactMutations != 1 || grantMutations != 1 {
		t.Fatalf(
			"final watermark=%d want=%d artifact mutations=%d grant mutations=%d want 1/1",
			finalWatermark, grantWatermark, artifactMutations, grantMutations,
		)
	}
}
