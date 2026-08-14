package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestTaskRemediationDiagnosticsResolveRepairAndIgnoreOpaqueCriteria(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)

	questionID := uuid.NewString()
	controlTaskID := uuid.NewString()
	opaqueTaskID := uuid.NewString()
	otherClaimID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, questionID, ArtifactKindQuestion, `
		INSERT INTO research_question (
		  id,workspace_id,session_id,client_key,kind,question,goal_version,plan_version
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'q.valid','gap','What remains?',1,1)
	`, questionID, fixture.workspaceID, fixture.sessionID)
	other := seedResearchRunFixture(t, ctx, pool)
	seedDiagnosticArtifact(t, ctx, pool, other.workspaceID, other.sessionID, otherClaimID, ArtifactKindClaim, `
		INSERT INTO research_claim (
		  id,workspace_id,session_id,client_key,claim_text,goal_version,plan_version
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'claim.other','Other claim',1,1)
	`, otherClaimID, other.workspaceID, other.sessionID)

	insertTask := `INSERT INTO research_task (
		id,workspace_id,session_id,client_key,kind,objective,required_capability,
		expected_result,acceptance_criteria,goal_version,plan_version
	) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,'discover','Repair','scout',
	  'research_evidence_v5',$5::jsonb,1,1)`
	malformed := `{"remediation":{"target_findings":[{"metadata":{"question_id":"not-a-uuid"}}]}}`
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, opaqueTaskID, ArtifactKindTask,
		insertTask, opaqueTaskID, fixture.workspaceID, fixture.sessionID, "agent.task", malformed)
	if got := taskDiagnosticCount(t, ctx, pool, opaqueTaskID); got != 0 {
		t.Fatalf("opaque Agent criteria produced %d diagnostics", got)
	}
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, controlTaskID, ArtifactKindTask,
		insertTask, controlTaskID, fixture.workspaceID, fixture.sessionID, "control:discover:1:1:1", malformed)
	assertTaskDiagnostic(t, ctx, pool, controlTaskID,
		"/acceptance_criteria/remediation/target_findings/0/metadata/question_id", "malformed_uuid")

	valid := `{"remediation":{"target_findings":[{"metadata":{"question_id":"` + questionID + `"}}]}}`
	if _, err = pool.Exec(ctx, `UPDATE research_task SET acceptance_criteria=$2::jsonb WHERE id=$1::uuid`, controlTaskID, valid); err != nil {
		t.Fatal(err)
	}
	if got := taskDiagnosticCount(t, ctx, pool, controlTaskID); got != 0 {
		t.Fatalf("same-scope repair left %d diagnostics", got)
	}

	crossScope := `{"remediation":{"target_findings":[{"metadata":{"answer_claim_id":"` + otherClaimID + `"}}]}}`
	if _, err = pool.Exec(ctx, `UPDATE research_task SET acceptance_criteria=$2::jsonb WHERE id=$1::uuid`, controlTaskID, crossScope); err != nil {
		t.Fatal(err)
	}
	assertTaskDiagnostic(t, ctx, pool, controlTaskID,
		"/acceptance_criteria/remediation/target_findings/0/metadata/answer_claim_id", "cross_scope_reference")

	localKey := `{"remediation":{"target_findings":[{"metadata":{"claim_key":"claim.repair"}}]}}`
	if _, err = pool.Exec(ctx, `UPDATE research_task SET acceptance_criteria=$2::jsonb WHERE id=$1::uuid`, controlTaskID, localKey); err != nil {
		t.Fatal(err)
	}
	assertTaskDiagnostic(t, ctx, pool, controlTaskID,
		"/acceptance_criteria/remediation/target_findings/0/metadata/claim_key", "dangling_local_key")
	repairClaimID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, repairClaimID, ArtifactKindClaim, `
		INSERT INTO research_claim (id,workspace_id,session_id,client_key,claim_text,goal_version,plan_version)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'claim.repair','Repaired claim',1,1)
	`, repairClaimID, fixture.workspaceID, fixture.sessionID)
	if _, err = pool.Exec(ctx, `UPDATE research_task SET acceptance_criteria=acceptance_criteria WHERE id=$1::uuid`, controlTaskID); err != nil {
		t.Fatal(err)
	}
	if got := taskDiagnosticCount(t, ctx, pool, controlTaskID); got != 0 {
		t.Fatalf("local-key repair left %d diagnostics", got)
	}
}

func taskDiagnosticCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*)::int FROM research_artifact_migration_diagnostic WHERE owner_kind='task' AND owner_id=$1::uuid`, taskID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertTaskDiagnostic(t *testing.T, ctx context.Context, pool *pgxpool.Pool, taskID, path, reason string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `SELECT reason_code FROM research_artifact_migration_diagnostic WHERE owner_kind='task' AND owner_id=$1::uuid AND field_path=$2`, taskID, path).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != reason {
		t.Fatalf("diagnostic reason=%q want=%q", got, reason)
	}
}
