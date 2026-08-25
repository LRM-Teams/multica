package researchrun

import (
	"encoding/json"
	"fmt"
	"strings"
)

type v6WorkDispatchIdentity struct {
	WorkspaceID    string         `json:"workspace_id"`
	ManifestID     string         `json:"manifest_id"`
	ManifestHash   string         `json:"manifest_hash"`
	RunID          string         `json:"run_id"`
	WorkItemID     string         `json:"work_item_id"`
	AttemptID      string         `json:"attempt_id"`
	MissionPrompt  string         `json:"mission_prompt"`
	ExpectedResult V6ContractKind `json:"expected_result_schema"`
	TaskID         string         `json:"task_id"`
	AssignedAgent  string         `json:"assigned_agent_id"`
	Goal           struct {
		GoalVersion int `json:"goal_version"`
	} `json:"goal"`
	BranchRefs    []V6BranchRef   `json:"branch_refs"`
	CatalogAccess json.RawMessage `json:"catalog_access"`
	TaskSchema    json.RawMessage `json:"task_specific_schema"`
}

type v6DispatchContext struct {
	InputNodes  []V6NodeRef     `json:"input_nodes"`
	TaskContext json.RawMessage `json:"task_context"`
}

// BuildV6WorkDispatchPrompt turns a frozen Work Manifest into an executable
// task prompt. The manifest remains the authority; the prompt only exposes the
// task-bound CLI sequence needed to read, acknowledge, and submit it.
func BuildV6WorkDispatchPrompt(manifest V6WorkManifest) (string, error) {
	decoded, err := DecodeV6Contract(manifest.Bytes, V6ContractWorkManifest, nil)
	if err != nil {
		return "", err
	}
	var identity v6WorkDispatchIdentity
	if err = json.Unmarshal(decoded.Envelope, &identity); err != nil {
		return "", fmt.Errorf("%w: decode V6 work identity", ErrInvalidContract)
	}
	var dispatchContext v6DispatchContext
	if len(identity.TaskSchema) > 0 && string(identity.TaskSchema) != "null" {
		if err = json.Unmarshal(identity.TaskSchema, &dispatchContext); err != nil {
			return "", fmt.Errorf("%w: decode V6 dispatch context", ErrInvalidContract)
		}
	}
	if strings.TrimSpace(identity.RunID) == "" || strings.TrimSpace(identity.WorkItemID) == "" || strings.TrimSpace(identity.AttemptID) == "" ||
		strings.TrimSpace(identity.ManifestID) == "" || strings.TrimSpace(identity.ManifestHash) == "" || strings.TrimSpace(string(identity.ExpectedResult)) == "" {
		return "", fmt.Errorf("%w: incomplete V6 work identity", ErrInvalidContract)
	}
	if identity.ExpectedResult == V6ContractAtomicResultSubmission && strings.TrimSpace(identity.TaskID) == "" {
		return "", fmt.Errorf("%w: atomic V6 Work Item has no Task identity", ErrInvalidContract)
	}

	base := fmt.Sprintf("%s %s %s", identity.RunID, identity.WorkItemID, identity.AttemptID)
	var prompt strings.Builder
	prompt.WriteString("## 持久化 Research V6 Work Item\n\n")
	prompt.WriteString("使用 `multica-research-fleet` skill。这是与任务绑定的 V6 派发；仅在聊天中回复不能完成任务。\n\n")
	prompt.WriteString("最高优先级语言要求：从收到任务到结束，所有自然语言输出，包括执行进度、智能体之间的消息、分析说明、错误说明和最终摘要，都必须使用简体中文。不得用英文叙述“我将……”“让我……”或工具探查过程。只有协议字段、JSON key、枚举值、命令、代码、专有名词和来源原文保持原样；冻结 Manifest 明确要求其他语言时除外。\n\n")
	prompt.WriteString("面向用户的输出不得叙述 Manifest 查找、标识符、JSON 拼装、CLI 命令、工具调用或隐藏推理。持久提交返回 received 后，只报告一段简短的中文摘要，说明已完成的调研和仍存在的不确定性。\n\n")
	fmt.Fprintf(&prompt, "- Run ID：`%s`\n- Work Item ID：`%s`\n- Attempt ID：`%s`\n", identity.RunID, identity.WorkItemID, identity.AttemptID)
	fmt.Fprintf(&prompt, "- Manifest ID：`%s`\n- Manifest hash：`%s`\n- 预期结果：`%s`\n\n", identity.ManifestID, identity.ManifestHash, identity.ExpectedResult)
	prompt.WriteString("首先读取冻结的权威 Manifest：\n\n```bash\nmultica research work-manifest " + base + " --output json\n```\n\n")
	prompt.WriteString("如果守护进程中安装的 CLI 不认识 V6 命令，使用其凭据代理；不得读取或暴露任何 token：\n\n")
	prompt.WriteString("```bash\nV6_API=\"http://127.0.0.1:${MULTICA_DAEMON_PORT}/api/agent/research/sessions/" + identity.RunID + "/work-items/" + identity.WorkItemID + "/attempts/" + identity.AttemptID + "\"\n")
	prompt.WriteString("V6_CURL=(curl -fsS -H \"X-Agent-ID: ${MULTICA_AGENT_ID}\" -H \"X-Workspace-ID: ${MULTICA_WORKSPACE_ID}\")\n")
	prompt.WriteString("\"${V6_CURL[@]}\" \"${V6_API}/manifest\"\n```\n\n")
	prompt.WriteString("该回退路径与 CLI 使用相同的任务级授权。写入 JSON 时设置 `Content-Type: application/json`，提交文件时使用 `--data-binary @file`。\n\n")
	prompt.WriteString("持续报告实时进度，供用户了解当前阶段。读取 Manifest 后立即报告一次；此后每次进入新阶段（读取 Brief 或 catalog、搜索、阅读来源、分析、起草、验证）时，向 `${V6_API}/progress` POST 一条简体中文进度（最多 240 个字符）：\n\n")
	prompt.WriteString("```bash\n\"${V6_CURL[@]}\" -X POST -H 'Content-Type: application/json' \\\n  -d '{\"client_request_id\":\"'\"$(uuidgen | tr A-Z a-z)\"'\",\"text\":\"<正在进行的工作，用简体中文>\",\"stage\":\"<短 key，例如 searching>\"}' \\\n  \"${V6_API}/progress\"\n```\n\n")
	prompt.WriteString("进度说明不会结算 Work Item。进度 POST 失败不得阻塞任务或形成重试循环；忽略错误并继续。每条被接受的进度也会延长 Work Item 租约，因此长回合至少每 15 分钟报告一次，否则租约可能在工作期间过期。\n\n")
	if identity.ExpectedResult == V6ContractDirectorActionProposal {
		prompt.WriteString(RonaldoV6DirectorSystemProtocol + "\n\n")
		prompt.WriteString("读取每一页 Director Brief，使用每页的精确 ID 和 hash 逐页确认，然后提交 proposal：\n\n")
		prompt.WriteString("```bash\nmultica research director-brief " + base + " --output json\nmultica research director-brief-ack " + base + " --client-request-id <uuid> --brief-id <brief-id> --brief-hash <brief-hash> --page-key <page-key> --page-hash <page-hash> --output json\n```\n\n")
		prompt.WriteString("使用凭据代理回退时，GET `${V6_API}/director-brief`；存在 `next_cursor` 时追加 `?cursor=<next_cursor>`。把每页确认 POST 到 `${V6_API}/director-brief-acks`。确认 JSON 只能包含 `client_request_id`、`brief_id`、`brief_hash`、`page_key` 和 `page_hash`。\n\n")
		prompt.WriteString("提交必须使用下面的精确根结构；替换所有尖括号占位值，最终 JSON 中不得保留尖括号：\n\n")
		prompt.WriteString("```json\n{\n")
		prompt.WriteString("  \"contract_kind\": \"director_action_proposal\",\n  \"schema_version\": 6,\n  \"client_request_id\": \"<new-uuid>\",\n")
		fmt.Fprintf(&prompt, "  \"workspace_id\": \"<manifest.workspace_id>\",\n  \"run_id\": \"%s\",\n  \"work_item_id\": \"%s\",\n  \"attempt_id\": \"%s\",\n", identity.RunID, identity.WorkItemID, identity.AttemptID)
		fmt.Fprintf(&prompt, "  \"manifest_id\": \"%s\",\n  \"manifest_hash\": \"%s\",\n", identity.ManifestID, identity.ManifestHash)
		prompt.WriteString("  \"director_assignment_id\": \"<brief.director_assignment_id>\",\n  \"director_generation\": <brief.director_generation>,\n  \"brief_id\": \"<brief.brief_id>\",\n  \"brief_hash\": \"<brief.brief_hash>\",\n  \"reviewed_page_count\": <brief.page.page_count>,\n  \"expected_state_version\": <brief.state_version>,\n  \"through_event_sequence\": <brief.through_event_sequence>,\n  \"actions\": [\n    {\n      \"action_id\": \"<stable-key>\",\n      \"kind\": \"<allowed-kind>\",\n      \"reason\": \"<reason>\",\n      \"idempotency_key\": \"<stable-key>\",\n      \"expected_state_version\": <brief.state_version>,\n      \"payload_schema\": \"<one manifest.task_specific_schema.payload_schemas key>\",\n      \"payload\": <object matching that payload schema>,\n      \"depends_on_action_ids\": []\n    }\n  ]\n}\n```\n\n")
		prompt.WriteString("只能使用根合同允许的 action kind，以及 `manifest.task_specific_schema.payload_schemas` 中存在的 payload schema。新申请的 Agent 采用异步加入，不能在同一个 proposal 中接收 Work；先创建它，等待 joined 事件和下一次 Director cycle。分配给现有团队成员的 Work 可以立即创建。原子 Work 必须把 `expected_result_schema_id` 设为 `atomic_result_submission`，选择非空 `payload_schema_id`，并把该结果校验器放在 `payload.task_specific_schema`。如果没有有效状态变更，提交一个 `payload_schema` 为 `no_op.v1`、payload 为 `{\"reason\":\"<reason>\"}` 的 `no_op` action。\n\n")
	}
	if identity.ExpectedResult == V6ContractAtomicResultSubmission {
		taskSchemaID := "<one manifest.task_specific_schema.payload_schemas key>"
		var registry struct {
			PayloadSchemas map[string]json.RawMessage `json:"payload_schemas"`
		}
		if json.Unmarshal(identity.TaskSchema, &registry) == nil && len(registry.PayloadSchemas) == 1 {
			for schemaID := range registry.PayloadSchemas {
				taskSchemaID = schemaID
			}
		}
		prompt.WriteString("原子提交的根对象必须精确包含下列合同字段。复制身份与冻结引用，不得编造旧版 Task ID：\n\n")
		prompt.WriteString("```json\n{\n  \"contract_kind\": \"atomic_result_submission\",\n  \"schema_version\": 6,\n  \"client_request_id\": \"<new-uuid>\",\n")
		fmt.Fprintf(&prompt, "  \"workspace_id\": \"%s\",\n  \"run_id\": \"%s\",\n  \"work_item_id\": \"%s\",\n  \"task_id\": \"%s\",\n  \"attempt_id\": \"%s\",\n  \"agent_id\": \"%s\",\n  \"manifest_id\": \"%s\",\n  \"manifest_hash\": \"%s\",\n  \"goal_version\": %d,\n", identity.WorkspaceID, identity.RunID, identity.WorkItemID, identity.TaskID, identity.AttemptID, identity.AssignedAgent, identity.ManifestID, identity.ManifestHash, identity.Goal.GoalVersion)
		fmt.Fprintf(&prompt, "  \"branch_refs\": <manifest.branch_refs>,\n  \"content_layers\": {\"catalog_summary\":\"<summary>\",\"brief_summary\":\"<summary>\",\"objective\":\"<objective>\",\"conclusion\":\"<conclusion>\",\"content\":\"<content>\",\"scope\":{},\"uncertainties\":[],\"conflicts\":[],\"open_questions\":[]},\n  \"evidence_refs\": <only frozen manifest artifact versions actually used>,\n  \"state_proposal\": {\"conclusion_state\":\"proposed\",\"integration_state\":\"candidate\"},\n  \"related_candidates\": [],\n  \"task_specific_schema\": \"%s\",\n  \"task_specific_payload\": <object matching the schema under that exact manifest key>,\n  \"content_hash\": \"sha256:<RFC-8785-hash>\"\n}\n```\n\n", taskSchemaID)
		prompt.WriteString("必须使用 `manifest.task_specific_schema.payload_schemas` 下唯一的 key，不得编造或重命名 schema ID。`content_layers.catalog_summary` 最多 512 个字符。根层的 `content_layers.uncertainties`、`content_layers.conflicts` 和 `content_layers.open_questions` 是字符串数组；`task_specific_payload` 内同名字段遵循冻结的任务 schema，可能包含对象。计算 `content_hash` 时只移除 `content_hash` 字段，用 RFC 8785 JCS 规范化其余对象，对所得字节做 SHA-256，然后写入小写 `sha256:<64-hex>`。提交前必须读取并确认实际使用的每一页 catalog。\n\n")
	}
	if identity.ExpectedResult == V6ContractDiscussionTurnSubmission {
		var discussion struct {
			DiscussionID       string `json:"discussion_id"`
			DiscussionRevision int    `json:"discussion_revision"`
			InputSetHash       string `json:"input_set_hash"`
		}
		if json.Unmarshal(dispatchContext.TaskContext, &discussion) != nil || discussion.DiscussionID == "" || discussion.DiscussionRevision < 1 || discussion.InputSetHash == "" {
			return "", fmt.Errorf("%w: incomplete V6 discussion identity", ErrInvalidContract)
		}
		prompt.WriteString("你是候选节点的当前 Steward。逐项比较 Manifest 中冻结的 input_nodes，判断它们是否存在足够的共同语义与新增价值；不得因为数量足够就同意融合。提交下面的精确结构：\n\n")
		prompt.WriteString("```json\n{\n  \"contract_kind\": \"discussion_turn_submission\",\n  \"schema_version\": 6,\n  \"client_request_id\": \"<new-uuid>\",\n")
		fmt.Fprintf(&prompt, "  \"workspace_id\": \"%s\",\n  \"run_id\": \"%s\",\n  \"work_item_id\": \"%s\",\n  \"attempt_id\": \"%s\",\n  \"manifest_id\": \"%s\",\n  \"manifest_hash\": \"%s\",\n  \"discussion_id\": \"%s\",\n  \"discussion_revision\": %d,\n  \"input_set_hash\": \"%s\",\n  \"agent_id\": \"%s\",\n", identity.WorkspaceID, identity.RunID, identity.WorkItemID, identity.AttemptID, identity.ManifestID, identity.ManifestHash, discussion.DiscussionID, discussion.DiscussionRevision, discussion.InputSetHash, identity.AssignedAgent)
		prompt.WriteString("  \"visible_message\": \"<面向用户可见的中文判断>\",\n  \"contribution\": {\"common_findings\":[],\"unique_findings\":[],\"conflicts\":[],\"scope\":{},\"omissions\":[],\"vote\":\"<accept|reject|uncertain>\",\"reason\":\"<中文理由>\"},\n  \"evidence_refs\": []\n}\n```\n\n")
		prompt.WriteString("只有确有语义增益且不存在未解决冲突时才投 accept；不相关或重复投 reject；证据不足或存在冲突投 uncertain。\n\n")
	}
	if identity.ExpectedResult == V6ContractIntegrationSubmission {
		var integration struct {
			DiscussionID       string `json:"discussion_id"`
			DiscussionRevision int    `json:"discussion_revision"`
			InputSetHash       string `json:"input_set_hash"`
		}
		if json.Unmarshal(dispatchContext.TaskContext, &integration) != nil || integration.DiscussionID == "" || integration.DiscussionRevision < 1 || integration.InputSetHash == "" || len(dispatchContext.InputNodes) < 2 || len(identity.BranchRefs) == 0 {
			return "", fmt.Errorf("%w: incomplete V6 integration identity", ErrInvalidContract)
		}
		inputNodes, _ := json.Marshal(dispatchContext.InputNodes)
		branchRefs, _ := json.Marshal(identity.BranchRefs)
		prompt.WriteString("Discussion 已全体同意。综合冻结 input_nodes，生成一个有明确语义增益、可独立阅读的 successor。两个同级输入晋升一级；一个最高层输入加低层输入为 assimilation 且保持最高 tier；两个 XXL 为 xxl_merge。提交下面的精确结构：\n\n")
		prompt.WriteString("```json\n{\n  \"contract_kind\": \"integration_submission\",\n  \"schema_version\": 6,\n  \"client_request_id\": \"<new-uuid>\",\n")
		fmt.Fprintf(&prompt, "  \"workspace_id\": \"%s\",\n  \"run_id\": \"%s\",\n  \"work_item_id\": \"%s\",\n  \"attempt_id\": \"%s\",\n  \"agent_id\": \"%s\",\n  \"manifest_id\": \"%s\",\n  \"manifest_hash\": \"%s\",\n  \"discussion_id\": \"%s\",\n  \"discussion_revision\": %d,\n  \"input_set_hash\": \"%s\",\n", identity.WorkspaceID, identity.RunID, identity.WorkItemID, identity.AttemptID, identity.AssignedAgent, identity.ManifestID, identity.ManifestHash, integration.DiscussionID, integration.DiscussionRevision, integration.InputSetHash)
		fmt.Fprintf(&prompt, "  \"mode\": \"<promotion|assimilation|xxl_merge>\",\n  \"input_nodes\": %s,\n  \"output_tier\": \"<M|L|XL|XXL>\",\n  \"output_content\": {\"catalog_summary\":\"<summary>\",\"brief_summary\":\"<summary>\",\"objective\":\"<objective>\",\"conclusion\":\"<conclusion>\",\"content\":\"<完整中文整合结果>\",\"scope\":{},\"uncertainties\":[],\"conflicts\":[],\"open_questions\":[]},\n  \"branch_refs\": %s,\n", inputNodes, branchRefs)
		prompt.WriteString("  \"semantic_gain\": \"<相对于输入新增的中文综合价值>\",\n  \"steward_agent_id\": \"" + identity.AssignedAgent + "\",\n  \"content_hash\": \"sha256:<RFC-8785-hash>\"\n}\n```\n\n")
		prompt.WriteString("计算 `content_hash` 时只移除 `content_hash` 字段，用 RFC 8785 JCS 规范化其余对象并计算 SHA-256。不得改写 Manifest 冻结的 input_nodes、branch_refs 或 discussion identity。\n\n")
	}
	if len(identity.CatalogAccess) > 0 && string(identity.CatalogAccess) != "null" {
		prompt.WriteString("读取并确认本次工作所需的每一页已授权 catalog：\n\n")
		prompt.WriteString("```bash\nmultica research work-catalog " + base + " --view same_tier --output json\nmultica research work-catalog-ack " + base + " --client-request-id <uuid> --page-key <page-key> --page-hash <page-hash> --output json\n```\n\n")
		prompt.WriteString("使用凭据代理回退时，GET `${V6_API}/catalog?view=<same_tier|higher_candidates>`，并把确认 POST 到 `${V6_API}/catalog-acks`。Manifest 中没有 `catalog_access` 时不得调用 catalog endpoint。\n\n")
	}
	if identity.ExpectedResult == V6ContractReportPackageSubmission {
		prompt.WriteString("先上传每个报告资源，再引用上传返回的 resource ID：\n\n")
		prompt.WriteString("```bash\nmultica research report-upload " + base + " --file <absolute-file> --path <package-path> --role <role> --media-type <media-type> --output json\n```\n\n")
		prompt.WriteString("使用凭据代理回退时，按冻结 Manifest 描述的 `${V6_API}/report-uploads` 工作流操作。\n\n")
	}
	prompt.WriteString("提交 endpoint 没有仅校验或 dry-run 模式。不得发送探测、占位或最小测试 payload：任何 HTTP 200 都是持久交接，可能永久结算此 Work Item。必须先完成并检查任务的真实结果。\n\n")
	prompt.WriteString("严格写出 `expected_result_schema` 指定的最终 envelope，保留 Manifest 中的每个身份和 hash，然后提交：\n\n")
	prompt.WriteString("```bash\nmultica research work-submit " + base + " --file <absolute-result.json> --output json\n```\n\n")
	prompt.WriteString("使用凭据代理回退时，携带 `Content-Type: application/json`，用 `--data-binary @<absolute-result.json>` 把精确结果文件 POST 到 `${V6_API}/submission`。\n\n")
	prompt.WriteString("`work-submit` 会持久记录 envelope，通常返回状态 `received`；这表示 Agent 已成功交接，应立即停止执行并报告完成。服务端会异步应用，之后可能标记为 `accepted` 或 `rejected`。只有传输失败或结果未知时才可重试，并且必须使用完全相同的 client request ID 和字节等价的 envelope。\n\n### 调研任务\n\n")
	prompt.WriteString(strings.TrimSpace(identity.MissionPrompt))
	prompt.WriteString("\n")
	return prompt.String(), nil
}
