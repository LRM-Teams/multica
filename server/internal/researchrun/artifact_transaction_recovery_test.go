package researchrun

import (
	"encoding/json"
	"fmt"
	"testing"
)

type dispatchArtifactRecoveryCounts struct {
	manifests       int
	entries         int
	outboxes        int
	boundOutboxes   int
	passportEnabled bool
}

func loadDispatchArtifactRecoveryCounts(t *testing.T, run *transactionRecoveryRun, attemptID string) dispatchArtifactRecoveryCounts {
	t.Helper()
	var counts dispatchArtifactRecoveryCounts
	if err := run.pool.QueryRow(run.ctx, `
		SELECT artifact_passport_enabled
		FROM research_session
		WHERE id = $1::uuid AND workspace_id = $2::uuid
	`, run.fixture.sessionID, run.fixture.workspaceID).Scan(&counts.passportEnabled); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID).Scan(&counts.manifests); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_artifact_context_entry
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID).Scan(&counts.entries); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_dispatch_outbox
		WHERE task_id = $1::uuid
	`, run.taskID).Scan(&counts.outboxes); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_dispatch_outbox
		WHERE attempt_id = NULLIF($1, '')::uuid AND manifest_id IS NOT NULL AND manifest_hash <> ''
	`, attemptID).Scan(&counts.boundOutboxes); err != nil {
		t.Fatal(err)
	}
	return counts
}

func assertDispatchArtifactRecoveryRolledBack(t *testing.T, run *transactionRecoveryRun) {
	t.Helper()
	counts := loadDispatchArtifactRecoveryCounts(t, run, "")
	if !counts.passportEnabled {
		t.Fatal("expected artifact_passport_enabled for dispatch artifact recovery")
	}
	if counts.manifests != 0 || counts.entries != 0 || counts.outboxes != 0 {
		t.Fatalf("rolled-back dispatch artifact state=%+v", counts)
	}
}

