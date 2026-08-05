package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
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

type nonRetryableDispatchTestError struct{}

func (nonRetryableDispatchTestError) Error() string   { return "deterministic dispatch contract failure" }
func (nonRetryableDispatchTestError) Retryable() bool { return false }

type nonRetryableTestDispatcher struct{}

func (nonRetryableTestDispatcher) Dispatch(context.Context, DispatchRequest) (DispatchResult, error) {
	return DispatchResult{}, nonRetryableDispatchTestError{}
}

func (nonRetryableTestDispatcher) Inspect(context.Context, []string) (map[string]InboxTaskState, error) {
	return map[string]InboxTaskState{}, nil
}

func (nonRetryableTestDispatcher) Cancel(context.Context, []string, string) error { return nil }

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

func TestNonRetryableDispatchFailureStopsRunWithoutRemediationLoop(t *testing.T) {
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
	engine := NewEngine(store, nonRetryableTestDispatcher{}, nil)
	_, err = engine.Start(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test permanent dispatch failure",
		Title: "Permanent dispatch failure", DepthTier: "standard", Language: "English",
	})
	if err == nil {
		t.Fatal("Start succeeded after a non-retryable dispatch failure")
	}
	run, getErr := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if run.Status != RunStatusFailed {
		t.Fatalf("run status=%s, want failed", run.Status)
	}
	tasks, listErr := store.ListTasks(ctx, fixture.sessionID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	attempts, listErr := store.ListAttempts(ctx, fixture.sessionID)
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(tasks) != 1 || len(attempts) != 1 {
		t.Fatalf("tasks=%d attempts=%d, want one initial plan and one attempt", len(tasks), len(attempts))
	}
	if attempts[0].FailureClass != "dispatch_failed" || attempts[0].Status != AttemptStatusFailed {
		t.Fatalf("attempt=%+v", attempts[0])
	}
}

func TestExhaustedInitialPlanStopsRunWithoutCreatingReplan(t *testing.T) {
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
	_, _, err = store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test exhausted planning",
		Title: "Exhausted planning", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_task
		SET status = 'blocked', terminal_reason = 'result_not_submitted', completed_at = now()
		WHERE session_id = $1::uuid AND kind = 'plan'
	`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}

	engine := NewEngine(store, &recordingCancellationDispatcher{states: map[string]InboxTaskState{}}, nil)
	if err = engine.ReconcileSession(ctx, fixture.sessionID); err == nil || !strings.Contains(err.Error(), "exhausted its attempts") {
		t.Fatalf("ReconcileSession err=%v", err)
	}
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunStatusFailed || !strings.Contains(run.StopReason, "result_not_submitted") {
		t.Fatalf("run=%+v", run)
	}
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].Kind != TaskKindPlan {
		t.Fatalf("tasks=%+v, want only initial plan", tasks)
	}
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
	planTaskID := tasks[0].ID
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

	result := upgradeResultToV5(validV4PlanResult(t))
	raw, _ := json.Marshal(result)
	validated, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
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
	if outcome.QuestionsCreated != 1 || outcome.TasksCreated != 5 || outcome.Event.Type != "task_result_accepted" {
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
	if err != nil || len(tasks) != 6 || !discoverReady {
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
	method, err := store.GetCurrentMethod(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || method == nil || method.DecisionQuestion != result.Plan.Method.DecisionQuestion || method.PlanVersion != run.PlanVersion {
		t.Fatalf("method=%+v err=%v", method, err)
	}
	snapshot, err := store.TaskContext(ctx, discoverTask.ID, fixture.workspaceID)
	if err != nil || snapshot.Method == nil || snapshot.Method.CreatedByTaskID != planTaskID {
		t.Fatalf("snapshot method=%+v err=%v", snapshot.Method, err)
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
	evidence.SchemaVersion = 4
	evidence.Sources[0].EvidenceTraits = []string{"official_record"}
	evidence.Claims[0].EvidenceStandardKey = "authoritative-record"
	evidence.Claims[0].Evidence[0].Directness = 0.9
	evidence.Claims[0].Evidence[0].MethodFit = 0.9
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

func TestPostgresStoreReplanVersionsResearchMethod(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Choose an operating model",
		Title: "Operating model", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	initial := upgradeResultToV5(validV4PlanResult(t))
	initial.ClientRequestID = "method-plan-initial"
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", initial, run.Config)

	replanTask, _, err := store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: fixture.sessionID, Kind: TaskKindReplan, Objective: "Replace the method after a scope mismatch", Capability: "lead", Priority: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	replanned := upgradeResultToV5(validV4PlanResult(t))
	replanned.ClientRequestID = "method-plan-replanned"
	replanned.Plan.Method.DecisionQuestion = "Which operating model meets the revised scope and failure constraints?"
	replanned.Plan.Method.MethodRationale = "Compare the revised operating boundary and explicitly test failure scenarios omitted by the first plan."
	submitStoreTask(t, ctx, pool, store, fixture, replanTask.ClientKey, replanned, run.Config)

	run, err = store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || run.PlanVersion != 2 {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	current, err := store.GetCurrentMethod(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || current == nil || current.PlanVersion != 2 || current.DecisionQuestion != replanned.Plan.Method.DecisionQuestion {
		t.Fatalf("current method=%+v err=%v", current, err)
	}
	var methodCount int
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'research_method'
	`, fixture.sessionID).Scan(&methodCount); err != nil || methodCount != 2 {
		t.Fatalf("method history count=%d err=%v", methodCount, err)
	}
	var initialOutcome []byte
	if err = pool.QueryRow(ctx, `
		SELECT outcome FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'research_method' AND plan_version = 1
	`, fixture.sessionID).Scan(&initialOutcome); err != nil {
		t.Fatal(err)
	}
	var preserved ResearchMethod
	if err = json.Unmarshal(initialOutcome, &preserved); err != nil || preserved.DecisionQuestion != initial.Plan.Method.DecisionQuestion {
		t.Fatalf("preserved method=%+v err=%v", preserved, err)
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

func TestCreateControlTaskDoesNotReuseSucceededDeliveryTask(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Revise a report",
		Title: "Revision", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	succeededID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_task (
		  id, workspace_id, session_id, client_key, kind, objective,
		  required_capability, expected_result, status, goal_version, plan_version,
		  max_attempts, timeout_seconds, completed_at
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'synthesize-initial', 'synthesize', 'Initial report',
		  'reporter', 'research_report_v2', 'succeeded', $4, $5, 3, 1800, now())
	`, succeededID, fixture.workspaceID, fixture.sessionID, run.GoalVersion, run.PlanVersion); err != nil {
		t.Fatal(err)
	}
	task, _, err := store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: fixture.sessionID, Kind: TaskKindSynthesize, Objective: "Revise the report after quality failure", Capability: "reporter", Priority: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID == succeededID || task.Status != TaskStatusReady || task.ClientKey == "synthesize-initial" {
		t.Fatalf("control task reused succeeded work: %+v", task)
	}
}

func TestControlTaskBindsGateQuestionAndRecordsRoutingDecision(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Answer the required decision question",
		Title: "Targeted remediation", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	plan := upgradeResultToV5(validV4PlanResult(t))
	plan.ClientRequestID = "targeted-control-plan"
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var questionFinding GateFinding
	for _, finding := range gate.Findings {
		if finding.Code == "required_questions_unanswered" {
			questionFinding = finding
			break
		}
	}
	questionID, _ := questionFinding.Metadata["question_id"].(string)
	questionKey, _ := questionFinding.Metadata["question_key"].(string)
	if questionID == "" || questionKey != "question-1" {
		t.Fatalf("required question finding=%+v", questionFinding)
	}
	control := remediationTask(gate)
	control.SessionID = fixture.sessionID
	if control.Kind != TaskKindDiscover || control.QuestionID != questionID {
		t.Fatalf("control=%+v", control)
	}
	task, event, err := store.CreateControlTask(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	if task.QuestionID != questionID || event.Type != "control_task_created" {
		t.Fatalf("task=%+v event=%+v", task, event)
	}
	runAfter, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || runAfter.PlanVersion != run.PlanVersion {
		t.Fatalf("evidence remediation changed plan version: before=%d after=%d err=%v", run.PlanVersion, runAfter.PlanVersion, err)
	}
	var decisionCount int
	var outcome []byte
	if err = pool.QueryRow(ctx, `
		SELECT count(*)::int
		FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'remediation_routing'
	`, fixture.sessionID).Scan(&decisionCount); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `
		SELECT outcome FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'remediation_routing'
		ORDER BY created_at DESC LIMIT 1
	`, fixture.sessionID).Scan(&outcome); err != nil {
		t.Fatal(err)
	}
	if decisionCount != 1 || !strings.Contains(string(outcome), questionID) || !strings.Contains(string(outcome), "required_questions_unanswered") {
		t.Fatalf("routing decisions=%d outcome=%s", decisionCount, outcome)
	}
	replayed, replayEvent, err := store.CreateControlTask(ctx, control)
	if err != nil || replayed.ID != task.ID || replayEvent.ID != "" {
		t.Fatalf("replayed=%+v event=%+v err=%v", replayed, replayEvent, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET plan_version = plan_version + 1 WHERE id = $1::uuid`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.CreateControlTask(ctx, control); !errors.Is(err, ErrControlTargetChanged) {
		t.Fatalf("stale-plan question error=%v", err)
	}
	if _, _, err = store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: fixture.sessionID, Kind: TaskKindDiscover, Objective: "Invalid cross-session target",
		Capability: "scout", Priority: 1, QuestionID: uuid.NewString(),
	}); !errors.Is(err, ErrControlTargetChanged) {
		t.Fatalf("cross-session question error=%v", err)
	}
}

