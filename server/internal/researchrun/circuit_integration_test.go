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
	"github.com/jackc/pgx/v5/pgconn"
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

func TestExecutionTargetHealthBlocksOpenCircuitAndExposesDueProbe(t *testing.T) {
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
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)
	attempt := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: attempt.ID,
		Target: target, Disposition: failureDisposition(FailureCredential), SourceReason: "agent_provider_auth_or_access",
	})
	if err != nil || opened.State != CircuitOpen || opened.NextProbeAt == nil {
		t.Fatalf("opened=%+v err=%v", opened, err)
	}
	members, err := store.ListFleetMembers(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	health, err := store.EvaluateExecutionTargets(ctx, fixture.workspaceID, members)
	if err != nil {
		t.Fatal(err)
	}
	blocked := health[fixture.agentID]
	if blocked.Dispatchable || blocked.RetryAt == nil || len(blocked.Blocking) != 1 || blocked.Blocking[0].Scope != CircuitProvider {
		t.Fatalf("blocked health=%+v", blocked)
	}
	waitEvent, err := store.DeferTaskForExecutionTarget(ctx, fixture.sessionID, tasks[1].ID, blocked.RetryAt, []ExecutionTargetHealth{blocked})
	if err != nil {
		t.Fatal(err)
	}
	replayedWait, err := store.DeferTaskForExecutionTarget(ctx, fixture.sessionID, tasks[1].ID, blocked.RetryAt, []ExecutionTargetHealth{blocked})
	if err != nil || replayedWait.ID != waitEvent.ID {
		t.Fatalf("wait event=%+v replay=%+v err=%v", waitEvent, replayedWait, err)
	}
	deferredTask, err := store.GetTask(ctx, tasks[1].ID, fixture.sessionID)
	if err != nil || deferredTask.ReadyAt == nil || blocked.RetryAt == nil || deferredTask.ReadyAt.Sub(*blocked.RetryAt).Abs() > time.Millisecond {
		t.Fatalf("deferred task=%+v blocked=%+v err=%v", deferredTask, blocked, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}
	health, err = store.EvaluateExecutionTargets(ctx, fixture.workspaceID, members)
	if err != nil {
		t.Fatal(err)
	}
	due := health[fixture.agentID]
	if !due.Dispatchable || len(due.ProbeTargets) != 1 || due.ProbeTargets[0].Scope != CircuitProvider || len(due.Blocking) != 0 {
		t.Fatalf("due health=%+v", due)
	}
	members[0].ProviderBlockDetail = "{}"
	members[0].ProviderBlockedUntil = nil
	health, err = store.EvaluateExecutionTargets(ctx, fixture.workspaceID, members)
	if err != nil {
		t.Fatal(err)
	}
	placeholder := health[fixture.agentID]
	if !placeholder.Dispatchable || placeholder.BlockedReason != "" {
		t.Fatalf("blank JSON provider detail must not block routing: health=%+v", placeholder)
	}
	members[0].ProviderBlockDetail = "provider credentials require attention"
	members[0].ProviderBlockedUntil = nil
	health, err = store.EvaluateExecutionTargets(ctx, fixture.workspaceID, members)
	if err != nil {
		t.Fatal(err)
	}
	locked := health[fixture.agentID]
	if locked.Dispatchable || locked.BlockedReason != "provider_blocked" || len(locked.ProbeTargets) != 0 || locked.RetryAt != nil {
		t.Fatalf("provider-locked health=%+v", locked)
	}
}

func TestAttemptProbeBindingCommitsAtomicallyAndSuccessClosesCircuit(t *testing.T) {
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
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)
	sourceAttempt := createCircuitAttempt(t, ctx, store, fixture, tasks[1], target)
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: sourceAttempt.ID,
		Target: target, Disposition: failureDisposition(FailureCredential), SourceReason: "agent_provider_auth_or_access",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}
	providerTarget, err := CircuitTargetForExecution(target, CircuitProvider)
	if err != nil {
		t.Fatal(err)
	}
	probeAttempt := createCircuitProbeAttempt(t, ctx, store, fixture, tasks[0], target, []CircuitTarget{providerTarget})
	var bindingStatus string
	if err = pool.QueryRow(ctx, `
		SELECT status FROM research_attempt_circuit_probe
		WHERE attempt_id = $1::uuid AND circuit_id = $2::uuid
	`, probeAttempt.ID, opened.ID).Scan(&bindingStatus); err != nil || bindingStatus != "active" {
		t.Fatalf("binding status=%q err=%v", bindingStatus, err)
	}
	_, mutationErr := pool.Exec(ctx, `
		UPDATE research_attempt_circuit_probe SET generation = generation + 1
		WHERE attempt_id = $1::uuid
	`, probeAttempt.ID)
	var pgErr *pgconn.PgError
	if !errors.As(mutationErr, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "research_attempt_circuit_probe_identity_immutable_check" {
		t.Fatalf("mutable probe identity err=%v", mutationErr)
	}
	_, ownerErr := pool.Exec(ctx, `
		INSERT INTO research_attempt_circuit_probe (
		  workspace_id, session_id, attempt_id, circuit_id, scope,
		  probe_token, generation, config_fingerprint, lease_expires_at
		)
		SELECT workspace_id, session_id, attempt_id, circuit_id, scope,
		       gen_random_uuid(), generation, config_fingerprint, lease_expires_at
		FROM research_attempt_circuit_probe WHERE attempt_id = $1::uuid
	`, probeAttempt.ID)
	if !errors.As(ownerErr, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "research_attempt_circuit_probe_owner_check" {
		t.Fatalf("invalid probe owner err=%v", ownerErr)
	}
	inboxID := uuid.NewString()
	if _, err = pool.Exec(ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'draining')
	`, inboxID, fixture.workspaceID, fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.AttachInboxTask(ctx, probeAttempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	result := upgradeResultToV5(validV4PlanResult(t))
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	validated, hash, err := DecodeAndValidateResultForVersion(run.OrchestratorVersion, raw, tasks[0], run.Config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptResult(ctx, AcceptResultInput{
		SessionID: fixture.sessionID, AttemptID: probeAttempt.ID, AgentID: fixture.agentID,
		InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
	}); err != nil {
		t.Fatal(err)
	}
	closed, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, providerTarget)
	if err != nil || closed.State != CircuitClosed || closed.ConsecutiveFailures != 0 {
		t.Fatalf("closed circuit=%+v err=%v", closed, err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM research_attempt_circuit_probe WHERE attempt_id = $1::uuid`, probeAttempt.ID).Scan(&bindingStatus); err != nil || bindingStatus != "succeeded" {
		t.Fatalf("settled binding status=%q err=%v", bindingStatus, err)
	}
}

