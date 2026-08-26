package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

type submissionStoreStub struct {
	binding       V6SubmissionBinding
	seen          map[string]V6SubmissionOutcome
	settled       []string
	settlement    string
	settlementErr error
}

func (s *submissionStoreStub) AuthorizeV6Submission(context.Context, V6AttemptAccess) (V6SubmissionBinding, error) {
	return s.binding, nil
}

func (s *submissionStoreStub) RecordV6Submission(_ context.Context, _ V6AttemptAccess, decoded DecodedV6Contract, requestID string) (V6SubmissionOutcome, error) {
	if previous, ok := s.seen[requestID]; ok {
		if previous.ContentHash != decoded.ContentHash {
			return V6SubmissionOutcome{}, ErrV6IdempotencyConflict
		}
		previous.Replayed = true
		return previous, nil
	}
	outcome := V6SubmissionOutcome{SubmissionID: "submission", ClientRequestID: requestID, ContentHash: decoded.ContentHash, Kind: decoded.Kind, Status: "received"}
	s.seen[requestID] = outcome
	return outcome, nil
}

func (s *submissionStoreStub) SettleV6DirectorSubmission(_ context.Context, _, _, submissionID string) (string, error) {
	s.settled = append(s.settled, submissionID)
	return s.settlement, s.settlementErr
}

func TestV6DirectorSubmissionSettlesImmediatelyAfterDurableRecord(t *testing.T) {
	raw := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "director-no-op-v6.example.json"))
	access := V6AttemptAccess{
		WorkspaceID: "00000000-0000-4000-8000-000000000002",
		RunID:       "00000000-0000-4000-8000-000000000003",
		WorkItemID:  "00000000-0000-4000-8000-000000000112",
		AttemptID:   "00000000-0000-4000-8000-000000000113",
		AgentID:     "00000000-0000-4000-8000-000000000004",
	}
	store := &submissionStoreStub{
		binding: V6SubmissionBinding{
			ManifestID:   "00000000-0000-4000-8000-000000000114",
			ManifestHash: "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			ExpectedKind: V6ContractDirectorActionProposal,
			TaskSchema:   json.RawMessage(`{"payload_schemas":{"research.no_op.v1":{"type":"object"}}}`),
		},
		seen:       map[string]V6SubmissionOutcome{},
		settlement: "accepted",
	}
	outcome, err := (v6SubmissionModule{store: store}).Submit(context.Background(), V6SubmissionInput{V6AttemptAccess: access, Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.settled) != 1 || store.settled[0] != outcome.SubmissionID {
		t.Fatalf("settled=%v submission=%s: Director proposal must settle in the submission request", store.settled, outcome.SubmissionID)
	}
	if outcome.Status != "accepted" {
		t.Fatalf("status=%s want accepted", outcome.Status)
	}
}