func TestGateRanksRequiredQuestionFrontierByExpectedValue(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Rank unresolved questions",
		Title: "Frontier ranking", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	plan := upgradeResultToV5(validV4PlanResult(t))
	plan.ClientRequestID = "frontier-ranking-plan"
	plan.Plan.Questions[0].Priority = 1
	plan.Plan.Questions[0].Impact = 0.1
	plan.Plan.Questions[0].Uncertainty = 0.1
	plan.Plan.Questions[0].Novelty = 0.1
	plan.Plan.Questions = append(plan.Plan.Questions, QuestionProposal{
		ClientKey: "decision-reversing-gap", Kind: QuestionKindGap, Text: "Which unresolved fact could reverse the decision?",
		Required: true, Priority: 0.8, Impact: 1, Uncertainty: 1, Novelty: 1,
	})
	plan.Plan.Tasks = append(plan.Plan.Tasks, TaskProposal{
		ClientKey: "verify-decision-gap", QuestionKey: "decision-reversing-gap", Kind: TaskKindVerify,
		Objective: "Resolve the decision-reversing gap", RequiredCapability: "validator",
		ExpectedResult: "research_evidence_v5", Priority: 0.9,
	})
	for i := range plan.Plan.Tasks {
		if plan.Plan.Tasks[i].Kind == TaskKindSynthesize {
			plan.Plan.Tasks[i].DependsOn = append(plan.Plan.Tasks[i].DependsOn, "verify-decision-gap")
		}
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)
	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var finding GateFinding
	for _, candidate := range gate.Findings {
		if candidate.Code == "required_questions_unanswered" {
			finding = candidate
			break
		}
	}
	if finding.Metadata["question_key"] != "decision-reversing-gap" {
		t.Fatalf("frontier finding=%+v", finding)
	}
	frontierScore, _ := finding.Metadata["frontier_score"].(float64)
	if frontierScore < 0.9 {
		t.Fatalf("frontier score=%v finding=%+v", frontierScore, finding)
	}
}

