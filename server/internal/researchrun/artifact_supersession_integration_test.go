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
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "withdrawn-claim", "withdrawn claim")

	operationID := uuid.NewString()
	outcome, err := (artifactLifecycleModule{store: store}).Change(ctx, artifactLifecycleChange{
		OperationID: operationID, WorkspaceID: fixture.workspaceID, SessionID: run.SessionID,
		ArtifactID: claimID, Kind: artifactLifecycleWithdraw, Reason: "integration withdrawal",
	})
	if err != nil {
		t.Fatalf("withdraw through lifecycle module: %v", err)
	}
	replayed, err := (artifactLifecycleModule{store: store}).Change(ctx, artifactLifecycleChange{
		OperationID: operationID, WorkspaceID: fixture.workspaceID, SessionID: run.SessionID,
		ArtifactID: claimID, Kind: artifactLifecycleWithdraw, Reason: "integration withdrawal",
	})
	if err != nil || !replayed.Replayed || replayed != (artifactLifecycleOutcome{
		ArtifactID: outcome.ArtifactID, Lifecycle: outcome.Lifecycle,
		EligibilityRevision: outcome.EligibilityRevision, PolicyWatermark: outcome.PolicyWatermark, Replayed: true,
	}) {
		t.Fatalf("withdraw replay=%+v err=%v initial=%+v", replayed, err, outcome)
	}
	oldRevision, newRevision, withdrawalWatermark := outcome.EligibilityRevision-1, outcome.EligibilityRevision, outcome.PolicyWatermark
	var lifecycleEvents, policyMutations int
	if err = pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_lifecycle_event
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid
		     AND old_status='registered' AND new_status='withdrawn'
		     AND old_eligibility_revision=$4 AND new_eligibility_revision=$5
		     AND policy_watermark=$6),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation
		   WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND artifact_id=$3::uuid
		     AND mutation_kind='lifecycle'
		     AND old_eligibility_revision=$4 AND new_eligibility_revision=$5
		     AND old_lifecycle_status='registered' AND new_lifecycle_status='withdrawn'
		     AND watermark=$6)
	`, fixture.workspaceID, run.SessionID, claimID, oldRevision, newRevision, withdrawalWatermark).Scan(&lifecycleEvents, &policyMutations); err != nil {
		t.Fatalf("load withdrawal ledger: %v", err)
	}
	if lifecycleEvents != 1 || policyMutations != 1 {
		t.Fatalf("withdrawal ledger events=%d mutations=%d want 1/1", lifecycleEvents, policyMutations)
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

func withdrawIntegrationArtifact(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, artifactID string,
) (oldRevision, newRevision, watermark int64) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var lockedSessionID string
	if err = tx.QueryRow(ctx, `
		SELECT id::text FROM research_session
		WHERE workspace_id=$1::uuid AND id=$2::uuid
		FOR UPDATE
	`, workspaceID, sessionID).Scan(&lockedSessionID); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `
		SELECT research_artifact_policy_watermark_for_tx($1::uuid, $2::uuid)
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		t.Fatal(err)
	}
	var oldStatus string
	if err = tx.QueryRow(ctx, `
		SELECT lifecycle_status, eligibility_revision
		FROM research_artifact_passport
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		FOR UPDATE
	`, workspaceID, sessionID, artifactID).Scan(&oldStatus, &oldRevision); err != nil {
		t.Fatal(err)
	}
	if oldStatus != string(ArtifactLifecycleRegistered) {
		t.Fatalf("artifact lifecycle=%q want registered", oldStatus)
	}
	newRevision = oldRevision + 1
	if _, err = tx.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status='withdrawn', eligibility_revision=$4
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND lifecycle_status='registered' AND eligibility_revision=$5
	`, workspaceID, sessionID, artifactID, newRevision, oldRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, artifact_id,
		  old_eligibility_revision, new_eligibility_revision,
		  old_lifecycle_status, new_lifecycle_status
		) VALUES ($1::uuid,$2::uuid,$3,'lifecycle',$4::uuid,$5,$6,'registered','withdrawn')
	`, workspaceID, sessionID, watermark, artifactID, oldRevision, newRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_lifecycle_event (
		  workspace_id, session_id, artifact_id, old_status, new_status,
		  old_eligibility_revision, new_eligibility_revision, policy_watermark,
		  actor_type, reason
		) VALUES ($1::uuid,$2::uuid,$3::uuid,'registered','withdrawn',$4,$5,$6,'system','integration withdrawal')
	`, workspaceID, sessionID, artifactID, oldRevision, newRevision, watermark); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	return oldRevision, newRevision, watermark
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
	tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
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
