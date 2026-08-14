package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestClaimMethodStandardDiagnosticsResolveAndRepair(t *testing.T) {
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

	methodID := uuid.NewString()
	claimID := uuid.NewString()
	legacyClaimID := uuid.NewString()
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, methodID, ArtifactKindMethodDecision, `
		INSERT INTO research_decision (
		  id,workspace_id,session_id,decision_kind,actor_type,goal_version,plan_version,outcome
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'research_method','system',1,1,
		  '{"evidence_standards":[{"client_key":"standard.valid"}]}'::jsonb)
	`, methodID, fixture.workspaceID, fixture.sessionID)
	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, claimID, ArtifactKindClaim, `
		INSERT INTO research_claim (
		  id,workspace_id,session_id,client_key,evidence_standard_key,claim_text,goal_version,plan_version
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'claim.test','standard.missing','Claim',1,1)
	`, claimID, fixture.workspaceID, fixture.sessionID)
	assertClaimMethodDiagnostic(t, ctx, pool, claimID, "dangling_local_key")

	if _, err = pool.Exec(ctx, `UPDATE research_claim SET evidence_standard_key='standard.valid' WHERE id=$1::uuid`, claimID); err != nil {
		t.Fatal(err)
	}
	if got := claimMethodDiagnosticCount(t, ctx, pool, claimID); got != 0 {
		t.Fatalf("valid Method standard left %d diagnostics", got)
	}

	if _, err = pool.Exec(ctx, `
		UPDATE research_decision SET outcome='{"evidence_standards":[{"client_key":"standard.valid"},{"client_key":"standard.valid"}]}'::jsonb
		WHERE id=$1::uuid
	`, methodID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `SELECT research_artifact_scan_research_claim_method_diagnostics($1::uuid,$2::uuid,$3::uuid)`, fixture.workspaceID, fixture.sessionID, claimID); err != nil {
		t.Fatal(err)
	}
	assertClaimMethodDiagnostic(t, ctx, pool, claimID, "ambiguous_local_key")

	seedDiagnosticArtifact(t, ctx, pool, fixture.workspaceID, fixture.sessionID, legacyClaimID, ArtifactKindClaim, `
		INSERT INTO research_claim (
		  id,workspace_id,session_id,client_key,evidence_standard_key,claim_text,goal_version,plan_version
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'claim.legacy','','Legacy claim',1,1)
	`, legacyClaimID, fixture.workspaceID, fixture.sessionID)
	if got := claimMethodDiagnosticCount(t, ctx, pool, legacyClaimID); got != 0 {
		t.Fatalf("empty legacy standard key produced %d diagnostics", got)
	}
}

func claimMethodDiagnosticCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_migration_diagnostic
		WHERE owner_kind='claim' AND owner_id=$1::uuid AND field_path='/evidence_standard_key'
	`, claimID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func assertClaimMethodDiagnostic(t *testing.T, ctx context.Context, pool *pgxpool.Pool, claimID, reason string) {
	t.Helper()
	var got string
	if err := pool.QueryRow(ctx, `
		SELECT reason_code FROM research_artifact_migration_diagnostic
		WHERE owner_kind='claim' AND owner_id=$1::uuid AND field_path='/evidence_standard_key'
	`, claimID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != reason {
		t.Fatalf("diagnostic reason=%q want=%q", got, reason)
	}
}
