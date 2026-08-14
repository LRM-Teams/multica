package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResearchArtifactVerificationGuards321BothConstraintModes(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_verification_321_matrix_%d", time.Now().UnixNano())
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

	fixtures := []struct {
		name             string
		table            string
		kind             string
		domainConstraint string
		immediateID      string
		ordinaryID       string
	}{
		{
			name:             "source_snapshot",
			table:            "research_source_snapshot",
			kind:             "source_snapshot",
			domainConstraint: "research_source_snapshot_verification_to_policy_guard",
			immediateID:      "31000000-0000-4000-8000-000000000001",
			ordinaryID:       "31000000-0000-4000-8000-000000000002",
		},
		{
			name:             "observation",
			table:            "research_observation",
			kind:             "observation",
			domainConstraint: "research_observation_verification_to_policy_guard",
			immediateID:      "32000000-0000-4000-8000-000000000001",
			ordinaryID:       "32000000-0000-4000-8000-000000000002",
		},
		{
			name:             "evidence_link",
			table:            "research_claim_evidence",
			kind:             "evidence_link",
			domainConstraint: "research_claim_evidence_verification_to_policy_guard",
			immediateID:      "33000000-0000-4000-8000-000000000001",
			ordinaryID:       "33000000-0000-4000-8000-000000000002",
		},
	}

	for _, fixture := range fixtures[:2] {
		for _, artifactID := range []string{fixture.immediateID, fixture.ordinaryID} {
			if _, err = conn.Exec(ctx, fmt.Sprintf(`
				INSERT INTO %s (id, workspace_id, session_id, verification_status)
				VALUES ($1::uuid, $2::uuid, $3::uuid, 'pending')
			`, fixture.table), artifactID, workspaceID, sessionID); err != nil {
				t.Fatalf("seed %s %s: %v", fixture.name, artifactID, err)
			}
		}
	}
	claimID := "34000000-0000-4000-8000-000000000001"
	evidenceObservationID := "34000000-0000-4000-8000-000000000002"
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_claim (id, workspace_id, session_id) VALUES ($1::uuid, $2::uuid, $3::uuid)
	`, claimID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed evidence claim: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO research_observation (id, workspace_id, session_id, verification_status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'pending')
	`, evidenceObservationID, workspaceID, sessionID); err != nil {
		t.Fatalf("seed evidence observation: %v", err)
	}
	for _, artifactID := range []string{fixtures[2].immediateID, fixtures[2].ordinaryID} {
		if _, err = conn.Exec(ctx, `
			INSERT INTO research_claim_evidence (
			  id, workspace_id, session_id, claim_id, observation_id, verification_status
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'pending')
		`, artifactID, workspaceID, sessionID, claimID, evidenceObservationID); err != nil {
			t.Fatalf("seed evidence link %s: %v", artifactID, err)
		}
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
	if _, err = conn.Exec(ctx, `
		UPDATE research_artifact_passport
		SET eligibility_revision = eligibility_revision + 10
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, workspaceID, sessionID); err != nil {
		t.Fatalf("detach backfill mutations from verification coupling: %v", err)
	}
	if _, err = conn.Exec(ctx, `
		DELETE FROM research_artifact_policy_mutation
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, workspaceID, sessionID); err != nil {
		t.Fatalf("clear backfill mutations: %v", err)
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
	}{
		{name: "immediate", immediate: true},
		{name: "ordinary_commit", immediate: false},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		for _, mode := range modes {
			mode := mode
			t.Run(fixture.name+"/"+mode.name, func(t *testing.T) {
				artifactID := fixture.ordinaryID
				if mode.immediate {
					artifactID = fixture.immediateID
				}

				pairedTx, beginErr := conn.Begin(ctx)
				if beginErr != nil {
					t.Fatalf("begin paired tx: %v", beginErr)
				}
				defer pairedTx.Rollback(ctx)
				watermark := reserveWatermark(t, pairedTx)
				if _, execErr := pairedTx.Exec(ctx, `
					UPDATE research_artifact_passport SET eligibility_revision = 2
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, workspaceID, sessionID, artifactID); execErr != nil {
					t.Fatalf("bump paired passport: %v", execErr)
				}
				if _, execErr := pairedTx.Exec(ctx, `
					INSERT INTO research_artifact_policy_mutation (
					  workspace_id, session_id, watermark, mutation_kind, artifact_id,
					  old_eligibility_revision, new_eligibility_revision
					) VALUES ($1::uuid, $2::uuid, $3, 'verification', $4::uuid, 1, 2)
				`, workspaceID, sessionID, watermark, artifactID); execErr != nil {
					t.Fatalf("insert paired verification mutation: %v", execErr)
				}
				if _, execErr := pairedTx.Exec(ctx, fmt.Sprintf(`
					UPDATE %s SET verification_status = 'verified' WHERE id = $1::uuid
				`, fixture.table), artifactID); execErr != nil {
					t.Fatalf("update paired verification status: %v", execErr)
				}
				if commitErr := commitOrForce(pairedTx, mode.immediate); commitErr != nil {
					t.Fatalf("commit paired verification transition: %v", commitErr)
				}

				domainOnlyTx, beginErr := conn.Begin(ctx)
				if beginErr != nil {
					t.Fatalf("begin domain-only tx: %v", beginErr)
				}
				defer domainOnlyTx.Rollback(ctx)
				if _, execErr := domainOnlyTx.Exec(ctx, fmt.Sprintf(`
					UPDATE %s SET verification_status = 'rejected' WHERE id = $1::uuid
				`, fixture.table), artifactID); execErr != nil {
					t.Fatalf("update verification without policy transition: %v", execErr)
				}
				assertConstraint(t, commitOrForce(domainOnlyTx, mode.immediate), fixture.domainConstraint)
				_ = domainOnlyTx.Rollback(ctx)

				ledgerOnlyTx, beginErr := conn.Begin(ctx)
				if beginErr != nil {
					t.Fatalf("begin ledger-only tx: %v", beginErr)
				}
				defer ledgerOnlyTx.Rollback(ctx)
				watermark = reserveWatermark(t, ledgerOnlyTx)
				if _, execErr := ledgerOnlyTx.Exec(ctx, `
					UPDATE research_artifact_passport SET eligibility_revision = 3
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, workspaceID, sessionID, artifactID); execErr != nil {
					t.Fatalf("bump ledger-only passport: %v", execErr)
				}
				if _, execErr := ledgerOnlyTx.Exec(ctx, `
					INSERT INTO research_artifact_policy_mutation (
					  workspace_id, session_id, watermark, mutation_kind, artifact_id,
					  old_eligibility_revision, new_eligibility_revision
					) VALUES ($1::uuid, $2::uuid, $3, 'verification', $4::uuid, 2, 3)
				`, workspaceID, sessionID, watermark, artifactID); execErr != nil {
					t.Fatalf("insert mutation without domain transition: %v", execErr)
				}
				assertConstraint(t, commitOrForce(ledgerOnlyTx, mode.immediate), "research_artifact_policy_mutation_to_verification_guard")
			})
		}
	}
}
