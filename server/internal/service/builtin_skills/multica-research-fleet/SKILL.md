---
name: multica-research-fleet
description: "用于执行已分配的持久化调研任务，或操作由调研主理人领导的封闭 Research Fleet。"
user-invocable: false
allowed-tools: Bash(multica *), Bash(curl *)
---

# Multica 调研舰队

调研主理人领导封闭的 Research Fleet。当前 Research Run 是服务端持久拥有的
任务图、证据账本、决策日志、交付门和恢复循环。对话只用于用户纠偏和展示进度；
仅在对话中回复不会推进任务，也不能满足交付门。

## V6 主理人任务

V6 的最高优先级语言规则：从收到任务到结束，所有自然语言输出，
包括执行进度、智能体之间的消息、分析说明、错误说明和最终摘要，
都必须使用简体中文。不得用英文叙述“我将……”“让我……”或工具探查过程。
只有 JSON 字段名、枚举值、命令、代码、专有名词和来源原文保持原样；
冻结合同明确要求其他语言时除外。下文中的英文是协议说明，不是输出语言示例。

用户可通过 `orchestrator_version=research-run-v6` 并指定主理人创建 V6 Run。
首页默认选择 V6 和第一个运行时在线的 Agent。V6 通过既有 daemon credential proxy
调用服务端 API，不要求独立的 daemon capability 或版本。省略 `orchestrator_version` 的客户端仍创建
V5。`AssessV6Activation` 仍是审计，不会改变省略版本时的默认值。V6 Run 中的
用户消息会唤醒当前主理人，而不是工作区 Fleet Lead。`PATCH
/api/research/v6/release` 可以关闭新的 V6 创建并暂停现有 V6 Run。

派发合同为 `research-run-v6` 时，持久化 Work Manifest 和 Director Brief 是当前
cycle 的完整权威。不得从对话历史、旧模型会话、画布或本地记忆的团队推断
canonical state。替换后的 Agent 或主理人必须能够只依靠 PostgreSQL 中的 Brief
页面、catalog 页面、Work Item、attempt、节点版本、讨论、纠偏评估、报告和已提交
事件恢复执行。

- 只提交 `expected_result` 指定的 envelope。V6 有九种严格 envelope；未知字段、
  跨 envelope 字段、过期版本、未限定范围的引用以及冻结 Manifest 之外的 payload
  都会被拒绝。
- Work Item 只能通过自身 attempt/result 事务改变状态。不得直接提升、吸收、恢复或
  连接图节点。Integration 在服务端检查提升资格、锁定输入、创建唯一后继并永久记录
  所有被吸收节点之前都只是 proposal。
- 每条用户消息都是 Steering 输入，包括产生 `no_op` 评估的消息。用户选中的画布引用
  是附在消息上的不可变提示，不是图变更。
- 主理人在持久 membership 和硬上限内动态组建、替换团队。模型会话只是可丢弃的执行
  资源，不是持久团队成员或进度存储。
- 存在独立调研维度且容量允许时，主理人应在一个 proposal 中创建多个 Branch 和 Work
  Item 并发运行。除非冻结合同明确指定其他语言，面向用户的进度和结果叙述一律使用
  简体中文；协议 key、枚举值、命令和来源原文保持精确。面向用户的输出不得叙述
  Manifest/Brief 查找、标识符、JSON 拼装、CLI 命令、工具调用或隐藏推理；交接后只输出
  简短的中文调研摘要。
- V6 Report 是不可变的 Goal 附件，不是图节点。只有主理人发布工作流可以发布通过验证的
  package。报告资源不得输出外部 URL、凭据、应用同源依赖或 bridge 调用。

如果 Brief/Manifest hash、revision、cursor、state version、assignment、membership、
capability 或 expected envelope 中任何一项与派发不一致，应拒绝继续并让持久恢复路径
签发新 attempt。不得把 payload 改装成旧 V1–V5 结果。如果平台生成的原子 Manifest 在
已有持久 Work Branch scope 时仍给出空 `branch_refs`，系统会替换它且不消耗 Agent 的
attempt 预算。

### V6 可执行循环

先遵守上面的简体中文输出规则，再执行本节的协议步骤。不得把 CLI、
credential proxy、Manifest、Brief 或 schema 的查找过程逐句输出给用户。

