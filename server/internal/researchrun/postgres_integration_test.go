package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestPostgresStoreCreateRunIsAtomic(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	input := StartInput{
		WorkspaceID: fixture.workspaceID,
		FleetID:     fixture.fleetID,
		CreatedBy:   fixture.userID,
		LeadAgentID: uuid.NewString(),
		Goal:        "Test atomic run creation",
		Title:       "Atomic creation",
		DepthTier:   "standard",
		Language:    "English",
	}

	var sessionsBefore int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM research_session WHERE workspace_id = $1::uuid`, fixture.workspaceID).Scan(&sessionsBefore); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CreateRun(ctx, input, DefaultRunConfig(input.DepthTier)); err == nil {
		t.Fatal("CreateRun with a missing lead agent succeeded")
	}
	var sessionsAfterFailure int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM research_session WHERE workspace_id = $1::uuid`, fixture.workspaceID).Scan(&sessionsAfterFailure); err != nil {
		t.Fatal(err)
	}
	if sessionsAfterFailure != sessionsBefore {
		t.Fatalf("failed CreateRun persisted a partial session: before=%d after=%d", sessionsBefore, sessionsAfterFailure)
	}

	input.LeadAgentID = fixture.agentID
	run, event, err := store.CreateRun(ctx, input, DefaultRunConfig(input.DepthTier))
	if err != nil {
		t.Fatalf("CreateRun: %v", err)
	}
	if run.SessionID == "" || run.InitializedAt == nil || event.Type != "run_started" {
		t.Fatalf("run=%+v event=%+v", run, event)
	}
	for table, want := range map[string]int{
		"research_contract_revision": 1,
		"research_question":          1,
		"research_task":              1,
	} {
		var got int
		query := fmt.Sprintf(`SELECT count(*) FROM %s WHERE session_id = $1::uuid`, table)
		if err = pool.QueryRow(ctx, query, run.SessionID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%s rows=%d want=%d", table, got, want)
		}
	}
}

func TestCancelledAttemptRemainsScheduledUntilInboxCancellationCompletes(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test durable cancellation",
		Title: "Durable cancellation", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, tasks[0].ID, fixture.agentID)
	if err != nil {
		t.Fatal(err)
	}
	inboxID := uuid.NewString()
	run, _, _, err = store.Pause(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
	pending, err := store.ListPendingCancellations(ctx, fixture.sessionID)
	if err != nil || len(pending) != 1 || pending[0].InboxTaskID != "" || pending[0].DispatchKey != attempt.DispatchKey {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	due, err := store.ListDueRunIDs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range due {
		found = found || id == fixture.sessionID
	}
	if !found {
		t.Fatalf("paused run with pending cancellation was not scheduled: %v", due)
	}

	dispatcher := &recordingCancellationDispatcher{states: map[string]InboxTaskState{}}
	engine := NewEngine(store, dispatcher, nil)
	if pendingStill, cancelErr := engine.cancelPendingAttempts(ctx, run, "test_cancellation"); cancelErr != nil {
		t.Fatal(cancelErr)
	} else if !pendingStill {
		t.Fatal("missing dispatch must keep cancellation pending during its stale window")
	}
	dispatcher.states[attempt.DispatchKey] = InboxTaskState{ID: inboxID, Status: "running"}
	if pendingStill, cancelErr := engine.cancelPendingAttempts(ctx, run, "test_cancellation"); cancelErr != nil {
		t.Fatal(cancelErr)
	} else if pendingStill {
		t.Fatal("cancellation remained pending after the dispatch key resolved")
	}
	if len(dispatcher.cancelled) != 1 || dispatcher.cancelled[0] != inboxID {
		t.Fatalf("cancelled=%v", dispatcher.cancelled)
	}
	pending, err = store.ListPendingCancellations(ctx, fixture.sessionID)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after cancellation=%+v err=%v", pending, err)
	}
}

type recordingCancellationDispatcher struct {
	cancelled []string
	states    map[string]InboxTaskState
}

func (*recordingCancellationDispatcher) Dispatch(context.Context, DispatchRequest) (DispatchResult, error) {
	return DispatchResult{}, errors.New("not implemented")
}

