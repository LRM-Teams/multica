package researchrun

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
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
		"The submission must use this exact root shape",
		`"contract_kind": "director_action_proposal"`,
		`"director_assignment_id": "<brief.director_assignment_id>"`,
		`"reviewed_page_count": <brief.page.page_count>`,
		`"payload_schema": "<one manifest.task_specific_schema.payload_schemas key>"`,
		"POST each acknowledgement to `${V6_API}/director-brief-acks`",
		"POST the exact result file to `${V6_API}/submission`",
		"normally returns status `received`",
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
	if strings.Contains(prompt, "GET `/catalog`") || strings.Contains(prompt, "${V6_API}/catalog?") {
		t.Fatal("Director prompt advertised catalog access that was not authorized")
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

func TestBuildV6WorkDispatchPromptBindsAtomicTaskIdentity(t *testing.T) {
	manifest := validV6DispatchPromptManifest(t, map[string]any{
		"expected_result_schema": string(V6ContractAtomicResultSubmission),
		"task_id":                "00000000-0000-4000-8000-000000000214",
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"contract_kind": "atomic_result_submission"`,
		`"task_id": "00000000-0000-4000-8000-000000000214"`,
		`"agent_id": "00000000-0000-4000-8000-000000000009"`,
		"RFC 8785 JCS",
		"successful Agent handoff",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestAtomicV6WorkManifestGetsOneBackingTaskAndFrozenMission(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Bind atomic V6 Work")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, workItemID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(time.Minute))
	payload := `{"mission_prompt":"Inspect the assigned production boundary.","task_specific_schema":{"type":"object","additionalProperties":false,"required":["finding"],"properties":{"finding":{"type":"string","minLength":1}}}}`
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item SET expected_result_schema_id='atomic_result_submission',payload_schema_id='research.finding.v1',payload=$2::jsonb,reason='fallback reason' WHERE id=$1::uuid`, workItemID, payload); err != nil {
		t.Fatal(err)
	}
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(run.ctx)
	taskID, err := ensureV6BackingTaskTx(run.ctx, tx, workItemID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, _, err := compileV6WorkManifestTx(run.ctx, tx, run.fixture.workspaceID, run.fixture.sessionID, workItemID,
		uuid.NewString(), uuid.NewString(), run.fixture.agentID, "Inspect the assigned production boundary.", string(V6ContractAtomicResultSubmission), run.goalVersion, 1, 1, json.RawMessage(payload))
	if err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	var identity struct {
		TaskID        string `json:"task_id"`
		MissionPrompt string `json:"mission_prompt"`
	}
	if err = json.Unmarshal(manifest, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.TaskID != taskID || identity.MissionPrompt != "Inspect the assigned production boundary." {
		t.Fatalf("manifest task=%q mission=%q", identity.TaskID, identity.MissionPrompt)
	}
	var count int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_task WHERE work_item_id=$1::uuid`, workItemID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backing task count=%d want=1", count)
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
