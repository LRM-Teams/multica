package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
)

type submissionStoreStub struct {
	binding V6SubmissionBinding
	seen    map[string]V6SubmissionOutcome
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
