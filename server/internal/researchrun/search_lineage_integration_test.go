package researchrun

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestRecordSearchLineageBatchTransactionRecovery(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Search lineage", Title: "Search lineage",
		DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("ListTasks: %v len=%d", err, len(tasks))
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, run.SessionID, fixture.workspaceID, tasks[0].ID, fixture.agentID))
	if err != nil {
		t.Fatalf("CreateDispatchIntent: %v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running' WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("mark attempt running: %v", err)
	}

	batch := validSearchLineageBatch()
	batch.WorkspaceID, batch.SessionID, batch.TaskID, batch.AttemptID = fixture.workspaceID, run.SessionID, tasks[0].ID, attempt.ID
	faultErr := errors.New("search lineage fault")
	store.txFaultHook = func(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
		if operation == txOpSearchLineageRecord && point == txBeforeCommit {
			return faultErr
		}
		return nil
	}
	if _, err = store.RecordSearchLineageBatch(ctx, batch); !errors.Is(err, faultErr) {
		t.Fatalf("before-commit err=%v", err)
	}
	assertSearchLineageCounts(t, ctx, pool, run.SessionID, 0, 0, 0, 0, 0)

	store.txFaultHook = nil
	created, err := store.RecordSearchLineageBatch(ctx, batch)
	if err != nil {
		t.Fatalf("RecordSearchLineageBatch: %v", err)
	}
	if created.Replayed || len(created.CandidateIDs) != 2 || len(created.DecisionIDs) != 2 {
		t.Fatalf("created=%+v", created)
	}
	assertSearchLineageCounts(t, ctx, pool, run.SessionID, 1, 1, 2, 2, 6)

	replayed, err := store.RecordSearchLineageBatch(ctx, batch)
	if err != nil || !replayed.Replayed || replayed.QueryExecutionID != created.QueryExecutionID {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	changed := batch
	changed.Query = "changed query"
	if _, err = store.RecordSearchLineageBatch(ctx, changed); !errors.Is(err, ErrResultConflict) {
		t.Fatalf("changed replay err=%v want ErrResultConflict", err)
	}

	unknown := batch
	unknown.ClientRequestID = "search-request-after-commit"
	store.txFaultHook = func(_ context.Context, operation researchTxOperation, point researchTxFaultPoint) error {
		if operation == txOpSearchLineageRecord && point == txAfterCommit {
			return faultErr
		}
		return nil
	}
	if _, err = store.RecordSearchLineageBatch(ctx, unknown); !errors.Is(err, ErrCommitOutcomeUnknown) {
		t.Fatalf("after-commit err=%v", err)
	}
	store.txFaultHook = nil
	recovered, err := store.RecordSearchLineageBatch(ctx, unknown)
	if err != nil || !recovered.Replayed {
		t.Fatalf("recover unknown commit=%+v err=%v", recovered, err)
	}
	assertSearchLineageCounts(t, ctx, pool, run.SessionID, 1, 2, 4, 4, 11)
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='succeeded', completed_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid`, fixture.workspaceID, run.SessionID, attempt.ID); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	terminalReplay, err := store.RecordSearchLineageBatch(ctx, batch)
	if err != nil || !terminalReplay.Replayed || terminalReplay.QueryExecutionID != created.QueryExecutionID {
		t.Fatalf("terminal replay=%+v err=%v", terminalReplay, err)
	}
}

func assertSearchLineageCounts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID string, plans, executions, candidates, decisions, artifacts int) {
	t.Helper()
	var gotPlans, gotExecutions, gotCandidates, gotDecisions, gotArtifacts int
	if err := pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_search_plan WHERE session_id=$1::uuid),
		  (SELECT count(*)::int FROM research_query_execution WHERE session_id=$1::uuid),
		  (SELECT count(*)::int FROM research_source_candidate WHERE session_id=$1::uuid),
		  (SELECT count(*)::int FROM research_screening_decision WHERE session_id=$1::uuid),
		  (SELECT count(*)::int FROM research_artifact_passport WHERE session_id=$1::uuid
		     AND entity_kind IN ('search_plan','query_execution','source_candidate','screening_decision'))
	`, sessionID).Scan(&gotPlans, &gotExecutions, &gotCandidates, &gotDecisions, &gotArtifacts); err != nil {
		t.Fatal(err)
	}
	if gotPlans != plans || gotExecutions != executions || gotCandidates != candidates || gotDecisions != decisions || gotArtifacts != artifacts {
		t.Fatalf("counts got=(%d,%d,%d,%d,%d) want=(%d,%d,%d,%d,%d)", gotPlans, gotExecutions, gotCandidates, gotDecisions, gotArtifacts, plans, executions, candidates, decisions, artifacts)
	}
}