func TestInformationGainTracksCanonicalVerificationUpgrade(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Verify the controlling record",
		Title: "Canonical gain", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	plan := upgradeResultToV5(validV4PlanResult(t))
	plan.ClientRequestID = "canonical-gain-plan"
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)
	discovery := upgradeResultToV5(authoritativeRecordEvidenceV4())
	discovery.ClientRequestID = "canonical-gain-discovery"
	discovery.Claims[0].Status = ClaimStatusDisputed
	discovery.Claims[0].Confidence = 0.95
	discovery.Claims[0].Resolution = "The first pass found a plausible answer that still requires independent adjudication."
	discovery.ProposedTasks = []TaskProposal{{
		ClientKey: "follow-up-deep-read", QuestionKey: "question-1", Kind: TaskKindDeepRead,
		Objective: "Inspect the controlling record's material context", RequiredCapability: "analyst",
		ExpectedResult: "research_evidence_v5", Priority: 0.8,
	}}
	submitStoreTask(t, ctx, pool, store, fixture, "discover-1", discovery, run.Config)
	var dynamicDeliveryEdges, dynamicParentEdges int
	if err = pool.QueryRow(ctx, `
		SELECT
		  count(*) FILTER (WHERE task.client_key = 'synthesize' AND dependency.client_key = 'follow-up-deep-read')::int,
		  count(*) FILTER (WHERE task.client_key = 'follow-up-deep-read' AND dependency.client_key = 'discover-1')::int
		FROM research_task_dependency edge
		JOIN research_task task ON task.id = edge.task_id
		JOIN research_task dependency ON dependency.id = edge.depends_on_task_id
		WHERE task.session_id = $1::uuid
	`, fixture.sessionID).Scan(&dynamicDeliveryEdges, &dynamicParentEdges); err != nil {
		t.Fatal(err)
	}
	if dynamicDeliveryEdges != 1 || dynamicParentEdges != 1 {
		t.Fatalf("dynamic delivery edges=%d parent edges=%d", dynamicDeliveryEdges, dynamicParentEdges)
	}

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	control := remediationTask(gate)
	if control.Kind != TaskKindVerify || control.QuestionID == "" {
		t.Fatalf("unverified answer control=%+v gate=%+v", control, gate)
	}
	verification := upgradeResultToV5(authoritativeRecordEvidenceV4())
	verification.ClientRequestID = "canonical-gain-verification"
	verification.Claims[0].Confidence = 0.72
	verification.Claims[0].Resolution = "The controlling record resolves the disputed answer with verified support."
	submitStoreTask(t, ctx, pool, store, fixture, "verify-1", verification, run.Config)
	var claimStatus ClaimStatus
	var claimConfidence float64
	var claimResolution string
	if err = pool.QueryRow(ctx, `
		SELECT status, confidence, resolution
		FROM research_claim
		WHERE session_id = $1::uuid AND client_key = 'registered-value'
	`, fixture.sessionID).Scan(&claimStatus, &claimConfidence, &claimResolution); err != nil {
		t.Fatal(err)
	}
	if claimStatus != ClaimStatusSupported || math.Abs(claimConfidence-0.72) > 1e-9 || claimResolution != verification.Claims[0].Resolution {
		t.Fatalf("adjudicated claim status=%s confidence=%v resolution=%q", claimStatus, claimConfidence, claimResolution)
	}

	rows, err := pool.Query(ctx, `
		SELECT outcome FROM research_decision
		WHERE session_id = $1::uuid AND decision_kind = 'information_gain'
		ORDER BY created_at, id
	`, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type gainDecision struct {
		Gain    informationGainBreakdown `json:"gain"`
		LowGain bool                     `json:"low_gain"`
	}
	decisions := make([]gainDecision, 0, 2)
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var decision gainDecision
		if err = json.Unmarshal(raw, &decision); err != nil {
			t.Fatal(err)
		}
		decisions = append(decisions, decision)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(decisions) != 2 || decisions[0].Gain.Score < 0.049 || decisions[0].Gain.VerifiedCoverage != 0 ||
		decisions[0].Gain.AnsweredQuestions != 0 || decisions[0].Gain.ClaimResolution != 0 ||
		decisions[0].Gain.ClaimAdjudication != 0 || decisions[1].Gain.Score < 0.7 ||
		decisions[1].Gain.ClaimResolution == 0 || decisions[1].Gain.ClaimAdjudication == 0 || decisions[1].LowGain {
		t.Fatalf("gain decisions=%+v", decisions)
	}
	run, err = store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || math.Abs(run.Stats.LastMeasuredGain-decisions[1].Gain.Score) > 1e-9 || run.Stats.LowGainStreak != 0 {
		t.Fatalf("run stats=%+v decisions=%+v err=%v", run.Stats, decisions, err)
	}
	gate, err = store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil || hasGateFinding(gate, "required_questions_unanswered") {
		t.Fatalf("verified answer gate=%+v err=%v", gate, err)
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
	config := DefaultRunConfig("standard")
	config.MarginalGainThreshold = 0.1
	run, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Establish the measured value",
		Title: "Measured value", DepthTier: "standard", Language: "English",
	}, config)
	if err != nil {
		t.Fatal(err)
	}
	plan := ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "e2e-plan", Summary: "dependency-safe plan", Confidence: 0.8,
		Plan: &PlanProposal{
			Method: &MethodProposal{
				DecisionQuestion:     "What value is supported by comparable independent measurements?",
				MethodRationale:      "Triangulate equivalent measurements and challenge the result with independent repeats.",
				AnalysisMethods:      []string{"Cross-source measurement comparison"},
				EvidenceRequirements: []string{"Three traceable independent measurements"},
				EvidenceStandards: []EvidenceStandard{{
					ClientKey: "independent-measurements", Purpose: "Establish a measured value from comparable independent measurements.",
					MinimumIndependentSources: 3, RequiredSourceTraits: []string{"direct_measurement"},
					MinimumStrength: 0.8, MinimumDirectness: 0.8, MinimumMethodFit: 0.8,
				}},
				CounterevidenceStrategy: []string{"Search for independently measured conflicting values"},
				StoppingConditions:      []string{"The required answer is verified and repeated work produces negligible information gain"},
			},
			Questions: []QuestionProposal{{
				ClientKey: "answer-question", Kind: QuestionKindDimension, Text: "What is the measured value?",
				Required: true, Priority: 1, Impact: 1, Uncertainty: 0.8, Novelty: 0.5,
			}},
			Tasks: []TaskProposal{
				{ClientKey: "verify-1", QuestionKey: "answer-question", Kind: TaskKindVerify, Objective: "Triangulate three independent measurements", RequiredCapability: "validator", ExpectedResult: "research_evidence_v5", Priority: 1},
				{ClientKey: "verify-2", QuestionKey: "answer-question", Kind: TaskKindVerify, Objective: "Repeat verification for marginal-gain measurement", RequiredCapability: "validator", ExpectedResult: "research_evidence_v5", Priority: 0.9, DependsOn: []string{"verify-1"}},
				{ClientKey: "verify-3", QuestionKey: "answer-question", Kind: TaskKindVerify, Objective: "Confirm saturation", RequiredCapability: "validator", ExpectedResult: "research_evidence_v5", Priority: 0.8, DependsOn: []string{"verify-2"}},
				{ClientKey: "synthesize", Kind: TaskKindSynthesize, Objective: "Write evidence-linked report", RequiredCapability: "reporter", ExpectedResult: "research_report_v5", Priority: 0.7, DependsOn: []string{"verify-3"}},
				{ClientKey: "quality", Kind: TaskKindQualityGate, Objective: "Evaluate report quality", RequiredCapability: "validator", ExpectedResult: "research_quality_evaluation_v5", Priority: 0.6, DependsOn: []string{"synthesize"}},
				{ClientKey: "citations", Kind: TaskKindCitationAudit, Objective: "Audit report citations", RequiredCapability: "validator", ExpectedResult: "research_citation_audit_v5", Priority: 0.6, DependsOn: []string{"synthesize"}},
			},
			InclusionCriteria: []string{"Primary evidence"}, ExclusionCriteria: []string{"Unverifiable summaries"},
			SourceStrategy: []string{"Independent source families"}, Uncertainties: []string{"Measurement context"}, PlanningRisks: []string{"Source disagreement"},
		},
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)

	evidence := upgradeResultToV5(e2eVerifiedEvidenceV4())
	evidence.AnswerClaimKey = "answer-claim"
	evidence.ClientRequestID = "e2e-evidence-1"
	evidence.CoverageDelta = 0.8
	submitStoreTask(t, ctx, pool, store, fixture, "verify-1", evidence, run.Config)
	evidence.ClientRequestID = "e2e-evidence-2"
	evidence.CoverageDelta = 0
	submitStoreTask(t, ctx, pool, store, fixture, "verify-2", evidence, run.Config)
	evidence.ClientRequestID = "e2e-evidence-3"
	evidence.Sources[0].ClientKey = "source-4"
	evidence.Sources[0].URL = "https://delta.example/measurement"
	evidence.Sources[0].Publisher = "delta.example"
	evidence.Sources[0].IndependenceKey = "delta.example"
	evidence.Sources[0].SnapshotText = "The additional independent measurement is 42."
	evidence.Observations[0].ClientKey = "observation-4"
	evidence.Observations[0].SourceKey = "source-4"
	evidence.Observations[0].Quote = "additional independent measurement is 42"
	evidence.Claims[0].Evidence[0].ObservationKey = "observation-4"
	submitStoreTask(t, ctx, pool, store, fixture, "verify-3", evidence, run.Config)
	runAfterEvidence, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || runAfterEvidence.Stats.LastMeasuredGain <= 0 {
		t.Fatalf("run after evidence stats=%+v err=%v", runAfterEvidence.Stats, err)
	}

	report := e2eStructuredReport(t, ctx, pool, fixture.sessionID)
	submitStoreTask(t, ctx, pool, store, fixture, "synthesize", ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "e2e-report", Summary: "report", Confidence: 0.9,
		Report: &report,
	}, run.Config)
	evaluation := EvaluationProposal{
		Passed: true, FactualGrounding: 0.9, Coverage: 0.9, AnalyticalDepth: 0.9,
		SourceQuality: 0.9, ContradictionHandling: 0.9, InstructionAdherence: 0.9, Readability: 0.9,
		DimensionFindings: e2eDimensionFindings(), ReviewedClaimKeys: []string{"answer-claim"},
		ReviewedSectionIDs: []string{"executive-summary", "method", "finding", "limitations", "conclusion"},
	}
	submitStoreTask(t, ctx, pool, store, fixture, "quality", ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "e2e-quality", Summary: "quality passed", Confidence: 0.9, Evaluation: &evaluation,
	}, run.Config)
	submitStoreTask(t, ctx, pool, store, fixture, "citations", ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "e2e-citations", Summary: "citations passed", Confidence: 0.9, Evaluation: &evaluation,
	}, run.Config)
	runAfterDelivery, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil || math.Abs(runAfterDelivery.Stats.LastMeasuredGain-runAfterEvidence.Stats.LastMeasuredGain) > 1e-9 {
		t.Fatalf("delivery changed measured gain before=%v after=%v err=%v", runAfterEvidence.Stats.LastMeasuredGain, runAfterDelivery.Stats.LastMeasuredGain, err)
	}

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil || !gate.Passed {
		t.Fatalf("gate=%+v err=%v", gate, err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_decision (
		  workspace_id, session_id, decision_kind, actor_type, goal_version, plan_version, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, 'information_gain', 'system', 1, 1,
		          '{"canonical_changed":false,"gain":{"score":0}}'::jsonb,
		          'Simulate a duplicate saturation probe after the latest report.')
	`, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if freshGate, freshErr := store.EvaluateGate(ctx, fixture.sessionID); freshErr != nil || !freshGate.Passed {
		t.Fatalf("duplicate evidence invalidated report gate=%+v err=%v", freshGate, freshErr)
	}
	awaiting, _, err := store.SetAwaitingConfirmation(ctx, fixture.sessionID, gate)
	if err != nil || awaiting.Status != RunStatusAwaitingUserConfirm {
		t.Fatalf("awaiting=%+v err=%v", awaiting, err)
	}
	completed, _, err := store.Complete(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID)
	if err != nil || completed.Status != RunStatusCompleted {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_decision (
		  workspace_id, session_id, decision_kind, actor_type, goal_version, plan_version, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, 'information_gain', 'system', 1, 1,
		          '{"canonical_changed":true,"gain":{"score":0.2}}'::jsonb,
		          'Simulate canonical evidence accepted after the latest report.')
	`, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	staleGate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil || !hasGateFinding(staleGate, "report_stale_after_evidence") {
		t.Fatalf("stale report gate=%+v err=%v", staleGate, err)
	}
}

func TestPostgresStoreV4EvidenceFitnessAcceptsOneControllingRecordAtDeepTier(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Read the registered value from its controlling record",
		Title: "Registered value", DepthTier: "deep", Language: "English",
	}, DefaultRunConfig("deep"))
	if err != nil {
		t.Fatal(err)
	}
	plan := upgradeResultToV5(validV4PlanResult(t))
	plan.ClientRequestID = "authoritative-record-plan"
	plan.Plan.Questions[0].Text = "What value is recorded in the controlling registry?"
	plan.Plan.Tasks[0].ClientKey = "verify-record"
	plan.Plan.Tasks[0].Kind = TaskKindVerify
	plan.Plan.Tasks[0].RequiredCapability = "validator"
	for i := 1; i < len(plan.Plan.Tasks); i++ {
		for dependencyIndex, dependency := range plan.Plan.Tasks[i].DependsOn {
			if dependency == "discover-1" {
				plan.Plan.Tasks[i].DependsOn[dependencyIndex] = "verify-record"
			}
		}
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)

	evidence := upgradeResultToV5(authoritativeRecordEvidenceV4())
	submitStoreTask(t, ctx, pool, store, fixture, "verify-record", evidence, run.Config)

	findings, err := store.evaluateEvidenceFitnessV4(ctx, fixture.sessionID, 1, 1)
	if err != nil || len(findings) != 0 {
		t.Fatalf("evidence fitness findings=%+v err=%v", findings, err)
	}
	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if hasGateFinding(gate, "independent_sources_insufficient") || hasGateFinding(gate, "claim_evidence_standard_unmet") {
		t.Fatalf("deep run applied a global source quota: %+v", gate.Findings)
	}

	snapshots, err := store.ListSourceSnapshots(ctx, fixture.sessionID)
	if err != nil || len(snapshots) != 1 || len(snapshots[0].EvidenceTraits) != 1 || snapshots[0].EvidenceTraits[0] != "official_record" {
		t.Fatalf("snapshots=%+v err=%v", snapshots, err)
	}
	claims, err := store.ListClaims(ctx, fixture.sessionID)
	if err != nil || len(claims) != 1 || claims[0].EvidenceStandardKey != "authoritative-record" || len(claims[0].Evidence) != 1 || claims[0].Evidence[0].Directness != 1 || claims[0].Evidence[0].MethodFit != 1 {
		t.Fatalf("claims=%+v err=%v", claims, err)
	}
}

