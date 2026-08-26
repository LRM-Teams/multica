package researchrun

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestDirectorAtomicWorkSchemaMatchesExecutionContract(t *testing.T) {
	registry, err := json.Marshal(map[string]any{"payload_schemas": v6DirectorActionPayloadSchemas()})
	if err != nil {
		t.Fatal(err)
	}
	validator := boundV6SecondStage{schema: registry}
	valid := map[string]any{
		"kind": "research", "assignee_agent_id": uuid.NewString(), "mission": "核验一个独立研究方向。",
		"expected_result_schema_id": "atomic_result_submission", "payload_schema_id": "research.atomic_findings.v1",
		"payload":  map[string]any{"task_specific_schema": map[string]any{"type": "object"}},
		"priority": 0.9, "max_attempts": 2, "branch_ids": []string{uuid.NewString()},
	}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	if err = validator.ValidateV6Payload("work.create.v1", raw); err != nil {
		t.Fatalf("valid atomic Work rejected: %v", err)
	}

	for _, test := range []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{name: "branch is required", mutate: func(value map[string]any) { delete(value, "branch_ids") }, wantErr: "branch_ids"},
		{name: "one branch only", mutate: func(value map[string]any) { value["branch_ids"] = []string{uuid.NewString(), uuid.NewString()} }, wantErr: "exceeds 1 items"},
		{name: "result validator is required", mutate: func(value map[string]any) { value["payload"] = map[string]any{} }, wantErr: "task_specific_schema"},
		{name: "result validator is non-empty", mutate: func(value map[string]any) {
			value["payload"] = map[string]any{"task_specific_schema": map[string]any{}}
		}, wantErr: "at least 1 properties"},
		{name: "research schema id only", mutate: func(value map[string]any) { value["payload_schema_id"] = "no_op.v1" }, wantErr: "required pattern"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			test.mutate(value)
			candidate, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			err = validator.ValidateV6Payload("work.create.v1", candidate)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("error=%v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestDirectorReportSchemaAcceptsOnlyServerOwnedReportIntent(t *testing.T) {
	registry, err := json.Marshal(map[string]any{"payload_schemas": v6DirectorActionPayloadSchemas()})
	if err != nil {
		t.Fatal(err)
	}
	validator := boundV6SecondStage{schema: registry}
	if err = validator.ValidateV6Payload("report.create.v1", json.RawMessage(`{"title":"阶段性调研报告"}`)); err != nil {
		t.Fatalf("title-only report intent rejected: %v", err)
	}
	for _, candidate := range []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"title":"阶段性调研报告","assignee_agent_id":"00000000-0000-4000-8000-000000000001"}`),
		json.RawMessage(`{"title":"阶段性调研报告","inputs":[]}`),
	} {
		if err = validator.ValidateV6Payload("report.create.v1", candidate); err == nil {
			t.Fatalf("invalid Director-owned report details accepted: %s", candidate)
		}
	}
}
