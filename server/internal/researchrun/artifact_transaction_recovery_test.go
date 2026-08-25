package researchrun

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
)

type dispatchArtifactRecoveryCounts struct {
	attempts        int
	passports       int
	versions        int
	manifests       int
	entries         int
	omissions       int
	grants          int
	grantMutations  int
	inputReferences int
	outboxes        int
	boundOutboxes   int
	events          int
	taskStatus      string
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
		SELECT
		  (SELECT count(*)::int FROM research_task_attempt WHERE task_id=$1::uuid),
		  (SELECT count(*)::int FROM research_artifact_passport
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid
		     AND entity_kind IN ('attempt','context_manifest')),
		  (SELECT count(*)::int FROM research_artifact_version v
		   JOIN research_artifact_passport p
		     ON (p.workspace_id,p.session_id,p.id)=(v.workspace_id,v.session_id,v.artifact_id)
		   WHERE p.workspace_id=$2::uuid AND p.session_id=$3::uuid
		     AND p.entity_kind IN ('attempt','context_manifest')),
		  (SELECT count(*)::int FROM research_artifact_context_manifest
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_entry
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_context_omission
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_grant
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid),
		  (SELECT count(*)::int FROM research_artifact_policy_mutation
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND policy_grant_id IS NOT NULL),
		  (SELECT count(*)::int FROM research_artifact_input_reference
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND manifest_id IS NOT NULL),
		  (SELECT count(*)::int FROM research_dispatch_outbox WHERE task_id=$1::uuid),
		  (SELECT count(*)::int FROM research_dispatch_outbox
		   WHERE attempt_id=NULLIF($4,'')::uuid AND manifest_id IS NOT NULL AND manifest_hash<>''),
		  (SELECT count(*)::int FROM research_run_event
		   WHERE workspace_id=$2::uuid AND session_id=$3::uuid AND event_type='task_dispatching'),
		  (SELECT status FROM research_task WHERE id=$1::uuid)
	`, run.taskID, run.fixture.workspaceID, run.fixture.sessionID, attemptID).Scan(
		&counts.attempts, &counts.passports, &counts.versions,
		&counts.manifests, &counts.entries, &counts.omissions,
		&counts.grants, &counts.grantMutations, &counts.inputReferences,
		&counts.outboxes, &counts.boundOutboxes, &counts.events, &counts.taskStatus,
	); err != nil {
		t.Fatal(err)
	}
	return counts
}

func assertDispatchArtifactRecoveryRolledBack(t *testing.T, run *transactionRecoveryRun) {
	t.Helper()
	counts := loadDispatchArtifactRecoveryCounts(t, run, "00000000-0000-0000-0000-000000000000")
	if !counts.passportEnabled {
		t.Fatal("expected artifact_passport_enabled for dispatch artifact recovery")
	}
	if counts.attempts != 0 || counts.passports != 0 || counts.versions != 0 ||
		counts.manifests != 0 || counts.entries != 0 || counts.omissions != 0 ||
		counts.grants != 0 || counts.grantMutations != 0 || counts.inputReferences != 0 ||
		counts.outboxes != 0 || counts.boundOutboxes != 0 || counts.events != 0 ||
		(counts.taskStatus != string(TaskStatusReady) && counts.taskStatus != string(TaskStatusPending)) {
		t.Fatalf("rolled-back dispatch artifact state=%+v", counts)
	}
}

func assertDispatchArtifactRecoveryCommitted(t *testing.T, run *transactionRecoveryRun, attemptID string) {
	t.Helper()
	counts := loadDispatchArtifactRecoveryCounts(t, run, attemptID)
	if !counts.passportEnabled {
		t.Fatal("expected artifact_passport_enabled for dispatch artifact recovery")
	}
	if counts.attempts != 1 || counts.passports != 2 || counts.versions != 2 ||
		counts.manifests != 1 || counts.entries == 0 ||
		counts.grants != 1 || counts.grantMutations != 1 ||
		counts.inputReferences != counts.entries || counts.outboxes != 1 ||
		counts.boundOutboxes != 1 || counts.events != 1 ||
		counts.taskStatus != string(TaskStatusDispatching) {
		t.Fatalf("committed dispatch artifact state=%+v", counts)
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

func dispatchArtifactRecoveryOperation(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
	t.Helper()
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
}

func TestCreateDispatchIntentTransactionRecoveryCountsManifestArtifacts(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpDispatchIntentCreate, dispatchArtifactRecoveryOperation)
}

func TestCreateDispatchIntentAfterBeginRecoveryCountsManifestArtifacts(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Recover dispatch_intent.create after_begin")
	row := dispatchArtifactRecoveryOperation(t, run)
	injected := errors.New("injected dispatch_intent.create after_begin")
	fault := &oneShotResearchTxFault{
		operation: txOpDispatchIntentCreate,
		point:     txAfterBegin,
		err:       injected,
	}
	run.store.txFaultHook = fault.hook
	if err := row.invoke(); !errors.Is(err, injected) {
		t.Fatalf("injected call error=%v", err)
	}
	if !fault.fired {
		t.Fatal("after_begin fault did not fire")
	}
	row.assertRolledBack()
	if err := row.invoke(); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	row.assertCommitted()
}

type resultArtifactRecoveryCounts struct {
	resultArtifacts int
	resultPassports int
	resultVersions  int
	inputReferences int
	policyMutations int
	methodDecisions int
	questions       int
	tasks           int
	acceptedEvents  int
	attemptStatus   string
	taskStatus      string
	resultHash      string
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
		SELECT
		  (SELECT count(*)::int FROM research_artifact_passport passport
		   JOIN research_artifact_version version
		     ON (version.workspace_id, version.session_id, version.artifact_id, version.version) =
		        (passport.workspace_id, passport.session_id, passport.id, passport.current_version)
		   WHERE passport.workspace_id = $1::uuid AND passport.session_id = $2::uuid
		     AND passport.entity_kind = 'result_artifact' AND version.produced_by_attempt_id = $3::uuid),
		  (SELECT count(*)::int FROM research_artifact_version version
		   JOIN research_artifact_passport passport
		     ON (passport.workspace_id, passport.session_id, passport.id) =
		        (version.workspace_id, version.session_id, version.artifact_id)
		   WHERE passport.workspace_id = $1::uuid AND passport.session_id = $2::uuid
		     AND passport.entity_kind = 'result_artifact' AND version.produced_by_attempt_id = $3::uuid)
	`, run.fixture.workspaceID, run.fixture.sessionID, attemptID).Scan(
		&counts.resultPassports, &counts.resultVersions,
	); err != nil {
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
	if err := run.pool.QueryRow(run.ctx, `
		SELECT
		  (SELECT count(*)::int FROM research_decision
		   WHERE session_id = $1::uuid AND decision_kind = 'research_method'),
		  (SELECT count(*)::int FROM research_question WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_task WHERE session_id = $1::uuid),
		  (SELECT count(*)::int FROM research_run_event
		   WHERE session_id = $1::uuid AND event_type = 'task_result_accepted'),
		  attempt.status, task.status, COALESCE(attempt.result_hash, '')
		FROM research_task_attempt attempt
		JOIN research_task task ON task.id = attempt.task_id
		WHERE attempt.id = $2::uuid
	`, run.fixture.sessionID, attemptID).Scan(
		&counts.methodDecisions, &counts.questions, &counts.tasks, &counts.acceptedEvents,
		&counts.attemptStatus, &counts.taskStatus, &counts.resultHash,
	); err != nil {
		t.Fatal(err)
	}
	return counts
}