func (d *recordingCancellationDispatcher) Inspect(_ context.Context, keys []string) (map[string]InboxTaskState, error) {
	out := map[string]InboxTaskState{}
	for _, key := range keys {
		if state, ok := d.states[key]; ok {
			out[key] = state
		}
	}
	return out, nil
}

func (d *recordingCancellationDispatcher) Cancel(_ context.Context, ids []string, _ string) error {
	d.cancelled = append(d.cancelled, ids...)
	return nil
}

func TestPostgresStorePersistsPlanAndReplaysResult(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, event, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Compare the evidence",
		Title: "Evidence comparison", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatalf("InitializeRun: %v", err)
	}
	if run.InitializedAt == nil || event.Type != "run_started" || run.Config.MaxRunSeconds == 0 {
		t.Fatalf("run=%+v event=%+v", run, event)
	}
	contract, err := store.GetCurrentContract(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || contract.Goal != run.Goal || contract.Language != "English" || contract.GoalVersion != 1 {
		t.Fatalf("contract=%+v err=%v", contract, err)
	}
	pendingEvents, err := store.ListUnprojectedEvents(ctx, fixture.sessionID, 10)
	if err != nil || len(pendingEvents) != 1 || pendingEvents[0].ID != event.ID || pendingEvents[0].ProjectionAttempts != 0 {
		t.Fatalf("pending events=%+v err=%v", pendingEvents, err)
	}
	eventTx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	firstReplayable, err := appendEvent(ctx, eventTx, fixture.workspaceID, fixture.sessionID, "test_event", "test-event-idempotency", "system", "", map[string]any{"value": 1})
	if err != nil {
		t.Fatal(err)
	}
	secondReplayable, err := appendEvent(ctx, eventTx, fixture.workspaceID, fixture.sessionID, "test_event", "test-event-idempotency", "system", "", map[string]any{"value": 1})
	if err != nil || secondReplayable.ID != firstReplayable.ID || secondReplayable.Sequence != firstReplayable.Sequence {
		t.Fatalf("event replay first=%+v second=%+v err=%v", firstReplayable, secondReplayable, err)
	}
	if err = eventTx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	runAfterReplay, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || runAfterReplay.StateVersion != run.StateVersion+1 {
		t.Fatalf("event replay state_version=%d initial=%d err=%v", runAfterReplay.StateVersion, run.StateVersion, err)
	}

	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 || tasks[0].Kind != TaskKindPlan {
		t.Fatalf("ListTasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, tasks[0].ID, fixture.agentID)
	if err != nil {
		t.Fatalf("CreateAttempt: %v", err)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'draining')
	`, inboxID, fixture.workspaceID, fixture.agentID); err != nil {
		t.Fatalf("insert inbox event: %v", err)
	}
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatalf("AttachInboxTask: %v", err)
	}

	result := validPlanResult(t)
	raw, _ := json.Marshal(result)
	validated, hash, err := DecodeAndValidateResult(raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := store.AcceptResult(ctx, AcceptResultInput{
		SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
	})
	if err != nil {
		t.Fatalf("AcceptResult: %v", err)
	}
	if outcome.QuestionsCreated != 1 || outcome.TasksCreated != 1 || outcome.Event.Type != "task_result_accepted" {
		t.Fatalf("outcome=%+v", outcome)
	}
	replayed, err := store.AcceptResult(ctx, AcceptResultInput{
		SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
	})
	if err != nil || !replayed.Replayed {
		t.Fatalf("replay=%+v err=%v", replayed, err)
	}
	questions, err := store.ListQuestions(ctx, fixture.sessionID)
	if err != nil || len(questions) != 2 {
		t.Fatalf("questions=%+v err=%v", questions, err)
	}
	tasks, err = store.ListTasks(ctx, fixture.sessionID)
	discoverReady := false
	for _, task := range tasks {
		discoverReady = discoverReady || task.Kind == TaskKindDiscover && task.Status == TaskStatusReady
	}
	if err != nil || len(tasks) != 2 || !discoverReady {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	run, err = store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || run.Stats.AcceptedResults != 1 {
		t.Fatalf("run stats=%+v err=%v", run.Stats, err)
	}
	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil || gate.Passed || !hasGateFinding(gate, "required_questions_unanswered") {
		t.Fatalf("gate=%+v err=%v", gate, err)
	}

	var discoverTask Task
	for _, candidate := range tasks {
		if candidate.Kind == TaskKindDiscover {
			discoverTask = candidate
			break
		}
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	state := acceptedResultState{
		workspaceID: fixture.workspaceID,
		task:        discoverTask,
		run:         run,
		targetPlan:  run.PlanVersion,
	}
	evidence := validEvidenceResult()
	sourceIDs, sourceCount, err := materializeSources(ctx, tx, state, evidence)
	if err != nil || sourceCount != 1 {
		t.Fatalf("first sources=%d err=%v", sourceCount, err)
	}
	observationIDs, observationCount, err := materializeObservations(ctx, tx, state, evidence, sourceIDs)
	if err != nil || observationCount != 1 {
		t.Fatalf("first observations=%d err=%v", observationCount, err)
	}
	_, claimCount, err := materializeClaims(ctx, tx, state, evidence, observationIDs)
	if err != nil || claimCount != 1 {
		t.Fatalf("first claims=%d err=%v", claimCount, err)
	}
	sourceIDs, sourceCount, err = materializeSources(ctx, tx, state, evidence)
	if err != nil || sourceCount != 0 {
		t.Fatalf("duplicate sources=%d err=%v", sourceCount, err)
	}
	observationIDs, observationCount, err = materializeObservations(ctx, tx, state, evidence, sourceIDs)
	if err != nil || observationCount != 0 {
		t.Fatalf("duplicate observations=%d err=%v", observationCount, err)
	}
	_, claimCount, err = materializeClaims(ctx, tx, state, evidence, observationIDs)
	if err != nil || claimCount != 0 {
		t.Fatalf("duplicate claims=%d err=%v", claimCount, err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	sources, err := store.ListSourceSnapshots(ctx, fixture.sessionID)
	if err != nil || len(sources) != 1 || sources[0].SnapshotExcerpt == "" {
		t.Fatalf("source views=%+v err=%v", sources, err)
	}
	observations, err := store.ListObservations(ctx, fixture.sessionID)
	if err != nil || len(observations) != 1 {
		t.Fatalf("observation views=%+v err=%v", observations, err)
	}
	claims, err := store.ListClaims(ctx, fixture.sessionID)
	if err != nil || len(claims) != 1 || len(claims[0].Evidence) != 1 {
		t.Fatalf("claim views=%+v err=%v", claims, err)
	}
}

func TestPostgresStoreSteerPreservesContractAndValidatesRunLimits(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Compare the evidence",
		Title: "Evidence comparison", DepthTier: "standard", Language: "zh",
		SourcePolicy: json.RawMessage(`{"prefer_primary":true,"weights":{"primary":0.9}}`),
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = store.Steer(ctx, SteerInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, UserID: fixture.userID,
		Goal: "Compare the evidence in China", RunLimits: json.RawMessage(`{"max_parallel_tasks":7}`),
	}); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || run.Config.MaxParallelTasks != 7 || run.Config.MaxTasks != 60 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	contract, err := store.GetCurrentContract(ctx, fixture.sessionID, fixture.workspaceID)
	var contractLimits RunConfig
	limitsErr := json.Unmarshal(contract.RunLimits, &contractLimits)
	if err != nil || limitsErr != nil || contract.GoalVersion != 2 || contract.Language != "zh" || !strings.Contains(string(contract.SourcePolicy), "prefer_primary") || contractLimits.MaxParallelTasks != 7 {
		t.Fatalf("contract=%+v err=%v", contract, err)
	}
	if _, _, _, err = store.Steer(ctx, SteerInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, UserID: fixture.userID,
		Goal: "Invalid limits must roll back", RunLimits: json.RawMessage(`{"max_parallel_tasks":0}`),
	}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid limits err=%v", err)
	}
	tooLongLanguage := strings.Repeat("x", 65)
	if _, _, _, err = store.Steer(ctx, SteerInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, UserID: fixture.userID,
		Goal: "Invalid language must roll back", Language: &tooLongLanguage,
	}); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid language err=%v", err)
	}
	after, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || after.GoalVersion != 2 || after.Goal != "Compare the evidence in China" {
		t.Fatalf("run changed after invalid steering: %+v err=%v", after, err)
	}
}

func TestPostgresStoreLeasesTerminalRunForPendingProjection(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()

	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Compare the evidence",
		Title: "Evidence comparison", DepthTier: "standard",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	firstBudgetEvent, err := store.RecordBudgetExhausted(ctx, fixture.sessionID, "wall_time", "test limit")
	if err != nil {
		t.Fatal(err)
	}
	replayedBudgetEvent, err := store.RecordBudgetExhausted(ctx, fixture.sessionID, "wall_time", "test limit")
	if err != nil || replayedBudgetEvent.ID != firstBudgetEvent.ID {
		t.Fatalf("budget replay=%+v first=%+v err=%v", replayedBudgetEvent, firstBudgetEvent, err)
	}
	budgetRun, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || budgetRun.Stats.BudgetExhaustionCount != 1 {
		t.Fatalf("budget stats=%+v err=%v", budgetRun.Stats, err)
	}
	if _, _, _, err = store.Pause(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET next_reconcile_at = now() + interval '1 day' WHERE id = $1::uuid`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	due, err := store.ListDueRunIDs(ctx, 100)
	if err != nil {
		t.Fatal(err)
	}
	if !containsString(due, fixture.sessionID) {
		t.Fatalf("terminal run with pending projection not due: %v", due)
	}
	run, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed || run.Status != RunStatusPaused {
		t.Fatalf("run=%+v claimed=%v err=%v", run, claimed, err)
	}
}

func TestPostgresStoreAttemptAttributionSurvivesAgentDeletion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Preserve attribution",
		Title: "Attribution", DepthTier: "standard", Language: "en",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) != 1 {
		t.Fatalf("tasks=%+v err=%v", tasks, err)
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, tasks[0].ID, fixture.agentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: attempt.ID, FailureClass: "agent_removed", Diagnostics: "test teardown", Retryable: false,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM agent WHERE id = $1::uuid`, fixture.agentID); err != nil {
		t.Fatalf("delete attributed agent: %v", err)
	}
	var assignedAgentID string
	if err = pool.QueryRow(ctx, `SELECT assigned_agent_id::text FROM research_task_attempt WHERE id = $1::uuid`, attempt.ID).Scan(&assignedAgentID); err != nil {
		t.Fatalf("read preserved attempt: %v", err)
	}
	if assignedAgentID != fixture.agentID {
		t.Fatalf("assigned agent id=%q, want %q", assignedAgentID, fixture.agentID)
	}
}

func TestPostgresStoreBlocksTasksWhoseDependencyIsTerminal(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()

	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Compare the evidence",
		Title: "Evidence comparison", DepthTier: "standard",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	parentID := uuid.NewString()
	childID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_task (
		  id, workspace_id, session_id, client_key, kind, objective,
		  required_capability, expected_result, status, goal_version, plan_version,
		  max_attempts, timeout_seconds, ready_at
		) VALUES
		  ($1::uuid, $3::uuid, $4::uuid, 'parent', 'discover', 'parent', 'lead', 'research_evidence_v1', 'ready', $5, $6, 2, 300, now()),
		  ($2::uuid, $3::uuid, $4::uuid, 'child', 'verify', 'child', 'lead', 'research_evidence_v1', 'pending', $5, $6, 1, 300, NULL)
	`, parentID, childID, fixture.workspaceID, fixture.sessionID, run.GoalVersion, run.PlanVersion); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `INSERT INTO research_task_dependency (task_id, depends_on_task_id) VALUES ($1::uuid, $2::uuid)`, childID, parentID); err != nil {
		t.Fatal(err)
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, parentID, fixture.agentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "transient_test_failure", Retryable: true}); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CreateAttempt(ctx, fixture.sessionID, parentID, fixture.agentID); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("immediate retry err=%v", err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_task SET ready_at = now() - interval '1 second' WHERE id = $1::uuid`, parentID); err != nil {
		t.Fatal(err)
	}
	attempt, _, err = store.CreateAttempt(ctx, fixture.sessionID, parentID, fixture.agentID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FailAttempt(ctx, AttemptFailure{AttemptID: attempt.ID, FailureClass: "permanent_test_failure", Retryable: false}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ActivateReadyTasks(ctx, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	child, err := store.GetTask(ctx, childID, fixture.sessionID)
	if err != nil || child.Status != TaskStatusBlocked || child.TerminalReason != "dependency_terminal" {
		t.Fatalf("child=%+v err=%v", child, err)
	}
	var blockedEvents int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_run_event WHERE session_id = $1::uuid AND event_type = 'task_blocked'`, fixture.sessionID).Scan(&blockedEvents); err != nil || blockedEvents != 1 {
		t.Fatalf("blocked events=%d err=%v", blockedEvents, err)
	}
}

