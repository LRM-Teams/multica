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
		"## 持久化 Research V6 Work Item",
		"Run ID：`00000000-0000-4000-8000-000000000003`",
		"Work Item ID：`00000000-0000-4000-8000-000000000212`",
		"Attempt ID：`00000000-0000-4000-8000-000000000213`",
		"预期结果：`director_action_proposal`",
		"multica research work-manifest 00000000-0000-4000-8000-000000000003 00000000-0000-4000-8000-000000000212 00000000-0000-4000-8000-000000000213",
		"multica research director-brief 00000000-0000-4000-8000-000000000003 00000000-0000-4000-8000-000000000212 00000000-0000-4000-8000-000000000213",
		"multica research director-brief-ack",
		"提交必须使用下面的精确根结构",
		`"contract_kind": "director_action_proposal"`,
		`"director_assignment_id": "<brief.director_assignment_id>"`,
		`"reviewed_page_count": <brief.page.page_count>`,
		`"payload_schema": "<one manifest.task_specific_schema.payload_schemas key>"`,
		"把每页确认 POST 到 `${V6_API}/director-brief-acks`",
		"精确结果文件 POST 到 `${V6_API}/submission`",
		"通常返回状态 `received`",
		"没有仅校验或 dry-run 模式",
		"不得发送探测、占位或最小测试 payload",
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

func TestRonaldoV6DirectorProtocolRequiresParallelChineseResearch(t *testing.T) {
	for _, want := range []string{
		"所有自然语言输出",
		"多个独立方向",
		"独立子 Branch",
		"不得使用根 Branch",
		"同一 proposal",
		"payload_schema_id 绝不能使用 no_op.v1",
		"payload.task_specific_schema",
		"纠正并重新提交被拒绝的 Work 分配",
		"不得叙述合同查找",
		"不得自行暂停整场调研",
		"失败分类和诊断",
		"待回答问题",
		"不得在存在高价值待回答问题",
		"action.kind 必须是 create_work_item",
		"action.payload_schema 必须是 work.create.v1",
		"action.payload.kind 必须是 research",
		"action.payload.expected_result_schema_id 必须是 atomic_result_submission",
		"action.kind 必须是 create_integration",
		"action.payload_schema 必须是 integration.create.v1",
		"S promotion 为 M",
		"全体同意后自动创建 integration Work",
		"持续推进 S→M→L→XL→XXL",
		"一级研究方向总数不得超过 max_parallel_tasks",
	} {
		if !strings.Contains(RonaldoV6DirectorSystemProtocol, want) {
			t.Fatalf("director protocol missing %q", want)
		}
	}
}

