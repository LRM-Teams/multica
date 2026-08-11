package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type dispatchRaceFixture struct {
	pool    *pgxpool.Pool
	store   *PostgresStore
	fixture researchRunFixture
	run     Run
	task    Task
	claimID string
	input   CreateDispatchIntentInput
}

func setupDispatchRaceFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) dispatchRaceFixture {
	t.Helper()
	fixture := seedResearchRunFixture(t, ctx, pool)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{
		WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Dispatch race", Title: "Dispatch race",
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
		  $1::uuid, $2::uuid, $3::uuid, 'dispatch-race-claim', '', 'claim for dispatch race',
		  0.5, 0.5, 'proposed', 1, 1, ''
		)
	`, claimID, fixture.workspaceID, run.SessionID); err != nil {
		t.Fatalf("insert claim: %v", err)
	}
	backfillIntegrationArtifactPassport(t, ctx, pool, fixture.workspaceID, run.SessionID, claimID, string(ArtifactKindClaim), intPtr(1), intPtr(1))

	input := testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID)
	return dispatchRaceFixture{
		pool: pool, store: store, fixture: fixture, run: run, task: task,
		claimID: claimID, input: input,
	}
}

func invokeDispatchWithBeforeCommitFault(t *testing.T, ctx context.Context, fx dispatchRaceFixture) {
	t.Helper()
	injected := errors.New("injected dispatch before_commit")
	fault := &oneShotResearchTxFault{
		operation: txOpDispatchIntentCreate,
		point:     txBeforeCommit,
		err:       injected,
	}
	fx.store.txFaultHook = fault.hook
	_, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	fx.store.txFaultHook = nil
	if !errors.Is(err, injected) {
		t.Fatalf("CreateDispatchIntent err=%v want injected before_commit fault", err)
	}
	if !fault.fired {
		t.Fatal("before_commit fault did not fire")
	}
}

func assertDispatchRolledBack(t *testing.T, ctx context.Context, fx dispatchRaceFixture) {
	t.Helper()
	var attemptCount, manifestCount, outboxCount int
	if err := fx.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_task_attempt WHERE id = $1::uuid
	`, fx.input.AttemptID).Scan(&attemptCount); err != nil {
		t.Fatal(err)
	}
	if err := fx.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID).Scan(&manifestCount); err != nil {
		t.Fatal(err)
	}
	if err := fx.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_dispatch_outbox WHERE attempt_id = $1::uuid
	`, fx.input.AttemptID).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if attemptCount != 0 || manifestCount != 0 || outboxCount != 0 {
		t.Fatalf("rolled-back dispatch leaked attempt=%d manifest=%d outbox=%d",
			attemptCount, manifestCount, outboxCount)
	}
	var taskStatus string
	if err := fx.pool.QueryRow(ctx, `
		SELECT status FROM research_task WHERE id = $1::uuid
	`, fx.task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != string(TaskStatusReady) && taskStatus != string(TaskStatusPending) {
		t.Fatalf("task status=%q want ready or pending after rolled-back dispatch", taskStatus)
	}
}

func TestDispatchRaceRejectsWhenCandidateFactsChangeAfterRolledBackIntent(t *testing.T) {
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
		mutate func(context.Context, dispatchRaceFixture) error
	}{
		{
			name: "eligibility_revision",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_passport
					SET eligibility_revision = eligibility_revision + 1
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return err
			},
		},
		{
			name: "stale_run_state_version",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_session
					SET state_version = state_version + 1, updated_at = now()
					WHERE workspace_id = $1::uuid AND id = $2::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID)
				return err
			},
		},
		{
			name: "version_content_hash",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_version
					SET content_hash = 'sha256:mutated-after-dispatch-preflight'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return err
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupDispatchRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			invokeDispatchWithBeforeCommitFault(t, ctx, fx)
			assertDispatchRolledBack(t, ctx, fx)
			if err := tc.mutate(ctx, fx); err != nil {
				t.Fatalf("mutate: %v", err)
			}
			if _, _, err = fx.store.CreateDispatchIntent(ctx, fx.input); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("CreateDispatchIntent after mutation err=%v want ErrInvalidTransition", err)
			}
			assertDispatchRolledBack(t, ctx, fx)
		})
	}
}

func TestDispatchRaceAcceptsAfterRolledBackIntentWhenOnlyUnrelatedWatermarkAdvances(t *testing.T) {
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

	fx := setupDispatchRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeDispatchWithBeforeCommitFault(t, ctx, fx)
	assertDispatchRolledBack(t, ctx, fx)
	if _, err = pool.Exec(ctx, `
		UPDATE research_artifact_policy_state
		SET watermark = watermark + 1, updated_at = now()
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID); err != nil {
		t.Fatalf("advance unrelated watermark: %v", err)
	}
	attempt, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent retry: %v", err)
	}
	var manifestCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, fx.fixture.workspaceID, fx.run.SessionID, attempt.ID).Scan(&manifestCount); err != nil {
		t.Fatal(err)
	}
	if manifestCount != 1 {
		t.Fatalf("manifest count=%d want 1", manifestCount)
	}
}

func TestDispatchRaceConvergesAfterRolledBackIntentWithoutMutation(t *testing.T) {
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

	fx := setupDispatchRaceFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, fx.fixture)
	invokeDispatchWithBeforeCommitFault(t, ctx, fx)
	assertDispatchRolledBack(t, ctx, fx)
	attempt, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent retry: %v", err)
	}
	replayed, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
	if err != nil {
		t.Fatalf("CreateDispatchIntent replay: %v", err)
	}
	if replayed.ID != attempt.ID {
		t.Fatalf("replay attempt=%q want=%q", replayed.ID, attempt.ID)
	}
}