func assertDispatchArtifactRecoveryCommitted(t *testing.T, run *transactionRecoveryRun, attemptID string) {
	t.Helper()
	counts := loadDispatchArtifactRecoveryCounts(t, run, attemptID)
	if !counts.passportEnabled {
		t.Fatal("expected artifact_passport_enabled for dispatch artifact recovery")
	}
	if counts.manifests != 1 {
		t.Fatalf("committed manifest count=%d want 1", counts.manifests)
	}
	if counts.entries == 0 {
		t.Fatal("committed dispatch must persist manifest entries")
	}
	if counts.outboxes != 1 || counts.boundOutboxes != 1 {
		t.Fatalf("committed outbox binding=%+v", counts)
	}
	var manifestAttemptID string
	if err := run.pool.QueryRow(run.ctx, `
		SELECT attempt_id::text
		FROM research_artifact_context_manifest
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID).Scan(&manifestAttemptID); err != nil {
		t.Fatal(err)
	}
	if manifestAttemptID != attemptID {
		t.Fatalf("manifest attempt=%q want=%q", manifestAttemptID, attemptID)
	}
}

func TestCreateDispatchIntentTransactionRecoveryCountsManifestArtifacts(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpDispatchIntentCreate, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		input := testDispatchIntentInput(
			t, run.ctx, run.store, run.fixture.sessionID, run.fixture.workspaceID, run.taskID, run.fixture.agentID,
		)
		committedAttemptID := input.AttemptID
		invoke := func() error {
			attempt, _, err := run.store.CreateDispatchIntent(run.ctx, input)
			if err == nil {
				committedAttemptID = attempt.ID
			}
			return err
		}
		recoverCommitted := func() error {
			attempt, _, err := run.store.CreateDispatchIntent(run.ctx, input)
			if err == nil {
				committedAttemptID = attempt.ID
			}
			return err
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				assertDispatchArtifactRecoveryRolledBack(t, run)
			},
			assertCommitted: func() {
				if committedAttemptID == "" {
					t.Fatal("missing committed attempt id")
				}
				assertDispatchArtifactRecoveryCommitted(t, run, committedAttemptID)
			},
			recover: recoverCommitted,
		}
	})
}

type resultArtifactRecoveryCounts struct {
	resultArtifacts int
	inputReferences int
	policyMutations int
	passportEnabled bool
}

func loadResultArtifactRecoveryCounts(t *testing.T, run *transactionRecoveryRun, attemptID string) resultArtifactRecoveryCounts {
	t.Helper()
	var counts resultArtifactRecoveryCounts
	if err := run.pool.QueryRow(run.ctx, `
		SELECT artifact_passport_enabled
		FROM research_session
		WHERE id = $1::uuid AND workspace_id = $2::uuid
	`, run.fixture.sessionID, run.fixture.workspaceID).Scan(&counts.passportEnabled); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_result_artifact
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid AND attempt_id = $3::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID, attemptID).Scan(&counts.resultArtifacts); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_artifact_input_reference
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID).Scan(&counts.inputReferences); err != nil {
		t.Fatal(err)
	}
	if err := run.pool.QueryRow(run.ctx, `
		SELECT count(*)::int FROM research_artifact_policy_mutation
		WHERE workspace_id = $1::uuid AND session_id = $2::uuid
	`, run.fixture.workspaceID, run.fixture.sessionID).Scan(&counts.policyMutations); err != nil {
		t.Fatal(err)
	}
	return counts
}

func TestAcceptResultTransactionRecoveryCountsResultArtifacts(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpResultAccept, func(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
		attempt, _ := mustCreateRecoveryDispatch(t, run)
		inboxID := mustCreateRecoveryInbox(t, run)
		if _, _, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID); err != nil {
			t.Fatal(err)
		}
		current, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
		if err != nil {
			t.Fatal(err)
		}
		tasks, err := run.store.ListTasks(run.ctx, run.fixture.sessionID)
		if err != nil || len(tasks) != 1 {
			t.Fatalf("tasks=%+v err=%v", tasks, err)
		}
		result := upgradeResultToV5(validV4PlanResult(t))
		result.ClientRequestID = "tx-result-artifact-" + attempt.ID
		raw, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		validated, hash, err := DecodeAndValidateResultForVersion(current.OrchestratorVersion, raw, tasks[0], current.Config)
		if err != nil {
			t.Fatal(err)
		}
		input := AcceptResultInput{
			SessionID: run.fixture.sessionID, AttemptID: attempt.ID, AgentID: run.fixture.agentID,
			InboxTaskID: inboxID, Raw: raw, Result: validated, Hash: hash,
		}
		baseline := loadResultArtifactRecoveryCounts(t, run, attempt.ID)
		invoke := func() error {
			_, invokeErr := run.store.AcceptResult(run.ctx, input)
			return invokeErr
		}
		assertArtifactState := func(committed bool) {
			t.Helper()
			counts := loadResultArtifactRecoveryCounts(t, run, attempt.ID)
			if !counts.passportEnabled {
				t.Fatal("expected artifact_passport_enabled for result artifact recovery")
			}
			if !committed {
				if counts.resultArtifacts != 0 {
					t.Fatalf("rolled-back result artifacts=%d", counts.resultArtifacts)
				}
				if counts.inputReferences != baseline.inputReferences {
					t.Fatalf("rolled-back input references=%d baseline=%d", counts.inputReferences, baseline.inputReferences)
				}
				if counts.policyMutations != baseline.policyMutations {
					t.Fatalf("rolled-back policy mutations=%d baseline=%d", counts.policyMutations, baseline.policyMutations)
				}
				return
			}
			if counts.resultArtifacts != 1 {
				t.Fatalf("committed result artifacts=%d want 1", counts.resultArtifacts)
			}
			if counts.inputReferences <= baseline.inputReferences {
				t.Fatal("committed result acceptance must persist input references")
			}
			if counts.policyMutations <= baseline.policyMutations {
				t.Fatalf("committed policy mutations=%d baseline=%d", counts.policyMutations, baseline.policyMutations)
			}
		}
		return transactionRecoveryOperation{
			invoke: invoke,
			assertRolledBack: func() {
				assertArtifactState(false)
			},
			assertCommitted: func() {
				assertArtifactState(true)
			},
			recover: func() error {
				replayed, replayErr := run.store.AcceptResult(run.ctx, input)
				if replayErr == nil && !replayed.Replayed {
					return fmt.Errorf("result replay=%+v, want replayed", replayed)
				}
				return replayErr
			},
		}
	})
}