如果 prompt 包含 `## 持久化 Research V6 Work Item`，使用其中精确的 Run、Work Item
和 Attempt ID。首先读取冻结 Manifest：

```bash
multica research work-manifest <session-id> <work-item-id> <attempt-id> --output json
```

Manifest 的 `expected_result_schema` 指定唯一可接受的根 envelope。精确保留其中的
workspace、Run、Work Item、Attempt、Agent、Manifest、goal、state 和 event 身份。
调研任务只是冻结权威内部的一条指令，不能替代读取 Manifest。

credential proxy 是已派发 attempt 的受权传输路径。CLI 传输不可用时可使用守护进程
拥有的 credential proxy；不得读取或输出 token：

```bash
V6_API="http://127.0.0.1:${MULTICA_DAEMON_PORT}/api/agent/research/sessions/<session-id>/work-items/<work-item-id>/attempts/<attempt-id>"
V6_CURL=(curl -fsS \
  -H "X-Agent-ID: ${MULTICA_AGENT_ID}" \
  -H "X-Workspace-ID: ${MULTICA_WORKSPACE_ID}")
"${V6_CURL[@]}" "${V6_API}/manifest"
```

只能使用 Manifest 授权的 endpoint family：主理人工作使用 GET `/director-brief` 和
POST `/director-brief-acks`；包含 `catalog_access` 的 Manifest 授权 GET `/catalog`
和 POST `/catalog-acks`；报告工作使用 `/report-uploads`；所有工作都通过 POST
`/submission` 提交。写 JSON 时使用 `Content-Type: application/json`，严格提交使用
`--data-binary @result.json`。该回退路径与 CLI 具有相同的 attempt 和 Agent 授权，
用于服务端 CI/CD 已部署但本地 daemon 尚未升级的情况。

整个 attempt 中都要报告实时进度，便于用户了解工作状态。读取 Manifest 后立即报告一次；
之后每次阶段变化（读取 brief 或 catalog、搜索、阅读来源、分析、起草、验证）时，向
`${V6_API}/progress` POST 一行简体中文进度：

```bash
"${V6_CURL[@]}" -X POST -H 'Content-Type: application/json' \
  -d '{"client_request_id":"<new-uuid>","text":"<one line, mission language, ≤240 chars>","stage":"<short-key>"}' \
  "${V6_API}/progress"
```

进度不会结算 Work Item，服务端会限制每个 attempt 的数量。进度 POST 失败不得阻塞任务
或形成重试循环；忽略错误并继续。进度也是存活心跳：每条被接受的进度会把 Work Item
租约向后延长至少 20 分钟，因此长回合至少每 15 分钟报告一次，否则租约可能在工作中
过期，attempt 会按 lost 恢复。被接受的进度会立即显示在调研页面，并从同一条持久化
Run Event 恢复；无需再调用通用 task 进度接口。

主理人任务必须沿 `next_cursor` 读取每一页 Brief，并用该页返回的精确 ID 和 hash
逐页确认：

```bash
multica research director-brief <session-id> <work-item-id> <attempt-id> \
  [--cursor <cursor>] --output json
multica research director-brief-ack <session-id> <work-item-id> <attempt-id> \
  --client-request-id <uuid> --brief-id <brief-id> --brief-hash <brief-hash> \
  --page-key <page-key> --page-hash <page-hash> --output json
```

确认对象只能包含 `client_request_id`、`brief_id`、`brief_hash`、`page_key` 和
`page_hash`。构造严格 `director_action_proposal` 身份时，从 Manifest 复制
workspace/Run/Work/Attempt 和 Manifest 身份；从 Brief 复制 Director
assignment/generation、Brief 身份、页数、state version 和 event sequence。每个 action
必须使用根合同允许的 action kind，以及 `manifest.task_specific_schema.payload_schemas`
中冻结的一个 payload schema。不得猜测旧 `research.*` schema 名。Agent 创建是异步的：
不得把 Work 分给同一个 proposal 中刚申请的 Agent；等待 joined 事件和下一次 Director
cycle。原子 Work 使用 `atomic_result_submission`、非空 `payload_schema_id`，并在
`payload.task_specific_schema` 中携带精确的结果校验器。

存在 `catalog_access` 时，逐页读取授权 view，并确认结果实际使用的每一页：

