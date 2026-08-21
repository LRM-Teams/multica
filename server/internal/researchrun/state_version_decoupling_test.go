package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// For V6 runs the event sequence is bookkeeping; research_session.state_version
// is a semantic concurrency token that only goal / steering / report
// transitions may advance. Coupling them made every dispatch or submission
// event invalidate all in-flight Director proposals and Agent results
// (livelock). Legacy orchestrators keep the coupled contract.
func TestAppendEventDoesNotAdvanceSemanticStateVersion(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Decouple event sequence")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	var stateBefore, maxSequenceBefore int64
	if err := run.pool.QueryRow(run.ctx, `SELECT s.state_version,
		COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=s.id),0)
		FROM research_session s WHERE s.id=$1::uuid`, run.fixture.sessionID).Scan(&stateBefore, &maxSequenceBefore); err != nil {
		t.Fatal(err)
	}
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	first, err := appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID,
		"v6_work_item_dispatched", "decouple-test:"+uuid.NewString(), "system", "", map[string]any{"work_item_id": uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID,
		"v6_work_item_dispatched", "decouple-test:"+uuid.NewString(), "system", "", map[string]any{"work_item_id": uuid.NewString()})
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	if first.Sequence != maxSequenceBefore+1 || second.Sequence != maxSequenceBefore+2 {
		t.Fatalf("sequences=%d,%d want %d,%d", first.Sequence, second.Sequence, maxSequenceBefore+1, maxSequenceBefore+2)
	}
	var stateAfter int64
	if err = run.pool.QueryRow(run.ctx, `SELECT state_version FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateAfter); err != nil {
		t.Fatal(err)
	}
	if stateAfter != stateBefore {
		t.Fatalf("state_version=%d want unchanged %d: events must not consume the semantic token", stateAfter, stateBefore)
	}
}

type directorProposalFixture struct {
	run          *transactionRecoveryRun
	cycle        V6DirectorCycle
	submissionID string
	envelope     json.RawMessage
	otherWorkID  string
}

func setupV6DirectorProposalFixture(t *testing.T, title string) directorProposalFixture {
	t.Helper()
	run := newTransactionRecoveryRun(t, title)
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
	})
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	// Seed before assigning the Director: the membership doubles as the
	// Director attempt's membership (AssignV6Director reuses an active one),
	// and the extra Work Item is the "someone else's operational activity"
	// the proposal must survive.
	membershipID, otherWorkID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	if _, err := run.store.AssignV6Director(run.ctx, AssignV6DirectorInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		AgentID: run.fixture.agentID, UserID: run.fixture.userID,
		Reason: title, ClientRequestID: uuid.NewString(), ExpectedStateVersion: 0,
	}); err != nil {
		t.Fatal(err)
	}
	var stateVersion, throughSequence int64
	if err := run.pool.QueryRow(run.ctx, `SELECT state_version,
		COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=$1::uuid),0)
		FROM research_session WHERE id=$1::uuid`, run.fixture.sessionID).Scan(&stateVersion, &throughSequence); err != nil {
		t.Fatal(err)
	}
	cycle, err := (directorBriefModule{store: run.store, compiler: contextCompilerModule{}}).Start(run.ctx, StartV6DirectorCycleInput{
		WorkspaceID: run.fixture.workspaceID, RunID: run.fixture.sessionID,
		TriggerKey: uuid.NewString(), FromSequence: throughSequence, ThroughSequence: throughSequence,
		ExpectedStateVersion: stateVersion, Now: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_director_brief_page SET reviewed_at=now()
		WHERE director_cycle_id=$1::uuid`, cycle.ID); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, cycle.WorkItemID)
	var manifestID, manifestHash string
	var inputStateVersion, inputEventSequence int64
	if err = run.pool.QueryRow(run.ctx, `SELECT a.manifest_id::text,a.manifest_hash,w.input_state_version,w.input_event_sequence
		FROM research_work_item_attempt a JOIN research_work_item w ON w.id=a.work_item_id
		WHERE a.id=$1::uuid`, attemptID).Scan(&manifestID, &manifestHash, &inputStateVersion, &inputEventSequence); err != nil {
		t.Fatal(err)
	}
	proposal := map[string]any{
		"workspace_id": run.fixture.workspaceID, "run_id": run.fixture.sessionID,
		"work_item_id": cycle.WorkItemID, "attempt_id": attemptID,
		"manifest_id": manifestID, "manifest_hash": manifestHash,
		"director_assignment_id": cycle.AssignmentID, "brief_id": cycle.BriefID, "brief_hash": cycle.BriefHash,
		"director_generation": cycle.Generation, "reviewed_page_count": cycle.PageCount,
		"expected_state_version": inputStateVersion, "through_event_sequence": inputEventSequence,
		"actions": []map[string]any{{
			"action_id": uuid.NewString(), "kind": "no_op", "reason": "Waiting on in-flight research.",
			"idempotency_key": "no-op:" + uuid.NewString(), "payload_schema": "no_op.v1",
			"payload": map[string]any{"reason": "Waiting on in-flight research."},
		}},
	}
	envelope, err := json.Marshal(proposal)
	if err != nil {
		t.Fatal(err)
	}
	submissionID := uuid.NewString()
	if _, err = run.pool.Exec(run.ctx, `INSERT INTO research_v6_work_submission(
		id,workspace_id,session_id,work_item_id,attempt_id,client_request_id,contract_kind,content_hash,envelope,status
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'director_action_proposal',$7,$8::jsonb,'processing')`,
		submissionID, run.fixture.workspaceID, run.fixture.sessionID, cycle.WorkItemID, attemptID, uuid.NewString(),
		"sha256:"+strings.Repeat("7", 64), envelope); err != nil {
		t.Fatal(err)
	}
	return directorProposalFixture{run: run, cycle: cycle, submissionID: submissionID, envelope: envelope, otherWorkID: otherWorkID}
}