func TestPostgresStoreRunsFromPlanThroughConfirmedDelivery(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Establish the measured value",
		Title: "Measured value", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	plan := ResultEnvelope{
		SchemaVersion: 1, ClientRequestID: "e2e-plan", Summary: "dependency-safe plan", Confidence: 0.8,
		Plan: &PlanProposal{
			Questions: []QuestionProposal{{
				ClientKey: "answer-question", Kind: QuestionKindDimension, Text: "What is the measured value?",
				Required: true, Priority: 1, Impact: 1, Uncertainty: 0.8, Novelty: 0.5,
			}},
			Tasks: []TaskProposal{
				{ClientKey: "verify-1", QuestionKey: "answer-question", Kind: TaskKindVerify, Objective: "Triangulate three primary sources", RequiredCapability: "lead", ExpectedResult: "research_evidence_v1", Priority: 1},
				{ClientKey: "verify-2", QuestionKey: "answer-question", Kind: TaskKindVerify, Objective: "Repeat verification for marginal-gain measurement", RequiredCapability: "lead", ExpectedResult: "research_evidence_v1", Priority: 0.9, DependsOn: []string{"verify-1"}},
				{ClientKey: "verify-3", QuestionKey: "answer-question", Kind: TaskKindVerify, Objective: "Confirm saturation", RequiredCapability: "lead", ExpectedResult: "research_evidence_v1", Priority: 0.8, DependsOn: []string{"verify-2"}},
				{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write evidence-linked report", RequiredCapability: "lead", ExpectedResult: "research_report_v1", Priority: 0.7, DependsOn: []string{"verify-3"}},
				{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Evaluate report quality", RequiredCapability: "lead", ExpectedResult: "research_quality_evaluation_v1", Priority: 0.6, DependsOn: []string{"synthesize"}},
				{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit report citations", RequiredCapability: "lead", ExpectedResult: "research_citation_audit_v1", Priority: 0.6, DependsOn: []string{"synthesize"}},
			},
			InclusionCriteria: []string{"Primary evidence"}, ExclusionCriteria: []string{"Unverifiable summaries"},
			SourceStrategy: []string{"Independent source families"}, Uncertainties: []string{"Measurement context"}, PlanningRisks: []string{"Source disagreement"},
		},
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)

	evidence := e2eVerifiedEvidence()
	evidence.ClientRequestID = "e2e-evidence-1"
	evidence.CoverageDelta = 0.8
	submitStoreTask(t, ctx, pool, store, fixture, "verify-1", evidence, run.Config)
	evidence.ClientRequestID = "e2e-evidence-2"
	evidence.CoverageDelta = 0
	submitStoreTask(t, ctx, pool, store, fixture, "verify-2", evidence, run.Config)
	evidence.ClientRequestID = "e2e-evidence-3"
	submitStoreTask(t, ctx, pool, store, fixture, "verify-3", evidence, run.Config)

	submitStoreTask(t, ctx, pool, store, fixture, "synthesize", ResultEnvelope{
		SchemaVersion: 1, ClientRequestID: "e2e-report", Summary: "report", Confidence: 0.9,
		Report: &ReportProposal{
			ContentMD: "# Finding\n\nThe measured value is 42.", Structured: json.RawMessage(`{"sections":["finding"]}`),
			Claims: []ReportClaimProposal{{ClaimKey: "answer-claim", SectionID: "finding"}},
		},
	}, run.Config)
	evaluation := EvaluationProposal{
		Passed: true, FactualGrounding: 0.9, Coverage: 0.9, AnalyticalDepth: 0.9,
		SourceQuality: 0.9, ContradictionHandling: 0.9, InstructionAdherence: 0.9, Readability: 0.9,
	}
	submitStoreTask(t, ctx, pool, store, fixture, "quality", ResultEnvelope{
		SchemaVersion: 1, ClientRequestID: "e2e-quality", Summary: "quality passed", Confidence: 0.9, Evaluation: &evaluation,
	}, run.Config)
	submitStoreTask(t, ctx, pool, store, fixture, "citations", ResultEnvelope{
		SchemaVersion: 1, ClientRequestID: "e2e-citations", Summary: "citations passed", Confidence: 0.9, Evaluation: &evaluation,
	}, run.Config)

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil || !gate.Passed {
		t.Fatalf("gate=%+v err=%v", gate, err)
	}
	awaiting, _, err := store.SetAwaitingConfirmation(ctx, fixture.sessionID, gate)
	if err != nil || awaiting.Status != RunStatusAwaitingUserConfirm {
		t.Fatalf("awaiting=%+v err=%v", awaiting, err)
	}
	completed, _, err := store.Complete(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID)
	if err != nil || completed.Status != RunStatusCompleted {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
}

func TestEvaluateGateIgnoresReportFromPriorPlanVersion(t *testing.T) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	fixture := seedResearchRunFixture(t, ctx, pool)
	defer func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
	}()
	store := NewPostgresStore(pool)
	if _, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Original goal",
		Title: "Versioned report", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report (
			workspace_id, session_id, revision, content_md, goal_version, plan_version
		) VALUES ($1::uuid, $2::uuid, 1, '# Old report', 1, 1)
	`, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET plan_version = 2 WHERE id = $1::uuid`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if !hasGateFinding(gate, "report_missing") {
		t.Fatalf("prior-plan report counted as current: %+v", gate.Findings)
	}
	if hasGateFinding(gate, "report_claims_missing") || hasGateFinding(gate, "report_claims_stale") {
		t.Fatalf("prior-plan report produced current-report findings: %+v", gate.Findings)
	}
}

func submitStoreTask(t *testing.T, ctx context.Context, pool *pgxpool.Pool, store *PostgresStore, fixture researchRunFixture, clientKey string, result ResultEnvelope, config RunConfig) {
	t.Helper()
	if _, err := store.ActivateReadyTasks(ctx, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var task Task
	for _, candidate := range tasks {
		if candidate.ClientKey == clientKey {
			task = candidate
			break
		}
	}
	if task.ID == "" || task.Status != TaskStatusReady {
		t.Fatalf("task %q is not ready: %+v", clientKey, task)
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, task.ID, fixture.agentID)
	if err != nil {
		t.Fatal(err)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'draining')
	`, inboxID, fixture.workspaceID, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	validated, hash, err := DecodeAndValidateResult(raw, task, config)
	if err != nil {
		t.Fatalf("validate %s: %v", clientKey, err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
	}); err != nil {
		t.Fatalf("accept %s: %v", clientKey, err)
	}
}

func e2eVerifiedEvidence() ResultEnvelope {
	sources := make([]SourceProposal, 0, 3)
	observations := make([]ObservationProposal, 0, 3)
	evidence := make([]EvidenceProposal, 0, 3)
	for i, host := range []string{"alpha.example", "beta.example", "gamma.example"} {
		key := fmt.Sprintf("source-%d", i+1)
		observationKey := fmt.Sprintf("observation-%d", i+1)
		sources = append(sources, SourceProposal{
			ClientKey: key, URL: "https://" + host + "/measurement", Title: "Measurement",
			Publisher: host, SourceClass: "primary", IndependenceKey: host,
			RetrievedAt: time.Now().UTC(), SnapshotText: "The independently measured value is 42.",
		})
		observations = append(observations, ObservationProposal{
			ClientKey: observationKey, SourceKey: key, Quote: "measured value is 42",
			Locator: "result", Interpretation: "The value is 42.",
		})
		evidence = append(evidence, EvidenceProposal{ObservationKey: observationKey, Relation: "supports", Strength: 0.9})
	}
	return ResultEnvelope{
		SchemaVersion: 1, Summary: "three-source verification", Confidence: 0.9,
		Sources: sources, Observations: observations,
		Claims: []ClaimProposal{{
			ClientKey: "answer-claim", Text: "The measured value is 42.", Significance: "high",
			Confidence: 0.9, Status: ClaimStatusSupported, Evidence: evidence,
		}},
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasGateFinding(gate GateResult, code string) bool {
	for _, finding := range gate.Findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func validEvidenceResult() ResultEnvelope {
	return ResultEnvelope{
		SchemaVersion:   1,
		ClientRequestID: "evidence-request-1",
		Summary:         "evidence",
		Sources: []SourceProposal{{
			ClientKey: "source-1", URL: "https://example.com/evidence", Title: "Evidence",
			Publisher: "Example", SourceClass: "primary", IndependenceKey: "example-primary",
			RetrievedAt: time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC), SnapshotText: "Direct evidence reports the value 42.",
		}},
		Observations: []ObservationProposal{{
			ClientKey: "observation-1", SourceKey: "source-1", Quote: "the value 42",
			Locator: "paragraph 1", Interpretation: "The measured value is 42.",
		}},
		Claims: []ClaimProposal{{
			ClientKey: "claim-1", Text: "The measured value is 42.", Significance: "high",
			Confidence: 0.8, Status: ClaimStatusSupported,
			Evidence: []EvidenceProposal{{ObservationKey: "observation-1", Relation: "supports", Strength: 0.9}},
		}},
		CoverageDelta: 0.4,
		Confidence:    0.8,
	}
}

type researchRunFixture struct {
	workspaceID string
	userID      string
	agentID     string
	fleetID     string
	sessionID   string
}

func seedResearchRunFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) researchRunFixture {
	t.Helper()
	suffix := uuid.NewString()
	fixture := researchRunFixture{
		workspaceID: uuid.NewString(), userID: uuid.NewString(), agentID: uuid.NewString(),
		fleetID: uuid.NewString(), sessionID: uuid.NewString(),
	}
	runtimeID := uuid.NewString()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO "user" (id, name, email) VALUES ($1::uuid, $2, $3)`, []any{fixture.userID, "research-user-" + suffix, suffix + "@example.test"}},
		{`INSERT INTO workspace (id, name, slug) VALUES ($1::uuid, $2, $3)`, []any{fixture.workspaceID, "Research workspace", "research-" + suffix}},
		{`INSERT INTO agent_runtime (id, workspace_id, name, runtime_mode, provider, status, owner_id) VALUES ($1::uuid, $2::uuid, $3, 'local', 'codex', 'online', $4::uuid)`, []any{runtimeID, fixture.workspaceID, "research-runtime-" + suffix, fixture.userID}},
		{`INSERT INTO agent (id, workspace_id, name, avatar_url, runtime_mode, status, owner_id, runtime_id, model, managed_role) VALUES ($1::uuid, $2::uuid, $3, '/avatars/default.png', 'local', 'idle', $4::uuid, $5::uuid, 'test-model', 'research_fleet')`, []any{fixture.agentID, fixture.workspaceID, "research-agent-" + suffix, fixture.userID, runtimeID}},
		{`INSERT INTO research_fleet (id, workspace_id, lead_agent_id) VALUES ($1::uuid, $2::uuid, $3::uuid)`, []any{fixture.fleetID, fixture.workspaceID, fixture.agentID}},
		{`INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead) VALUES ($1::uuid, $2::uuid, $3::uuid, 'lead', 'active', true)`, []any{fixture.workspaceID, fixture.fleetID, fixture.agentID}},
		{`INSERT INTO research_session (id, workspace_id, fleet_id, created_by, title, goal, status, depth_tier) VALUES ($1::uuid, $2::uuid, $3::uuid, $4::uuid, 'Evidence comparison', 'Compare the evidence', 'running', 'standard')`, []any{fixture.sessionID, fixture.workspaceID, fixture.fleetID, fixture.userID}},
	}
	for _, statement := range statements {
		if _, err := pool.Exec(ctx, statement.query, statement.args...); err != nil {
			pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
			pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
			t.Fatalf("seed fixture: %v", err)
		}
	}
	return fixture
}
