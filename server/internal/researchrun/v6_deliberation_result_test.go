package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
)

func validV6DeliberationResultJSON(t *testing.T) []byte {
	t.Helper()
	disputeKey := "dispute-1"
	position := func(actor, statement string) map[string]any {
		return map[string]any{
			"actor_agent_id": actor, "statement": statement, "scope": map[string]any{"region": "EU"},
			"claim_refs": []any{map[string]any{"kind": "claim", "key": "claim-1"}}, "evidence_refs": []any{},
			"challenge": "", "concession": "", "proposed_action": map[string]any{"kind": "retain_condition"},
			"canonical_delta":              map[string]any{"position_hashes": []any{"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}, "evidence_refs": []any{}, "scope_hashes": []any{"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}},
			"resolution_proposal_by_agent": map[string]any{}, "needs_external_evidence": false, "unavailable_participant_ids": []any{},
			"elapsed_seconds": 30, "token_cost": 100, "tool_call_cost": 0,
		}
	}
	fixture := map[string]any{
		"contract_kind": "task_result", "schema_version": 6, "client_request_id": "11111111-1111-4111-8111-111111111111",
		"summary": "The participants compared the material disagreement.", "query_executions": []any{}, "source_candidates": []any{},
		"status_updates": []any{map[string]any{"target": map[string]any{"kind": "dispute", "key": disputeKey}, "before": "open", "after": "investigating",
			"reason": "The positions remain materially distinct.", "evidence_refs": []any{map[string]any{"kind": "claim", "key": "claim-1"}}}},
		"integration_contributions": []any{}, "insights": []any{},
		"disputes": []any{map[string]any{"client_key": disputeKey, "subject": map[string]any{"kind": "claim", "key": "claim-1"},
			"positions":   []any{position("22222222-2222-4222-8222-222222222222", "Position A"), position("33333333-3333-4333-8333-333333333333", "Position B")},
			"materiality": 0.9, "resolution_request": "Determine whether the regional condition resolves the conflict."}},
		"proposed_tasks": []any{}, "confidence": 0.8,
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeAndValidateV6DeliberationResult(t *testing.T) {
	result, hash, err := DecodeAndValidateV6DeliberationResult(validV6DeliberationResultJSON(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Turns) != 2 || result.Dispute.ClientKey != "dispute-1" || !validLowerSHA256(hash) {
		t.Fatalf("result=%+v hash=%q", result, hash)
	}
}

func TestDecodeAndValidateV6DeliberationResultRejectsOpenObjectEscape(t *testing.T) {
	var fixture map[string]any
	if err := json.Unmarshal(validV6DeliberationResultJSON(t), &fixture); err != nil {
		t.Fatal(err)
	}
	disputes := fixture["disputes"].([]any)
	positions := disputes[0].(map[string]any)["positions"].([]any)
	positions[0].(map[string]any)["future_unchecked_command"] = true
	raw, _ := json.Marshal(fixture)
	if _, _, err := DecodeAndValidateV6DeliberationResult(raw); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeAndValidateV6DirectorAdjudicationRequiresEvidencePerPosition(t *testing.T) {
	var fixture map[string]any
	if err := json.Unmarshal(validV6DeliberationResultJSON(t), &fixture); err != nil {
		t.Fatal(err)
	}
	dispute := fixture["disputes"].([]any)[0].(map[string]any)
	dispute["positions"] = []any{map[string]any{
		"actor_agent_id": "22222222-2222-4222-8222-222222222222", "director_identity_version": 1,
		"decision": "resolved", "rationale": "The primary record resolves the contradiction.", "conditions": []any{}, "residual_uncertainty": "",
		"position_assessments": []any{map[string]any{
			"position_id": "44444444-4444-4444-8444-444444444444", "disposition": "retained", "rationale": "Supported by the primary record.",
			"evidence_refs": []any{map[string]any{"kind": "source", "key": "55555555-5555-4555-8555-555555555555"}},
		}},
	}}
	fixture["status_updates"] = []any{map[string]any{"target": map[string]any{"kind": "dispute", "key": "dispute-1"}, "before": "irreducible", "after": "resolved", "reason": "Evidence-bound adjudication.", "evidence_refs": []any{map[string]any{"kind": "source", "key": "55555555-5555-4555-8555-555555555555"}}}}
	raw, _ := json.Marshal(fixture)
	result, _, err := DecodeAndValidateV6DeliberationResult(raw)
	if err != nil || result.Adjudication == nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	assessment := dispute["positions"].([]any)[0].(map[string]any)["position_assessments"].([]any)[0].(map[string]any)
	assessment["evidence_refs"] = []any{}
	raw, _ = json.Marshal(fixture)
	if _, _, err = DecodeAndValidateV6DeliberationResult(raw); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("authority-only adjudication err=%v", err)
	}
}