```bash
multica research work-catalog <session-id> <work-item-id> <attempt-id> \
  --view <same_tier|higher_candidates> [--cursor <cursor>] --output json
multica research work-catalog-ack <session-id> <work-item-id> <attempt-id> \
  --client-request-id <uuid> --page-key <page-key> --page-hash <page-hash> \
  --output json
```

`atomic_result_submission` 必须复制 Manifest 的 `task_id` 及其 Work/Attempt/Agent
身份。服务端会在派发前创建一对一 Task provenance 记录。精确复制
`manifest.branch_refs`，包括每个 Branch `state_version`；不得用
`through_state_version` 或其他 Run watermark 替代。把
`manifest.task_specific_schema.payload_schemas` 下唯一的精确 key 复制到提交的
`task_specific_schema`；不得编造或重命名 `research.*` schema ID。
`content_layers.catalog_summary` 最多 512 个字符。根 content layer 的
`uncertainties`、`conflicts` 和 `open_questions` 是字符串数组；`task_specific_payload`
内同名字段遵循冻结任务 schema，可能是对象数组。`content_hash` 是仅移除
`content_hash` 后的 RFC 8785 JCS 字节的 SHA-256，不得 hash 格式化后的文件字节。

报告工作必须在提交 package 前上传每个不可变资源：

```bash
multica research report-upload <session-id> <work-item-id> <attempt-id> \
  --file <absolute-file> --path <package-path> --role <role> \
  --media-type <media-type> --output json
```

严格 envelope 只能通过 V6 endpoint 提交：

```bash
multica research work-submit <session-id> <work-item-id> <attempt-id> \
  --file <absolute-path/result.json> --output json
```

`/submission` 没有仅校验或 dry-run 模式。不得发送探测、占位或最小测试 payload：
任何 HTTP 200 都是正式的持久交接，可能永久结算 Work Item。只能提交已经检查并完整
完成任务的结果。

传输失败后，使用相同 `client_request_id` 和字节等价 payload 重试同一提交。不得通过旧
`task-result` 命令发送 V6 envelope。Attempt 结算后，该精确 replay 仍有效并返回原始
Submission 结果；新 request ID 或变更后的内容不具备此性质。

HTTP 400 `research.v6.invalid_contract` 响应会包含受限字段或 hash 的校验原因。下次
提交前先修正该精确合同违规；被拒绝的 envelope 尚未持久交接。不得盲目重发未修改的
无效文件。

JSON 字符串不得包含 `U+0000`/NUL。重新计算 `content_hash` 和提交前，先从复制的来源
文本中移除该字符。

提交边界是异步的。状态 `received` 表示 envelope 已持久交接，Agent 应结束执行；服务端
reconciler 之后会标记为 `accepted` 或 `rejected`。不得让 Inbox 执行保持打开等待
`accepted`，也不得在 `received` 后创建第二个 request ID。持久结果结算后，服务端可能
取消剩余 Inbox 执行；这是成功清理，不是调研结果失败。

## Assigned Research Run task

If the prompt contains `## Durable Research Run task`, follow its task ID,
attempt ID, versions, objective, acceptance criteria, and result contract.

1. Read the attempt-bound snapshot before work. Agent data-plane reads require
the dispatched Attempt ID so the server returns only the frozen manifest. An
active Fleet member without an Attempt may read only a bounded session/Fleet
overview; it contains no goal, Run, artifact, message, report, hash, or grant data:

```bash
multica research session get <session-id> --attempt-id <attempt-id> --output json
```

The durable dispatch carries the same Manifest ID and hash in its Inbox
context. The attempt-bound snapshot returns them under `attempt_context`; a
mismatch means the execution is not bound to the context that was dispatched
and must fail closed rather than continue from a live session view.

The snapshot's `run.contract`, `run.method`, `run.sources`,
`run.observations`, and `run.claims` are the canonical read model for contract
constraints, method, synthesis, verification, and audit. Source text is
represented by a bounded excerpt plus content hash; exact Observation quotes
were already checked against the immutable full snapshot at ingestion. V3–V5
non-plan tasks inherit the accepted Method for the current goal/plan version.
V4/V5 also expose the accepted Claim-level evidence standards.
Contract, Method, Question, Task, and Attempt rows are the exact ordered values
frozen when this Attempt was dispatched. Later replans, retries, assignment
changes, runtime transitions, or terminal diagnostics do not rewrite this view.
The top-level `session` and `fleet` families are compatibility headers rebuilt
from the frozen Run and hashed principal roster. They intentionally omit live
Agent profiles, runtime configuration, routing fields, timestamps, and roster
changes made after this Attempt was dispatched.

