package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestExecutionCircuitFailureWindowAndProbeLease(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 4)
	attempts := make([]Attempt, 0, len(tasks))
	for _, task := range tasks {
		attempts = append(attempts, createCircuitAttempt(t, ctx, store, fixture, task, target))
	}

	disposition := failureDisposition(FailureRateLimited)
	first, transitions, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[0].ID,
		Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
		Diagnostics: "first rate-limit observation",
	})
	if err != nil || first.State != CircuitClosed || first.ConsecutiveFailures != 1 || len(transitions) != 1 {
		t.Fatalf("first failure circuit=%+v transitions=%+v err=%v", first, transitions, err)
	}
	duplicate, duplicateTransitions, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[0].ID,
		Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
		Diagnostics: "must be idempotent",
	})
	if err != nil || duplicate.Generation != first.Generation || duplicate.ConsecutiveFailures != 1 || len(duplicateTransitions) != 1 || duplicateTransitions[0].ID != transitions[0].ID {
		t.Fatalf("duplicate failure circuit=%+v transitions=%+v err=%v", duplicate, duplicateTransitions, err)
	}
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[1].ID,
		Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
		Diagnostics: "second rate-limit observation",
	})
	if err != nil || opened.State != CircuitOpen || opened.ConsecutiveFailures != 2 || opened.NextProbeAt == nil {
		t.Fatalf("opened circuit=%+v err=%v", opened, err)
	}
	events, err := store.ListRunEvents(ctx, fixture.sessionID, fixture.workspaceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var transitionPayload map[string]any
	for _, event := range events {
		if event.Type == "execution_circuit_transition" && event.IdempotencyKey == "circuit:"+opened.ID+":generation:2" {
			if err = json.Unmarshal(event.Payload, &transitionPayload); err != nil {
				t.Fatal(err)
			}
		}
	}
	if transitionPayload["scope"] != "provider" || transitionPayload["to_state"] != "open" ||
		transitionPayload["source_failure_reason"] != "agent_provider_capacity_or_rate_limit" ||
		transitionPayload["config_fingerprint"] != providerCircuitFingerprint(t, target) {
		t.Fatalf("circuit transition payload=%+v", transitionPayload)
	}
	providerTarget, err := CircuitTargetForExecution(target, CircuitProvider)
	if err != nil {
		t.Fatal(err)
	}
	if _, claimed, claimErr := store.ClaimCircuitProbe(ctx, fixture.workspaceID, fixture.sessionID, providerTarget, uuid.NewString(), time.Minute); claimErr != nil || claimed {
		t.Fatalf("probe claimed before cooldown: claimed=%v err=%v", claimed, claimErr)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}

	type claimResult struct {
		lease   CircuitProbeLease
		claimed bool
		err     error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for i := 0; i < 2; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			lease, claimed, claimErr := store.ClaimCircuitProbe(ctx, fixture.workspaceID, fixture.sessionID, providerTarget, uuid.NewString(), time.Minute)
			results <- claimResult{lease: lease, claimed: claimed, err: claimErr}
		}()
	}
	wait.Wait()
	close(results)
	var winningLease CircuitProbeLease
	claims := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.claimed {
			claims++
			winningLease = result.lease
		}
	}
	if claims != 1 {
		t.Fatalf("concurrent probe claims=%d want=1", claims)
	}
	closed, transition, err := store.ResolveCircuitProbe(ctx, winningLease, true, FailureDisposition{}, "", "")
	if err != nil || closed.State != CircuitClosed || closed.ConsecutiveFailures != 0 || transition.Cause != "probe_succeeded" {
		t.Fatalf("resolved circuit=%+v transition=%+v err=%v", closed, transition, err)
	}

	for i := 2; i < 4; i++ {
		if _, _, err = store.RecordCircuitFailure(ctx, CircuitFailureInput{
			WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[i].ID,
			Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
		}); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, providerTarget)
	if err != nil || reopened.State != CircuitOpen {
		t.Fatalf("reopened circuit=%+v err=%v", reopened, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, reopened.ID); err != nil {
		t.Fatal(err)
	}
	oldLease, claimed, err := store.ClaimCircuitProbe(ctx, fixture.workspaceID, fixture.sessionID, providerTarget, uuid.NewString(), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("old probe claim=%v lease=%+v err=%v", claimed, oldLease, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET probe_lease_expires_at = now() - interval '1 second' WHERE id = $1::uuid`, reopened.ID); err != nil {
		t.Fatal(err)
	}
	newLease, claimed, err := store.ClaimCircuitProbe(ctx, fixture.workspaceID, fixture.sessionID, providerTarget, uuid.NewString(), time.Minute)
	if err != nil || !claimed || newLease.Generation <= oldLease.Generation {
		t.Fatalf("replacement probe claim=%v old=%+v new=%+v err=%v", claimed, oldLease, newLease, err)
	}
	if _, _, err = store.ResolveCircuitProbe(ctx, oldLease, true, FailureDisposition{}, "", ""); !errors.Is(err, ErrCircuitProbeLeaseLost) {
		t.Fatalf("expired probe resolved err=%v", err)
	}
	failed, failedTransition, err := store.ResolveCircuitProbe(ctx, newLease, false, disposition, "agent_provider_capacity_or_rate_limit", "probe still rate limited")
	if err != nil || failed.State != CircuitOpen || failed.NextProbeAt == nil || failedTransition.Cause != "probe_failed" {
		t.Fatalf("failed probe circuit=%+v transition=%+v err=%v", failed, failedTransition, err)
	}
}

func providerCircuitFingerprint(t *testing.T, target ExecutionTarget) string {
	t.Helper()
	providerTarget, err := CircuitTargetForExecution(target, CircuitProvider)
	if err != nil {
		t.Fatal(err)
	}
	return providerTarget.ConfigFingerprint
}

func TestExecutionCircuitConfigurationResetAndRunLeaseFence(t *testing.T) {
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
	defer cleanupResearchCircuitFixture(pool, fixture)
	store := NewPostgresStore(pool)
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 3)
	attempts := make([]Attempt, 0, 2)
	for _, task := range tasks[:2] {
		attempts = append(attempts, createCircuitAttempt(t, ctx, store, fixture, task, target))
	}

	disposition := failureDisposition(FailureConfiguration)
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[0].ID,
		Target: target, Disposition: disposition, SourceReason: "agent_missing_config",
	})
	if err != nil || opened.State != CircuitOpen {
		t.Fatalf("configuration circuit=%+v err=%v", opened, err)
	}
	mismatched := target
	mismatched.AgentConfigFingerprint = "not-the-frozen-fingerprint"
	if _, _, err = store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[1].ID,
		Target: mismatched, Disposition: disposition,
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("mismatched frozen target err=%v", err)
	}

	_, firstLease, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("first run lease=%+v claimed=%v err=%v", firstLease, claimed, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_session SET reconcile_lease_expires_at = now() - interval '1 second' WHERE id = $1::uuid`, fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, secondLease, claimed, err := store.ClaimRun(ctx, fixture.sessionID, uuid.NewString(), time.Minute)
	if err != nil || !claimed {
		t.Fatalf("replacement run lease=%+v claimed=%v err=%v", secondLease, claimed, err)
	}
	if _, _, err = store.RecordCircuitFailure(withRunLease(ctx, firstLease), CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempts[1].ID,
		Target: target, Disposition: disposition,
	}); !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("stale reconciler circuit mutation err=%v", err)
	}
	if err = store.ReleaseRun(ctx, secondLease, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}

	if _, err = pool.Exec(ctx, `UPDATE agent SET model = 'changed-model' WHERE id = $1::uuid`, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	members, err := store.ListFleetMembers(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	changedTarget := selectedExecutionTarget(fixture.agentID, members)
	if changedTarget.AgentConfigFingerprint == target.AgentConfigFingerprint {
		t.Fatal("agent configuration fingerprint did not change")
	}
	changedAttempt := createCircuitAttempt(t, ctx, store, fixture, tasks[2], changedTarget)
	reset, transition, changed, err := store.RecordCircuitSuccess(ctx, CircuitSuccessInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: changedAttempt.ID,
		Target: changedTarget, Scope: CircuitAgent,
	})
	if err != nil || !changed || reset.State != CircuitClosed || reset.ConsecutiveFailures != 0 || transition.Cause != "configuration_changed" {
		t.Fatalf("configuration reset circuit=%+v transition=%+v changed=%v err=%v", reset, transition, changed, err)
	}
}

func TestExecutionCircuitIsIsolatedByWorkspace(t *testing.T) {
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
	store := NewPostgresStore(pool)
	fixtures := []researchRunFixture{
		seedResearchRunFixture(t, ctx, pool),
		seedResearchRunFixture(t, ctx, pool),
	}
	defer cleanupResearchCircuitFixture(pool, fixtures[0])
	defer cleanupResearchCircuitFixture(pool, fixtures[1])
	circuits := make([]ExecutionCircuit, 0, 2)
	for _, fixture := range fixtures {
		target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 1)
		attempt := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
		circuit, _, recordErr := store.RecordCircuitFailure(ctx, CircuitFailureInput{
			WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempt.ID,
			Target: target, Disposition: failureDisposition(FailureInternal),
			SourceReason: "adapter_invariant",
		})
		if recordErr != nil || circuit.State != CircuitOpen {
			t.Fatalf("workspace=%s circuit=%+v err=%v", fixture.workspaceID, circuit, recordErr)
		}
		circuits = append(circuits, circuit)
	}
	if circuits[0].Target.Key != circuits[1].Target.Key || circuits[0].ID == circuits[1].ID || circuits[0].WorkspaceID == circuits[1].WorkspaceID {
		t.Fatalf("adapter circuit workspace isolation failed: first=%+v second=%+v", circuits[0], circuits[1])
	}
}

