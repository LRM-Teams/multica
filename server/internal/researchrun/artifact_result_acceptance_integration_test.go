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

func TestAcceptResultRejectsWhenManifestPolicyWatermarkAhead(t *testing.T) {
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

	attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET policy_watermark = policy_watermark + 5
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("inflate manifest watermark: %v", err)
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func TestAcceptResultAcceptsAfterUnrelatedPolicyWatermarkAdvance(t *testing.T) {
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

	attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("advance unrelated policy watermark: %v", err)
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if err != nil {
		t.Fatalf("AcceptResult: %v", err)
	}
	if outcome.TaskID != task.ID {
		t.Fatalf("outcome=%+v", outcome)
	}
	var resultArtifacts int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_result_artifact
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID).Scan(&resultArtifacts); err != nil {
		t.Fatal(err)
	}
	if resultArtifacts != 1 {
		t.Fatalf("result artifacts=%d want 1", resultArtifacts)
	}
}

func TestAcceptResultReplayRequiresMatchingHash(t *testing.T) {
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

	attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	input := AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	}
	if _, err = store.AcceptResult(ctx, input); err != nil {
		t.Fatalf("first AcceptResult: %v", err)
	}
	replayed, err := store.AcceptResult(ctx, input)
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay err=%v outcome=%+v", err, replayed)
	}
	changed := result
	if changed.Plan != nil && len(changed.Plan.Tasks) > 0 {
		changed.Plan.Tasks[0].Objective = "changed objective for conflict test"
	}
	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	_, changedHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, changedRaw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: input.SessionID, AttemptID: input.AttemptID, AgentID: input.AgentID,
		InboxTaskID: input.InboxTaskID, Raw: changedRaw, Result: changed, Hash: changedHash,
	})
	if !errors.Is(err, ErrResultConflict) {
		t.Fatalf("changed replay err=%v want ErrResultConflict", err)
	}
}

func TestAcceptResultRejectsWhenManifestEntryEligibilityAdvances(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Affected eligibility gate", Title: "Affected eligibility",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	seedIntegrationClaimArtifact(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, "accept-eligibility-claim", "claim for accept eligibility")

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	mutateIntegrationArtifactForCASTest(t, ctx, pool, `
		UPDATE research_artifact_passport
		SET eligibility_revision = eligibility_revision + 1
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID)

	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
	var attemptStatus string
	if err = pool.QueryRow(ctx, `
		SELECT status FROM research_task_attempt WHERE id = $1::uuid
	`, attempt.ID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != "dispatching" {
		t.Fatalf("attempt status=%q want dispatching after failed accept", attemptStatus)
	}
}

func TestAcceptResultRejectsWhenManifestEntryArtifactWithdrawn(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Withdrawn in-flight accept", Title: "Withdrawn accept",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'accept-withdraw-claim', '', 'claim withdrawn before accept',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := uuid.NewString()
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_passport
		SET lifecycle_status = 'withdrawn'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
	`, fixture.workspaceID, run.SessionID, claimID); err != nil {
		t.Fatalf("withdraw passport: %v", err)
	}

	raw, err := json.Marshal(validPlanResult(t))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func TestAcceptResultRejectsWhenManifestEntryRepresentationChanges(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Representation tamper", Title: "Representation tamper",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	claimID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'accept-repr-claim', '', 'claim for representation tamper',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := uuid.NewString()
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_entry e
		SET representation_bytes = convert_to('sha256:tampered-representation', 'UTF8')
		FROM research_artifact_context_manifest m
		WHERE e.manifest_id = m.id
		  AND m.workspace_id = $1::uuid
		  AND m.session_id = $2::uuid
		  AND m.attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("tamper representation bytes: %v", err)
	}

	raw, err := json.Marshal(validPlanResult(t))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func TestAcceptResultRejectsWhenManifestHashTampered(t *testing.T) {
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

	attempt, inboxID, raw, run, task := setupRunningPlanAttempt(t, ctx, store, fixture)
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_context_manifest
		SET manifest_hash = 'sha256:tampered-manifest-hash'
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("tamper manifest hash: %v", err)
	}

	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("AcceptResult err=%v want ErrInvalidTransition", err)
	}
}

func setupRunningPlanAttempt(
	t *testing.T,
	ctx context.Context,
	store *PostgresStore,
	fixture researchRunFixture,
) (Attempt, string, json.RawMessage, Run, Task) {
	t.Helper()
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Acceptance integration", Title: "Acceptance",
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
	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID)
	attempt, _, err := store.CreateDispatchIntent(ctx, input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, store.pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	raw, err := json.Marshal(upgradeResultToV5(validV4PlanResult(t)))
	if err != nil {
		t.Fatal(err)
	}
	return attempt, inboxID, raw, run, task
}
