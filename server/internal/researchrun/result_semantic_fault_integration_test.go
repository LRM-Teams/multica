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
			tasks, err := store.ListTasks(ctx, run.SessionID)
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