func initializeCircuitFixture(t *testing.T, ctx context.Context, store *PostgresStore, fixture researchRunFixture, taskCount int) (ExecutionTarget, []Task) {
	t.Helper()
	if _, _, err := store.InitializeRun(ctx, StartInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID, FleetID: fixture.fleetID,
		CreatedBy: fixture.userID, LeadAgentID: fixture.agentID, Goal: "Test execution circuit",
		Title: "Execution circuit", DepthTier: "standard", Language: "English",
	}, DefaultRunConfig("standard")); err != nil {
		t.Fatal(err)
	}
	members, err := store.ListFleetMembers(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	target := selectedExecutionTarget(fixture.agentID, members)
	tasks, err := store.ListTasks(ctx, fixture.sessionID)
	if err != nil || len(tasks) == 0 {
		t.Fatalf("initial tasks=%+v err=%v", tasks, err)
	}
	for len(tasks) < taskCount {
		task, _, createErr := store.CreateControlTask(ctx, ControlTaskInput{
			SessionID: fixture.sessionID, Kind: TaskKindDiscover,
			Objective: "circuit observation " + uuid.NewString(), Capability: "lead", Priority: 1,
		})
		if createErr != nil {
			t.Fatal(createErr)
		}
		tasks = append(tasks, task)
	}
	return target, tasks[:taskCount]
}

func createCircuitAttempt(t *testing.T, ctx context.Context, store *PostgresStore, fixture researchRunFixture, task Task, target ExecutionTarget) Attempt {
	t.Helper()
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := uuid.NewString()
	request := DispatchRequest{
		Run: run, Task: task, AttemptID: attemptID, AgentID: fixture.agentID,
		Target: target, Prompt: "test circuit", Key: "research-circuit:" + attemptID,
	}
	request.RequestHash, err = HashDispatchRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	attempt, _, err := store.CreateDispatchIntent(ctx, CreateDispatchIntentInput{
		AttemptID: attemptID, SessionID: fixture.sessionID, TaskID: task.ID,
		AgentID: fixture.agentID, Target: target, ExpectedStateVersion: run.StateVersion, Request: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func cleanupResearchCircuitFixture(pool *pgxpool.Pool, fixture researchRunFixture) {
	_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1::uuid`, fixture.workspaceID)
	_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, fixture.userID)
}