func appendOperationalEvent(t *testing.T, run *transactionRecoveryRun, eventType, workItemID string) {
	t.Helper()
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	if _, err = appendEvent(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID,
		eventType, "operational:"+uuid.NewString(), "system", "", map[string]any{"work_item_id": workItemID}); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
}

// Regression test for the production livelock: while research Agents are
// active, dispatch / submission / recovery events land between the Director's
// brief and its proposal. Those events must not reject the proposal — only a
// semantic state change (goal revision, steering, report review) may.
func TestV6DirectorProposalSurvivesOperationalEventsAfterBrief(t *testing.T) {
	fx := setupV6DirectorProposalFixture(t, "Survive operational events")
	appendOperationalEvent(t, fx.run, "v6_work_item_dispatched", fx.otherWorkID)
	appendOperationalEvent(t, fx.run, "v6_work_submission_received", fx.otherWorkID)
	if err := fx.run.store.executeV6DirectorProposal(fx.run.ctx, fx.submissionID, fx.envelope); err != nil {
		t.Fatalf("proposal rejected despite only operational drift: %v", err)
	}
	var cycleStatus, attemptStatus string
	if err := fx.run.pool.QueryRow(fx.run.ctx, `SELECT c.status,a.status
		FROM research_director_cycle c JOIN research_v6_work_submission s ON s.work_item_id=c.work_item_id
		JOIN research_work_item_attempt a ON a.id=s.attempt_id
		WHERE s.id=$1::uuid`, fx.submissionID).Scan(&cycleStatus, &attemptStatus); err != nil {
		t.Fatal(err)
	}
	if cycleStatus != "applied" || attemptStatus != "succeeded" {
		t.Fatalf("cycle=%s attempt=%s want applied/succeeded", cycleStatus, attemptStatus)
	}
}

