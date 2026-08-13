package researchrun

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestEvaluatedSubjectSerializationExcludesPrivateArtifactWhileGraderUsesFrozenVersion(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Private evaluation serialization", Title: "Private evaluation serialization",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}

	privateID := uuid.NewString()
	privateContent := "private-content-canary"
	privateMetadata := "private-metadata-canary"
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_stage_eval (
		  id, workspace_id, session_id, stage, passed, score, findings, remediation
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 's1_plan', false, 0.42,
		  jsonb_build_array(jsonb_build_object('code', $4::text, 'metadata', $5::text)),
		  $4
		)
	`, privateID, fixture.workspaceID, run.SessionID, privateContent, privateMetadata); err != nil {
		t.Fatalf("insert private stage eval: %v", err)
	}
	backfillIntegrationArtifactPassport(
		t, ctx, tx, fixture.workspaceID, run.SessionID, privateID, string(ArtifactKindStageEvaluation), nil, nil,
	)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit private stage eval: %v", err)
	}

	var privateHash string
	if err = pool.QueryRow(ctx, `
		SELECT v.content_hash
		FROM research_artifact_passport p
		JOIN research_artifact_version v
		  ON (v.workspace_id, v.session_id, v.artifact_id, v.version) =
		     (p.workspace_id, p.session_id, p.id, p.current_version)
		WHERE p.workspace_id = $1::uuid AND p.session_id = $2::uuid AND p.id = $3::uuid
	`, fixture.workspaceID, run.SessionID, privateID).Scan(&privateHash); err != nil {
		t.Fatalf("load private content hash: %v", err)
	}
	if privateHash == "" {
		t.Fatal("private artifact content hash is empty")
	}

	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	subjectInput := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	subjectAttempt, _, err := store.CreateDispatchIntent(ctx, subjectInput)
	if err != nil {
		t.Fatalf("CreateDispatchIntent subject: %v", err)
	}
	subjectPrompt := loadIntegrationDispatchPrompt(t, ctx, pool, subjectAttempt.ID)
	for label, canary := range map[string]string{
		"private artifact ID":  privateID,
		"private content hash": privateHash,
		"private metadata":     privateMetadata,
		"private content":      privateContent,
	} {
		if strings.Contains(subjectPrompt, canary) {
			t.Fatalf("evaluated subject prompt leaked %s %q", label, canary)
		}
	}

	graderTaskID := uuid.NewString()
	insertIntegrationTasksWithPassports(t, ctx, pool, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion, `
		INSERT INTO research_task (
		  id, workspace_id, session_id, client_key, kind, objective,
		  required_capability, expected_result, status, goal_version, plan_version,
		  max_attempts, timeout_seconds, ready_at
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'private-grader', 'quality_gate', 'grade frozen private context',
		  'lead', 'research_quality_evaluation_v1', 'ready', $4, $5, 1, 300, now()
		)
	`, []any{graderTaskID, fixture.workspaceID, run.SessionID, run.GoalVersion, run.PlanVersion}, []string{graderTaskID})
	graderInput := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, graderTaskID, fixture.agentID)
	graderAttempt, _, err := store.CreateDispatchIntent(ctx, graderInput)
	if err != nil {
		t.Fatalf("CreateDispatchIntent grader: %v", err)
	}
	graderPrompt := loadIntegrationDispatchPrompt(t, ctx, pool, graderAttempt.ID)
	for label, canary := range map[string]string{
		"private artifact ID": privateID,
		"private metadata":    privateMetadata,
		"private content":     privateContent,
	} {
		if !strings.Contains(graderPrompt, canary) {
			t.Fatalf("grader prompt missing frozen %s %q", label, canary)
		}
	}
	if strings.Contains(graderPrompt, privateHash) {
		t.Fatalf("grader prompt leaked private passport hash %q", privateHash)
	}

	laterPrivateID := uuid.NewString()
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_stage_eval (
		  id, workspace_id, session_id, stage, passed, score, findings, remediation
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 's1_plan', true, 1, '[]'::jsonb, 'later-private-version')
	`, laterPrivateID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert later private stage eval: %v", err)
	}
	backfillIntegrationArtifactPassport(
		t, ctx, tx, fixture.workspaceID, run.SessionID, laterPrivateID, string(ArtifactKindStageEvaluation), nil, nil,
	)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit later private stage eval: %v", err)
	}

	replayed, err := replayDispatchPromptFromManifest(ctx, store, fixture.workspaceID, graderAttempt.ID)
	if err != nil {
		t.Fatalf("replay grader prompt: %v", err)
	}
	if replayed != graderPrompt {
		t.Fatal("grader prompt changed after a later evaluation-private version was created")
	}
	if strings.Contains(replayed, laterPrivateID) || strings.Contains(replayed, "later-private-version") {
		t.Fatal("grader prompt included evaluation-private data created after its manifest")
	}
}

func loadIntegrationDispatchPrompt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, attemptID string) string {
	t.Helper()
	var prompt string
	if err := pool.QueryRow(ctx, `
		SELECT request_payload->>'prompt'
		FROM research_dispatch_outbox
		WHERE attempt_id = $1::uuid
	`, attemptID).Scan(&prompt); err != nil {
		t.Fatalf("load dispatch prompt: %v", err)
	}
	return prompt
}

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
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `
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
		t, ctx, tx, fixture.workspaceID, run.SessionID, stageEvalID, string(ArtifactKindStageEvaluation), nil, nil,
	)
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit stage eval and passport: %v", err)
	}

	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "allowed-claim", "ordinary task claim")

	tx, err = pool.Begin(ctx)
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