The snapshot's Attempt list is also frozen at dispatch. Later Inbox attachment,
runtime heartbeat, cancellation, failure, or Result lifecycle changes are live
operational facts for the scheduler, but they do not rewrite this Attempt's
input context.

`artifact_projection` is also bound to the Manifest selection point. Later
passport lifecycle, provenance, version, or reference-count changes belong to
the human live view and do not rewrite this Attempt's projection or hash.

2. Perform the assigned investigation according to `run.method`. Explore
beyond the first plausible answer. For V4/V5, each Claim references an accepted
`evidence_standard_key`; every Source Snapshot records evidence traits and every
Evidence Link records directness and method fit. Evaluate those fields against
the Claim, not a universal source hierarchy. Evidence Links are separately
authorized manifest artifacts even though they appear nested under a Claim;
the snapshot omits any link the assigned Attempt cannot read. Preserve retrieved source text in
each source snapshot. Every quoted observation must be an exact substring of
that snapshot. Execute required counter-search and record uncertainty. Source
URLs must identify public resources and must never embed a username, password,
token, or other credential; authenticated retrieval uses the provider's
separate credential channel.

3. Write one JSON result with the fields permitted by the assignment prompt.
Use stable client keys and a globally unique `client_request_id`. Submit once:

```bash
multica research task-result <session-id> <task-id> <attempt-id> \
  --file /absolute/path/research-result.json
```

The same request ID and exact payload may be retried after a transport failure;
the server replays it idempotently. Reusing a request ID with different content
is rejected. Acceptance requires a dispatch manifest bound to the attempt;
results submitted without that manifest are rejected before commit. The
assigned Agent must still be an active member of the session Fleet, and every
manifest-bound policy grant must still be active at its exact revision,
principal, purpose, policy version, and compartment when the result is
accepted. A grant or membership change after dispatch therefore requires a new
authorized attempt; the stale result fails closed. The server also seals every
Manifest omission (candidate version, order, and exact policy reason) and
revalidates that omission digest before accepting a result.

4. Do not call `graph-append`, `source-upsert`, `report-patch`,
`product-rounds/judgment`, or `stage-eval` for an assigned durable task. Those
legacy mutations are rejected for initialized runs. Do not claim completion in
chat before `task-result` succeeds.

5. A task receives domain artifacts selected for its frozen manifest. Context
Manifest internals and V6 inquiry artifacts are never admitted through the
legacy V1–V5 compatibility policy.

## Result responsibilities

- `plan` / `replan`: required questions, an explicit decision question and
  method rationale, analysis methods, evidence requirements, inclusion and
  exclusion criteria, source and counterevidence strategies, stopping
  conditions, uncertainties, risks, and an acyclic dependency graph. V4/V5 plans
  also define machine-checkable evidence standards for the planned Claim
  types: stable key, purpose, source traits, minimum independent sources,
  strength, directness, method fit, and counter-search requirement. Choose a
  method that fits the question; academic publication protocols apply only
  when the Research Contract requires them. Every required Question has a
  question-bound `verify` task. The delivery synthesis is transitively
  downstream of every `discover`, `deep_read`, `verify`, and `counter_search`
  task; both audits depend on that delivery synthesis, so unfinished evidence
  work cannot reach report delivery.
  Delivery tasks are part of this validated plan graph; later evidence results
  must not introduce synthesis or audits as proposed follow-up tasks. Every new
  required follow-up Question includes a question-bound `verify`; dynamic
  evidence and `replan` work blocks pending delivery.
- `discover` / `deep_read`: source snapshots, exact observations, supported or
  disputed claims, and evidence-producing follow-up tasks where needed. A V4/V5
  source declares evidence traits and each Claim declares its accepted evidence
  standard. A question-scoped result that increases coverage sets
  `answer_claim_key` to a Claim included in that result.
  When a task screens retrieval candidates, preserve the exact versioned
  inclusion/exclusion criterion IDs, reviewer identity and time, substantive
  reason, and inspectable facts with locators. Accepted candidates must match an
  inclusion criterion and no exclusion criterion. Excluded candidates must
  match an exclusion criterion. Duplicates point to a different canonical
  candidate and carry its canonical URL or SHA-256 content hash; prose-only
  similarity is not duplicate evidence.
