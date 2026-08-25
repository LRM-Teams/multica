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

var planResultSemanticFaultPoints = []researchTxFaultPoint{
	txResultAfterMethod,
	txResultAfterQuestion,
	txResultAfterTask,
	txResultAfterTaskDependency,
	txResultAfterAttemptTerminal,
	txResultAfterCircuitSettlement,
	txResultAfterResultArtifact,
	txResultAfterArtifactLineage,
	txResultAfterTaskTerminal,
	txResultAfterRunUpdate,
	txResultAfterEvent,
}

var evidenceResultSemanticFaultPoints = []researchTxFaultPoint{
	txResultAfterSourceSnapshot,
	txResultAfterLegacySource,
	txResultAfterObservation,
	txResultAfterClaim,
	txResultAfterEvidenceLink,
}

type evidenceSemanticRollbackState struct {
	sourceSnapshots int
	legacySources   int
	observations    int
	claims          int
	evidenceLinks   int
	results         int
	passports       int
	versions        int
	inputReferences int
	lifecycleEvents int
	policyMutations int
	acceptedEvents  int
	attemptStatus   string
	attemptHash     string
	taskStatus      string
}

func loadEvidenceSemanticRollbackState(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	workspaceID, sessionID, attemptID, taskID string,
) evidenceSemanticRollbackState {
	t.Helper()
	var state evidenceSemanticRollbackState
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_source_snapshot WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_source WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_observation WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_claim WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_claim_evidence WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_result_artifact WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_passport WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_version WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_input_reference WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_lifecycle_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		  (SELECT count(*)::int FROM research_run_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND event_type='task_result_accepted'),
		  attempt.status, COALESCE(attempt.result_hash, ''), task.status
		FROM research_task_attempt attempt
		JOIN research_task task ON task.id=$4::uuid AND task.session_id=attempt.session_id
		WHERE attempt.id=$3::uuid AND attempt.session_id=$2::uuid
	`, workspaceID, sessionID, attemptID, taskID).Scan(
		&state.sourceSnapshots, &state.legacySources, &state.observations,
		&state.claims, &state.evidenceLinks, &state.results,
		&state.passports, &state.versions, &state.inputReferences,
		&state.lifecycleEvents, &state.policyMutations, &state.acceptedEvents,
		&state.attemptStatus, &state.attemptHash, &state.taskStatus,
	); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestResultAcceptanceSemanticFaultPointInventory(t *testing.T) {
	points := resultAcceptanceSemanticFaultPoints()
	if len(points) != 19 {
		t.Fatalf("semantic fault point count=%d want=19", len(points))
	}
	seen := make(map[researchTxFaultPoint]struct{}, len(points))
	for _, point := range points {
		if point == "" {
			t.Fatal("empty semantic fault point")
		}
		if _, exists := seen[point]; exists {
			t.Fatalf("duplicate semantic fault point %q", point)
		}
		seen[point] = struct{}{}
	}
}

func TestAcceptResultSemanticFaultsRollbackCompletePlanAcceptance(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	for _, point := range planResultSemanticFaultPoints {
		point := point
		t.Run(string(point), func(t *testing.T) {
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
				LeadAgentID: fixture.agentID, Goal: "Semantic result rollback", Title: "Semantic result rollback",
				DepthTier: "standard", Language: "English",
			}, DefaultRunConfig("standard"))
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
			attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
			if err != nil {
				t.Fatal(err)
			}
			inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
			if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
				t.Fatal(err)
			}
			result := upgradeResultToV5(validV4PlanResult(t))
			result.ClientRequestID = "semantic-fault-" + uuid.NewString()
			raw, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			validated, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
			if err != nil {
				t.Fatal(err)
			}
			injected := errors.New("injected " + string(point))
			fault := &oneShotResearchTxFault{operation: txOpResultAccept, point: point, err: injected}
			store.txFaultHook = fault.hook
			_, err = store.AcceptResult(ctx, AcceptResultInput{
				SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
				InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
			})
			if !errors.Is(err, injected) || !fault.fired {
				t.Fatalf("AcceptResult point=%s err=%v fired=%v", point, err, fault.fired)
			}
			var questionCount, taskCount, decisionCount, resultCount, acceptedEvents int
			var attemptStatus, resultHash string
			if err = pool.QueryRow(ctx, `
				SELECT
				  (SELECT count(*)::int FROM research_question WHERE session_id=$1::uuid),
				  (SELECT count(*)::int FROM research_task WHERE session_id=$1::uuid),
				  (SELECT count(*)::int FROM research_decision WHERE session_id=$1::uuid),
				  (SELECT count(*)::int FROM research_result_artifact WHERE session_id=$1::uuid),
				  (SELECT count(*)::int FROM research_run_event WHERE session_id=$1::uuid AND event_type='task_result_accepted'),
				  status, COALESCE(result_hash, '')
				FROM research_task_attempt WHERE id=$2::uuid
			`, run.SessionID, attempt.ID).Scan(
				&questionCount, &taskCount, &decisionCount, &resultCount, &acceptedEvents,
				&attemptStatus, &resultHash,
			); err != nil {
				t.Fatal(err)
			}
			if questionCount != 1 || taskCount != 1 || decisionCount != 0 || resultCount != 0 ||
				acceptedEvents != 0 || attemptStatus != string(AttemptStatusDispatching) || resultHash != "" {
				t.Fatalf("partial acceptance survived point=%s questions=%d tasks=%d decisions=%d results=%d events=%d attempt=%s hash=%q",
					point, questionCount, taskCount, decisionCount, resultCount, acceptedEvents, attemptStatus, resultHash)
			}
		})
	}
}

func TestAcceptResultSemanticFaultsRollbackCompleteEvidenceAcceptance(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	for _, point := range evidenceResultSemanticFaultPoints {
		point := point
		t.Run(string(point), func(t *testing.T) {
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
				LeadAgentID: fixture.agentID, Goal: "Evidence semantic rollback", Title: "Evidence semantic rollback",
				DepthTier: "standard", Language: "English",
			}, DefaultRunConfig("standard"))
			if err != nil {
				t.Fatal(err)
			}
			tasks, err := store.ListTasks(ctx, run.SessionID, run.WorkspaceID)
			if err != nil || len(tasks) != 1 {
				t.Fatalf("plan tasks=%+v err=%v", tasks, err)
			}
			planAttempt, _, err := store.CreateDispatchIntent(
				ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID),
			)
			if err != nil {
				t.Fatal(err)
			}
			planInboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
			if _, _, err = store.AttachInboxTask(ctx, planAttempt.ID, planInboxID); err != nil {
				t.Fatal(err)
			}
			plan := upgradeResultToV5(validV4PlanResult(t))
			plan.ClientRequestID = "evidence-fault-plan-" + uuid.NewString()
			planRaw, err := json.Marshal(plan)
			if err != nil {
				t.Fatal(err)
			}
			planResult, planHash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, planRaw, tasks[0], run.Config)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = store.AcceptResult(ctx, AcceptResultInput{
				SessionID: run.SessionID, AttemptID: planAttempt.ID, AgentID: fixture.agentID,
				InboxTaskID: planInboxID, Raw: planRaw, Result: planResult, Hash: planHash,
			}); err != nil {
				t.Fatalf("AcceptResult plan: %v", err)
			}
			if _, err = store.ActivateReadyTasks(ctx, run.SessionID); err != nil {
				t.Fatalf("ActivateReadyTasks: %v", err)
			}
			discoverTasks, err := listDiscoverTasks(ctx, store, run.SessionID, run.WorkspaceID)
			if err != nil || len(discoverTasks) == 0 {
				t.Fatalf("discover tasks=%+v err=%v", discoverTasks, err)
			}
			task := discoverTasks[0]
			attempt, _, err := store.CreateDispatchIntent(
				ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, task.ID, fixture.agentID),
			)
			if err != nil {
				t.Fatal(err)
			}
			inboxID := seedIntegrationInboxEvent(t, ctx, pool, fixture.workspaceID, fixture.agentID)
			if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
				t.Fatal(err)
			}
			evidence := upgradeResultToV5(evidenceResultWithReferenceOrder(true))
			evidence.ClientRequestID = "evidence-semantic-fault-" + uuid.NewString()
			raw, err := json.Marshal(evidence)
			if err != nil {
				t.Fatal(err)
			}
			validated, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, run.Config)
			if err != nil {
				t.Fatal(err)
			}
			baseline := loadEvidenceSemanticRollbackState(
				t, ctx, pool, fixture.workspaceID, run.SessionID, attempt.ID, task.ID,
			)
			injected := errors.New("injected " + string(point))
			fault := &oneShotResearchTxFault{operation: txOpResultAccept, point: point, err: injected}
			store.txFaultHook = fault.hook
			_, err = store.AcceptResult(ctx, AcceptResultInput{
				SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
				InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
			})
			store.txFaultHook = nil
			if !errors.Is(err, injected) || !fault.fired {
				t.Fatalf("AcceptResult point=%s err=%v fired=%v", point, err, fault.fired)
			}
			after := loadEvidenceSemanticRollbackState(
				t, ctx, pool, fixture.workspaceID, run.SessionID, attempt.ID, task.ID,
			)
			if after != baseline {
				t.Fatalf("partial evidence acceptance survived point=%s\nafter=%+v\nbaseline=%+v", point, after, baseline)
			}
		})
	}
}
