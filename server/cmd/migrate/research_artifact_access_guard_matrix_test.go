package main

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestResearchArtifactAccessAndCurrentVersionGuardMatrix(t *testing.T) {
	pool := openTestPool(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire connection: %v", err)
	}
	defer conn.Release()

	schema := fmt.Sprintf("research_artifact_access_guard_test_%d", time.Now().UnixNano())
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

	const (
		workspaceID = "10000000-0000-4000-8000-000000000001"
		sessionID   = "20000000-0000-4000-8000-000000000001"
	)
	taskIDs := []string{
		"30000000-0000-4000-8000-000000000003",
		"30000000-0000-4000-8000-000000000004",
	}
	if _, err = conn.Exec(ctx, `
		INSERT INTO workspace(id) VALUES ($1::uuid);
		INSERT INTO research_session(id,workspace_id) VALUES ($2::uuid,$1::uuid);
		INSERT INTO research_task(id,workspace_id,session_id,client_key,goal_version,plan_version)
		VALUES
		  ($3::uuid,$1::uuid,$2::uuid,'access-immediate',1,1),
		  ($4::uuid,$1::uuid,$2::uuid,'access-ordinary',1,1);
	`, workspaceID, sessionID, taskIDs[0], taskIDs[1]); err != nil {
		t.Fatalf("seed access fixture: %v", err)
	}

	for _, migration := range []string{
		"318_research_artifact_passport",
		"319_research_artifact_passport_backfill",
		"320_research_artifact_reciprocal_guards",
		"321_research_artifact_policy_coupling_guards",
		"322_research_artifact_policy_ledger_guards",
		"350_research_artifact_policy_mutation_facts",
		"356_research_artifact_current_version_policy_guard",
		"362_research_artifact_access_policy_guard",
	} {
		upSQL, _ := readMigrationPair(t, migration)
		if _, err = conn.Exec(ctx, upSQL); err != nil {
			t.Fatalf("apply %s: %v", migration, err)
		}
	}

	for _, taskID := range taskIDs {
		for version, access := range map[int]string{2: "raw", 3: "redacted", 4: "redacted", 5: "verified_only"} {
			if _, err = conn.Exec(ctx, `
				INSERT INTO research_artifact_version(
				  workspace_id,session_id,artifact_id,version,content_hash,access_level
				) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,$6)
			`, workspaceID, sessionID, taskID, version,
				fmt.Sprintf("sha256:%064x", version), access); err != nil {
				t.Fatalf("seed task %s version %d: %v", taskID, version, err)
			}
		}
	}

	modes := []struct {
		name      string
		immediate bool
		taskID    string
	}{
		{name: "immediate", immediate: true, taskID: taskIDs[0]},
		{name: "ordinary_commit", taskID: taskIDs[1]},
	}
	for _, mode := range modes {
		t.Run("paired_current_version/"+mode.name, func(t *testing.T) {
			tx := beginAccessGuardTx(t, ctx, conn.Conn())
			defer tx.Rollback(ctx)
			watermark := reserveAccessGuardWatermark(t, ctx, tx, workspaceID, sessionID)
			if _, execErr := tx.Exec(ctx, `
				UPDATE research_artifact_passport SET current_version=2,eligibility_revision=2
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid;
				INSERT INTO research_artifact_policy_mutation(
				  workspace_id,session_id,watermark,mutation_kind,artifact_id,
				  old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version
				) VALUES ($1::uuid,$2::uuid,$4,'current_version',$3::uuid,1,2,1,2);
			`, workspaceID, sessionID, mode.taskID, watermark); execErr != nil {
				t.Fatalf("write paired current-version transition: %v", execErr)
			}
			if finishErr := finishAccessGuardTx(ctx, tx, mode.immediate); finishErr != nil {
				t.Fatalf("paired current-version transition rejected: %v", finishErr)
			}
		})

		t.Run("paired_access/"+mode.name, func(t *testing.T) {
			tx := beginAccessGuardTx(t, ctx, conn.Conn())
			defer tx.Rollback(ctx)
			watermark := reserveAccessGuardWatermark(t, ctx, tx, workspaceID, sessionID)
			if _, execErr := tx.Exec(ctx, `
				UPDATE research_artifact_passport SET current_version=3,eligibility_revision=3
				WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid;
				INSERT INTO research_artifact_policy_mutation(
				  workspace_id,session_id,watermark,mutation_kind,artifact_id,
				  old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version,
				  old_access_level,new_access_level
				) VALUES ($1::uuid,$2::uuid,$4,'access',$3::uuid,2,3,2,3,'raw','redacted');
			`, workspaceID, sessionID, mode.taskID, watermark); execErr != nil {
				t.Fatalf("write paired access transition: %v", execErr)
			}
			if finishErr := finishAccessGuardTx(ctx, tx, mode.immediate); finishErr != nil {
				t.Fatalf("paired access transition rejected: %v", finishErr)
			}
		})

		for _, negative := range []struct {
			name  string
			query string
			wants []string
		}{
			{
				name: "current_version_passport_only",
				query: `UPDATE research_artifact_passport SET current_version=4,eligibility_revision=4
				        WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`,
				wants: []string{"research_artifact_current_version_to_policy_guard", "research_artifact_passport_to_policy_mutation_guard"},
			},
			{
				name: "current_version_ledger_only",
				query: `INSERT INTO research_artifact_policy_mutation(
				          workspace_id,session_id,watermark,mutation_kind,artifact_id,
				          old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version
				        ) VALUES ($1::uuid,$2::uuid,$4,'current_version',$3::uuid,3,4,3,4)`,
				wants: []string{"research_artifact_policy_mutation_to_current_version_guard", "research_artifact_policy_mutation_to_passport_guard"},
			},
			{
				name: "access_passport_only",
				query: `UPDATE research_artifact_passport SET current_version=5,eligibility_revision=4
				        WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`,
				wants: []string{"research_artifact_current_version_to_policy_guard", "research_artifact_passport_to_policy_mutation_guard"},
			},
			{
				name: "access_ledger_only",
				query: `INSERT INTO research_artifact_policy_mutation(
				          workspace_id,session_id,watermark,mutation_kind,artifact_id,
				          old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version,
				          old_access_level,new_access_level
				        ) VALUES ($1::uuid,$2::uuid,$4,'access',$3::uuid,3,4,3,5,'redacted','verified_only')`,
				wants: []string{"research_artifact_policy_mutation_to_current_version_guard", "research_artifact_policy_mutation_to_passport_guard"},
			},
			{
				name: "access_change_as_current_version",
				query: `UPDATE research_artifact_passport SET current_version=5,eligibility_revision=4
				        WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid;
				        INSERT INTO research_artifact_policy_mutation(
				          workspace_id,session_id,watermark,mutation_kind,artifact_id,
				          old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version
				        ) VALUES ($1::uuid,$2::uuid,$4,'current_version',$3::uuid,3,4,3,5)`,
				wants: []string{"research_artifact_current_version_to_policy_guard", "research_artifact_policy_mutation_to_current_version_guard"},
			},
			{
				name: "publication_as_access_change",
				query: `UPDATE research_artifact_passport SET current_version=4,eligibility_revision=4
				        WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid;
				        INSERT INTO research_artifact_policy_mutation(
				          workspace_id,session_id,watermark,mutation_kind,artifact_id,
				          old_eligibility_revision,new_eligibility_revision,old_current_version,new_current_version,
				          old_access_level,new_access_level
				        ) VALUES ($1::uuid,$2::uuid,$4,'access',$3::uuid,3,4,3,4,'raw','redacted')`,
				wants: []string{"research_artifact_current_version_to_policy_guard", "research_artifact_policy_mutation_to_current_version_guard"},
			},
		} {
			t.Run(negative.name+"/"+mode.name, func(t *testing.T) {
				tx := beginAccessGuardTx(t, ctx, conn.Conn())
				defer tx.Rollback(ctx)
				watermark := reserveAccessGuardWatermark(t, ctx, tx, workspaceID, sessionID)
				if _, execErr := tx.Exec(ctx, negative.query, workspaceID, sessionID, mode.taskID, watermark); execErr != nil {
					t.Fatalf("write %s: %v", negative.name, execErr)
				}
				assertAccessGuardConstraint(t, finishAccessGuardTx(ctx, tx, mode.immediate), negative.wants...)
			})
		}
	}

	for index, mode := range modes {
		t.Run("wrong_kind_reciprocity/"+mode.name, func(t *testing.T) {
			tx := beginAccessGuardTx(t, ctx, conn.Conn())
			defer tx.Rollback(ctx)
			wrongID := fmt.Sprintf("39000000-0000-4000-8000-%012d", index+1)
			watermark := reserveAccessGuardWatermark(t, ctx, tx, workspaceID, sessionID)
			if _, execErr := tx.Exec(ctx, `
				INSERT INTO research_task(id,workspace_id,session_id,client_key,goal_version,plan_version)
				VALUES ($3::uuid,$1::uuid,$2::uuid,$5,1,1);
				INSERT INTO research_artifact_passport(
				  id,workspace_id,session_id,entity_kind,eligibility_revision,lifecycle_status,provenance_completeness
				) VALUES ($3::uuid,$1::uuid,$2::uuid,'question',1,'registered','complete');
				INSERT INTO research_artifact_policy_mutation(
				  workspace_id,session_id,watermark,mutation_kind,artifact_id,old_eligibility_revision,new_eligibility_revision
				) VALUES ($1::uuid,$2::uuid,$4,'artifact_create',$3::uuid,0,1);
			`, workspaceID, sessionID, wrongID, watermark, "wrong-kind-"+mode.name); execErr != nil {
				t.Fatalf("write wrong-kind reciprocal pair: %v", execErr)
			}
			assertAccessGuardConstraint(t, finishAccessGuardTx(ctx, tx, mode.immediate),
				"research_task_artifact_passport_guard", "research_artifact_passport_domain_guard")
		})
	}

	_, down362 := readMigrationPair(t, "362_research_artifact_access_policy_guard")
	if _, err = conn.Exec(ctx, down362); err != nil {
		t.Fatalf("apply 362 down: %v", err)
	}
	up362, _ := readMigrationPair(t, "362_research_artifact_access_policy_guard")
	if _, err = conn.Exec(ctx, up362); err != nil {
		t.Fatalf("reapply 362 up: %v", err)
	}
}

func beginAccessGuardTx(t *testing.T, ctx context.Context, conn *pgx.Conn) pgx.Tx {
	t.Helper()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func reserveAccessGuardWatermark(t *testing.T, ctx context.Context, tx pgx.Tx, workspaceID, sessionID string) int64 {
	t.Helper()
	var watermark int64
	if err := tx.QueryRow(ctx, `SELECT research_artifact_reserve_policy_watermark($1::uuid,$2::uuid)`, workspaceID, sessionID).Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	return watermark
}

func finishAccessGuardTx(ctx context.Context, tx pgx.Tx, immediate bool) error {
	if immediate {
		if _, err := tx.Exec(ctx, `SET CONSTRAINTS ALL IMMEDIATE`); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func assertAccessGuardConstraint(t *testing.T, err error, wants ...string) {
	t.Helper()
	pgErr, ok := err.(*pgconn.PgError)
	if ok {
		for _, want := range wants {
			if pgErr.ConstraintName == want {
				return
			}
		}
	}
	t.Fatalf("constraint error=%v want one of %v", err, wants)
}
