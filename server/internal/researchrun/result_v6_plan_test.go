package researchrun

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func validResearchV6PlanFixture() map[string]any {
	return map[string]any{
		"contract_kind": "plan_result", "schema_version": 6, "client_request_id": uuid.NewString(), "summary": "Bounded initial plan",
		"questions":     []any{map[string]any{"client_key": "q.root", "text": "What is true?", "kind": "dimension", "required": true, "priority": 1.0, "impact": 1.0, "uncertainty": 0.8, "novelty": 0.5}},
		"hypotheses":    []any{map[string]any{"client_key": "h.primary", "question_key": "q.root", "statement": "The primary effect is reproducible", "applicability": map[string]any{}, "expected_observations": []any{"Repeated signal"}, "weakening_conditions": []any{"Failed replication"}, "confidence_low": 0.2, "confidence_high": 0.7}},
		"branches":      []any{map[string]any{"client_key": "b.primary", "objective": "Test the primary effect", "entry_conditions": []any{"Question is open"}, "exit_conditions": []any{"Evidence threshold reached"}, "budget_share": 0.7}},
		"inquiry_edges": []any{map[string]any{"client_key": "edge.tests", "from": map[string]any{"kind": "question", "key": "q.root"}, "to": map[string]any{"kind": "hypothesis", "key": "h.primary"}, "relation": "tests", "rationale": "The hypothesis operationalizes the question"}},
		"tasks":         []any{map[string]any{"client_key": "task.discover", "kind": "discover", "objective": "Find primary evidence", "required_capability": "researcher", "expected_result": "research_evidence_v6", "priority": 1.0, "targets": []any{map[string]any{"kind": "hypothesis", "key": "h.primary"}}, "depends_on": []any{}, "acceptance_criteria": map[string]any{}, "max_attempts": 2, "timeout_seconds": 600}},
		"search_plans":  []any{map[string]any{"client_key": "search.primary", "targets": []any{map[string]any{"kind": "hypothesis", "key": "h.primary"}}, "adapter": "web", "query_strategy": "Search primary records", "inclusion_criteria": []any{"Primary evidence"}, "exclusion_criteria": []any{"Unverifiable claims"}, "stopping_conditions": []any{"Independent support found"}, "strategy_version": "strategy.v1"}},
		"method":        map[string]any{"evidence_standard": "triangulated"},
	}
}

func encodeResearchV6PlanFixture(t *testing.T, value map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDecodeAndValidateResearchV6PlanResultStrictBoundary(t *testing.T) {
	fixture := validResearchV6PlanFixture()
	decoded, hash, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if decoded.SchemaVersion != 6 || len(decoded.Hypotheses) != 1 || !strings.HasPrefix(hash, "sha256:") {
		t.Fatalf("decoded=%+v hash=%q", decoded, hash)
	}

	fixture["unexpected"] = true
	if _, _, err = DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("unknown field err=%v", err)
	}
	delete(fixture, "unexpected")
	raw := append(encodeResearchV6PlanFixture(t, fixture), []byte(` {}`)...)
	if _, _, err = DecodeAndValidateResearchV6PlanResult(raw); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("trailing JSON err=%v", err)
	}
	fixture = validResearchV6PlanFixture()
	fixture["hypotheses"].([]any)[0].(map[string]any)["confidence_low"] = nil
	if _, _, err = DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "cannot be null") {
		t.Fatalf("optional null err=%v", err)
	}
}

func TestDecodeAndValidateResearchV6PlanResultRequiresFieldsAndReferences(t *testing.T) {
	fixture := validResearchV6PlanFixture()
	delete(fixture, "hypotheses")
	if _, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "requires hypotheses") {
		t.Fatalf("missing hypotheses err=%v", err)
	}

	fixture = validResearchV6PlanFixture()
	question := fixture["questions"].([]any)[0].(map[string]any)
	delete(question, "required")
	if _, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) {
		t.Fatalf("missing required err=%v", err)
	}

	fixture = validResearchV6PlanFixture()
	task := fixture["tasks"].([]any)[0].(map[string]any)
	task["targets"] = []any{map[string]any{"kind": "hypothesis", "key": "h.missing"}}
	if _, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "unresolved target") {
		t.Fatalf("unresolved target err=%v", err)
	}
}

func TestDecodeAndValidateResearchV6PlanResultRejectsInquiryCycle(t *testing.T) {
	fixture := validResearchV6PlanFixture()
	fixture["inquiry_edges"] = []any{
		map[string]any{"client_key": "edge.forward", "from": map[string]any{"kind": "question", "key": "q.root"}, "to": map[string]any{"kind": "hypothesis", "key": "h.primary"}, "relation": "depends_on", "rationale": "Forward dependency"},
		map[string]any{"client_key": "edge.reverse", "from": map[string]any{"kind": "hypothesis", "key": "h.primary"}, "to": map[string]any{"kind": "question", "key": "q.root"}, "relation": "refines", "rationale": "Reverse dependency"},
	}
	if _, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "inquiry dependency cycle") {
		t.Fatalf("inquiry cycle err=%v", err)
	}
}

func TestDecodeAndValidateResearchV6PlanResultRejectsCyclesAndOverBudget(t *testing.T) {
	fixture := validResearchV6PlanFixture()
	fixture["branches"] = []any{
		map[string]any{"client_key": "b.one", "parent_branch_key": "b.two", "objective": "One", "entry_conditions": []any{"open"}, "exit_conditions": []any{"done"}, "budget_share": 0.4},
		map[string]any{"client_key": "b.two", "parent_branch_key": "b.one", "objective": "Two", "entry_conditions": []any{"open"}, "exit_conditions": []any{"done"}, "budget_share": 0.4},
	}
	if _, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("branch cycle err=%v", err)
	}

	fixture = validResearchV6PlanFixture()
	fixture["branches"] = append(fixture["branches"].([]any), map[string]any{"client_key": "b.second", "objective": "Second", "entry_conditions": []any{"open"}, "exit_conditions": []any{"done"}, "budget_share": 0.5})
	if _, _, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture)); !errors.Is(err, ErrInvalidResult) || !strings.Contains(err.Error(), "budget_share") {
		t.Fatalf("budget err=%v", err)
	}
}

func TestDecodeAndValidateResearchV6PlanResultHashIsSemantic(t *testing.T) {
	fixture := validResearchV6PlanFixture()
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	_, first, err := DecodeAndValidateResearchV6PlanResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, second, err := DecodeAndValidateResearchV6PlanResult(encodeResearchV6PlanFixture(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("semantic hashes differ: %s != %s", first, second)
	}
}
