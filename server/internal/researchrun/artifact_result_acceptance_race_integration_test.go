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

type acceptanceRaceFixture struct {
	pool      *pgxpool.Pool
	store     *PostgresStore
	fixture   researchRunFixture
	run       Run
	task      Task
	attempt   Attempt
	inboxID   string
	claimID   string
	input     AcceptResultInput
}

func setupPlanAcceptanceRaceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) acceptanceRaceFixture {
	t.Helper()
	fixture := seedResearchRunFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Accept race", Title: "Accept race",
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
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, evidence_standard_key, claim_text,
		  significance, confidence, status, goal_version, plan_version, resolution
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, 'accept-race-claim', '', 'claim referenced by manifest',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID))
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := uuid.NewString()
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	raw, err := json.Marshal(validPlanResult(t))
	if err != nil {
		t.Fatal(err)
	}
	result, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	return acceptanceRaceFixture{
		pool: pool, store: store, fixture: fixture, run: run, task: task,
		attempt: attempt, inboxID: inboxID, claimID: claimID,
		input: AcceptResultInput{
			SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
			InboxTaskID: inboxID, Raw: raw, Result: result, Hash: hash,
		},
	}
}

func invokeAcceptWithBeforeCommitFault(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) {
	t.Helper()
	injected := errors.New("injected accept before_commit")
	fault := &oneShotResearchTxFault{
		operation: txOpResultAccept,
		point:     txBeforeCommit,
		err:       injected,
	}
	fx.store.txFaultHook = fault.hook
	_, err := fx.store.AcceptResult(ctx, fx.input)
	fx.store.txFaultHook = nil
	if !errors.Is(err, injected) {
		t.Fatalf("AcceptResult err=%v want injected before_commit fault", err)
	}
	if !fault.fired {
		t.Fatal("before_commit fault did not fire")
	}
}

func assertAcceptanceRolledBack(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) {
	t.Helper()
	var attemptStatus string
	var resultArtifacts int
	if err := fx.pool.QueryRow(ctx, `
		SELECT status FROM research_task_attempt WHERE id = $1::uuid
	`, fx.attempt.ID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != string(AttemptStatusDispatching) {
		t.Fatalf("attempt status=%q want dispatching after rolled-back accept", attemptStatus)
	}
	if err := fx.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_result_artifact
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID).Scan(&resultArtifacts); err != nil {
		t.Fatal(err)
	}
	if resultArtifacts != 0 {
		t.Fatalf("result artifacts=%d want 0 after rolled-back accept", resultArtifacts)
	}
}

func TestAcceptResultRaceRejectsWhenPreflightFactsChangeAfterRolledBackAccept(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	cases := []struct {
		name   string
		mutate func(context.Context, acceptanceRaceFixture) error
	}{
		{
			name: "eligibility_revision",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_passport
					SET eligibility_revision = eligibility_revision + 1
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return err
			},
		},
		{
			name: "withdrawn_lifecycle",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_passport
					SET lifecycle_status = 'withdrawn'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return err
			},
		},
		{
			name: "version_content_hash",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_version
					SET content_hash = 'sha256:mutated-after-preflight'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return err
			},
		},
		{
			name: "manifest_hash",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_context_manifest
					SET manifest_hash = 'sha256:tampered-after-preflight'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupPlanAcceptanceRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			invokeAcceptWithBeforeCommitFault(t, ctx, fx)
			assertAcceptanceRolledBack(t, ctx, fx)
			if err := tc.mutate(ctx, fx); err != nil {
				t.Fatalf("mutate: %v", err)
			}
			if _, err = fx.store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("AcceptResult after mutation err=%v want ErrInvalidTransition", err)
			}
			assertAcceptanceRolledBack(t, ctx, fx)
		})
	}
}

func TestAcceptResultRaceAcceptsAfterRolledBackAcceptWhenOnlyUnrelatedWatermarkAdvances(t *testing.T) {
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

	fx := setupPlanAcceptanceRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeAcceptWithBeforeCommitFault(t, ctx, fx)
	assertAcceptanceRolledBack(t, ctx, fx)
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID); err != nil {
		t.Fatalf("advance unrelated watermark: %v", err)
	}
	outcome, err := fx.store.AcceptResult(ctx, fx.input)
	if err != nil {
		t.Fatalf("AcceptResult after unrelated watermark advance: %v", err)
	}
	if outcome.TaskID != fx.task.ID {
		t.Fatalf("outcome=%+v", outcome)
	}
	var resultArtifacts int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_result_artifact
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID).Scan(&resultArtifacts); err != nil {
		t.Fatal(err)
	}
	if resultArtifacts != 1 {
		t.Fatalf("result artifacts=%d want 1", resultArtifacts)
	}
}

func TestAcceptResultRaceConvergesAfterRolledBackAcceptWithoutMutation(t *testing.T) {
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

	fx := setupPlanAcceptanceRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeAcceptWithBeforeCommitFault(t, ctx, fx)
	assertAcceptanceRolledBack(t, ctx, fx)
	outcome, err := fx.store.AcceptResult(ctx, fx.input)
	if err != nil {
		t.Fatalf("AcceptResult retry: %v", err)
	}
	if outcome.TaskID != fx.task.ID {
		t.Fatalf("outcome=%+v", outcome)
	}
	replayed, err := fx.store.AcceptResult(ctx, fx.input)
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay err=%v outcome=%+v", err, replayed)
	}
}