func TestV6DirectorProposalRejectedAfterSemanticStateChange(t *testing.T) {
	fx := setupV6DirectorProposalFixture(t, "Reject after semantic change")
	// Simulate a goal revision / steering decision landing after the brief froze.
	if _, err := fx.run.pool.Exec(fx.run.ctx, `UPDATE research_session SET state_version=state_version+1
		WHERE id=$1::uuid`, fx.run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	if err := fx.run.store.executeV6DirectorProposal(fx.run.ctx, fx.submissionID, fx.envelope); !errors.Is(err, ErrWorkItemChanged) {
		t.Fatalf("err=%v want ErrWorkItemChanged after semantic state change", err)
	}
}

func seedV6ConflictSubmission(t *testing.T, run *transactionRecoveryRun, workItemID, attemptID string) string {
	t.Helper()
	contentHash := "sha256:" + strings.Repeat("9", 64)
	envelope, err := json.Marshal(V6AtomicResultSubmission{
		ClientRequestID: uuid.NewString(),
		WorkspaceID:     run.fixture.workspaceID, RunID: run.fixture.sessionID,
		WorkItemID: workItemID, TaskID: uuid.NewString(), AttemptID: attemptID,
		AgentID: run.fixture.agentID,
		// The manifest mismatch below is the optimistic-concurrency conflict.
		ManifestID: uuid.NewString(), ManifestHash: "sha256:" + strings.Repeat("4", 64),
		GoalVersion: 1,
		BranchRefs:  []V6BranchRef{{ID: uuid.NewString(), StateVersion: 1}},
		ContentHash: contentHash,
	})
	if err != nil {
		t.Fatal(err)
	}
	submissionID := uuid.NewString()
	if _, err = run.pool.Exec(run.ctx, `INSERT INTO research_v6_work_submission(
		id,workspace_id,session_id,work_item_id,attempt_id,client_request_id,contract_kind,content_hash,envelope,status
	) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,'atomic_result_submission',$7,$8::jsonb,'received')`,
		submissionID, run.fixture.workspaceID, run.fixture.sessionID, workItemID, attemptID,
		uuid.NewString(), contentHash, envelope); err != nil {
		t.Fatal(err)
	}
	return submissionID
}

// A conflict rejection means the Work Item premise moved while the attempt
// was in flight — not that the Agent failed. The attempt must settle
// immediately (no 15-minute lease limbo), the Work Item must requeue for a
// fresh manifest, and the consumed attempt must be refunded.
func TestConflictRejectedResultRequeuesWorkItemWithoutBurningBudget(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Requeue conflicted V6 result")
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
	})
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item
		SET expected_result_schema_id='atomic_result_submission',attempt_count=1,max_attempts=3 WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	submissionID := seedV6ConflictSubmission(t, run, workItemID, attemptID)

	applied, err := run.store.ApplyReceivedV6Submissions(run.ctx, 4)
	if err != nil || applied != 1 {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	var submissionStatus, outcomeError, attemptStatus, failureClass string
	if err = run.pool.QueryRow(run.ctx, `SELECT s.status,s.outcome->>'error',a.status,COALESCE(a.failure_class,'')
		FROM research_v6_work_submission s JOIN research_work_item_attempt a ON a.id=s.attempt_id
		WHERE s.id=$1::uuid`, submissionID).Scan(&submissionStatus, &outcomeError, &attemptStatus, &failureClass); err != nil {
		t.Fatal(err)
	}
	if submissionStatus != "rejected" || !strings.Contains(outcomeError, "changed") {
		t.Fatalf("submission=%s outcome=%q", submissionStatus, outcomeError)
	}
	if attemptStatus != "failed" || failureClass != "contract_rejected" {
		t.Fatalf("attempt=%s class=%s want failed/contract_rejected (no lease limbo)", attemptStatus, failureClass)
	}
	var workStatus, terminalReason string
	var attemptCount int
	var leaseToken *string
	if err = run.pool.QueryRow(run.ctx, `SELECT status,COALESCE(terminal_reason_code,''),attempt_count,lease_token::text
		FROM research_work_item WHERE id=$1::uuid`, workItemID).Scan(&workStatus, &terminalReason, &attemptCount, &leaseToken); err != nil {
		t.Fatal(err)
	}
	if workStatus != "ready" || terminalReason != "" || leaseToken != nil {
		t.Fatalf("work status=%s reason=%q lease=%v want ready for immediate redispatch", workStatus, terminalReason, leaseToken)
	}
	if attemptCount != 0 {
		t.Fatalf("attempt_count=%d want 0: conflicts must not burn the attempt budget", attemptCount)
	}
}

// Refunds are bounded: once total attempts pass 4x max_attempts the conflict
// stops being "bad luck" and the Work Item must terminate instead of
// dispatching forever.
func TestConflictRefundStopsAfterRepeatedConflicts(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Bound conflict refunds")
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
	})
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item
		SET expected_result_schema_id='atomic_result_submission',attempt_count=3,max_attempts=3 WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item_attempt SET attempt_number=13 WHERE id=$1::uuid`, attemptID); err != nil {
		t.Fatal(err)
	}
	seedV6ConflictSubmission(t, run, workItemID, attemptID)

	applied, err := run.store.ApplyReceivedV6Submissions(run.ctx, 4)
	if err != nil || applied != 1 {
		t.Fatalf("applied=%d err=%v", applied, err)
	}
	var workStatus, terminalReason string
	var attemptCount int
	if err = run.pool.QueryRow(run.ctx, `SELECT status,COALESCE(terminal_reason_code,''),attempt_count
		FROM research_work_item WHERE id=$1::uuid`, workItemID).Scan(&workStatus, &terminalReason, &attemptCount); err != nil {
		t.Fatal(err)
	}
	if workStatus != "failed" || terminalReason != "attempt_budget_exhausted" || attemptCount != 3 {
		t.Fatalf("status=%s reason=%q attempts=%d want failed/attempt_budget_exhausted/3", workStatus, terminalReason, attemptCount)
	}
}
