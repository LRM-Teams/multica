package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMigrationDiagnosticBlocksManifestAdmissionUntilRepair(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fixture)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Diagnostic admission", Title: "Diagnostic admission",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	allowedClaimID := uuid.NewString()
	seedIntegrationClaimArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID,
		allowedClaimID, "diagnostic-positive-control", "same-scope allowed control",
	)

	messageID := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_message (
		  id, workspace_id, session_id, sender_type, body
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'system', 'legacy message with unresolved graph reference')
	`, messageID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatal(err)
	}
	if err = RegisterProductionResearchMessageTx(ctx, tx, fixture.workspaceID, run.SessionID, messageID); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_migration_diagnostic (
		  workspace_id, session_id, owner_kind, owner_id, field_path,
		  expected_target_kind, reference_value, reason_code
		) VALUES (
		  $1::uuid, $2::uuid, 'research_message', $3::uuid,
		  '/meta/match_decision/matched_node_ids/0', 'graph_node',
		  'missing-node', 'unresolved_reference'
		)
	`, fixture.workspaceID, run.SessionID, messageID); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	planWithDiagnostic := loadDiagnosticAdmissionPlan(t, ctx, pool, fixture.workspaceID, run.SessionID, run.StateVersion)
	if _, ok := manifestEntryForArtifact(planWithDiagnostic, messageID); ok {
		t.Fatal("diagnostic-bearing message entered the manifest")
	}
	if !manifestOmissionMatches(planWithDiagnostic, messageID, "policy_denied") {
		t.Fatal("diagnostic-bearing message lacked a bounded policy_denied omission")
	}
	if _, ok := manifestEntryForArtifact(planWithDiagnostic, allowedClaimID); !ok {
		t.Fatal("diagnostic denial hid the same-scope allowed control")
	}

	if _, err = pool.Exec(ctx, `
		DELETE FROM research_artifact_migration_diagnostic
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid
		  AND owner_kind='research_message' AND owner_id=$3::uuid
	`, fixture.workspaceID, run.SessionID, messageID); err != nil {
		t.Fatal(err)
	}
	planAfterRepair := loadDiagnosticAdmissionPlan(t, ctx, pool, fixture.workspaceID, run.SessionID, run.StateVersion)
	if _, ok := manifestEntryForArtifact(planAfterRepair, messageID); !ok {
		t.Fatal("repaired message remained denied after diagnostic clearance")
	}
}

func loadDiagnosticAdmissionPlan(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID string,
	stateVersion int64,
) dispatchManifestPlan {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	plan, err := NewArtifactContextModule().PlanDispatchManifest(ctx, tx, workspaceID, sessionID, stateVersion)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func manifestOmissionMatches(plan dispatchManifestPlan, artifactID, reason string) bool {
	for _, omission := range plan.Omissions {
		if omission.ArtifactID == artifactID && omission.OmissionReason == reason {
			return true
		}
	}
	return false
}
