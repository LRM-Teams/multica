package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResearchArtifactGrantGuards321BothConstraintModes(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_grant_321_matrix_%d", time.Now().UnixNano())
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
	userID := "40000000-0000-4000-8000-000000000001"
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
	for _, upSQL := range []string{up318, up319, up320, up321} {
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply migration: %v", err)
		}
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_artifact_policy_state (workspace_id, session_id, policy_version, watermark)
		VALUES ($1::uuid, $2::uuid, 'legacy-v1-v5-compat-v1', 0)
		ON CONFLICT (workspace_id, session_id) DO NOTHING
	`, workspaceID, sessionID); err != nil {
		t.Fatalf("seed policy state: %v", err)
	}

	commitOrForce := func(tx pgx.Tx, immediate bool) error {
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

	modes := []struct {
		name      string
		immediate bool
		pairedID  string
		orphanID  string
	}{
		{
			name:      "immediate",
			immediate: true,
			pairedID:  "50000000-0000-4000-8000-000000000001",
			orphanID:  "50000000-0000-4000-8000-000000000002",
		},
		{
			name:      "ordinary_commit",
			immediate: false,
			pairedID:  "50000000-0000-4000-8000-000000000003",
			orphanID:  "50000000-0000-4000-8000-000000000004",
		},
	}
	for _, mode := range modes {
		mode := mode
		t.Run(mode.name, func(t *testing.T) {
			pairedTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin paired tx: %v", beginErr)
			}
			defer pairedTx.Rollback(ctx)
			watermark := reserveWatermark(t, pairedTx)
			if _, execErr := pairedTx.Exec(ctx, `
				INSERT INTO research_artifact_policy_grant (
				  id, workspace_id, session_id, principal_kind, principal_id, purpose,
				  normal_clearance, revision, status
				) VALUES ($1::uuid, $2::uuid, $3::uuid, 'user', $4::uuid, 'ordinary', 'raw', 1, 'active')
			`, mode.pairedID, workspaceID, sessionID, userID); execErr != nil {
				t.Fatalf("insert paired grant: %v", execErr)
			}
			if _, execErr := pairedTx.Exec(ctx, `
				INSERT INTO research_artifact_policy_mutation (
				  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
				  old_grant_revision, new_grant_revision, new_grant_status
				) VALUES ($1::uuid, $2::uuid, $3, 'grant_create', $4::uuid, 0, 1, 'active')
			`, workspaceID, sessionID, watermark, mode.pairedID); execErr != nil {
				t.Fatalf("insert paired grant mutation: %v", execErr)
			}
			if commitErr := commitOrForce(pairedTx, mode.immediate); commitErr != nil {
				t.Fatalf("commit paired grant and mutation: %v", commitErr)
			}

			grantOnlyTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin grant-only tx: %v", beginErr)
			}
			defer grantOnlyTx.Rollback(ctx)
			reserveWatermark(t, grantOnlyTx)
			if _, execErr := grantOnlyTx.Exec(ctx, `
				INSERT INTO research_artifact_policy_grant (
				  id, workspace_id, session_id, principal_kind, principal_id, purpose,
				  normal_clearance, revision, status
				) VALUES ($1::uuid, $2::uuid, $3::uuid, 'user', $4::uuid, 'ordinary', 'raw', 1, 'active')
			`, mode.orphanID, workspaceID, sessionID, userID); execErr != nil {
				t.Fatalf("insert grant without mutation: %v", execErr)
			}
			assertConstraint(t, commitOrForce(grantOnlyTx, mode.immediate), "research_artifact_policy_grant_to_mutation_guard")
			_ = grantOnlyTx.Rollback(ctx)

			mutationOnlyTx, beginErr := conn.Begin(ctx)
			if beginErr != nil {
				t.Fatalf("begin mutation-only tx: %v", beginErr)
			}
			defer mutationOnlyTx.Rollback(ctx)
			watermark = reserveWatermark(t, mutationOnlyTx)
			if _, execErr := mutationOnlyTx.Exec(ctx, `
				INSERT INTO research_artifact_policy_mutation (
				  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
				  old_grant_revision, new_grant_revision, old_grant_status, new_grant_status
				) VALUES ($1::uuid, $2::uuid, $3, 'grant_revoke', $4::uuid, 1, 2, 'active', 'revoked')
			`, workspaceID, sessionID, watermark, mode.pairedID); execErr != nil {
				t.Fatalf("insert mutation without grant transition: %v", execErr)
			}
			assertConstraint(t, commitOrForce(mutationOnlyTx, mode.immediate), "research_artifact_policy_mutation_to_grant_guard")
		})
	}
}