func TestPostgresStoreV4RequiresCounterSearchForTheTargetClaim(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Verify and challenge a registered value",
		Title: "Challenge registered value", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	plan := upgradeResultToV5(validV4PlanResult(t))
	plan.ClientRequestID = "counter-search-plan"
	plan.Plan.Method.EvidenceStandards[0].CounterevidenceRequired = true
	plan.Plan.Tasks[0] = TaskProposal{
		ClientKey: "verify-record", QuestionKey: "question-1", Kind: TaskKindVerify,
		Objective: "Verify the controlling record", RequiredCapability: "validator", ExpectedResult: "research_evidence_v5", Priority: 1,
	}
	plan.Plan.Tasks = append(plan.Plan.Tasks, TaskProposal{
		ClientKey: "challenge-record", QuestionKey: "question-1", Kind: TaskKindCounterSearch,
		Objective: "Search for a superseding or conflicting controlling record", RequiredCapability: "validator",
		ExpectedResult: "research_evidence_v5", Priority: 0.9, DependsOn: []string{"verify-record"},
	})
	for i := 1; i < len(plan.Plan.Tasks)-1; i++ {
		for dependencyIndex, dependency := range plan.Plan.Tasks[i].DependsOn {
			if dependency == "discover-1" {
				plan.Plan.Tasks[i].DependsOn[dependencyIndex] = "challenge-record"
			}
		}
	}
	submitStoreTask(t, ctx, pool, store, fixture, "plan:1", plan, run.Config)

	evidence := upgradeResultToV5(authoritativeRecordEvidenceV4())
	submitStoreTask(t, ctx, pool, store, fixture, "verify-record", evidence, run.Config)
	findings, err := store.evaluateEvidenceFitnessV4(ctx, fixture.sessionID, 1, 1)
	if err != nil || !hasFindingCode(findings, "claim_counterevidence_search_missing") {
		t.Fatalf("findings before targeted counter-search=%+v err=%v", findings, err)
	}

	evidence.ClientRequestID = "targeted-counter-search-result"
	evidence.Summary = "No superseding or conflicting controlling record was found in the bounded counter-search."
	submitStoreTask(t, ctx, pool, store, fixture, "challenge-record", evidence, run.Config)
	findings, err = store.evaluateEvidenceFitnessV4(ctx, fixture.sessionID, 1, 1)
	if err != nil || hasFindingCode(findings, "claim_counterevidence_search_missing") {
		t.Fatalf("findings after targeted counter-search=%+v err=%v", findings, err)
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

func TestEvaluateGateProjectsActionableEvaluationFeedback(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Revise from reviewer evidence",
		Title: "Evaluation feedback", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	reportID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report (
		  id, workspace_id, session_id, revision, content_md, structured,
		  goal_version, plan_version, author_agent_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, '# Report', '{}'::jsonb, 1, 1, $4::uuid)
	`, reportID, fixture.workspaceID, fixture.sessionID, fixture.reporterID); err != nil {
		t.Fatal(err)
	}
	evaluation := EvaluationProposal{
		Passed: false, FactualGrounding: 0.45, Coverage: 0.6, AnalyticalDepth: 0.9,
		SourceQuality: 0.9, ContradictionHandling: 0.9, InstructionAdherence: 0.9, Readability: 0.9,
		DimensionFindings: e2eDimensionFindings(),
		Findings:          []string{"The executive summary overstates claim-alpha beyond its cited observation."},
		ReviewedClaimKeys: []string{"claim-alpha"}, ReviewedSectionIDs: []string{"executive-summary"},
	}
	evaluation.DimensionFindings["factual_grounding"] = "Claim alpha is stated categorically although the cited observation supports only a bounded conditional result."
	evaluation.DimensionFindings["coverage"] = "The report omits the required boundary condition from the executive summary and conclusion."
	outcome, err := json.Marshal(evaluation)
	if err != nil {
		t.Fatal(err)
	}
	decisionID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_decision (
		  id, workspace_id, session_id, decision_kind, actor_type, actor_id,
		  goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'quality_gate', 'agent', $4::uuid,
		          1, 1, jsonb_build_object('report_id', $5::text), $6::jsonb, $7)
	`, decisionID, fixture.workspaceID, fixture.sessionID, fixture.validatorID, reportID, outcome, evaluation.Findings[0]); err != nil {
		t.Fatal(err)
	}
	citationDecisionID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_decision (
		  id, workspace_id, session_id, decision_kind, actor_type, actor_id,
		  goal_version, plan_version, inputs, outcome, rationale
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'citation_audit', 'agent', $4::uuid,
		          1, 1, jsonb_build_object('report_id', $5::text), $6::jsonb, $7)
	`, citationDecisionID, fixture.workspaceID, fixture.sessionID, fixture.validatorID, reportID, outcome, evaluation.Findings[0]); err != nil {
		t.Fatal(err)
	}

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var qualityFinding GateFinding
	for _, finding := range gate.Findings {
		if finding.Code == "quality_evaluation_failed" {
			qualityFinding = finding
			break
		}
	}
	if qualityFinding.Code == "" || qualityFinding.Metadata["evaluation_decision_id"] != decisionID ||
		qualityFinding.Metadata["report_id"] != reportID || qualityFinding.Metadata["reviewer_agent_id"] != fixture.validatorID {
		t.Fatalf("quality finding=%+v", qualityFinding)
	}
	failed, ok := qualityFinding.Metadata["failed_dimensions"].([]map[string]any)
	if !ok || len(failed) != 2 || failed[0]["dimension"] != "factual_grounding" || failed[1]["dimension"] != "coverage" {
		t.Fatalf("failed dimensions=%#v", qualityFinding.Metadata["failed_dimensions"])
	}
	var citationFinding GateFinding
	for _, finding := range gate.Findings {
		if finding.Code == "citation_audit_failed" {
			citationFinding = finding
			break
		}
	}
	if citationFinding.Code == "" || citationFinding.Metadata["evaluation_decision_id"] != citationDecisionID ||
		citationFinding.Metadata["report_id"] != reportID || citationFinding.Metadata["reviewer_agent_id"] != fixture.validatorID {
		t.Fatalf("citation finding=%+v", citationFinding)
	}
	control := remediationTask(GateResult{Findings: []GateFinding{qualityFinding, citationFinding}})
	if control.Kind != TaskKindSynthesize || !strings.Contains(control.Objective, "overstates claim-alpha") || !strings.Contains(control.Objective, "factual_grounding") {
		t.Fatalf("control=%+v", control)
	}
	control.SessionID = fixture.sessionID
	revisionTask, _, err := store.CreateControlTask(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	var criteria struct {
		Remediation struct {
			FindingCodes   []string      `json:"finding_codes"`
			TargetFindings []GateFinding `json:"target_findings"`
		} `json:"remediation"`
	}
	if err = json.Unmarshal(revisionTask.AcceptanceCriteria, &criteria); err != nil {
		t.Fatal(err)
	}
	if len(criteria.Remediation.TargetFindings) != 2 ||
		criteria.Remediation.TargetFindings[0].Metadata["evaluation_decision_id"] != decisionID ||
		criteria.Remediation.TargetFindings[1].Metadata["evaluation_decision_id"] != citationDecisionID {
		t.Fatalf("acceptance criteria=%s", revisionTask.AcceptanceCriteria)
	}
}

func TestV5EvaluationDefectsPersistAndReachRemediation(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Repair exact reviewed defects",
		Title: "Structured evaluation defects", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard"))
	if err != nil {
		t.Fatal(err)
	}
	if run.OrchestratorVersion != OrchestratorVersionV5 {
		t.Fatalf("orchestrator=%s", run.OrchestratorVersion)
	}
	claimID := uuid.NewString()
	reportID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, claim_text, significance,
		  confidence, status, goal_version, plan_version
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'claim-alpha', 'The measured result applies inside the operating boundary.', 'high', 0.9, 'supported', 1, 1)
	`, claimID, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	structured := `{"schema_version":1,"title":"Reviewed report","outline":[{"id":"section-alpha","title":"Result","level":1,"children":[]}],"sections":[{"id":"section-alpha","title":"Result","level":1,"markdown":"The measured result applies everywhere without qualification.","citation_ids":[]}],"citations":[],"sources":[],"gaps":[],"conclusion":"The measured result applies everywhere without qualification."}`
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report (
		  id, workspace_id, session_id, revision, content_md, structured,
		  goal_version, plan_version, author_agent_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, '# Reviewed report', $4::jsonb, 1, 1, $5::uuid)
	`, reportID, fixture.workspaceID, fixture.sessionID, structured, fixture.reporterID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report_claim (report_id, claim_id, section_id, anchor_quote)
		VALUES ($1::uuid, $2::uuid, 'section-alpha', 'The measured result applies everywhere without qualification.')
	`, reportID, claimID); err != nil {
		t.Fatal(err)
	}
	qualityTask, _, err := store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: fixture.sessionID, Kind: TaskKindQualityGate, Capability: "validator", Priority: 1,
		Objective: "Evaluate the current report with addressable defects.", Findings: []GateFinding{{Code: "quality_evaluation_missing"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	evaluation := validV5EvaluationResult(false)
	submitStoreTask(t, ctx, pool, store, fixture, qualityTask.ClientKey, ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "v5-structured-quality", Summary: "The report overstates the supported scope.",
		Confidence: 0.9, Evaluation: &evaluation,
	}, DefaultRunConfig("standard"))

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	var finding GateFinding
	for _, candidate := range gate.Findings {
		if candidate.Code == "quality_evaluation_failed" {
			finding = candidate
			break
		}
	}
	defects, ok := finding.Metadata["defects"].([]EvaluationDefect)
	if finding.Code == "" || !ok || len(defects) != 1 || defects[0].ClientKey != "defect-grounding-alpha" ||
		defects[0].RequiredChange != evaluation.Defects[0].RequiredChange {
		t.Fatalf("quality finding=%+v defects=%#v", finding, finding.Metadata["defects"])
	}
	control := remediationTask(GateResult{Findings: []GateFinding{finding}})
	control.SessionID = fixture.sessionID
	revisionTask, _, err := store.CreateControlTask(ctx, control)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(revisionTask.Objective, "defect-grounding-alpha") ||
		!strings.Contains(string(revisionTask.AcceptanceCriteria), "retain the measured boundary") {
		t.Fatalf("revision objective=%s criteria=%s", revisionTask.Objective, revisionTask.AcceptanceCriteria)
	}
	invalidTask, _, err := store.CreateControlTask(ctx, ControlTaskInput{
		SessionID: fixture.sessionID, Kind: TaskKindCitationAudit, Capability: "validator", Priority: 1,
		Objective: "Reject a defect that points outside the reviewed report.", Findings: []GateFinding{{Code: "citation_audit_missing"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, invalidTask.ID, fixture.validatorID)
	if err != nil {
		t.Fatal(err)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'draining')
	`, inboxID, fixture.workspaceID, fixture.validatorID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	invalidEvaluation := validV5EvaluationResult(false)
	invalidEvaluation.Defects[0].ClaimKeys = []string{"claim-outside-report"}
	raw, err := json.Marshal(ResultEnvelope{
		SchemaVersion: 5, ClientRequestID: "v5-invalid-target", Summary: "Invalid target", Confidence: 0.9,
		Evaluation: &invalidEvaluation,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated, hash, err := DecodeAndValidateResultForVersion(OrchestratorVersionV5, raw, invalidTask, run.Config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: fixture.validatorID,
		InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
	})
	if !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "unknown report Claim") {
		t.Fatalf("invalid target err=%v", err)
	}
}

func TestEvaluateGateBlocksUnreportedRequiredAnswerAndAuthorSelfReview(t *testing.T) {
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
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Audit delivery",
		Title: "Audit delivery", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	claimID := uuid.NewString()
	questionID := uuid.NewString()
	reportID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_claim (
		  id, workspace_id, session_id, client_key, claim_text, significance,
		  confidence, status, goal_version, plan_version
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'required-answer', 'The required answer', 'high', 0.9, 'supported', 1, 1)
	`, claimID, fixture.workspaceID, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_question (
		  id, workspace_id, session_id, client_key, kind, question, required,
		  status, priority, impact, uncertainty, novelty, coverage,
		  goal_version, plan_version, answer_claim_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 'required-question', 'dimension', 'What is the answer?', true,
		  'answered', 1, 1, 0.5, 0.5, 1, 1, 1, $4::uuid)
	`, questionID, fixture.workspaceID, fixture.sessionID, claimID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		INSERT INTO research_report (
		  id, workspace_id, session_id, revision, content_md, structured,
		  goal_version, plan_version, author_agent_id
		) VALUES ($1::uuid, $2::uuid, $3::uuid, 1, '# Short', '{"schema_version":1}'::jsonb, 1, 1, $4::uuid)
	`, reportID, fixture.workspaceID, fixture.sessionID, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	evaluation := `{"passed":true,"factual_grounding":0.9,"coverage":0.9,"analytical_depth":0.9,"source_quality":0.9,"contradiction_handling":0.9,"instruction_adherence":0.9,"readability":0.9,"findings":[]}`
	for _, kind := range []TaskKind{TaskKindQualityGate, TaskKindCitationAudit} {
		if _, err = pool.Exec(ctx, `
			INSERT INTO research_decision (
			  workspace_id, session_id, decision_kind, actor_type, actor_id,
			  goal_version, plan_version, inputs, outcome
			) VALUES ($1::uuid, $2::uuid, $3, 'agent', $4::uuid, 1, 1,
			  jsonb_build_object('report_id', $5::text), $6::jsonb)
		`, fixture.workspaceID, fixture.sessionID, kind, fixture.agentID, reportID, evaluation); err != nil {
			t.Fatal(err)
		}
	}

	gate, err := store.EvaluateGate(ctx, fixture.sessionID)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"required_answers_unreported", "quality_evaluation_not_independent", "citation_audit_not_independent", "report_structure_incomplete"} {
		if !hasGateFinding(gate, code) {
			t.Fatalf("gate missing %q: %+v", code, gate.Findings)
		}
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
	agentID := fixture.agentID
	switch task.RequiredCapability {
	case "reporter":
		agentID = fixture.reporterID
	case "validator":
		agentID = fixture.validatorID
	}
	attempt, _, err := store.CreateAttempt(ctx, fixture.sessionID, task.ID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'draining')
	`, inboxID, fixture.workspaceID, agentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AttachInboxTask(ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	validated, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, task, config)
	if err != nil {
		t.Fatalf("validate %s: %v", clientKey, err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: fixture.sessionID, AttemptID: attempt.ID, AgentID: agentID,
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

func e2eVerifiedEvidenceV4() ResultEnvelope {
	result := e2eVerifiedEvidence()
	result.SchemaVersion = 4
	for i := range result.Sources {
		result.Sources[i].EvidenceTraits = []string{"direct_measurement"}
	}
	for i := range result.Claims {
		result.Claims[i].EvidenceStandardKey = "independent-measurements"
		for j := range result.Claims[i].Evidence {
			result.Claims[i].Evidence[j].Directness = 0.9
			result.Claims[i].Evidence[j].MethodFit = 0.9
		}
	}
	return result
}

func authoritativeRecordEvidenceV4() ResultEnvelope {
	return ResultEnvelope{
		SchemaVersion: 4, ClientRequestID: "authoritative-record-evidence", Summary: "verified controlling record", Confidence: 0.95,
		Sources: []SourceProposal{{
			ClientKey: "official-record", URL: "https://registry.example/record", Title: "Controlling record", Publisher: "Registry",
			SourceClass: "official", EvidenceTraits: []string{"official_record"}, IndependenceKey: "registry", RetrievedAt: time.Now().UTC(),
			SnapshotText: "The registered value is 42.",
		}},
		Observations: []ObservationProposal{{
			ClientKey: "record-value", SourceKey: "official-record", Quote: "The registered value is 42.", Locator: "record.value",
		}},
		Claims: []ClaimProposal{{
			ClientKey: "registered-value", EvidenceStandardKey: "authoritative-record", Text: "The registered value is 42.",
			Significance: "high", Confidence: 0.95, Status: ClaimStatusSupported,
			Evidence: []EvidenceProposal{{
				ObservationKey: "record-value", Relation: "supports", Strength: 1, Directness: 1, MethodFit: 1,
				Rationale: "The controlling record directly contains the registered value.",
			}},
		}},
		AnswerClaimKey: "registered-value", CoverageDelta: 0.9,
	}
}

func upgradeResultToV5(result ResultEnvelope) ResultEnvelope {
	result.SchemaVersion = 5
	result.ProposedTasks = translateTaskResultKinds(result.ProposedTasks, "_v4", "_v5")
	if result.Plan != nil {
		plan := *result.Plan
		plan.Tasks = translateTaskResultKinds(result.Plan.Tasks, "_v4", "_v5")
		result.Plan = &plan
	}
	return result
}

func hasFindingCode(findings []GateFinding, code string) bool {
	for _, finding := range findings {
		if finding.Code == code {
			return true
		}
	}
	return false
}

func e2eStructuredReport(t *testing.T, ctx context.Context, pool *pgxpool.Pool, sessionID string) ReportProposal {
	t.Helper()
	type projectedSource struct {
		ID          string
		URL         string
		Title       string
		Weight      float64
		SourceClass string
	}
	rows, err := pool.Query(ctx, `
		SELECT id::text, url, title, credibility_weight, source_class
		FROM research_source WHERE session_id = $1::uuid ORDER BY url
	`, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	projected := []projectedSource{}
	for rows.Next() {
		var source projectedSource
		if err = rows.Scan(&source.ID, &source.URL, &source.Title, &source.Weight, &source.SourceClass); err != nil {
			t.Fatal(err)
		}
		projected = append(projected, source)
	}
	if err = rows.Err(); err != nil || len(projected) < 3 {
		t.Fatalf("projected sources=%d err=%v", len(projected), err)
	}
	projected = projected[:3]

	sectionText := func(topic string) string {
		return strings.Repeat(topic+" explains the evidence boundary, comparison method, observed result, uncertainty, and decision consequence in complete prose. ", 3)
	}
	conclusion := strings.Repeat("The independently reproduced measurements support the value 42 while preserving the stated scope, uncertainty, and evidence limits. ", 2)
	sections := []reportStructuredSection{
		{ID: "executive-summary", Title: "Executive summary", Level: 1, Markdown: sectionText("The executive summary")},
		{ID: "method", Title: "Method", Level: 1, Markdown: sectionText("The method section")},
		{ID: "finding", Title: "Finding", Level: 1, Markdown: "The independently measured value is 42 across three source families. " + sectionText("The finding section"), CitationIDs: []string{"citation-1", "citation-2", "citation-3"}},
		{ID: "limitations", Title: "Limitations", Level: 1, Markdown: sectionText("The limitations section")},
		{ID: "conclusion", Title: "Conclusion", Level: 1, Markdown: conclusion},
	}
	structured := reportStructuredV1{SchemaVersion: 1, Title: "Measured value research report", Conclusion: conclusion}
	for i, section := range sections {
		structured.Sections = append(structured.Sections, section)
		structured.Outline = append(structured.Outline, reportOutlineItem{ID: section.ID, Title: section.Title, Level: section.Level, Children: []string{}})
		if i < len(projected) {
			citationID := fmt.Sprintf("citation-%d", i+1)
			structured.Citations = append(structured.Citations, reportStructuredCitation{ID: citationID, Index: i + 1, SourceID: projected[i].ID, Label: fmt.Sprintf("[%d]", i+1)})
			structured.Sources = append(structured.Sources, reportStructuredSource{SourceID: projected[i].ID, Title: projected[i].Title, URL: projected[i].URL, CredibilityWeight: projected[i].Weight, SourceClass: projected[i].SourceClass})
		}
	}
	structuredJSON, err := json.Marshal(structured)
	if err != nil {
		t.Fatal(err)
	}
	contentParts := []string{"# " + structured.Title}
	for _, section := range sections {
		contentParts = append(contentParts, "## "+section.Title+"\n\n"+section.Markdown)
	}
	return ReportProposal{
		ContentMD: strings.Join(contentParts, "\n\n"), Structured: structuredJSON,
		Claims: []ReportClaimProposal{{ClaimKey: "answer-claim", SectionID: "finding", AnchorQuote: "The independently measured value is 42 across three source families."}},
	}
}

func e2eDimensionFindings() map[string]string {
	findings := map[string]string{}
	for _, dimension := range evaluationDimensionNames {
		findings[dimension] = "The reviewer checked every report section and normalized claim against the stored evidence ledger and recorded no unresolved defect."
	}
	return findings
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
	reporterID  string
	validatorID string
	fleetID     string
	sessionID   string
}

func seedResearchRunFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) researchRunFixture {
	t.Helper()
	suffix := uuid.NewString()
	fixture := researchRunFixture{
		workspaceID: uuid.NewString(), userID: uuid.NewString(), agentID: uuid.NewString(),
		reporterID: uuid.NewString(), validatorID: uuid.NewString(),
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
		{`INSERT INTO agent (id, workspace_id, name, avatar_url, runtime_mode, status, owner_id, runtime_id, model, managed_role) VALUES ($1::uuid, $2::uuid, $3, '/avatars/default.png', 'local', 'idle', $4::uuid, $5::uuid, 'test-model', 'research_fleet')`, []any{fixture.reporterID, fixture.workspaceID, "research-reporter-" + suffix, fixture.userID, runtimeID}},
		{`INSERT INTO agent (id, workspace_id, name, avatar_url, runtime_mode, status, owner_id, runtime_id, model, managed_role) VALUES ($1::uuid, $2::uuid, $3, '/avatars/default.png', 'local', 'idle', $4::uuid, $5::uuid, 'test-model', 'research_fleet')`, []any{fixture.validatorID, fixture.workspaceID, "research-validator-" + suffix, fixture.userID, runtimeID}},
		{`INSERT INTO research_fleet (id, workspace_id, lead_agent_id) VALUES ($1::uuid, $2::uuid, $3::uuid)`, []any{fixture.fleetID, fixture.workspaceID, fixture.agentID}},
		{`INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead) VALUES ($1::uuid, $2::uuid, $3::uuid, 'lead', 'active', true)`, []any{fixture.workspaceID, fixture.fleetID, fixture.agentID}},
		{`INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead) VALUES ($1::uuid, $2::uuid, $3::uuid, 'reporter', 'active', false)`, []any{fixture.workspaceID, fixture.fleetID, fixture.reporterID}},
		{`INSERT INTO research_fleet_member (workspace_id, fleet_id, agent_id, role, status, is_lead) VALUES ($1::uuid, $2::uuid, $3::uuid, 'validator', 'active', false)`, []any{fixture.workspaceID, fixture.fleetID, fixture.validatorID}},
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
