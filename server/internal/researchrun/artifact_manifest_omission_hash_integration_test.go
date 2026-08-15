package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptResultRejectsRewrittenManifestOmission(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
		LeadAgentID: fixture.agentID, Goal: "Omission hash", Title: "Omission hash",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	task := tasks[0]
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "omitted-claim", "omitted claim")
	mutateIntegrationArtifactForCASTest(t, ctx, pool, `
		UPDATE research_artifact_passport
		SET lifecycle_status = 'withdrawn'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID)

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}

	mutateIntegrationArtifactForCASTest(t, ctx, pool, `
		UPDATE research_artifact_context_omission omission
		SET reason = 'policy_denied'
		FROM research_artifact_context_manifest manifest
		WHERE manifest.id = omission.manifest_id
		  AND manifest.workspace_id = omission.workspace_id
		  AND manifest.session_id = omission.session_id
		  AND manifest.attempt_id = $1::uuid
		  AND omission.candidate_version_id = (
		    SELECT id FROM research_artifact_version
		    WHERE workspace_id = $2::uuid AND session_id = $3::uuid AND artifact_id = $4::uuid
		  )
	`, attempt.ID, fixture.workspaceID, run.SessionID, claimID)

	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	result, resultHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: resultHash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}
