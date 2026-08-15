package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func validV6EvidenceResultFixture() map[string]any {
	started := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339Nano)
	finished := time.Now().UTC().Format(time.RFC3339Nano)
	return map[string]any{
		"contract_kind": "task_result", "schema_version": 6, "client_request_id": uuid.NewString(), "summary": "Retrieved and screened primary evidence",
		"query_executions":  []any{map[string]any{"client_key": "query.primary", "search_plan_key": "search.primary", "adapter": "web", "query": "primary records", "started_at": started, "finished_at": finished, "outcome": "succeeded", "cost": map[string]any{"requests": 1}, "safety": map[string]any{}}},
		"source_candidates": []any{map[string]any{"client_key": "source.primary", "query_execution_key": "query.primary", "url": "https://example.com/evidence", "title": "Primary evidence", "content_hash": "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "independence_family": "example.primary", "screening": map[string]any{"decision": "include", "reasons": []any{"Matches the frozen inclusion criteria"}, "reviewed_against_plan": true}}},
		"status_updates":    []any{}, "integration_contributions": []any{}, "insights": []any{}, "disputes": []any{}, "proposed_tasks": []any{}, "confidence": 0.8,
	}
}

func encodeV6EvidenceFixture(t *testing.T, fixture map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeAndValidateV6EvidenceResultStrictOwnershipAndHash(t *testing.T) {
	fixture := validV6EvidenceResultFixture()
	decoded, hash, err := DecodeAndValidateV6EvidenceResult(encodeV6EvidenceFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded.QueryExecutions) != 1 || len(decoded.SourceCandidates) != 1 || !validPrefixedSHA256(hash) {
		t.Fatalf("decoded=%+v hash=%s", decoded, hash)
	}

	fixture["insights"] = []any{map[string]any{}}
	if _, _, err = DecodeAndValidateV6EvidenceResult(encodeV6EvidenceFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("foreign field err=%v", err)
	}
}

func TestDecodeAndValidateV6EvidenceResultRejectsUnreviewedOrUnresolvedCandidates(t *testing.T) {
	for name, mutate := range map[string]func(map[string]any){
		"unreviewed": func(f map[string]any) {
			f["source_candidates"].([]any)[0].(map[string]any)["screening"].(map[string]any)["reviewed_against_plan"] = false
		},
		"unknown query": func(f map[string]any) {
			f["source_candidates"].([]any)[0].(map[string]any)["query_execution_key"] = "query.missing"
		},
		"credential URL": func(f map[string]any) {
			f["source_candidates"].([]any)[0].(map[string]any)["url"] = "https://user:secret@example.com/evidence"
		},
		"unknown nested field": func(f map[string]any) { f["query_executions"].([]any)[0].(map[string]any)["invented"] = true },
	} {
		t.Run(name, func(t *testing.T) {
			fixture := validV6EvidenceResultFixture()
			mutate(fixture)
			if _, _, err := DecodeAndValidateV6EvidenceResult(encodeV6EvidenceFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("err=%v", err)
			}
		})
	}
}