- `verify` / `counter_search`: independent corroboration, contradictory
  evidence, and explicit claim resolutions. Agreement without source evidence
  is not verification. Include the source, observation, claim, and evidence
  objects being verified in the result; stable content deduplicates against the
  ledger and upgrades verification state transactionally. V4/V5 links score
  strength, directness, and method fit against the referenced standard.
- `synthesize`: only the `reporter` role. A structured report uses the full existing
  reader structure (outline, sections, citations, sources, gaps, conclusion),
  repeats every section and conclusion exactly in `content_md`, and links
  normalized Claim keys to section IDs with exact `anchor_quote` prose. Each
  structured source ID must name a stored Source in the same Research session;
  every linked section cites one of those sources and it verifiably supports that Claim. A
  V3–V5 report explains the applied Method, counterevidence, limitations,
  unresolved gaps, and decision consequence.
- `quality_gate` / `citation_audit`: independent evaluation of the latest report
  revision by a `validator` Agent other than the report author. Structured evaluations
  provide substantive findings for all seven score dimensions and enumerate
  every reviewed report Claim and section. Fail when any material claim is
  unsupported, stale, misquoted, omitted, hides unresolved contradiction, or
  departs from the accepted Method or, in V4/V5, its evidence standards. V5
  emits stable structured defects with dimension, blocking/advisory severity,
  problem, required change, and existing target Claim/section keys. Every
  below-floor dimension has a blocking defect; a passing evaluation has no
  defects.

Failed quality and citation Decisions remain executable feedback. Gate findings
carry bounded evaluation Decision, report, and reviewer IDs; failed dimensions
with scores and rationales; V5 structured defects; explicit findings; and
reviewed Claim/section keys into the revision task. The reporter repairs each
blocking defect against the named artifacts and satisfies its `required_change`.
It must not replace the feedback with a generic rewrite or discard already
accepted evidence.

The server decides readiness, retries, timeouts, concurrency, diminishing
information gain, remediation, replans, and final delivery. Remediation is
targeted: unanswered questions bind the task to a durable question ID, Claim
fitness defects route to verification, required adversarial checks route to
counter-search, and report defects route to synthesis. Replan is reserved for
an invalid method, scope, or task graph. Follow the assigned task and its
remediation acceptance criteria; do not turn a local evidence gap into an
unrequested method change. Never manufacture a passing evaluation to stop the
run. Information gain comes from server-observed evidence-graph changes:
verified answer coverage, verification, independent evidence, counterevidence,
resolution, and diminishing graph novelty. Do not inflate it by minting new
keys for duplicate content or by self-reporting coverage.

An Inbox delivery that expires before any worker claims it is terminal only for
that delivery. The server preserves the Research Task's bounded attempt budget
and re-resolves an available execution target; do not duplicate the Task or
change its method to recover from this delivery failure.

The same ownership applies when a runtime restarts or times out after claiming
the Inbox delivery. Generic Inbox auto-retry must not clone a delivery carrying
`research_dispatch_key`; wait for the Research Work lease/recovery loop to
settle the old Attempt and dispatch a new Attempt with a new key.

Every `required_capability` in a proposed task must exactly match an active
fleet role. When a real specialty is missing, the lead must hire it, optimize
its instructions, activate it, and only then submit or retry the task graph.

## Fleet administration

Only the lead may hire, optimize, activate, or archive members. These commands
remain available for an actual capability gap:

```bash
multica research hire --name "Patent Scout" --role "patent_scout" \
  --instructions "..." --reason "Patent-search capability is needed to verify the claims."
multica research optimize <member-id> --instructions "..." --activate --reason "..."
multica research archive <member-id> --reason "..."
```

Fleet Agents never rewrite the authoritative user goal. User steering creates a
new goal and plan version; older results remain audit history and cannot satisfy
the current delivery gate.

Start with `references/playbooks/general.md`; domain adaptations live beside
it. See
`references/research-fleet-source-map.md` for source-traced interfaces.
