package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type seededV6AtomicSubmission struct {
	run                            *transactionRecoveryRun
	membershipID, workItemID       string
	attemptID, requestID, branchID string
}

func seedReceivedV6AtomicSubmission(t *testing.T, title string) seededV6AtomicSubmission {
	t.Helper()
	run := newTransactionRecoveryRun(t, title)
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
	})
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	branchID := seedV6WorkBranchScope(t, run, workItemID, "atomic-result:", "Research primary sources", 1)
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	var manifestID, manifestHash string
	if err := run.pool.QueryRow(run.ctx, `SELECT manifest_id::text,manifest_hash FROM research_work_item_attempt WHERE id=$1::uuid`, attemptID).Scan(&manifestID, &manifestHash); err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(map[string]any{
		"expected_result_schema": "atomic_result_submission",
		"branch_refs":            []map[string]any{{"id": branchID, "state_version": 1}},
		"artifacts":              []any{},
		"task_specific_schema": map[string]any{"payload_schemas": map[string]any{
			"research.test.v1": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"required":             []string{"finding"},
				"properties": map[string]any{
					"finding": map[string]any{"type": "string", "minLength": 1},
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item_attempt SET manifest=$2::jsonb WHERE id=$1::uuid`, attemptID, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item SET expected_result_schema_id='atomic_result_submission',payload_schema_id='research.test.v1',attempt_count=1,max_attempts=3 WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_task SET work_item_id=$2::uuid,goal_version=$3 WHERE id=$1::uuid`, run.taskID, workItemID, run.goalVersion); err != nil {
		t.Fatal(err)
	}

	requestID := uuid.NewString()
	raw, err := json.Marshal(V6AtomicResultSubmission{
		ClientRequestID: requestID,
		WorkspaceID:     run.fixture.workspaceID,
		RunID:           run.fixture.sessionID,
		WorkItemID:      workItemID,
		TaskID:          run.taskID,
		AttemptID:       attemptID,
		AgentID:         run.fixture.agentID,
		ManifestID:      manifestID,
		ManifestHash:    manifestHash,
		GoalVersion:     run.goalVersion,
		BranchRefs:      []V6BranchRef{{ID: branchID, StateVersion: 1}},
		ContentLayers: V6ContentLayers{
			CatalogSummary: "Official sources reviewed",
			BriefSummary:   "Primary-source result",
			Objective:      "Research primary sources",
			Conclusion:     "The documented capability is available.",
			Content:        "Detailed findings",
			Scope:          json.RawMessage(`{"source_kind":"official"}`),
			Uncertainties:  json.RawMessage(`[]`),
			Conflicts:      json.RawMessage(`[]`),
			OpenQuestions:  json.RawMessage(`[]`),
		},
		EvidenceRefs: []V6EvidenceRef{},
		StateProposal: V6ResultStateProposal{
			ConclusionState:  "proposed",
			IntegrationState: "candidate",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope["contract_kind"] = string(V6ContractAtomicResultSubmission)
	envelope["schema_version"] = 6
	envelope["related_candidates"] = []any{}
	envelope["task_specific_schema"] = "research.test.v1"
	envelope["task_specific_payload"] = map[string]any{"finding": "documented capability"}
	raw, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	raw = withValidV6SelfHash(t, raw, "content_hash")
	decoded, err := DecodeV6Contract(raw, V6ContractAtomicResultSubmission, acceptingV6SecondStage{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.store.RecordV6Submission(run.ctx, V6AttemptAccess{
		WorkspaceID: run.fixture.workspaceID,
		RunID:       run.fixture.sessionID,
		WorkItemID:  workItemID,
		AttemptID:   attemptID,
		AgentID:     run.fixture.agentID,
	}, decoded, requestID); err != nil {
		t.Fatal(err)
	}
	return seededV6AtomicSubmission{run: run, membershipID: membershipID, workItemID: workItemID, attemptID: attemptID, requestID: requestID, branchID: branchID}
}

func cleanupAcceptedV6ResultProducerBinding(t *testing.T, seeded seededV6AtomicSubmission) {
	t.Helper()
	if _, err := seeded.run.pool.Exec(context.Background(), `ALTER TABLE research_artifact_version DISABLE TRIGGER research_artifact_version_immutable_guard`); err != nil {
		t.Errorf("disable artifact cleanup guard: %v", err)
		return
	}
	if _, err := seeded.run.pool.Exec(context.Background(), `UPDATE research_artifact_version
		SET produced_by_work_item_id=NULL,produced_by_work_item_attempt_id=NULL
		WHERE workspace_id=$1::uuid AND produced_by_work_item_attempt_id IS NOT NULL`, seeded.run.fixture.workspaceID); err != nil {
		t.Errorf("clear V6 producer cleanup binding: %v", err)
	}
	if _, err := seeded.run.pool.Exec(context.Background(), `ALTER TABLE research_artifact_version ENABLE TRIGGER research_artifact_version_immutable_guard`); err != nil {
		t.Errorf("enable artifact cleanup guard: %v", err)
	}
}

func TestApplyReceivedV6AtomicResultCompletesCanonicalWork(t *testing.T) {
	seeded := seedReceivedV6AtomicSubmission(t, "Apply received V6 atomic result")
	run := seeded.run
	t.Cleanup(func() { cleanupAcceptedV6ResultProducerBinding(t, seeded) })

	applied, err := run.store.ApplyReceivedV6Submissions(run.ctx, 4)
	if err != nil {
		t.Fatalf("apply submission: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied=%d want=1", applied)
	}
	var submissionStatus, attemptStatus, workStatus, membershipState string
	var resultCount int
	if err = run.pool.QueryRow(run.ctx, `SELECT s.status,a.status,w.status,m.state,
		(SELECT count(*)::int FROM research_result_node n WHERE n.workspace_id=s.workspace_id AND n.session_id=s.session_id AND n.work_item_attempt_id=a.id)
		FROM research_v6_work_submission s
		JOIN research_work_item_attempt a ON (a.workspace_id,a.session_id,a.id)=(s.workspace_id,s.session_id,s.attempt_id)
		JOIN research_work_item w ON (w.workspace_id,w.session_id,w.id)=(s.workspace_id,s.session_id,s.work_item_id)
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE s.client_request_id=$1::uuid`, seeded.requestID).Scan(&submissionStatus, &attemptStatus, &workStatus, &membershipState, &resultCount); err != nil {
		t.Fatal(err)
	}
	if submissionStatus != "accepted" || attemptStatus != "succeeded" || workStatus != "succeeded" || membershipState != "idle" || resultCount != 1 {
		t.Fatalf("submission=%s attempt=%s work=%s member=%s results=%d", submissionStatus, attemptStatus, workStatus, membershipState, resultCount)
	}
	var resultHash string
	if err = run.pool.QueryRow(run.ctx, `SELECT content_hash FROM research_result_node WHERE work_item_attempt_id=$1::uuid`, seeded.attemptID).Scan(&resultHash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(resultHash, "sha256:") {
		t.Fatalf("result hash=%q", resultHash)
	}
}

func TestLoadV6WorkArtifactValidatesResultWithProducerFrozenSchema(t *testing.T) {
	seeded := seedReceivedV6AtomicSubmission(t, "Read accepted V6 atomic result")
	t.Cleanup(func() { cleanupAcceptedV6ResultProducerBinding(t, seeded) })
	if applied, err := seeded.run.store.ApplyReceivedV6Submissions(seeded.run.ctx, 1); err != nil || applied != 1 {
		t.Fatalf("apply submission: applied=%d err=%v", applied, err)
	}

	var artifactVersionID, artifactHash string
	if err := seeded.run.pool.QueryRow(seeded.run.ctx, `SELECT n.artifact_version_id::text,n.content_hash
		FROM research_result_node n
		WHERE n.workspace_id=$1::uuid AND n.session_id=$2::uuid AND n.work_item_attempt_id=$3::uuid`,
		seeded.run.fixture.workspaceID, seeded.run.fixture.sessionID, seeded.attemptID).Scan(&artifactVersionID, &artifactHash); err != nil {
		t.Fatal(err)
	}

	membershipID, workItemID := seedV6RecoveryWorkItemForAgent(t, seeded.run, seeded.run.fixture.reporterID, "running", time.Now().Add(time.Minute))
	attemptID := seedV6RecoveryAttemptForAgent(t, seeded.run, seeded.run.fixture.reporterID, membershipID, workItemID)
	manifest, err := json.Marshal(map[string]any{"artifacts": []map[string]any{{
		"artifact_version_id": artifactVersionID,
		"kind":                string(ArtifactKindResultArtifact),
		"representation":      "full",
		"representation_hash": artifactHash,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = seeded.run.pool.Exec(seeded.run.ctx, `UPDATE research_work_item_attempt SET manifest=$2::jsonb WHERE id=$1::uuid`, attemptID, manifest); err != nil {
		t.Fatal(err)
	}

	artifact, err := seeded.run.store.LoadV6WorkArtifact(seeded.run.ctx, V6AttemptAccess{
		WorkspaceID: seeded.run.fixture.workspaceID,
		RunID:       seeded.run.fixture.sessionID,
		WorkItemID:  workItemID,
		AttemptID:   attemptID,
		AgentID:     seeded.run.fixture.reporterID,
	}, artifactVersionID)
	if err != nil {
		t.Fatalf("load result artifact: %v", err)
	}
	if artifact.RepresentationHash != artifactHash || len(artifact.Content) == 0 {
		t.Fatalf("artifact hash=%q content=%s", artifact.RepresentationHash, artifact.Content)
	}
}

func TestApplyReceivedV6SubmissionQuarantinesRepeatedCanonicalFailure(t *testing.T) {
	seeded := seedReceivedV6AtomicSubmission(t, "Quarantine failed V6 canonical result")
	run := seeded.run
	functionName := "test_research_v6_atomic_apply_failure"
	triggerName := "test_research_v6_atomic_apply_failure"
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS `+triggerName+` ON research_result_node`)
		_, _ = run.pool.Exec(context.Background(), `DROP FUNCTION IF EXISTS `+functionName+`()`) //nolint:gosec -- static test identifier
	})
	ddl := fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
	BEGIN
		IF NEW.workspace_id='%s'::uuid THEN
			RAISE EXCEPTION 'injected canonical persistence failure' USING ERRCODE='XX000';
		END IF;
		RETURN NEW;
	END $$;
	CREATE CONSTRAINT TRIGGER %s AFTER INSERT ON research_result_node
	DEFERRABLE INITIALLY DEFERRED
	FOR EACH ROW EXECUTE FUNCTION %s()`, functionName, run.fixture.workspaceID, triggerName, functionName)
	if _, err := run.pool.Exec(run.ctx, ddl); err != nil {
		t.Fatal(err)
	}

	following := seedReceivedV6AtomicSubmission(t, "Apply V6 result behind failed queue head")
	t.Cleanup(func() { cleanupAcceptedV6ResultProducerBinding(t, following) })
	processed, err := run.store.ApplyReceivedV6Submissions(run.ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 2 {
		t.Fatalf("processed=%d want failed queue head and following submission", processed)
	}
	var followingStatus string
	if err = run.pool.QueryRow(run.ctx, `SELECT status FROM research_v6_work_submission
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND client_request_id=$3::uuid`,
		following.run.fixture.workspaceID, following.run.fixture.sessionID, following.requestID).Scan(&followingStatus); err != nil {
		t.Fatal(err)
	}
	if followingStatus != "accepted" {
		t.Fatalf("following submission=%s want accepted", followingStatus)
	}

	for failure := 2; failure <= v6SubmissionApplyFailureLimit; failure++ {
		if _, err = run.pool.Exec(run.ctx, `UPDATE research_v6_work_submission
			SET outcome=jsonb_set(outcome,'{next_apply_after}',to_jsonb(now()-interval '1 second'))
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND client_request_id=$3::uuid`,
			run.fixture.workspaceID, run.fixture.sessionID, seeded.requestID); err != nil {
			t.Fatal(err)
		}
		processed, err := run.store.ApplyReceivedV6Submissions(run.ctx, 1)
		if err != nil {
			t.Fatalf("failure %d: %v", failure, err)
		}
		if processed != 1 {
			t.Fatalf("failure %d processed=%d want=1", failure, processed)
		}
	}

	var submissionStatus, diagnostic, attemptStatus, failureClass, workStatus, membershipState string
	var failureCount, attemptCount int
	if err := run.pool.QueryRow(run.ctx, `SELECT s.status,s.outcome->>'last_error',(s.outcome->>'apply_failure_count')::int,
		a.status,a.failure_class,w.status,w.attempt_count,m.state
		FROM research_v6_work_submission s
		JOIN research_work_item_attempt a ON (a.workspace_id,a.session_id,a.id)=(s.workspace_id,s.session_id,s.attempt_id)
		JOIN research_work_item w ON (w.workspace_id,w.session_id,w.id)=(s.workspace_id,s.session_id,s.work_item_id)
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE s.client_request_id=$1::uuid`, seeded.requestID).Scan(
		&submissionStatus, &diagnostic, &failureCount, &attemptStatus, &failureClass, &workStatus, &attemptCount, &membershipState,
	); err != nil {
		t.Fatal(err)
	}
	if submissionStatus != "rejected" || failureCount != v6SubmissionApplyFailureLimit ||
		attemptStatus != "failed" || failureClass != "platform_error" || workStatus != "ready" || attemptCount != 0 || membershipState != "idle" {
		t.Fatalf("submission=%s failures=%d attempt=%s/%s work=%s attempts=%d member=%s",
			submissionStatus, failureCount, attemptStatus, failureClass, workStatus, attemptCount, membershipState)
	}
	if !strings.Contains(diagnostic, "XX000") {
		t.Fatalf("diagnostic=%q want PostgreSQL code", diagnostic)
	}
	var resultCount, rejectionEvents int
	if err := run.pool.QueryRow(run.ctx, `SELECT
		(SELECT count(*)::int FROM research_result_node WHERE workspace_id=$1::uuid AND session_id=$2::uuid),
		(SELECT count(*)::int FROM research_run_event WHERE workspace_id=$1::uuid AND session_id=$2::uuid
			AND event_type='v6_work_submission_rejected' AND payload->>'failure_class'='platform_error')`,
		run.fixture.workspaceID, run.fixture.sessionID).Scan(&resultCount, &rejectionEvents); err != nil {
		t.Fatal(err)
	}
	if resultCount != 0 || rejectionEvents != 1 {
		t.Fatalf("results=%d rejection events=%d", resultCount, rejectionEvents)
	}
}
