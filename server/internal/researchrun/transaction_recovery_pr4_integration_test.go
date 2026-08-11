package researchrun

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRunSteerTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunSteer, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		baselineRun, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		input := SteerInput{
			SessionID: run.fixture.sessionID, WorkspaceID: run.fixture.workspaceID, UserID: run.fixture.userID,
			Goal: "steered goal for transaction recovery", Reason: "recovery test",
		}
		invoke := func() error {
			_, _, _, err := run.store.Steer(run.ctx, input)
			return err
		}
		recoverSteer := func() error {
			runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
			if err != nil {
				return err
			}
			if runRow.Goal == input.Goal && runRow.GoalVersion > run.goalVersion {
				return nil
			}
			_, _, _, err = run.store.Steer(run.ctx, input)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.GoalVersion != baselineRun.GoalVersion || runRow.Goal != baselineRun.Goal {
					t.Fatalf("rolled back steer run=%+v", runRow)
				}
			},
			assertCommitted: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.GoalVersion <= run.goalVersion || runRow.Goal != input.Goal {
					t.Fatalf("committed steer run=%+v", runRow)
				}
				if count := countSessionEvents(t, run, "goal_steered"); count != 1 {
					t.Fatalf("steer events=%d", count)
				}
			},
			recover: recoverSteer,
		}
	})
}

func TestRunTransitionPauseTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunTransition, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		invoke := func() error {
			_, _, _, err := run.store.Pause(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusRunning {
					t.Fatalf("rolled back status=%q", runRow.Status)
				}
			},
			assertCommitted: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusPaused {
					t.Fatalf("committed status=%q", runRow.Status)
				}
			},
			recover: invoke,
		}
	})
}

func TestInitializeRunTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunInitialize, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		invoke := func() error {
			_, _, err := run.store.InitializeRun(run.ctx, StartInput{
				SessionID: run.fixture.sessionID, WorkspaceID: run.fixture.workspaceID, FleetID: run.fixture.fleetID,
				CreatedBy: run.fixture.userID, LeadAgentID: run.fixture.agentID,
				Goal: "duplicate initialize", Title: "duplicate initialize", DepthTier: "standard", Language: "English",
			}, DefaultRunConfig("standard"))
			return err
		}
		return transactionRecoveryOperation{
			invoke:           invoke,
			assertRolledBack: func() {},
			assertCommitted:  func() {},
			recover:          invoke,
		}
	})
}

func TestRenewRunLeaseTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpReconcileLeaseRenew, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		token := uuid.NewString()
		_, lease, claimed, err := run.store.ClaimRun(run.ctx, run.fixture.sessionID, token, time.Minute)
		if err != nil || !claimed {
			t.Fatalf("claim run lease: claimed=%v err=%v", claimed, err)
		}
		var baselineExpiry time.Time
		if err = run.pool.QueryRow(run.ctx, `
			SELECT reconcile_lease_expires_at FROM research_session WHERE id = $1::uuid
		`, run.fixture.sessionID).Scan(&baselineExpiry); err != nil {
			t.Fatal(err)
		}
		invoke := func() error {
			_, err := run.store.RenewRunLease(run.ctx, lease, 2*time.Minute)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var expiresAt time.Time
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reconcile_lease_expires_at FROM research_session WHERE id = $1::uuid
				`, run.fixture.sessionID).Scan(&expiresAt); err != nil {
					t.Fatal(err)
				}
				if expiresAt.After(baselineExpiry.Add(30 * time.Second)) {
					t.Fatalf("lease renewed after rollback: baseline=%v current=%v", baselineExpiry, expiresAt)
				}
			},
			assertCommitted: func() {
				var expiresAt time.Time
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reconcile_lease_expires_at FROM research_session WHERE id = $1::uuid
				`, run.fixture.sessionID).Scan(&expiresAt); err != nil {
					t.Fatal(err)
				}
				if !expiresAt.After(baselineExpiry.Add(30 * time.Second)) {
					t.Fatalf("lease not renewed: baseline=%v current=%v", baselineExpiry, expiresAt)
				}
			},
			recover: invoke,
		}
	})
}

func TestReleaseRunTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpReconcileLeaseRelease, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		token := uuid.NewString()
		_, lease, claimed, err := run.store.ClaimRun(run.ctx, run.fixture.sessionID, token, time.Minute)
		if err != nil || !claimed {
			t.Fatalf("claim run lease: claimed=%v err=%v", claimed, err)
		}
		next := time.Now().UTC().Add(5 * time.Second)
		invoke := func() error {
			return run.store.ReleaseRun(run.ctx, lease, next)
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var leaseToken *string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reconcile_lease_token::text FROM research_session WHERE id = $1::uuid
				`, run.fixture.sessionID).Scan(&leaseToken); err != nil {
					t.Fatal(err)
				}
				if leaseToken == nil {
					t.Fatal("lease released after rollback")
				}
			},
			assertCommitted: func() {
				var leaseToken *string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT reconcile_lease_token::text FROM research_session WHERE id = $1::uuid
				`, run.fixture.sessionID).Scan(&leaseToken); err != nil {
					t.Fatal(err)
				}
				if leaseToken != nil {
					t.Fatalf("lease still held after release: %v", *leaseToken)
				}
			},
			recover: func() error {
				err := run.store.ReleaseRun(run.ctx, lease, next)
				if errors.Is(err, ErrRunLeaseLost) {
					return nil
				}
				return err
			},
		}
	})
}

func TestMarkEventProjectionFailedTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpProjectionRetry, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		events, err := run.store.ListRunEvents(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, 0, 10)
		if err != nil || len(events) == 0 {
			t.Fatalf("events=%+v err=%v", events, err)
		}
		eventID := events[0].ID
		next := time.Now().UTC().Add(time.Minute)
		invoke := func() error {
			return run.store.MarkEventProjectionFailed(run.ctx, eventID, "projection failed", next)
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var attempts int
				if err := run.pool.QueryRow(run.ctx, `
					SELECT projection_attempts FROM research_run_event WHERE id = $1::uuid
				`, eventID).Scan(&attempts); err != nil {
					t.Fatal(err)
				}
				if attempts != 0 {
					t.Fatalf("projection attempts=%d after rollback", attempts)
				}
			},
			assertCommitted: func() {
				var attempts int
				var projectionError string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT projection_attempts, projection_error FROM research_run_event WHERE id = $1::uuid
				`, eventID).Scan(&attempts, &projectionError); err != nil {
					t.Fatal(err)
				}
				if attempts != 1 || projectionError == "" {
					t.Fatalf("projection retry state attempts=%d error=%q", attempts, projectionError)
				}
			},
			recover: func() error {
				var attempts int
				if err := run.pool.QueryRow(run.ctx, `
					SELECT projection_attempts FROM research_run_event WHERE id = $1::uuid
				`, eventID).Scan(&attempts); err != nil {
					return err
				}
				if attempts > 0 {
					return nil
				}
				return invoke()
			},
		}
	})
}

func TestDeferTaskForExecutionTargetTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpExecutionTargetDefer, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		retryAt := time.Now().UTC().Add(10 * time.Minute)
		health := []ExecutionTargetHealth{{
			AgentID: run.fixture.agentID, Dispatchable: false, RetryAt: &retryAt,
			Blocking: []CircuitBlock{{Scope: CircuitProvider, State: CircuitOpen}},
		}}
		invoke := func() error {
			_, err := run.store.DeferTaskForExecutionTarget(run.ctx, run.fixture.sessionID, run.taskID, &retryAt, health)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if count := countSessionEvents(t, run, "task_waiting_for_execution_target"); count != 0 {
					t.Fatalf("defer events=%d after rollback", count)
				}
			},
			assertCommitted: func() {
				if count := countSessionEvents(t, run, "task_waiting_for_execution_target"); count != 1 {
					t.Fatalf("defer events=%d after commit", count)
				}
			},
			recover: invoke,
		}
	})
}

func TestRecordCircuitFailureTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpCircuitFailure, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		target, tasks := initializeCircuitFixture(t, run.ctx, run.store, run.fixture, 1)
		attempt := createCircuitAttempt(t, run.ctx, run.store, run.fixture, tasks[0], target)
		input := CircuitFailureInput{
			WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, AttemptID: attempt.ID,
			Target: target, Disposition: failureDisposition(FailureRateLimited),
			SourceReason: "agent_provider_capacity_or_rate_limit", Diagnostics: "recovery test",
		}
		var transitionCount int
		invoke := func() error {
			_, transitions, err := run.store.RecordCircuitFailure(run.ctx, input)
			if err == nil {
				transitionCount = len(transitions)
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var count int
				if err := run.pool.QueryRow(run.ctx, `
					SELECT count(*)::int FROM research_execution_circuit_transition
					WHERE session_id = $1::uuid
				`, run.fixture.sessionID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("circuit transitions=%d after rollback", count)
				}
			},
			assertCommitted: func() {
				var count int
				if err := run.pool.QueryRow(run.ctx, `
					SELECT count(*)::int FROM research_execution_circuit_transition
					WHERE session_id = $1::uuid
				`, run.fixture.sessionID).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count == 0 {
					t.Fatalf("circuit transitions=%d want non-zero", count)
				}
				if transitionCount == 0 {
					transitionCount = count
				}
				if count != transitionCount {
					t.Fatalf("circuit transitions=%d want %d", count, transitionCount)
				}
			},
			recover: func() error {
				_, _, err := run.store.RecordCircuitFailure(run.ctx, input)
				return err
			},
		}
	})
}

func mustSetupRecoveryAcknowledgedAttempt(t *testing.T, run *transactionRecoveryRun) (attemptID, inboxID, dispatchKey string) {
	t.Helper()
	input := testDispatchIntentInput(t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID, run.taskID, run.fixture.agentID)
	attempt, _, err := run.store.CreateDispatchIntent(run.ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	claimed, err := run.store.ClaimDispatchIntents(run.ctx, run.fixture.sessionID, token, time.Minute, 1)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	inboxID = uuid.NewString()
	if _, err = run.pool.Exec(run.ctx, `
		INSERT INTO agent_inbox_event (id, workspace_id, agent_id, reason, status)
		VALUES ($1::uuid, $2::uuid, $3::uuid, 'dm', 'pending')
	`, inboxID, run.fixture.workspaceID, run.fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if err = run.pool.QueryRow(run.ctx, `
		SELECT dispatch_key FROM research_task_attempt WHERE id = $1::uuid
	`, attempt.ID).Scan(&dispatchKey); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err = run.store.AcknowledgeDispatchIntent(run.ctx, claimed[0].ID, token, inboxID); err != nil {
		t.Fatal(err)
	}
	return attempt.ID, inboxID, dispatchKey
}

func TestCreateRunTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunCreate, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		title := "create run recovery " + uuid.NewString()
		var baselineCount int
		if err := run.pool.QueryRow(run.ctx, `
			SELECT count(*)::int FROM research_session WHERE workspace_id = $1::uuid
		`, run.fixture.workspaceID).Scan(&baselineCount); err != nil {
			t.Fatal(err)
		}
		input := StartInput{
			WorkspaceID: run.fixture.workspaceID, FleetID: run.fixture.fleetID,
			CreatedBy: run.fixture.userID, LeadAgentID: run.fixture.agentID,
			Goal: title, Title: title, DepthTier: "standard", Language: "English",
		}
		invoke := func() error {
			_, _, err := run.store.CreateRun(run.ctx, input, DefaultRunConfig("standard"))
			return err
		}
		countSessions := func() int {
			var count int
			if err := run.pool.QueryRow(run.ctx, `
				SELECT count(*)::int FROM research_session WHERE workspace_id = $1::uuid
			`, run.fixture.workspaceID).Scan(&count); err != nil {
				t.Fatal(err)
			}
			return count
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if countSessions() != baselineCount {
					t.Fatalf("session count=%d want baseline %d", countSessions(), baselineCount)
				}
			},
			assertCommitted: func() {
				if countSessions() != baselineCount+1 {
					t.Fatalf("session count=%d want %d", countSessions(), baselineCount+1)
				}
			},
			recover: func() error {
				if countSessions() >= baselineCount+1 {
					return nil
				}
				return invoke()
			},
		}
	})
}

func TestSetAwaitingConfirmationTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunAwaitConfirmation, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		gate := GateResult{Passed: true}
		invoke := func() error {
			_, _, err := run.store.SetAwaitingConfirmation(run.ctx, run.fixture.sessionID, gate)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusRunning {
					t.Fatalf("rolled back status=%q", runRow.Status)
				}
			},
			assertCommitted: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusAwaitingUserConfirm {
					t.Fatalf("committed status=%q", runRow.Status)
				}
				if count := countSessionEvents(t, run, "run_awaiting_confirmation"); count != 1 {
					t.Fatalf("awaiting events=%d", count)
				}
			},
			recover: invoke,
		}
	})
}

func TestCompleteRunTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpRunComplete, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		if _, _, err := run.store.SetAwaitingConfirmation(run.ctx, run.fixture.sessionID, GateResult{Passed: true}); err != nil {
			t.Fatal(err)
		}
		invoke := func() error {
			_, _, err := run.store.Complete(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusAwaitingUserConfirm {
					t.Fatalf("rolled back status=%q", runRow.Status)
				}
			},
			assertCommitted: func() {
				runRow, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
				if err != nil {
					t.Fatal(err)
				}
				if runRow.Status != RunStatusCompleted {
					t.Fatalf("committed status=%q", runRow.Status)
				}
				if count := countSessionEvents(t, run, "run_completed"); count != 1 {
					t.Fatalf("completed events=%d", count)
				}
			},
			recover: invoke,
		}
	})
}

func TestRecordCircuitSuccessTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpCircuitSuccess, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		target, tasks := initializeCircuitFixture(t, run.ctx, run.store, run.fixture, 1)
		attempt := createCircuitAttempt(t, run.ctx, run.store, run.fixture, tasks[0], target)
		disposition := failureDisposition(FailureRateLimited)
		if _, _, err := run.store.RecordCircuitFailure(run.ctx, CircuitFailureInput{
			WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, AttemptID: attempt.ID,
			Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
		}); err != nil {
			t.Fatal(err)
		}
		input := CircuitSuccessInput{
			WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, AttemptID: attempt.ID,
			Target: target, Scope: CircuitProvider,
		}
		invoke := func() error {
			_, _, _, err := run.store.RecordCircuitSuccess(run.ctx, input)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var consecutive int
				if err := run.pool.QueryRow(run.ctx, `
					SELECT consecutive_failures FROM research_execution_circuit
					WHERE last_session_id = $1::uuid LIMIT 1
				`, run.fixture.sessionID).Scan(&consecutive); err != nil {
					t.Fatal(err)
				}
				if consecutive != 1 {
					t.Fatalf("consecutive failures=%d after rollback", consecutive)
				}
			},
			assertCommitted: func() {
				var consecutive int
				if err := run.pool.QueryRow(run.ctx, `
					SELECT consecutive_failures FROM research_execution_circuit
					WHERE last_session_id = $1::uuid LIMIT 1
				`, run.fixture.sessionID).Scan(&consecutive); err != nil {
					t.Fatal(err)
				}
				if consecutive != 0 {
					t.Fatalf("consecutive failures=%d after success", consecutive)
				}
			},
			recover: invoke,
		}
	})
}

func TestClaimCircuitProbeTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpCircuitProbeClaim, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		target, tasks := initializeCircuitFixture(t, run.ctx, run.store, run.fixture, 2)
		disposition := failureDisposition(FailureRateLimited)
		for i := 0; i < 2; i++ {
			attempt := createCircuitAttempt(t, run.ctx, run.store, run.fixture, tasks[i], target)
			if _, _, err := run.store.RecordCircuitFailure(run.ctx, CircuitFailureInput{
				WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, AttemptID: attempt.ID,
				Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
			}); err != nil {
				t.Fatal(err)
			}
		}
		providerTarget, err := CircuitTargetForExecution(target, CircuitProvider)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = run.pool.Exec(run.ctx, `
			UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second'
			WHERE last_session_id = $1::uuid AND scope = 'provider'
		`, run.fixture.sessionID); err != nil {
			t.Fatal(err)
		}
		token := uuid.NewString()
		invoke := func() error {
			_, claimed, err := run.store.ClaimCircuitProbe(
				run.ctx, run.fixture.workspaceID, run.fixture.sessionID, providerTarget, token, time.Minute,
			)
			if err == nil && !claimed {
				return fmt.Errorf("expected probe claim")
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var probeToken *string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT probe_token::text FROM research_execution_circuit
					WHERE last_session_id = $1::uuid AND scope = 'provider'
				`, run.fixture.sessionID).Scan(&probeToken); err != nil {
					t.Fatal(err)
				}
				if probeToken != nil {
					t.Fatalf("probe leased after rollback: %v", *probeToken)
				}
			},
			assertCommitted: func() {
				var probeToken string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT probe_token::text FROM research_execution_circuit
					WHERE last_session_id = $1::uuid AND scope = 'provider'
				`, run.fixture.sessionID).Scan(&probeToken); err != nil {
					t.Fatal(err)
				}
				if probeToken != token {
					t.Fatalf("probe token=%q want %q", probeToken, token)
				}
			},
			recover: func() error {
				var probeToken *string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT probe_token::text FROM research_execution_circuit
					WHERE last_session_id = $1::uuid AND scope = 'provider'
				`, run.fixture.sessionID).Scan(&probeToken); err != nil {
					return err
				}
				if probeToken != nil && *probeToken == token {
					return nil
				}
				return invoke()
			},
		}
	})
}

func TestResolveCircuitProbeTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpCircuitProbeResolve, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		target, tasks := initializeCircuitFixture(t, run.ctx, run.store, run.fixture, 2)
		disposition := failureDisposition(FailureRateLimited)
		for i := 0; i < 2; i++ {
			attempt := createCircuitAttempt(t, run.ctx, run.store, run.fixture, tasks[i], target)
			if _, _, err := run.store.RecordCircuitFailure(run.ctx, CircuitFailureInput{
				WorkspaceID: run.fixture.workspaceID, SessionID: run.fixture.sessionID, AttemptID: attempt.ID,
				Target: target, Disposition: disposition, SourceReason: "agent_provider_capacity_or_rate_limit",
			}); err != nil {
				t.Fatal(err)
			}
		}
		providerTarget, err := CircuitTargetForExecution(target, CircuitProvider)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = run.pool.Exec(run.ctx, `
			UPDATE research_execution_circuit SET next_probe_at = now() - interval '1 second'
			WHERE last_session_id = $1::uuid AND scope = 'provider'
		`, run.fixture.sessionID); err != nil {
			t.Fatal(err)
		}
		token := uuid.NewString()
		lease, claimed, err := run.store.ClaimCircuitProbe(
			run.ctx, run.fixture.workspaceID, run.fixture.sessionID, providerTarget, token, time.Minute,
		)
		if err != nil || !claimed {
			t.Fatalf("claim probe: lease=%+v claimed=%v err=%v", lease, claimed, err)
		}
		invoke := func() error {
			_, _, err := run.store.ResolveCircuitProbe(run.ctx, lease, true, FailureDisposition{}, "", "")
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				var state string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT state FROM research_execution_circuit
					WHERE last_session_id = $1::uuid AND scope = 'provider'
				`, run.fixture.sessionID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if state != string(CircuitHalfOpen) {
					t.Fatalf("circuit state=%q after rollback", state)
				}
			},
			assertCommitted: func() {
				var state string
				if err := run.pool.QueryRow(run.ctx, `
					SELECT state FROM research_execution_circuit
					WHERE last_session_id = $1::uuid AND scope = 'provider'
				`, run.fixture.sessionID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if state != string(CircuitClosed) {
					t.Fatalf("circuit state=%q after resolve", state)
				}
			},
			recover: func() error {
				err := invoke()
				if errors.Is(err, ErrCircuitProbeLeaseLost) {
					return nil
				}
				return err
			},
		}
	})
}

func TestReconcileAttemptRuntimeTransactionRecovery(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpAttemptRuntimeReconcile, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attemptID, inboxID, dispatchKey := mustSetupRecoveryAcknowledgedAttempt(t, run)
		startedAt := time.Now().UTC().Add(time.Second)
		observedAt := startedAt.Add(time.Second)
		leaseUntil := observedAt.Add(time.Minute)
		state := InboxTaskState{
			ID: inboxID, Status: "running", StartedAt: &startedAt,
			ObservedAt: observedAt, LeaseExpiresAt: &leaseUntil, HasActiveLease: true,
		}
		observed := activeAttempt{id: attemptID, inboxID: inboxID, dispatchKey: dispatchKey}
		invoke := func() error {
			_, err := run.store.reconcileAttemptRuntime(run.ctx, observed, state, true)
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				if count := countAttemptEvents(t, run, attemptID, "task_started"); count != 0 {
					t.Fatalf("task_started events=%d after rollback", count)
				}
			},
			assertCommitted: func() {
				if count := countAttemptEvents(t, run, attemptID, "task_started"); count != 1 {
					t.Fatalf("task_started events=%d after commit", count)
				}
			},
			recover: invoke,
		}
	})
}
