package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEvaluationPrivateStageEvalExcludedFromTaskExecutionManifest(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Evaluation compartment", Title: "Evaluation compartment",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	stageEvalID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_stage_eval (
		  id, workspace_id, session_id, stage, passed, score, findings, remediation
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 's1_plan', false, 0.42,
		  '[{"code":"hidden_rubric","detail":"private grader expectation"}]'::jsonb,
		  'Improve plan coverage'
		)
	`, stageEvalID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert stage eval: %v", err)
	}
	backfillIntegrationArtifactPassport(
		t, ctx, pool, fixture.workspaceID, run.SessionID, stageEvalID, string(ArtifactKindStageEvaluation), nil, nil,
	)

	claimID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'allowed-claim', '', 'ordinary task claim',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

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
		if entry.ArtifactID == stageEvalID {
			t.Fatal("evaluation-private stage eval must not appear in task execution manifest entries")
		}
	}
	foundOmission := false
	for _, omission := range plan.Omissions {
		if omission.ArtifactID == stageEvalID && omission.OmissionReason == "evaluation_compartment" {
			foundOmission = true
			break
		}
	}
	if !foundOmission {
		t.Fatal("expected stage eval omission with evaluation_compartment reason")
	}
	foundAllowed := false
	for _, entry := range plan.Entries {
		if entry.ArtifactID == claimID {
			foundAllowed = true
			break
		}
	}
	if !foundAllowed {
		t.Fatal("expected positive control claim in manifest entries")
	}

	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	var included bool
	if err = pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM research_artifact_context_entry e
		  JOIN research_artifact_context_manifest m
		    ON m.workspace_id = e.workspace_id
		   AND m.session_id = e.session_id
		   AND m.id = e.manifest_id
		  JOIN research_artifact_version v
		    ON v.workspace_id = e.workspace_id
		   AND v.session_id = e.session_id
		   AND v.id = e.artifact_version_id
		  WHERE m.attempt_id = $1::uuid AND v.artifact_id = $2::uuid
		)
	`, attempt.ID, stageEvalID).Scan(&included); err != nil {
		t.Fatal(err)
	}
	if included {
		t.Fatal("persisted manifest must not include evaluation-private stage eval")
	}
}
