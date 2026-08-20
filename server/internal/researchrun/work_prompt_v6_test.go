package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildV6WorkDispatchPromptMakesDirectorAssignmentExecutable(t *testing.T) {
	manifest := validV6DispatchPromptManifest(t, map[string]any{
		"expected_result_schema": string(V6ContractDirectorActionProposal),
		"mission_prompt":         "Review the durable brief and propose the next actions.",
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## Durable Research V6 Work Item",
		"Run ID: `00000000-0000-4000-8000-000000000003`",
		"Work Item ID: `00000000-0000-4000-8000-000000000212`",
		"Attempt ID: `00000000-0000-4000-8000-000000000213`",
		"Expected result: `director_action_proposal`",
		"multica research work-manifest 00000000-0000-4000-8000-000000000003 00000000-0000-4000-8000-000000000212 00000000-0000-4000-8000-000000000213",
		"multica research director-brief 00000000-0000-4000-8000-000000000003 00000000-0000-4000-8000-000000000212 00000000-0000-4000-8000-000000000213",
		"multica research director-brief-ack",
		"multica research work-submit",
		"Review the durable brief and propose the next actions.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if prompt == "Review the durable brief and propose the next actions." {
		t.Fatal("dispatch regressed to the mission-only prompt")
	}
}

func TestBuildV6WorkDispatchPromptIncludesCatalogAndReportUploadPaths(t *testing.T) {
	manifest := validV6DispatchPromptManifest(t, map[string]any{
		"expected_result_schema": string(V6ContractReportPackageSubmission),
		"mission_prompt":         "Publish the verified report package.",
		"catalog_access": map[string]any{
			"same_tier": "S", "higher_candidate_branch_ids": []string{},
			"include_higher_candidates": true, "through_event_sequence": 47, "page_size": 128,
		},
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"multica research work-catalog", "multica research work-catalog-ack", "multica research report-upload"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func validV6DispatchPromptManifest(t *testing.T, overrides map[string]any) V6WorkManifest {
	t.Helper()
	value := map[string]any{
		"contract_kind": "work_manifest", "schema_version": 6,
		"manifest_id":       "00000000-0000-4000-8000-000000000211",
		"workspace_id":      "00000000-0000-4000-8000-000000000002",
		"run_id":            "00000000-0000-4000-8000-000000000003",
		"work_item_id":      "00000000-0000-4000-8000-000000000212",
		"attempt_id":        "00000000-0000-4000-8000-000000000213",
		"assigned_agent_id": "00000000-0000-4000-8000-000000000009",
		"goal":              map[string]any{"goal_version": 2, "goal": "Test", "scope": map[string]any{}, "audience": "", "freshness": "", "language": "en", "source_policy": map[string]any{}},
		"branch_refs":       []any{}, "runtime_protocol_version": "research-run-v6-runtime-v1",
		"mission_prompt": "Test", "expected_result_schema": string(V6ContractDirectorActionProposal),
		"artifacts": []any{}, "through_state_version": 13, "through_event_sequence": 47,
	}
	for key, item := range overrides {
		value[key] = item
	}
	canonical, err := marshalV6CanonicalJSON(value)
	if err != nil {
		t.Fatal(err)
	}
	value["manifest_hash"] = ArtifactContentHashFromCanonicalJSON(canonical)
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return V6WorkManifest{Bytes: raw}
}
