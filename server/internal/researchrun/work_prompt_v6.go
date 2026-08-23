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
	CatalogAccess json.RawMessage `json:"catalog_access"`
	TaskSchema    json.RawMessage `json:"task_specific_schema"`
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
	if strings.TrimSpace(identity.RunID) == "" || strings.TrimSpace(identity.WorkItemID) == "" || strings.TrimSpace(identity.AttemptID) == "" ||
		strings.TrimSpace(identity.ManifestID) == "" || strings.TrimSpace(identity.ManifestHash) == "" || strings.TrimSpace(string(identity.ExpectedResult)) == "" {
		return "", fmt.Errorf("%w: incomplete V6 work identity", ErrInvalidContract)
	}
	if identity.ExpectedResult == V6ContractAtomicResultSubmission && strings.TrimSpace(identity.TaskID) == "" {
		return "", fmt.Errorf("%w: atomic V6 Work Item has no Task identity", ErrInvalidContract)
	}

	base := fmt.Sprintf("%s %s %s", identity.RunID, identity.WorkItemID, identity.AttemptID)
	var prompt strings.Builder
	prompt.WriteString("## Durable Research V6 Work Item\n\n")
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
		prompt.WriteString("Read every Director Brief page, acknowledge each page with its exact IDs and hashes, then submit the proposal:\n\n")
		prompt.WriteString("```bash\nmultica research director-brief " + base + " --output json\nmultica research director-brief-ack " + base + " --client-request-id <uuid> --brief-id <brief-id> --brief-hash <brief-hash> --page-key <page-key> --page-hash <page-hash> --output json\n```\n\n")
		prompt.WriteString("For the credential-proxy fallback, GET `${V6_API}/director-brief` (append `?cursor=<next_cursor>` when present) and POST each acknowledgement to `${V6_API}/director-brief-acks`. The acknowledgement JSON has exactly `client_request_id`, `brief_id`, `brief_hash`, `page_key`, and `page_hash`.\n\n")
		prompt.WriteString("The submission must use this exact root shape (replace every angle-bracket value; do not include the angle brackets):\n\n")
		prompt.WriteString("```json\n{\n")
		prompt.WriteString("  \"contract_kind\": \"director_action_proposal\",\n  \"schema_version\": 6,\n  \"client_request_id\": \"<new-uuid>\",\n")
		fmt.Fprintf(&prompt, "  \"workspace_id\": \"<manifest.workspace_id>\",\n  \"run_id\": \"%s\",\n  \"work_item_id\": \"%s\",\n  \"attempt_id\": \"%s\",\n", identity.RunID, identity.WorkItemID, identity.AttemptID)
		fmt.Fprintf(&prompt, "  \"manifest_id\": \"%s\",\n  \"manifest_hash\": \"%s\",\n", identity.ManifestID, identity.ManifestHash)
		prompt.WriteString("  \"director_assignment_id\": \"<brief.director_assignment_id>\",\n  \"director_generation\": <brief.director_generation>,\n  \"brief_id\": \"<brief.brief_id>\",\n  \"brief_hash\": \"<brief.brief_hash>\",\n  \"reviewed_page_count\": <brief.page.page_count>,\n  \"expected_state_version\": <brief.state_version>,\n  \"through_event_sequence\": <brief.through_event_sequence>,\n  \"actions\": [\n    {\n      \"action_id\": \"<stable-key>\",\n      \"kind\": \"<allowed-kind>\",\n      \"reason\": \"<reason>\",\n      \"idempotency_key\": \"<stable-key>\",\n      \"expected_state_version\": <brief.state_version>,\n      \"payload_schema\": \"<one manifest.task_specific_schema.payload_schemas key>\",\n      \"payload\": <object matching that payload schema>,\n      \"depends_on_action_ids\": []\n    }\n  ]\n}\n```\n\n")
		prompt.WriteString("Use only action kinds from the root contract and payload schemas present in `manifest.task_specific_schema.payload_schemas`. A newly requested Agent is asynchronous and cannot receive Work in the same proposal; create it now and wait for its joined event/next Director cycle. Work assigned to an existing team member may be created immediately. Atomic Work must set `expected_result_schema_id` to `atomic_result_submission`, choose a non-empty `payload_schema_id`, and put that result validator under `payload.task_specific_schema`. If no useful mutation exists, submit one `no_op` action with `payload_schema` `no_op.v1` and payload `{\"reason\":\"<reason>\"}`.\n\n")
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
		prompt.WriteString("The atomic submission root must contain exactly the contract fields below. Copy identity and frozen references; do not invent a legacy Task ID:\n\n")
		prompt.WriteString("```json\n{\n  \"contract_kind\": \"atomic_result_submission\",\n  \"schema_version\": 6,\n  \"client_request_id\": \"<new-uuid>\",\n")
		fmt.Fprintf(&prompt, "  \"workspace_id\": \"%s\",\n  \"run_id\": \"%s\",\n  \"work_item_id\": \"%s\",\n  \"task_id\": \"%s\",\n  \"attempt_id\": \"%s\",\n  \"agent_id\": \"%s\",\n  \"manifest_id\": \"%s\",\n  \"manifest_hash\": \"%s\",\n  \"goal_version\": %d,\n", identity.WorkspaceID, identity.RunID, identity.WorkItemID, identity.TaskID, identity.AttemptID, identity.AssignedAgent, identity.ManifestID, identity.ManifestHash, identity.Goal.GoalVersion)
		fmt.Fprintf(&prompt, "  \"branch_refs\": <manifest.branch_refs>,\n  \"content_layers\": {\"catalog_summary\":\"<summary>\",\"brief_summary\":\"<summary>\",\"objective\":\"<objective>\",\"conclusion\":\"<conclusion>\",\"content\":\"<content>\",\"scope\":{},\"uncertainties\":[],\"conflicts\":[],\"open_questions\":[]},\n  \"evidence_refs\": <only frozen manifest artifact versions actually used>,\n  \"state_proposal\": {\"conclusion_state\":\"proposed\",\"integration_state\":\"candidate\"},\n  \"related_candidates\": [],\n  \"task_specific_schema\": \"%s\",\n  \"task_specific_payload\": <object matching the schema under that exact manifest key>,\n  \"content_hash\": \"sha256:<RFC-8785-hash>\"\n}\n```\n\n", taskSchemaID)
		prompt.WriteString("Use exactly the single key under `manifest.task_specific_schema.payload_schemas`; never invent or rename that schema ID. Keep `content_layers.catalog_summary` at 512 characters or fewer. Root `content_layers.uncertainties`, `content_layers.conflicts`, and `content_layers.open_questions` are arrays of strings; similarly named fields inside `task_specific_payload` follow the frozen task schema and may contain objects. For `content_hash`, remove only the `content_hash` field, canonicalize the remaining object with RFC 8785 JCS, SHA-256 those bytes, then write lowercase `sha256:<64-hex>`. Read and acknowledge every catalog page used before submitting.\n\n")
	}
	if len(identity.CatalogAccess) > 0 && string(identity.CatalogAccess) != "null" {
		prompt.WriteString("Read and acknowledge every authorized catalog page needed by the work:\n\n")
		prompt.WriteString("```bash\nmultica research work-catalog " + base + " --view same_tier --output json\nmultica research work-catalog-ack " + base + " --client-request-id <uuid> --page-key <page-key> --page-hash <page-hash> --output json\n```\n\n")
		prompt.WriteString("For the credential-proxy fallback, GET `${V6_API}/catalog?view=<same_tier|higher_candidates>` and POST acknowledgements to `${V6_API}/catalog-acks`. Do not call the catalog endpoint when `catalog_access` is absent.\n\n")
	}
	if identity.ExpectedResult == V6ContractReportPackageSubmission {
		prompt.WriteString("Upload each report resource before referencing its returned resource ID:\n\n")
		prompt.WriteString("```bash\nmultica research report-upload " + base + " --file <absolute-file> --path <package-path> --role <role> --media-type <media-type> --output json\n```\n\n")
		prompt.WriteString("For the credential-proxy fallback, use the `${V6_API}/report-uploads` workflow described by the frozen Manifest.\n\n")
	}
	prompt.WriteString("The submission endpoint has no validation-only or dry-run mode. Never send a probe, placeholder, or minimum test payload: any HTTP 200 is a durable handoff that may permanently settle this Work Item. Finish and inspect the mission's real result first.\n\n")
	prompt.WriteString("Write exactly the final envelope named by `expected_result_schema`, preserving every identity and hash from the manifest, then submit it:\n\n")
	prompt.WriteString("```bash\nmultica research work-submit " + base + " --file <absolute-result.json> --output json\n```\n\n")
	prompt.WriteString("For the credential-proxy fallback, POST the exact result file to `${V6_API}/submission` with `Content-Type: application/json` and `--data-binary @<absolute-result.json>`.\n\n")
	prompt.WriteString("`work-submit` durably records the envelope and normally returns status `received`; that is the successful Agent handoff, so stop and report completion. The server applies it asynchronously and may later mark it `accepted` or `rejected`. Retry only transport/unknown outcomes with the exact same client request ID and byte-equivalent envelope.\n\n### Mission\n\n")
	prompt.WriteString(strings.TrimSpace(identity.MissionPrompt))
	prompt.WriteString("\n")
	return prompt.String(), nil
}