func TestAttemptProbeFailureIsScopedAndConcurrentClaimRollsBackAttempt(t *testing.T) {
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
	providerSource := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	providerCircuit, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: providerSource.ID,
		Target: target, Disposition: failureDisposition(FailureCredential), SourceReason: "agent_provider_auth_or_access",
	})
	if err != nil {
		t.Fatal(err)
	}
	agentSource := createCircuitAttempt(t, ctx, store, fixture, tasks[1], target)
	agentCircuit, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: agentSource.ID,
		Target: target, Disposition: failureDisposition(FailureConfiguration), SourceReason: "agent_missing_config",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `
		UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second'
		WHERE id = ANY($1::uuid[])
	`, []string{providerCircuit.ID, agentCircuit.ID}); err != nil {
		t.Fatal(err)
	}
	providerTarget, _ := CircuitTargetForExecution(target, CircuitProvider)
	agentTarget, _ := CircuitTargetForExecution(target, CircuitAgent)
	probes := []CircuitTarget{providerTarget, agentTarget}
	probeAttempt := createCircuitProbeAttempt(t, ctx, store, fixture, tasks[2], target, probes)

	failedAttemptID := uuid.NewString()
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	request := testDispatchRequestForTarget(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, tasks[3], fixture.agentID,
		target, failedAttemptID, "research-circuit:"+failedAttemptID,
	)
	_, _, err = store.CreateDispatchIntent(ctx, CreateDispatchIntentInput{
		AttemptID: failedAttemptID, SessionID: fixture.sessionID, TaskID: tasks[3].ID,
		AgentID: fixture.agentID, Target: target, ProbeTargets: probes, ProbeLeaseDuration: time.Hour,
		ExpectedStateVersion: run.StateVersion, Request: request,
	})
	if !errors.Is(err, ErrCircuitUnavailable) {
		t.Fatalf("second probe claim err=%v", err)
	}
	var attempts, outboxes int
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_task_attempt WHERE id = $1::uuid`, failedAttemptID).Scan(&attempts); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT count(*)::int FROM research_dispatch_outbox WHERE attempt_id = $1::uuid`, failedAttemptID).Scan(&outboxes); err != nil {
		t.Fatal(err)
	}
	if attempts != 0 || outboxes != 0 {
		t.Fatalf("rolled-back attempt=%d outbox=%d", attempts, outboxes)
	}

	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: probeAttempt.ID, FailureClass: string(FailureProvider),
		SourceReason: "agent_provider_server_error", Diagnostics: "probe provider failed", Retryable: true,
	}); err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	rows, err := pool.Query(ctx, `SELECT scope, status FROM research_attempt_circuit_probe WHERE attempt_id = $1::uuid`, probeAttempt.ID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var scope, status string
		if err = rows.Scan(&scope, &status); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		statuses[scope] = status
	}
	rows.Close()
	if statuses[string(CircuitProvider)] != "failed" || statuses[string(CircuitAgent)] != "inconclusive" {
		t.Fatalf("binding statuses=%v", statuses)
	}
	providerAfter, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, providerTarget)
	if err != nil {
		t.Fatal(err)
	}
	agentAfter, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, agentTarget)
	if err != nil {
		t.Fatal(err)
	}
	if providerAfter.ConsecutiveFailures != providerCircuit.ConsecutiveFailures+1 || agentAfter.ConsecutiveFailures != agentCircuit.ConsecutiveFailures ||
		providerAfter.State != CircuitOpen || agentAfter.State != CircuitOpen {
		t.Fatalf("provider=%+v agent=%+v", providerAfter, agentAfter)
	}
}

