package researchrun

import (
	"context"
	"errors"
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

	subjectSnapshot, err := store.TaskContextForAttempt(ctx, subjectAttempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("load subject surface before grader revocation: %v", err)
	}
	assertEvaluationPrivateAbsent(t, subjectSnapshot, privateID, laterPrivateID)
	if subjectSnapshot.ArtifactProjection == nil || len(subjectSnapshot.ArtifactProjection.Items) == 0 {
		t.Fatal("subject lost every same-scope allowed passport from its projection")
	}

	graderSnapshot, err := store.TaskContextForAttempt(ctx, graderAttempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("load grader surface before revocation: %v", err)
	}
	if len(graderSnapshot.EvaluationPrivate) == 0 {
		t.Fatal("authorized grader surface omitted frozen evaluation-private context")
	}
	if !projectionContainsEntity(graderSnapshot.ArtifactProjection, privateID) {
		t.Fatalf("authorized grader projection omitted private passport %q", privateID)
	}
	if projectionContainsEntity(graderSnapshot.ArtifactProjection, laterPrivateID) {
		t.Fatalf("grader projection included post-manifest private passport %q", laterPrivateID)
	}

	privateEntriesBefore := countManifestEntriesForArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID, graderAttempt.ID, privateID,
	)
	if privateEntriesBefore != 1 {
		t.Fatalf("grader frozen private entries=%d want=1", privateEntriesBefore)
	}
	revokeIntegrationManifestEvaluationGrant(t, ctx, pool, fixture.workspaceID, run.SessionID, graderAttempt.ID)
	if _, err = store.TaskContextForAttempt(ctx, graderAttempt.ID, fixture.workspaceID); !errors.Is(err, ErrArtifactAccessDenied) || !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("revoked grader surface err=%v want access denial and transition compatibility", err)
	}
	if privateEntriesAfter := countManifestEntriesForArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID, graderAttempt.ID, privateID,
	); privateEntriesAfter != privateEntriesBefore {
		t.Fatalf("evaluation grant revocation changed frozen private history %d→%d", privateEntriesBefore, privateEntriesAfter)
	}

	subjectAfterRevocation, err := store.TaskContextForAttempt(ctx, subjectAttempt.ID, fixture.workspaceID)
	if err != nil {
		t.Fatalf("grader revocation denied unrelated subject surface: %v", err)
	}
	assertEvaluationPrivateAbsent(t, subjectAfterRevocation, privateID, laterPrivateID)
	if subjectAfterRevocation.ArtifactProjection == nil || len(subjectAfterRevocation.ArtifactProjection.Items) == 0 {
		t.Fatal("grader revocation removed subject's same-scope allowed projection")
	}
}

func assertEvaluationPrivateAbsent(t *testing.T, snapshot RunSnapshot, deniedIDs ...string) {
	t.Helper()
	if len(snapshot.EvaluationPrivate) != 0 {
		t.Fatalf("subject surface exposed evaluation-private context: %+v", snapshot.EvaluationPrivate)
	}
	for _, deniedID := range deniedIDs {
		if projectionContainsEntity(snapshot.ArtifactProjection, deniedID) {
			t.Fatalf("subject projection exposed evaluation-private passport %q", deniedID)
		}
	}
}

func projectionContainsEntity(projection *ArtifactProjection, entityID string) bool {
	if projection == nil {
		return false
	}
	for _, item := range projection.Items {
		if item.EntityID == entityID {
			return true
		}
	}
	return false
}

func countManifestEntriesForArtifact(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID, artifactID string,
) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		FROM research_artifact_context_entry entry
		JOIN research_artifact_context_manifest manifest
		  ON (manifest.workspace_id, manifest.session_id, manifest.id) =
		     (entry.workspace_id, entry.session_id, entry.manifest_id)
		WHERE manifest.workspace_id = $1::uuid
		  AND manifest.session_id = $2::uuid
		  AND manifest.attempt_id = $3::uuid
		  AND entry.artifact_id = $4::uuid
	`, workspaceID, sessionID, attemptID, artifactID).Scan(&count); err != nil {
		t.Fatalf("count frozen manifest entries for artifact: %v", err)
	}
	return count
}

func revokeIntegrationManifestEvaluationGrant(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID string,
) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	var grantID string
	var oldRevision, watermark int64
	if err = tx.QueryRow(ctx, `
		SELECT evaluation_grant_id::text, evaluation_grant_revision
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, workspaceID, sessionID, attemptID).Scan(&grantID, &oldRevision); err != nil {
		t.Fatalf("load evaluation grant: %v", err)
	}
	if err = tx.QueryRow(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		RETURNING watermark
	`, workspaceID, sessionID).Scan(&watermark); err != nil {
		t.Fatalf("advance evaluation revocation watermark: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		UPDATE research_artifact_policy_grant
		SET status = 'revoked', revision = revision + 1, revoked_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, workspaceID, sessionID, grantID); err != nil {
		t.Fatalf("revoke evaluation grant: %v", err)
	}
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_artifact_policy_mutation (
		  workspace_id, session_id, watermark, mutation_kind, policy_grant_id,
		  old_grant_revision, new_grant_revision, old_grant_status, new_grant_status
		) VALUES ($1::uuid, $2::uuid, $3, 'grant_revoke', $4::uuid, $5, $6, 'active', 'revoked')
	`, workspaceID, sessionID, watermark, grantID, oldRevision, oldRevision+1); err != nil {
		t.Fatalf("record evaluation grant revocation: %v", err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatalf("commit evaluation grant revocation: %v", err)
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
	humanProjection, err := (artifactProjectionModule{store: store}).Load(
		ctx, fixture.workspaceID, run.SessionID, artifactProjectionScope{},
	)
	if err != nil {
		t.Fatalf("load human artifact projection: %v", err)
	}
	for _, item := range humanProjection.Items {
		if item.EntityID == stageEvalID {
			t.Fatalf("human projection leaked evaluation-private artifact existence: %+v", item)
		}
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