func TestBuildV6WorkDispatchPromptMakesDiscussionExecutable(t *testing.T) {
	manifest := validV6DispatchPromptManifest(t, map[string]any{
		"expected_result_schema": string(V6ContractDiscussionTurnSubmission),
		"task_specific_schema": map[string]any{
			"task_context": map[string]any{
				"discussion_id":       "00000000-0000-4000-8000-000000000301",
				"discussion_revision": 1,
				"input_set_hash":      "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			},
		},
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"contract_kind": "discussion_turn_submission"`,
		`"discussion_id": "00000000-0000-4000-8000-000000000301"`,
		`"discussion_revision": 1`,
		`"vote":"<accept|reject|uncertain>"`,
		"不得因为数量足够就同意融合",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("discussion prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestBuildV6WorkDispatchPromptMakesIntegrationExecutable(t *testing.T) {
	manifest := validV6DispatchPromptManifest(t, map[string]any{
		"expected_result_schema": string(V6ContractIntegrationSubmission),
		"branch_refs": []any{
			map[string]any{"id": "00000000-0000-4000-8000-000000000302", "state_version": 2},
		},
		"task_specific_schema": map[string]any{
			"input_nodes": []any{
				map[string]any{"kind": "result_s", "id": "00000000-0000-4000-8000-000000000303", "version_id": "00000000-0000-4000-8000-000000000304", "tier": "S", "content_hash": "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"},
				map[string]any{"kind": "result_s", "id": "00000000-0000-4000-8000-000000000305", "version_id": "00000000-0000-4000-8000-000000000306", "tier": "S", "content_hash": "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"},
			},
			"task_context": map[string]any{
				"discussion_id":       "00000000-0000-4000-8000-000000000301",
				"discussion_revision": 1,
				"input_set_hash":      "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
			},
		},
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"contract_kind": "integration_submission"`,
		`"mode": "<promotion|assimilation|xxl_merge>"`,
		`"output_tier": "<M|L|XL|XXL>"`,
		`"steward_agent_id": "00000000-0000-4000-8000-000000000009"`,
		"不得改写 Manifest 冻结的 input_nodes",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("integration prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestV6WorkPromptKeepsUserFacingProgressInChinese(t *testing.T) {
	manifest := validV6DispatchPromptManifest(t, map[string]any{
		"expected_result_schema": string(V6ContractAtomicResultSubmission),
		"task_id":                "00000000-0000-4000-8000-000000000214",
		"mission_prompt":         "调研浏览器游戏的市场与技术可行性。",
		"task_specific_schema": map[string]any{"payload_schemas": map[string]any{
			"research.finding.v1": map[string]any{"type": "object"},
		}},
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, "所有自然语言输出") {
		t.Fatal("work prompt does not constrain user-facing progress language")
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
		"task_specific_schema": map[string]any{"payload_schemas": map[string]any{
			"research.finding.v1": map[string]any{"type": "object"},
		}},
	})
	prompt, err := BuildV6WorkDispatchPrompt(manifest)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"contract_kind": "atomic_result_submission"`,
		`"task_id": "00000000-0000-4000-8000-000000000214"`,
		`"agent_id": "00000000-0000-4000-8000-000000000009"`,
		`"task_specific_schema": "research.finding.v1"`,
		"catalog_summary` 最多 512 个字符",
		"content_layers.conflicts`",
		"字符串数组",
		"task_specific_payload` 内同名字段遵循冻结的任务 schema",
		"RFC 8785 JCS",
		"Agent 已成功交接",
		"没有仅校验或 dry-run 模式",
		"不得发送探测、占位或最小测试 payload",
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
	branchID := seedV6WorkBranchScope(t, run, workItemID, "manifest-branch:", "Inspect the assigned branch", 7)
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
		TaskID        string        `json:"task_id"`
		MissionPrompt string        `json:"mission_prompt"`
		BranchRefs    []V6BranchRef `json:"branch_refs"`
		TaskSchema    struct {
			PayloadSchemas map[string]json.RawMessage `json:"payload_schemas"`
		} `json:"task_specific_schema"`
	}
	if err = json.Unmarshal(manifest, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.TaskID != taskID || identity.MissionPrompt != "Inspect the assigned production boundary." {
		t.Fatalf("manifest task=%q mission=%q", identity.TaskID, identity.MissionPrompt)
	}
	if len(identity.BranchRefs) != 1 || identity.BranchRefs[0].ID != branchID || identity.BranchRefs[0].StateVersion != 7 {
		t.Fatalf("manifest branch refs=%+v", identity.BranchRefs)
	}
	if len(identity.TaskSchema.PayloadSchemas["research.finding.v1"]) == 0 {
		t.Fatalf("manifest task schema registry=%v", identity.TaskSchema.PayloadSchemas)
	}
	var count int
	if err = run.pool.QueryRow(run.ctx, `SELECT count(*)::int FROM research_task WHERE work_item_id=$1::uuid`, workItemID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("backing task count=%d want=1", count)
	}
}

func TestV6BackingTaskMirrorsWorkItemLifecycle(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Mirror atomic V6 Work lifecycle")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	_, workItemID := seedV6RecoveryWorkItem(t, run, "ready", time.Now().Add(time.Minute))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item SET expected_result_schema_id='atomic_result_submission',payload_schema_id='research.finding.v1' WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := ensureV6BackingTaskTx(run.ctx, tx, workItemID)
	if err != nil {
		_ = tx.Rollback(run.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}

	for _, transition := range []struct {
		workStatus string
		taskStatus string
	}{
		{workStatus: "ready", taskStatus: "ready"},
		{workStatus: "dispatching", taskStatus: "dispatching"},
		{workStatus: "running", taskStatus: "running"},
		{workStatus: "cancelled", taskStatus: "cancelled"},
		{workStatus: "ready", taskStatus: "ready"},
		{workStatus: "dispatching", taskStatus: "dispatching"},
		{workStatus: "running", taskStatus: "running"},
		{workStatus: "succeeded", taskStatus: "succeeded"},
	} {
		if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item SET status=$2,updated_at=now() WHERE id=$1::uuid`, workItemID, transition.workStatus); err != nil {
			t.Fatalf("set Work Item status %q: %v", transition.workStatus, err)
		}
		var got string
		if err = run.pool.QueryRow(run.ctx, `SELECT status FROM research_task WHERE id=$1::uuid`, taskID).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != transition.taskStatus {
			t.Fatalf("Work Item status %q projected Task status %q, want %q", transition.workStatus, got, transition.taskStatus)
		}
	}
}

func TestV6RunPauseCancelAndArchiveFollowWorkItemAuthority(t *testing.T) {
	run := newTransactionRecoveryRun(t, "Transition V6 Run with backing Task")
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_session SET orchestrator_version='research-run-v6' WHERE id=$1::uuid`, run.fixture.sessionID); err != nil {
		t.Fatal(err)
	}
	membershipID, workItemID := seedV6RecoveryWorkItem(t, run, "running", time.Now().Add(time.Minute))
	if _, err := run.pool.Exec(run.ctx, `UPDATE research_work_item SET expected_result_schema_id='atomic_result_submission',payload_schema_id='research.finding.v1' WHERE id=$1::uuid`, workItemID); err != nil {
		t.Fatal(err)
	}
	tx, err := run.pool.Begin(run.ctx)
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := ensureV6BackingTaskTx(run.ctx, tx, workItemID)
	if err != nil {
		_ = tx.Rollback(run.ctx)
		t.Fatal(err)
	}
	if err = tx.Commit(run.ctx); err != nil {
		t.Fatal(err)
	}
	attemptID := seedV6RecoveryAttempt(t, run, membershipID, workItemID)
	inboxTaskID := uuid.NewString()
	if _, err = run.pool.Exec(run.ctx, `
		INSERT INTO agent_inbox_event (id,workspace_id,agent_id,reason,requires_wake,status,seq_from,seq_to)
		VALUES ($1::uuid,$2::uuid,$3::uuid,'quick_create',true,'draining',0,0)
	`, inboxTaskID, run.fixture.workspaceID, run.fixture.agentID); err != nil {
		t.Fatal(err)
	}
	if _, err = run.pool.Exec(run.ctx, `UPDATE research_work_item_attempt SET inbox_task_id=$2::uuid WHERE id=$1::uuid`, attemptID, inboxTaskID); err != nil {
		t.Fatal(err)
	}

	dispatcher := &recordingCancellationDispatcher{}
	engine := newEngine(run.store, dispatcher, nil)
	paused, err := engine.Pause(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID)
	if err != nil {
		t.Fatal(err)
	}
	if paused.Status != RunStatusPaused {
		t.Fatalf("paused Run status=%q", paused.Status)
	}
	if len(dispatcher.cancelled) != 1 || dispatcher.cancelled[0] != inboxTaskID {
		t.Fatalf("cancelled Inbox tasks=%v want=%s", dispatcher.cancelled, inboxTaskID)
	}
	for label, check := range map[string]struct {
		table string
		id    string
		want  string
	}{
		"Work Item": {table: "research_work_item", id: workItemID, want: "ready"},
		"Task":      {table: "research_task", id: taskID, want: "ready"},
		"Attempt":   {table: "research_work_item_attempt", id: attemptID, want: "cancelled"},
	} {
		var got string
		if err = run.pool.QueryRow(run.ctx, "SELECT status FROM "+check.table+" WHERE id=$1::uuid", check.id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != check.want {
			t.Fatalf("%s status=%q want=%q", label, got, check.want)
		}
	}

	if _, _, err = run.store.Resume(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID); err != nil {
		t.Fatal(err)
	}
	cancelled, _, _, err := run.store.Cancel(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID, "user deleted")
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != RunStatusCancelled {
		t.Fatalf("cancelled Run status=%q", cancelled.Status)
	}
	archived, _, _, err := run.store.Archive(run.ctx, run.fixture.sessionID, run.fixture.workspaceID, run.fixture.userID, "hide from list")
	if err != nil {
		t.Fatal(err)
	}
	if archived.Status != RunStatusArchived {
		t.Fatalf("archived Run status=%q", archived.Status)
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