func TestRunPauseAbandonsOwnedAttemptProbeWithoutRecordingFailure(t *testing.T) {
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
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)
	sourceAttempt := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: sourceAttempt.ID,
		Target: target, Disposition: failureDisposition(FailureConfiguration), SourceReason: "agent_missing_config",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}
	agentTarget, _ := CircuitTargetForExecution(target, CircuitAgent)
	probeAttempt := createCircuitProbeAttempt(t, ctx, store, fixture, tasks[1], target, []CircuitTarget{agentTarget})
	if _, _, _, err = store.Pause(ctx, fixture.sessionID, fixture.workspaceID, fixture.userID); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = pool.QueryRow(ctx, `SELECT status FROM research_attempt_circuit_probe WHERE attempt_id = $1::uuid`, probeAttempt.ID).Scan(&status); err != nil || status != "abandoned" {
		t.Fatalf("binding status=%q err=%v", status, err)
	}
	after, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, agentTarget)
	if err != nil || after.State != CircuitOpen || after.ConsecutiveFailures != opened.ConsecutiveFailures || after.NextProbeAt == nil || after.NextProbeAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("abandoned circuit=%+v err=%v", after, err)
	}
}

func TestNodeReassignAbandonsOwnedAttemptProbeWithoutRecordingFailure(t *testing.T) {
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
	target, tasks := initializeCircuitFixture(t, ctx, store, fixture, 2)
	sourceAttempt := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: sourceAttempt.ID,
		Target: target, Disposition: failureDisposition(FailureConfiguration), SourceReason: "agent_missing_config",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}
	agentTarget, err := CircuitTargetForExecution(target, CircuitAgent)
	if err != nil {
		t.Fatal(err)
	}
	probeAttempt := createCircuitProbeAttempt(t, ctx, store, fixture, tasks[1], target, []CircuitTarget{agentTarget})
	outcome, err := store.NodeCommand(ctx, NodeCommandInput{
		SessionID: fixture.sessionID, WorkspaceID: fixture.workspaceID,
		NodeID: "task:" + tasks[1].ID, Action: NodeActionReassign,
		ClientRequestID: uuid.NewString(), ActorType: "user", ActorID: fixture.userID,
		TargetAgentID: fixture.reporterID, AnchorKind: "task", AnchorTaskID: tasks[1].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Reassign == nil || outcome.Reassign.ToAgentID != fixture.reporterID {
		t.Fatalf("reassign outcome=%+v", outcome)
	}
	var bindingStatus, attemptStatus, assignedAgentID string
	if err = pool.QueryRow(ctx, `
		SELECT binding.status, attempt.status, task.assigned_agent_id::text
		FROM research_attempt_circuit_probe binding
		JOIN research_task_attempt attempt ON attempt.id = binding.attempt_id
		JOIN research_task task ON task.id = attempt.task_id
		WHERE binding.attempt_id = $1::uuid
	`, probeAttempt.ID).Scan(&bindingStatus, &attemptStatus, &assignedAgentID); err != nil {
		t.Fatal(err)
	}
	if bindingStatus != "abandoned" || attemptStatus != string(AttemptStatusCancelled) || assignedAgentID != fixture.reporterID {
		t.Fatalf("binding=%q attempt=%q assigned=%q", bindingStatus, attemptStatus, assignedAgentID)
	}
	after, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, agentTarget)
	if err != nil || after.State != CircuitOpen || after.ConsecutiveFailures != opened.ConsecutiveFailures || after.NextProbeAt == nil || after.NextProbeAt.After(time.Now().UTC().Add(time.Second)) {
		t.Fatalf("reassigned circuit=%+v err=%v", after, err)
	}
}

