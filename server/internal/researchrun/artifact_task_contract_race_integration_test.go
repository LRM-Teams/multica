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

type taskContractRaceCounts struct {
	questions       int
	tasks           int
	dependencies    int
	methods         int
	resultArtifacts int
	acceptedEvents  int
	passports       int
	versions        int
	inputReferences int
	lifecycleEvents int
	policyMutations int
}

func loadTaskContractRaceCounts(t *testing.T, ctx context.Context, fx acceptanceRaceFixture) taskContractRaceCounts {
	t.Helper()
	var counts taskContractRaceCounts
	if err := fx.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_question WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_task_dependency WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_decision WHERE session_id = $1::uuid AND decision_kind = 'research_method'),
		  (SELECT count(*)::int FROM research_result_artifact WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid AND event_type = 'task_result_accepted'),
		  (SELECT count(*)::int FROM research_artifact_passport WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_artifact_version WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_artifact_input_reference WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_artifact_lifecycle_event WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation WHERE session_id = $1::uuid)
	`, fx.run.SessionID).Scan(
		&counts.questions, &counts.tasks, &counts.dependencies, &counts.methods,
		&counts.resultArtifacts, &counts.acceptedEvents, &counts.passports,
		&counts.versions, &counts.inputReferences, &counts.lifecycleEvents, &counts.policyMutations,
	); err != nil {
		t.Fatal(err)
	}
	return counts
}

func seedConflictingPlanTask(
	t *testing.T,
	ctx context.Context,
	fx acceptanceRaceFixture,
	timeoutSeconds int,
	extraDependency bool,
) {
	t.Helper()
	tx, err := fx.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	questionID := uuid.NewString()
	taskID := uuid.NewString()
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_question (
		  id, workspace_id, session_id, created_by_task_id, client_key, kind,
		  question, required, status, priority, impact, uncertainty, novelty,
		  goal_version, plan_version
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, 'answer-question', 'dimension',
		  'What is the measured value?', true, 'open', 1, 1, 0.8, 0.5, $5, $6
		)
	`, questionID, fx.fixture.workspaceID, fx.run.SessionID, fx.task.ID,
		fx.run.GoalVersion, fx.run.PlanVersion); err != nil {
		t.Fatal(err)
	}
	goalVersion, planVersion := fx.run.GoalVersion, fx.run.PlanVersion
	backfillIntegrationArtifactPassport(
		t, ctx, tx, fx.fixture.workspaceID, fx.run.SessionID, questionID,
		string(ArtifactKindQuestion), &goalVersion, &planVersion,
	)
	if _, err = tx.Exec(ctx, `
		INSERT INTO research_task (
		  id, workspace_id, session_id, question_id, parent_task_id, client_key,
		  kind, objective, required_capability, expected_result,
		  acceptance_criteria, priority, status, goal_version, plan_version,
		  max_attempts, timeout_seconds
		) VALUES (
		  $1::uuid, $2::uuid, $3::uuid, $4::uuid, $5::uuid, 'verify-1',
		  'verify', 'Triangulate three independent measurements', 'validator', 'research_evidence_v5',
		  '{}'::jsonb, 1, 'pending', $6, $7, $8, $9
		)
	`, taskID, fx.fixture.workspaceID, fx.run.SessionID, questionID, fx.task.ID,
		fx.run.GoalVersion, fx.run.PlanVersion, fx.run.Config.MaxAttemptsPerTask, timeoutSeconds); err != nil {
		t.Fatal(err)
	}
	backfillIntegrationArtifactPassport(
		t, ctx, tx, fx.fixture.workspaceID, fx.run.SessionID, taskID,
		string(ArtifactKindTask), &goalVersion, &planVersion,
	)
	if extraDependency {
		if _, err = tx.Exec(ctx, `
			INSERT INTO research_task_dependency (
			  workspace_id, session_id, task_id, depends_on_task_id
			) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid)
		`, fx.fixture.workspaceID, fx.run.SessionID, taskID, fx.task.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestAcceptResultRaceRejectsTaskContractAndDependencyDrift(t *testing.T) {
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
		name            string
		timeoutSeconds  func(RunConfig) int
		extraDependency bool
	}{
		{
			name: "hidden_timeout_contract",
			timeoutSeconds: func(config RunConfig) int {
				return config.TaskTimeoutSeconds + 1
			},
		},
		{
			name: "undeclared_dependency",
			timeoutSeconds: func(config RunConfig) int {
				return config.TaskTimeoutSeconds
			},
			extraDependency: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fx := setupPlanAcceptanceRaceFixture(t, ctx, pool)
			defer cleanupResearchRunFixture(pool, fx.fixture)
			invokeAcceptWithBeforeCommitFault(t, ctx, fx)
			assertAcceptanceRolledBack(t, ctx, fx)
			seedConflictingPlanTask(t, ctx, fx, tc.timeoutSeconds(fx.run.Config), tc.extraDependency)
			before := loadTaskContractRaceCounts(t, ctx, fx)

			if _, err = fx.store.AcceptResult(ctx, fx.input); !errors.Is(err, ErrResultConflict) {
				t.Fatalf("AcceptResult after task drift err=%v want ErrResultConflict", err)
			}
			assertAcceptanceRolledBack(t, ctx, fx)
			after := loadTaskContractRaceCounts(t, ctx, fx)
			if after != before {
				t.Fatalf("failed acceptance changed write set: before=%+v after=%+v", before, after)
			}
		})
	}
}
