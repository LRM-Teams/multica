package researchrun

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type inquiryTransitionFixture struct {
	input    InquiryTransitionInput
	entityID string
}

func seedInquiryTransitionFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *PostgresStore, workspaceID, sessionID, taskID, agentID string) inquiryTransitionFixture {
	t.Helper()
	var questionID string
	if err := pool.QueryRow(ctx, `SELECT id::text FROM research_question WHERE workspace_id=$1::uuid AND session_id=$2::uuid ORDER BY created_at,id LIMIT 1`, workspaceID, sessionID).Scan(&questionID); err != nil {
		t.Fatal(err)
	}
	entityID := uuid.NewString()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_hypothesis(id,workspace_id,session_id,question_id,statement,status)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Transition fixture','investigating')`, entityID, workspaceID, sessionID, questionID); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatal(err)
	}
	backfillIntegrationArtifactPassport(t, ctx, tx, workspaceID, sessionID, entityID, string(ArtifactKindHypothesis), nil, nil)
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, testDispatchIntentInput(t, ctx, store, sessionID, workspaceID, taskID, agentID))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task_attempt SET status='running',started_at=now() WHERE id=$1::uuid`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET status='running',started_at=now() WHERE id=$1::uuid`, taskID); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(ctx, sessionID, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	return inquiryTransitionFixture{entityID: entityID, input: InquiryTransitionInput{WorkspaceID: workspaceID, SessionID: sessionID, AttemptID: attempt.ID, AgentID: agentID,
		IdempotencyKey: "inquiry-transition:" + entityID, ExpectedStateVersion: run.StateVersion, Changes: []InquiryTransitionChange{{Kind: InquiryKindHypothesis, EntityID: entityID, BeforeStatus: "investigating", AfterStatus: "supported", Reason: "Independent replication succeeded"}}}}
}

func assertInquiryTransitionState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fixture inquiryTransitionFixture, status string, version int, events int) {
	t.Helper()
	var gotStatus string
	var gotVersion int
	var gotEvents, mutations, references int
	if err := pool.QueryRow(ctx, `SELECT h.status,p.current_version,
		(SELECT count(*)::int FROM research_run_event WHERE session_id=h.session_id AND event_type='inquiry_state_changed' AND payload->'changes'->0->>'entity_id'=h.id::text),
		(SELECT count(*)::int FROM research_artifact_policy_mutation WHERE artifact_id=h.id AND mutation_kind='current_version'),
		(SELECT count(*)::int FROM research_artifact_input_reference r JOIN research_artifact_version v ON v.id=r.consumer_version_id WHERE v.artifact_id=h.id AND r.relation='revises')
		FROM research_hypothesis h JOIN research_artifact_passport p ON p.id=h.id WHERE h.id=$1::uuid`, fixture.entityID).Scan(&gotStatus, &gotVersion, &gotEvents, &mutations, &references); err != nil {
		t.Fatal(err)
	}
	wantDerived := 0
	if version > 1 {
		wantDerived = 1
	}
	if gotStatus != status || gotVersion != version || gotEvents != events || mutations != wantDerived || references != wantDerived {
		t.Fatalf("status=%s version=%d events=%d mutations=%d refs=%d", gotStatus, gotVersion, gotEvents, mutations, references)
	}
}

func TestTransitionInquiryPersistsBeforeAfterVersionAndReplay(t *testing.T) {
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
	base := seedResearchRunFixture(t, ctx, pool)
	defer cleanupResearchRunFixture(pool, base)
	store := NewPostgresStore(pool)
	run, _, err := store.CreateRun(ctx, StartInput{WorkspaceID: base.workspaceID, FleetID: base.fleetID, CreatedBy: base.userID, LeadAgentID: base.agentID, Goal: "Transition Inquiry", Title: "Inquiry transition", DepthTier: "standard", Language: "English"}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, run.SessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	fixture := seedInquiryTransitionFixture(t, ctx, pool, store, base.workspaceID, run.SessionID, tasks[0].ID, base.agentID)
	result, err := store.TransitionInquiry(ctx, fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Event.Type != "inquiry_state_changed" {
		t.Fatalf("event=%+v", result.Event)
	}
	assertInquiryTransitionState(t, ctx, pool, fixture, "supported", 2, 1)
	replay, err := store.TransitionInquiry(ctx, fixture.input)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Event.ID != result.Event.ID {
		t.Fatalf("replay=%s want=%s", replay.Event.ID, result.Event.ID)
	}
	assertInquiryTransitionState(t, ctx, pool, fixture, "supported", 2, 1)
}

func TestTransitionInquiryTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpInquiryTransition, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		fixture := seedInquiryTransitionFixture(t, run.ctx, run.pool, run.store, run.fixture.workspaceID, run.fixture.sessionID, run.taskID, run.fixture.agentID)
		invoke := func() error { _, err := run.store.TransitionInquiry(run.ctx, fixture.input); return err }
		return transactionRecoveryOperation{invoke: invoke, recover: invoke, assertRolledBack: func() { assertInquiryTransitionState(t, run.ctx, run.pool, fixture, "investigating", 1, 0) }, assertCommitted: func() { assertInquiryTransitionState(t, run.ctx, run.pool, fixture, "supported", 2, 1) }}
	})
}
