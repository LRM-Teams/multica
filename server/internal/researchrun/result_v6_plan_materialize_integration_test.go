package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestAcceptV6PlanMaterializesInquiryTasksAndTargetsAtomically(t *testing.T) {
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
		LeadAgentID: fixture.agentID, Goal: "Build a real V6 inquiry plan", Title: "V6 plan acceptance", DepthTier: "standard", Language: "English"}, DefaultRunConfig("standard"))
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
	if _, err = pool.Exec(ctx, `UPDATE research_session SET orchestrator_version=$2 WHERE id=$1::uuid`, run.SessionID, OrchestratorVersionV6); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET expected_result='research_plan_v6',status='running',started_at=now() WHERE id=$1::uuid`, tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running',started_at=now() WHERE id=$1::uuid`, attempt.ID); err != nil {
		t.Fatal(err)
	}

	raw := encodeResearchV6PlanFixture(t, validResearchV6PlanFixture())
	plan, hash, err := DecodeAndValidateResearchV6PlanResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := store.AcceptResult(ctx, AcceptResultInput{SessionID: run.SessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: researchV6PlanEnvelope(plan), V6Plan: &plan, Hash: hash})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.QuestionsCreated != 1 || outcome.TasksCreated != 1 {
		t.Fatalf("outcome=%+v", outcome)
	}
	var questions, hypotheses, branches, edges, executionTasks, targets, graphEvents int
	if err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*)::int FROM research_question WHERE session_id=$1::uuid AND client_key='q.root'),
		(SELECT count(*)::int FROM research_hypothesis WHERE session_id=$1::uuid AND client_key='h.primary'),
		(SELECT count(*)::int FROM research_branch WHERE session_id=$1::uuid AND client_key='b.primary'),
		(SELECT count(*)::int FROM research_inquiry_edge WHERE session_id=$1::uuid AND client_key='edge.tests'),
		(SELECT count(*)::int FROM research_task WHERE session_id=$1::uuid AND client_key='task.discover'),
		(SELECT count(*)::int FROM research_task_inquiry_target WHERE session_id=$1::uuid),
		(SELECT count(*)::int FROM research_run_event WHERE session_id=$1::uuid AND event_type='v6_plan_materialized')`, run.SessionID).Scan(
		&questions, &hypotheses, &branches, &edges, &executionTasks, &targets, &graphEvents); err != nil {
		t.Fatal(err)
	}
	if questions != 1 || hypotheses != 1 || branches != 1 || edges != 1 || executionTasks != 1 || targets != 1 || graphEvents != 1 {
		t.Fatalf("q=%d h=%d b=%d e=%d tasks=%d targets=%d events=%d", questions, hypotheses, branches, edges, executionTasks, targets, graphEvents)
	}
}
