package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestCreateInquiryGraphPersistsPassportsEventAndReplay(t *testing.T) {
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
	run, _, err := store.CreateRun(ctx, StartInput{WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID, CreatedBy: fixture.userID,
		LeadAgentID: fixture.agentID, Goal: "Create Inquiry Graph", Title: "Inquiry write", DepthTier: "standard", Language: "English"}, DefaultRunConfig("standard"))
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
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running',started_at=now() WHERE id=$1::uuid`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET status='running',started_at=now() WHERE id=$1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	run, err = store.GetRun(ctx, run.SessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	var questionID string
	if err = pool.QueryRow(ctx, `SELECT id::text FROM research_question WHERE session_id=$1::uuid ORDER BY created_at,id LIMIT 1`, run.SessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	hypothesisID, parentID, childID, insightID, edgeID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	in := CreateInquiryGraphInput{WorkspaceID: fixture.workspaceID, SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		IdempotencyKey: "inquiry-create:" + attempt.ID, ExpectedStateVersion: run.StateVersion,
		Hypotheses: []InquiryHypothesisInput{{ID: hypothesisID, QuestionID: questionID, Statement: "The observed effect is reproducible"}},
		Branches:   []InquiryBranchInput{{ID: childID, ParentBranchID: parentID, Objective: "Validate edge cases", BudgetShare: .2}, {ID: parentID, Objective: "Test the primary hypothesis", BudgetShare: .5}},
		Insights:   []InquiryInsightInput{{ID: insightID, Title: "Initial synthesis", Summary: "A bounded proposed insight", Importance: .7, Level: 1}},
		Edges:      []InquiryEdgeInput{{ID: edgeID, From: InquiryEndpoint{Kind: InquiryKindQuestion, ID: questionID}, To: InquiryEndpoint{Kind: InquiryKindHypothesis, ID: hypothesisID}, Relation: InquiryRelationTests}},
	}
	result, err := store.CreateInquiryGraph(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Event.Type != "inquiry_graph_created" {
		t.Fatalf("event=%+v", result.Event)
	}
	replay, err := store.CreateInquiryGraph(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Event.ID != result.Event.ID {
		t.Fatalf("replay event=%s want=%s", replay.Event.ID, result.Event.ID)
	}
	var entities, passports, events int
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM research_hypothesis WHERE session_id=$1::uuid)+
		(SELECT count(*) FROM research_branch WHERE session_id=$1::uuid)+
		(SELECT count(*) FROM research_insight WHERE session_id=$1::uuid)+
		(SELECT count(*) FROM research_inquiry_edge WHERE session_id=$1::uuid),
		(SELECT count(*) FROM research_artifact_passport WHERE session_id=$1::uuid AND entity_kind IN ('hypothesis','branch','insight','inquiry_edge')),
		(SELECT count(*) FROM research_run_event WHERE session_id=$1::uuid AND event_type='inquiry_graph_created')`, run.SessionID).Scan(&entities, &passports, &events); err != nil {
		t.Fatal(err)
	}
	if entities != 5 || passports != 5 || events != 1 {
		t.Fatalf("entities=%d passports=%d events=%d", entities, passports, events)
	}
	var productionVersions int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_artifact_version
		WHERE session_id=$1::uuid AND artifact_id = ANY($2::uuid[]) AND schema_version='research-run-v6'
		  AND hash_origin='production' AND produced_by_attempt_id=$3::uuid`, run.SessionID,
		[]string{hypothesisID, parentID, childID, insightID, edgeID}, attempt.ID).Scan(&productionVersions); err != nil {
		t.Fatal(err)
	}
	if productionVersions != 5 {
		t.Fatalf("production inquiry versions=%d want=5", productionVersions)
	}
}

func TestCreateInquiryGraphTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpInquiryGraphCreate, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, _, err := run.store.CreateDispatchIntent(run.ctx, testDispatchIntentInput(t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID, run.taskID, run.fixture.agentID))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = run.pool.Exec(run.ctx, `UPDATE research_task_attempt SET status='running',started_at=now() WHERE id=$1::uuid`, attempt.ID); err != nil {
			t.Fatal(err)
		}
		if _, err = run.pool.Exec(run.ctx, `UPDATE research_task SET status='running',started_at=now() WHERE id=$1::uuid`, run.taskID); err != nil {
			t.Fatal(err)
		}
		current, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		var questionID string
		if err = run.pool.QueryRow(run.ctx, `SELECT id::text FROM research_question WHERE session_id=$1::uuid ORDER BY created_at,id LIMIT 1`, run.fixture.sessionID).Scan(&questionID); err != nil {
			t.Fatal(err)
		}
		hypothesisID := uuid.NewString()
		input := CreateInquiryGraphInput{WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, AttemptID: attempt.ID, AgentID: run.fixture.agentID,
			IdempotencyKey: "recover-inquiry:" + hypothesisID, ExpectedStateVersion: current.StateVersion, Hypotheses: []InquiryHypothesisInput{{ID: hypothesisID, QuestionID: questionID, Statement: "Recovery-safe hypothesis"}}}
		invoke := func() error { _, err := run.store.CreateInquiryGraph(run.ctx, input); return err }
		assertCount := func(want int) {
			var got int
			if err := run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_hypothesis WHERE id=$1::uuid`, hypothesisID).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Fatalf("hypotheses=%d want=%d", got, want)
			}
		}
		return transactionRecoveryOperation{invoke: invoke, recover: invoke, assertRolledBack: func() { assertCount(0) }, assertCommitted: func() { assertCount(1) }}
	})
}
