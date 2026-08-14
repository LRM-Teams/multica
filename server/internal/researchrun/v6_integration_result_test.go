package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeAndValidateV6IntegrationResult(t *testing.T) {
	raw := validV6IntegrationResultJSON(t)
	result, err := DecodeAndValidateV6IntegrationResult(raw)
	if err != nil {
		t.Fatalf("valid V6 integration result rejected: %v", err)
	}
	if result.SchemaVersion != 6 || len(result.IntegrationContributions) != 1 {
		t.Fatalf("decoded result=%+v", result)
	}
}

func TestDecodeAndValidateV6IntegrationResultRejectsContractDrift(t *testing.T) {
	base := decodeV6IntegrationFixtureMap(t)
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "unknown envelope field", mutate: func(v map[string]any) { v["future"] = true }},
		{name: "wrong schema", mutate: func(v map[string]any) { v["schema_version"] = 7 }},
		{name: "missing required array", mutate: func(v map[string]any) { delete(v, "status_updates") }},
		{name: "missing confidence", mutate: func(v map[string]any) { delete(v, "confidence") }},
		{name: "null confidence", mutate: func(v map[string]any) { v["confidence"] = nil }},
		{name: "null required array", mutate: func(v map[string]any) { v["insights"] = nil }},
		{name: "retrieval field", mutate: func(v map[string]any) { v["query_executions"] = []any{map[string]any{}} }},
		{name: "report field", mutate: func(v map[string]any) { v["report"] = map[string]any{} }},
		{name: "no contribution", mutate: func(v map[string]any) { v["integration_contributions"] = []any{} }},
		{name: "unknown contribution field", mutate: func(v map[string]any) {
			firstObject(v, "integration_contributions")["future"] = true
		}},
		{name: "invalid round", mutate: func(v map[string]any) {
			firstObject(v, "integration_contributions")["integration_round_id"] = "round-1"
		}},
		{name: "missing required question boolean", mutate: func(v map[string]any) {
			question := map[string]any{
				"client_key": "question-1", "text": "Which scope applies?", "kind": "follow_up",
				"priority": 0.5, "impact": 0.5, "uncertainty": 0.5, "novelty": 0.5,
			}
			firstObject(v, "integration_contributions")["follow_up_questions"] = []any{question}
		}},
		{name: "null optional question parent", mutate: func(v map[string]any) {
			question := map[string]any{
				"client_key": "question-1", "parent_client_key": nil, "text": "Which scope applies?", "kind": "follow_up", "required": true,
				"priority": 0.5, "impact": 0.5, "uncertainty": 0.5, "novelty": 0.5,
			}
			firstObject(v, "integration_contributions")["follow_up_questions"] = []any{question}
		}},
		{name: "missing compared artifact", mutate: func(v map[string]any) {
			firstObject(v, "integration_contributions")["compared_artifacts"] = []any{}
		}},
		{name: "invalid entity kind", mutate: func(v map[string]any) {
			contribution := firstObject(v, "integration_contributions")
			contribution["compared_artifacts"].([]any)[0].(map[string]any)["kind"] = "future"
		}},
		{name: "one-input insight", mutate: func(v map[string]any) {
			v["insights"] = []any{map[string]any{
				"client_key": "insight-1", "title": "Combined", "summary": "Combined finding",
				"inputs":   []any{map[string]any{"kind": "claim", "key": "claim-1"}},
				"relation": "integrates", "scope": map[string]any{}, "semantic_value": "new_explanation",
			}}
		}},
		{name: "duplicate proposal key", mutate: func(v map[string]any) {
			insight := map[string]any{
				"client_key": "insight-1", "title": "Combined", "summary": "Combined finding",
				"inputs":   []any{map[string]any{"kind": "claim", "key": "claim-1"}, map[string]any{"kind": "claim", "key": "claim-2"}},
				"relation": "integrates", "scope": map[string]any{}, "semantic_value": "new_explanation",
			}
			v["insights"] = []any{insight, insight}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := cloneJSONMap(t, base)
			test.mutate(fixture)
			raw, err := json.Marshal(fixture)
			if err != nil {
				t.Fatal(err)
			}
			_, err = DecodeAndValidateV6IntegrationResult(raw)
			if !errors.Is(err, ErrInvalidResult) {
				t.Fatalf("err=%v want ErrInvalidResult", err)
			}
		})
	}
	if _, err := DecodeAndValidateV6IntegrationResult(append(validV6IntegrationResultJSON(t), []byte(` {}`)...)); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("trailing JSON err=%v want ErrInvalidResult", err)
	}
}

func validV6IntegrationResultJSON(t *testing.T) []byte {
	t.Helper()
	fixture := map[string]any{
		"contract_kind": "task_result", "schema_version": 6,
		"client_request_id": "11111111-1111-4111-8111-111111111111",
		"summary":           "Compared the accepted results and retained the material differences.",
		"query_executions":  []any{}, "source_candidates": []any{}, "status_updates": []any{},
		"integration_contributions": []any{map[string]any{
			"client_key": "contribution-1", "integration_round_id": "22222222-2222-4222-8222-222222222222",
			"compared_artifacts": []any{map[string]any{"kind": "claim", "key": "claim-1"}},
			"common_findings":    []any{"Both results identify the same constraint."},
			"unique_findings":    []any{"The second result adds a regional boundary."},
			"conflicts":          []any{}, "scope": map[string]any{"region": "EU"}, "omissions": []any{},
			"proposed_insights": []any{"Condition the conclusion on region."}, "follow_up_questions": []any{},
		}},
		"insights": []any{}, "disputes": []any{}, "proposed_tasks": []any{},
		"confidence": 0.8, "incomplete_reason": nil,
	}
	raw, err := json.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func decodeV6IntegrationFixtureMap(t *testing.T) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(validV6IntegrationResultJSON(t), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func cloneJSONMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err = json.Unmarshal(raw, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func firstObject(value map[string]any, key string) map[string]any {
	return value[key].([]any)[0].(map[string]any)
}