func TestV6SubmissionReplayReturnsOriginalOutcomeAndRejectsChangedPayload(t *testing.T) {
	raw := readV6Fixture(t, filepath.Join("..", "..", "..", "docs", "research", "fixtures", "atomic-result-v6.example.json"))
	raw = withValidV6SelfHash(t, raw, "content_hash")
	access := V6AttemptAccess{
		WorkspaceID: "00000000-0000-4000-8000-000000000002",
		RunID:       "00000000-0000-4000-8000-000000000003",
		WorkItemID:  "00000000-0000-4000-8000-000000000202",
		AttemptID:   "00000000-0000-4000-8000-000000000204",
		AgentID:     "00000000-0000-4000-8000-000000000205",
	}
	store := &submissionStoreStub{
		binding: V6SubmissionBinding{
			ManifestID:   "00000000-0000-4000-8000-000000000201",
			ManifestHash: "sha256:3333333333333333333333333333333333333333333333333333333333333333",
			ExpectedKind: V6ContractAtomicResultSubmission,
			TaskSchemaID: "research.production_benchmark.v1",
			TaskSchema: json.RawMessage(`{
			  "type":"object","additionalProperties":false,"required":["benchmark_matrix"],
			  "properties":{"benchmark_matrix":{"type":"array","minItems":1}}
			}`),
		},
		seen: map[string]V6SubmissionOutcome{},
	}
	module := v6SubmissionModule{store: store}
	first, err := module.Submit(context.Background(), V6SubmissionInput{V6AttemptAccess: access, Raw: raw})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := module.Submit(context.Background(), V6SubmissionInput{V6AttemptAccess: access, Raw: raw})
	if err != nil || !replay.Replayed || replay.SubmissionID != first.SubmissionID {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}

	var changed map[string]any
	if err = json.Unmarshal(raw, &changed); err != nil {
		t.Fatal(err)
	}
	changed["task_specific_payload"].(map[string]any)["benchmark_matrix"].([]any)[0].(map[string]any)["p99_ms"] = float64(999)
	changedRaw, err := json.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	changedRaw = withValidV6SelfHash(t, changedRaw, "content_hash")
	if _, err = module.Submit(context.Background(), V6SubmissionInput{V6AttemptAccess: access, Raw: changedRaw}); !errors.Is(err, ErrV6IdempotencyConflict) {
		t.Fatalf("changed replay error=%v", err)
	}
}

func TestV6SubmissionReplaySurvivesSettledAttempt(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Replay settled V6 submission")
	t.Cleanup(func() {
		_, _ = run.pool.Exec(context.Background(), `DELETE FROM research_v6_work_submission WHERE workspace_id=$1::uuid`, run.fixture.workspaceID)
	})
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	if _, err := run.pool.Exec(run.ctx, `
		UPDATE research_work_item_attempt
		SET manifest=jsonb_build_object(
		  'expected_result_schema','atomic_result_submission',
		  'task_specific_schema',jsonb_build_object('type','object','required',jsonb_build_array('value'),'properties',jsonb_build_object('value',jsonb_build_object('type','string')))
		)
		WHERE id=$1::uuid
	`, attemptID); err != nil {
		t.Fatal(err)
	}
	access := V6AttemptAccess{
		WorkspaceID: run.fixture.workspaceID,
		RunID:       run.fixture.sessionID,
		WorkItemID:  workItemID,
		AttemptID:   attemptID,
		AgentID:     run.fixture.agentID,
	}
	requestID := uuid.NewString()
	decoded := DecodedV6Contract{
		Kind:        V6ContractAtomicResultSubmission,
		ContentHash: "sha256:" + strings.Repeat("7", 64),
		Canonical:   json.RawMessage(`{"contract_kind":"atomic_result_submission","schema_version":6}`),
	}
	first, err := run.store.RecordV6Submission(run.ctx, access, decoded, requestID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item_attempt SET status='succeeded',completed_at=now() WHERE id=$1::uuid`, attemptID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item SET status='succeeded',completed_at=now() WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	binding, err := run.store.AuthorizeV6Submission(run.ctx, access)
	if err != nil || binding.ExpectedKind != V6ContractAtomicResultSubmission || binding.TaskSchemaID != "schema" {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	replay, err := run.store.RecordV6Submission(run.ctx, access, decoded, requestID)
	if err != nil || !replay.Replayed || replay.SubmissionID != first.SubmissionID {
		t.Fatalf("replay=%+v err=%v, want submission %s", replay, err, first.SubmissionID)
	}
	if _, err = run.store.RecordV6Submission(run.ctx, access, decoded, uuid.NewString()); !errors.Is(err, ErrAttemptNotAssigned) {
		t.Fatalf("new request after settlement error=%v, want ErrAttemptNotAssigned", err)
	}
}

func withValidV6SelfHash(t *testing.T, raw []byte, field string) []byte {
	t.Helper()
	value, err := decodeSingleV6JSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	root := value.(map[string]any)
	delete(root, field)
	canonical, err := marshalV6CanonicalJSON(root)
	if err != nil {
		t.Fatal(err)
	}
	root[field] = ArtifactContentHashFromCanonicalJSON(canonical)
	result, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