func TestExpiredAttemptProbeCannotMutateSuccessorCircuit(t *testing.T) {
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
	sourceAttempt := createCircuitAttempt(t, ctx, store, fixture, tasks[0], target)
	opened, _, err := store.RecordCircuitFailure(ctx, CircuitFailureInput{
		WorkspaceID: fixture.workspaceID, SessionID: fixture.sessionID, AttemptID: sourceAttempt.ID,
		Target: target, Disposition: failureDisposition(FailureCredential), SourceReason: "agent_provider_auth_or_access",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}
	providerTarget, _ := CircuitTargetForExecution(target, CircuitProvider)
	oldAttempt := createCircuitProbeAttempt(t, ctx, store, fixture, tasks[1], target, []CircuitTarget{providerTarget})
	if _, err = pool.Exec(ctx, `UPDATE research_execution_circuit SET probe_lease_expires_at = now() - interval '1 second' WHERE id = $1::uuid`, opened.ID); err != nil {
		t.Fatal(err)
	}
	newAttempt := createCircuitProbeAttempt(t, ctx, store, fixture, tasks[2], target, []CircuitTarget{providerTarget})
	successor, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, providerTarget)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.FailAttempt(ctx, AttemptFailure{
		AttemptID: oldAttempt.ID, FailureClass: string(FailureProvider),
		SourceReason: "agent_provider_server_error", Diagnostics: "late stale failure", Retryable: true,
	}); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetExecutionCircuit(ctx, fixture.workspaceID, providerTarget)
	if err != nil {
		t.Fatal(err)
	}
	if after.State != CircuitHalfOpen || after.Generation != successor.Generation || after.ProbeToken != successor.ProbeToken || after.LastAttemptID == oldAttempt.ID {
		t.Fatalf("successor mutated before=%+v after=%+v", successor, after)
	}
	var oldStatus, newStatus, attemptStatus string
	if err = pool.QueryRow(ctx, `SELECT status FROM research_attempt_circuit_probe WHERE attempt_id = $1::uuid`, oldAttempt.ID).Scan(&oldStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM research_attempt_circuit_probe WHERE attempt_id = $1::uuid`, newAttempt.ID).Scan(&newStatus); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(ctx, `SELECT status FROM research_task_attempt WHERE id = $1::uuid`, oldAttempt.ID).Scan(&attemptStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus != "lost" || newStatus != "active" || attemptStatus != string(AttemptStatusFailed) {
		t.Fatalf("old binding=%q new binding=%q old attempt=%q", oldStatus, newStatus, attemptStatus)
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
	request := testDispatchRequestForTarget(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, task, fixture.agentID,
		target, attemptID, "research-circuit:"+attemptID,
	)
	attempt, _, err := store.CreateDispatchIntent(ctx, CreateDispatchIntentInput{
		AttemptID: attemptID, SessionID: fixture.sessionID, TaskID: task.ID,
		AgentID: fixture.agentID, Target: target, ExpectedStateVersion: run.StateVersion, Request: request,
	})
	if err != nil {
		t.Fatal(err)
	}
	return attempt
}

func createCircuitProbeAttempt(t *testing.T, ctx context.Context, store *PostgresStore, fixture researchRunFixture, task Task, target ExecutionTarget, probes []CircuitTarget) Attempt {
	t.Helper()
	run, err := store.GetRun(ctx, fixture.sessionID, fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	attemptID := uuid.NewString()
	request := testDispatchRequestForTarget(
		t, ctx, store, fixture.sessionID, fixture.workspaceID, task, fixture.agentID,
		target, attemptID, "research-circuit-probe:"+attemptID,
	)
	attempt, _, err := store.CreateDispatchIntent(ctx, CreateDispatchIntentInput{
		AttemptID: attemptID, SessionID: fixture.sessionID, TaskID: task.ID,
		AgentID: fixture.agentID, Target: target, ProbeTargets: probes, ProbeLeaseDuration: time.Hour,
		ExpectedStateVersion: run.StateVersion, Request: request,
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
