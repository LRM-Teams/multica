package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestWithdrawnPassportExcludedFromDispatchManifestPlan(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Supersession manifest", Title: "Supersession",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	claimID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'withdrawn-claim', '', 'withdrawn claim',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status = 'withdrawn', updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID); err != nil {
		t.Fatalf("withdraw passport: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var stateVersion int64
	if err = tx.QueryRow(ctx, `
		SELECT state_version FROM research_session
		WHERE workspace_id = $1::uuid AND id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&stateVersion); err != nil {
		t.Fatal(err)
	}
	module := NewArtifactContextModule()
	plan, err := module.PlanDispatchManifest(ctx, tx, fixture.workspaceID, run.SessionID, stateVersion)
	if err != nil {
		t.Fatalf("PlanDispatchManifest: %v", err)
	}
	for _, entry := range plan.Entries {
		if entry.ArtifactID == claimID {
			t.Fatal("withdrawn claim must not appear in dispatch manifest entries")
		}
	}
	foundOmission := false
	for _, omission := range plan.Omissions {
		if omission.ArtifactID == claimID && omission.OmissionReason == "lifecycle" {
			foundOmission = true
			break
		}
	}
	if !foundOmission {
		t.Fatal("expected withdrawn claim in manifest omissions with lifecycle reason")
	}
}

func TestCreateDispatchIntentRejectsStaleStateVersionWithoutArtifacts(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Stale dispatch", Title: "Stale dispatch",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	input.ExpectedStateVersion = run.StateVersion - 1
	_, _, err = store.CreateDispatchIntent(ctx, input)
	if err == nil {
		t.Fatal("expected stale state version dispatch to fail")
	}
	var manifestCount, outboxCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID).Scan(&manifestCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_dispatch_outbox WHERE attempt_id = $1::uuid
	`, input.AttemptID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if manifestCount != 0 || outboxCount != 0 {
		t.Fatalf("stale dispatch leaked artifacts manifest=%d outbox=%d", manifestCount, outboxCount)
	}
}
