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
	seedIntegrationClaimArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID,
		claimID, "dispatch-race-claim", "claim for dispatch race",
	)

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
	var passportCount, manifestEntryCount, omissionCount, inputReferenceCount, grantCount, eventCount int
	if err := fx.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_artifact_passport
		   WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		     AND (id = $3::uuid OR entity_kind = 'context_manifest')),
		  (SELECT count(*)::int FROM research_artifact_context_entry
		   WHERE workspace_id = $1::uuid AND session_id = $2::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_omission
		   WHERE workspace_id = $1::uuid AND session_id = $2::uuid),
		  (SELECT count(*)::int FROM research_artifact_input_reference ref
		   WHERE ref.workspace_id = $1::uuid AND ref.session_id = $2::uuid
		     AND ref.relation = 'manifest_input'),
		  (SELECT count(*)::int FROM research_artifact_policy_grant
		   WHERE workspace_id = $1::uuid AND session_id = $2::uuid),
		  (SELECT count(*)::int FROM research_run_event
		   WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		     AND idempotency_key = $4)
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.input.AttemptID, fx.input.Request.Key).Scan(
		&passportCount, &manifestEntryCount, &omissionCount, &inputReferenceCount, &grantCount, &eventCount,
	); err != nil {
		t.Fatal(err)
	}
	if passportCount != 0 || manifestEntryCount != 0 || omissionCount != 0 ||
		inputReferenceCount != 0 || grantCount != 0 || eventCount != 0 {
		t.Fatalf("rolled-back dispatch leaked passport=%d entry=%d omission=%d input=%d grant=%d event=%d",
			passportCount, manifestEntryCount, omissionCount, inputReferenceCount, grantCount, eventCount)
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

func TestDispatchCASMismatchRollsBackCompleteWriteSet(t *testing.T) {
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

	for _, tc := range []struct {
		name string
		hook func(context.Context, dispatchRaceFixture, *dispatchManifestPlan) error
	}{
		{
			name: "eligibility revision",
			hook: func(ctx context.Context, fx dispatchRaceFixture, _ *dispatchManifestPlan) error {
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_passport
					SET eligibility_revision = eligibility_revision + 1
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return nil
			},
		},
		{
			name: "representation bytes hash",
			hook: func(_ context.Context, fx dispatchRaceFixture, plan *dispatchManifestPlan) error {
				entry, ok := manifestEntryForArtifact(*plan, fx.claimID)
				if !ok {
					return errors.New("claim missing from dispatch manifest plan")
				}
				for i := range plan.Entries {
					if plan.Entries[i].ArtifactID == entry.ArtifactID {
						plan.Entries[i].RepresentationBytes = append([]byte(nil), entry.RepresentationBytes...)
						plan.Entries[i].RepresentationBytes = append(plan.Entries[i].RepresentationBytes, '!')
						return nil
					}
				}
				return errors.New("claim disappeared from dispatch manifest plan")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupDispatchRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			fx.store.dispatchManifestBeforeCASHook = func(ctx context.Context, plan *dispatchManifestPlan) error {
				return tc.hook(ctx, fx, plan)
			}
			_, _, err := fx.store.CreateDispatchIntent(ctx, fx.input)
			fx.store.dispatchManifestBeforeCASHook = nil
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("CreateDispatchIntent err=%v want ErrInvalidTransition", err)
			}
			assertDispatchRolledBack(t, ctx, fx)
		})
	}
}

func TestDispatchRaceReevaluatesFactsAfterRolledBackIntent(t *testing.T) {
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
		name        string
		mutate      func(context.Context, dispatchRaceFixture) error
		wantInvalid bool
	}{
		{
			name: "eligibility_revision",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_passport
					SET eligibility_revision = eligibility_revision + 1
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return nil
			},
		},
		{
			name:        "stale_run_state_version",
			wantInvalid: true,
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
				mutatedHash := contentHashFromPayload([]byte("mutated after dispatch preflight"))
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_version
					SET content_hash = $4
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND artifact_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID, mutatedHash)
				return nil
			},
		},
		{
			name: "withdrawn_lifecycle",
			mutate: func(ctx context.Context, fx dispatchRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_passport
					SET lifecycle_status = 'withdrawn'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
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
			_, _, err = fx.store.CreateDispatchIntent(ctx, fx.input)
			if tc.wantInvalid {
				if !errors.Is(err, ErrInvalidTransition) {
					t.Fatalf("CreateDispatchIntent after mutation err=%v want ErrInvalidTransition", err)
				}
				assertDispatchRolledBack(t, ctx, fx)
				return
			}
			if err != nil {
				t.Fatalf("CreateDispatchIntent after fresh candidate mutation: %v", err)
			}
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
