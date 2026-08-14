package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEvaluationDecisionLocalReferenceDiagnosticsRepair(t *testing.T) {
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

	reportID := uuid.NewString()
	claimID := uuid.NewString()
	decisionID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report (id,workspace_id,session_id,revision,content_md,structured,goal_version,plan_version)
		VALUES ($1::uuid,$2::uuid,$3::uuid,1,'Report','{"sections":[{"id":"section.valid"}]}',1,1)
	`, reportID, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (id,workspace_id,session_id,client_key,claim_text,goal_version,plan_version)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'claim.valid','Valid claim',1,1)
	`, claimID, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report_claim (workspace_id,session_id,report_id,claim_id,section_id)
		VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,'section.valid')
	`, fixture.workspaceID, fixture.sessionID, reportID, claimID); err != nil {
		t.Fatal(err)
	}

	badOutcome := `{
		"reviewed_claim_keys":["claim.missing"],
		"reviewed_section_ids":["section.valid"],
		"defects":[{"claim_keys":["claim.valid"],"section_ids":["section.missing"]}]
	}`
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_decision (
		  id,workspace_id,session_id,decision_kind,actor_type,goal_version,plan_version,inputs,outcome
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'quality_gate','system',1,1,
		  jsonb_build_object('report_id',$4::text),$5::jsonb)
	`, decisionID, fixture.workspaceID, fixture.sessionID, reportID, badOutcome); err != nil {
		t.Fatal(err)
	}
	assertEvaluationLocalDiagnostic(t, ctx, pool, decisionID, "/outcome/reviewed_claim_keys/0", "dangling_local_key")
	assertEvaluationLocalDiagnostic(t, ctx, pool, decisionID, "/outcome/defects/0/section_ids/0", "dangling_local_key")

	goodOutcome := `{
		"reviewed_claim_keys":["claim.valid"],
		"reviewed_section_ids":["section.valid"],
		"defects":[{"claim_keys":["claim.valid"],"section_ids":["section.valid"]}]
	}`
	if _, err = pool.Exec(ctx, `UPDATE research_decision SET outcome=$2::jsonb WHERE id=$1::uuid`, decisionID, goodOutcome); err != nil {
		t.Fatal(err)
	}
	if got := evaluationLocalDiagnosticCount(t, ctx, pool, decisionID); got != 0 {
		t.Fatalf("valid report-local repair left %d diagnostics", got)
	}

	if _, err = pool.Exec(ctx, `UPDATE research_decision SET outcome='{"reviewed_claim_keys":{}}'::jsonb WHERE id=$1::uuid`, decisionID); err != nil {
		t.Fatal(err)
	}
	assertEvaluationLocalDiagnostic(t, ctx, pool, decisionID, "/outcome/reviewed_claim_keys", "unknown_schema")
}

func evaluationLocalDiagnosticCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, decisionID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_migration_diagnostic
		WHERE owner_kind='evaluation_decision' AND owner_id=$1::uuid
		  AND (field_path LIKE '/outcome/reviewed\_%' ESCAPE '\' OR field_path LIKE '/outcome/defects/%')
	`, decisionID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertEvaluationLocalDiagnostic(t *testing.T, ctx context.Context, pool *pgxpool.Pool, decisionID, path, reason string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `
		SELECT reason_code FROM research_artifact_migration_diagnostic
		WHERE owner_kind='evaluation_decision' AND owner_id=$1::uuid AND field_path=$2
	`, decisionID, path).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != reason {
		t.Fatalf("diagnostic reason=%q want=%q", got, reason)
	}
}
