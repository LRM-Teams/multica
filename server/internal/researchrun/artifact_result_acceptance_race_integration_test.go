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
	pool    *pgxpool.Pool
	store   *PostgresStore
	fixture researchRunFixture
	run     Run
	task    Task
	attempt Attempt
	inboxID string
	claimID string
	input   AcceptResultInput
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
	seedIntegrationClaimArtifact(
		t, ctx, pool, fixture.workspaceID, run.SessionID,
		claimID, "accept-race-claim", "claim referenced by manifest",
	)

	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID))
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}
	raw, err := json.Marshal(e2eDeliveryPlan())
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
	var attemptStatus, taskStatus string
	var resultArtifacts, acceptedEvents int
	if err := fx.pool.QueryRow(ctx, `
		SELECT status FROM research_task_attempt WHERE id = $1::uuid
	`, fx.attempt.ID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if attemptStatus != string(AttemptStatusDispatching) {
		t.Fatalf("attempt status=%q want dispatching after rolled-back accept", attemptStatus)
	}
	if err := fx.pool.QueryRow(ctx, `
		SELECT status FROM research_task WHERE id = $1::uuid
	`, fx.task.ID).Scan(&taskStatus); err != nil {
		t.Fatal(err)
	}
	if taskStatus != string(TaskStatusDispatching) {
		t.Fatalf("task status=%q want dispatching after rolled-back accept", taskStatus)
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
	if err := fx.pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_run_event
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
		  AND event_type = 'task_result_accepted' AND payload->>'attempt_id' = $3
	`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID).Scan(&acceptedEvents); err != nil {
		t.Fatal(err)
	}
	if acceptedEvents != 0 {
		t.Fatalf("accepted events=%d want 0 after rolled-back accept", acceptedEvents)
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
			name: "run_orchestrator_version",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_session
					SET orchestrator_version = 'research-run-v4', updated_at = now()
					WHERE workspace_id = $1::uuid AND id = $2::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID)
				return err
			},
		},
		{
			name: "task_expected_result_contract",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_task
					SET expected_result = 'research_evidence_v5', updated_at = now()
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.task.ID)
				return err
			},
		},
		{
			name: "eligibility_revision",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_passport
					SET eligibility_revision = eligibility_revision + 1
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return nil
			},
		},
		{
			name: "withdrawn_lifecycle",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_passport
					SET lifecycle_status = 'withdrawn'
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.claimID)
				return nil
			},
		},
		{
			name: "superseded_manifest_artifact",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				successorID := uuid.NewString()
				seedIntegrationClaimArtifact(
					t, ctx, fx.pool, fx.fixture.workspaceID, fx.run.SessionID,
					successorID, "accept-race-successor", "verified successor claim",
				)
				decisionID := uuid.NewString()
				tx, err := fx.pool.Begin(ctx)
				if err != nil {
					return err
				}
				defer tx.Rollback(ctx)
				if _, err = tx.Exec(ctx, `
					INSERT INTO research_decision (
					  id, workspace_id, session_id, decision_kind, actor_type, actor_id,
					  goal_version, plan_version, inputs, outcome, rationale
					) VALUES ($1::uuid,$2::uuid,$3::uuid,'artifact_supersession','system',NULL,$4,$5,
					          jsonb_build_object('superseded_artifact_id',$6::text,'successor_artifact_id',$7::text),
					          '{"approved":true}'::jsonb,'successor invalidates the manifest input')
				`, decisionID, fx.fixture.workspaceID, fx.run.SessionID, fx.run.GoalVersion,
					fx.run.PlanVersion, fx.claimID, successorID); err != nil {
					return err
				}
				backfillIntegrationDecisionPassport(
					t, ctx, tx, fx.fixture.workspaceID, fx.run.SessionID, decisionID,
					"artifact_supersession", fx.run.GoalVersion, fx.run.PlanVersion,
				)
				if err = tx.Commit(ctx); err != nil {
					return err
				}
				supersedeIntegrationArtifact(
					t, ctx, fx.pool, fx.fixture.workspaceID, fx.run.SessionID,
					fx.claimID, successorID, decisionID,
				)
				return nil
			},
		},
		{
			name: "manifest_hash",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				mutatedHash := contentHashFromPayload([]byte("tampered after acceptance preflight"))
				mutateIntegrationArtifactForCASTest(t, ctx, fx.pool, `
					UPDATE research_artifact_context_manifest
					SET manifest_hash = $4
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID, mutatedHash)
				return nil
			},
		},
		{
			name: "manifest_entry_representation_bytes",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_context_entry e
					SET representation_bytes = convert_to('sha256:mutated-after-accept-preflight', 'UTF8')
					FROM research_artifact_context_manifest m
					WHERE e.manifest_id = m.id
					  AND m.workspace_id = $1::uuid
					  AND m.session_id = $2::uuid
					  AND m.attempt_id = $3::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID, fx.attempt.ID)
				return err
			},
		},
		{
			name: "manifest_policy_watermark_ahead",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := fx.pool.Exec(ctx, `
					UPDATE research_artifact_context_manifest
					SET policy_watermark = policy_watermark + 5
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

func TestAcceptResultRaceAcceptsAfterRolledBackAcceptWhenOnlyUnrelatedStateAdvances(t *testing.T) {
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

	cases := []struct {
		name   string
		mutate func(context.Context, acceptanceRaceFixture) error
	}{
		{
			name: "policy_watermark",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_artifact_policy_state
					SET watermark = watermark + 1, updated_at = now()
					WHERE workspace_id = $1::uuid AND session_id = $2::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID)
				return err
			},
		},
		{
			name: "run_state_version",
			mutate: func(ctx context.Context, fx acceptanceRaceFixture) error {
				_, err := pool.Exec(ctx, `
					UPDATE research_session
					SET state_version = state_version + 1, updated_at = now()
					WHERE workspace_id = $1::uuid AND id = $2::uuid
				`, fx.fixture.workspaceID, fx.run.SessionID)
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
			if err = tc.mutate(ctx, fx); err != nil {
				t.Fatalf("advance unrelated state: %v", err)
			}
			outcome, acceptErr := fx.store.AcceptResult(ctx, fx.input)
			if acceptErr != nil {
				t.Fatalf("AcceptResult after unrelated %s advance: %v", tc.name, acceptErr)
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
		})
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