func resultArtifactRecoveryOperation(t *testing.T, run *transactionRecoveryRun) transactionRecoveryOperation {
	t.Helper()
	attempt, _ := mustCreateRecoveryDispatch(t, run)
	inboxID := mustCreateRecoveryInbox(t, run)
	if _, _, err := run.store.AttachInboxTask(run.ctx, attempt.ID, inboxID); err != nil {
		t.Fatal(err)
	}
	current, err := run.store.GetRun(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := run.store.ListTasks(run.ctx, run.fixture.sessionID, run.fixture.workspaceID)
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
			if counts != baseline {
				t.Fatalf("rolled-back result acceptance state=%+v baseline=%+v", counts, baseline)
			}
			return
		}
		if counts.resultArtifacts != 1 || counts.resultPassports != 1 || counts.resultVersions != 1 {
			t.Fatalf("committed result artifact lineage=%+v", counts)
		}
		if counts.methodDecisions != baseline.methodDecisions+1 ||
			counts.questions != baseline.questions+1 || counts.tasks != baseline.tasks+5 ||
			counts.acceptedEvents != baseline.acceptedEvents+1 {
			t.Fatalf("committed result materialization=%+v baseline=%+v", counts, baseline)
		}
		if counts.attemptStatus != string(AttemptStatusSucceeded) || counts.taskStatus != string(TaskStatusSucceeded) ||
			counts.resultHash != normalizeArtifactContentHash(hash) {
			t.Fatalf("committed attempt/task state=%+v", counts)
		}
		if counts.inputReferences <= baseline.inputReferences || counts.policyMutations <= baseline.policyMutations {
			t.Fatalf("committed result provenance=%+v baseline=%+v", counts, baseline)
		}
	}
	return transactionRecoveryOperation{
		invoke:           invoke,
		assertRolledBack: func() { assertArtifactState(false) },
		assertCommitted:  func() { assertArtifactState(true) },
		recover: func() error {
			replayed, replayErr := run.store.AcceptResult(run.ctx, input)
			if replayErr == nil && !replayed.Replayed {
				return fmt.Errorf("result replay=%+v, want replayed", replayed)
			}
			return replayErr
		},
	}
}

func TestAcceptResultTransactionRecoveryCountsResultArtifacts(t *testing.T) {
	runTransactionRecoveryMatrix(t, txOpResultAccept, resultArtifactRecoveryOperation)
}

func TestAcceptResultAfterBeginRecoveryCountsResultArtifacts(t *testing.T) {
	run := newTransactionRecoveryRun(t, "result artifact after-begin recovery")
	operation := resultArtifactRecoveryOperation(t, run)
	injected := errors.New("injected result accept after_begin")
	fault := &oneShotResearchTxFault{operation: txOpResultAccept, point: txAfterBegin, err: injected}
	run.store.txFaultHook = fault.hook

	if err := operation.invoke(); !errors.Is(err, injected) {
		t.Fatalf("after_begin error=%v, want injected fault", err)
	}
	if !fault.fired {
		t.Fatal("result accept after_begin fault did not fire")
	}
	operation.assertRolledBack()
	if err := operation.invoke(); err != nil {
		t.Fatalf("retry after after_begin rollback: %v", err)
	}
	operation.assertCommitted()
}
