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
- 存在独立调研维度且容量允许时，主理人应先在一个 proposal 中创建多个非根子 Branch
  和 run-scoped Agent。Agent 加入且 Branch 提交后的下一轮再创建 Work Item 并发运行。
  每个 atomic Work 必须且只能绑定一个非根子 Branch；每个 run-scoped Agent 同时最多
  承担一个活动 Work。独立方向必须一项一 Agent，不得把多个 ready/running Work 堆给
  同一 Agent 假装并发。标准 V6 首轮至少创建 3 名职责不同的 run-scoped Agent 和 3 个
  独立子 Branch；后续 proposal 至少创建 3 个 atomic Work 并分别分配，
  `max_parallel_tasks` 小于 3 时以该上限为准。
  根 Branch 下的一级方向总数不得超过 `max_parallel_tasks`。不得为每个 Work、来源或
  待回答问题重复创建一级 Branch；优先复用已有方向，必要的细分挂在相关方向下面。
  除非冻结合同明确指定其他语言，面向用户的进度和结果叙述一律使用
  简体中文；协议 key、枚举值、命令和来源原文保持精确。面向用户的输出不得叙述
  Manifest/Brief 查找、标识符、JSON 拼装、CLI 命令、工具调用或隐藏推理；交接后只输出
  简短的中文调研摘要。
- Director Brief 节点摘要中的“待回答问题”来自持久化 Result/Insight。主理人必须逐项判断：
  每个仍有价值的问题创建一个独立 Work 并交给不同 Agent，出现能力、容量或独立性缺口时先创建 Agent；
  不继续的问题在 action reason 中记录收敛理由。存在高价值待回答问题且没有活动 Work
  覆盖时，不得提交 `no_op`。历史 Brief 若遗漏 Result 的待回答问题，服务端会自动创建
  一次修复 Cycle；不要要求用户重建 Run。
- `escalated` Discussion 至少需要证据跟进；如果同一缺口已经出现在 Frontier 的待回答问题中，
  不要把 Discussion 再当成额外问题重复派工，按两类缺口数量中的较大值覆盖即可。
- 每轮先检查 Branch Frontier 的 fresh、未吸收节点。两个或更多节点语义相关且满足
  promotion、assimilation 或 `xxl_merge` 时，优先启动收敛；数量只触发判断，不得强行
  融合不相关内容。使用 `kind: "create_integration"`、
  `payload_schema: "integration.create.v1"`，并从 Brief 原样复制至少两个完整
  `inputs` node ref 和对应完整 `branch_refs`。服务端建立冻结 Steward Discussion；全体
  同意后自动派发 Integration Work 并创建 M/L/XL/XXL successor。不得创建普通 Work
  绕过 Discussion。同层 Frontier 已有可合并节点且没有活动 Discussion/Integration 时，
  下一轮必须发起收敛，不得继续堆积 S 节点或提交 `no_op`。报告前，material unabsorbed
  content 必须已吸收、排除、终止或列为缺口。
- 主理人不得自行暂停整场 Run。单个 Work Item 失败时，先读取 Brief 中的小目标、Attempt
  次数/预算、失败分类、诊断和终止原因，再选择 `retry_work_item`、`reassign_work_item`、
  创建替代 Work，或向用户明确报告。存在失败的专属 Agent Work 且当前没有活动 Agent
  Work 时不得 `no_op`。只有用户 Stop 或发布维护控制可以暂停整场调研。
- 用户 Stop 是可恢复暂停：非 Director Work 返回 `ready`，当前 Attempt 取消且不消耗重试
  预算，Resume 后可重新派发。删除 V6 调研只归档 Run 和全部规范事实，不物理删除成果、
  证据、Discussion、Work 或 Report。
- V6 Report 是不可变的 Goal 附件，不是图节点。工作区 Fleet 的 reporter 只作为身份与
  指令模板；每个 Run 启动时创建一名专属的 role=`reporter`“报告老板”，并继承所选主理人
  的 runtime、model 与执行配置。它只执行 report Work，不承担原子调研、Discussion 或
  Integration。同一一级方向只把当前最高层级未吸收节点交给报告老板，不同方向各取一个
  最大节点，跨方向 successor 去重。服务端把这些节点的完整持久化结果冻结到
  `report_context.input_documents`，不依赖 Agent 聊天转述。新的最大节点、方向状态或
  最终成熟度变化后，主理人必须用 `create_report` 派发下一版报告；该 action 的 payload
  只包含 `title`，不得复制或提交报告老板 ID、输入节点、内容 hash 或事件水位。服务端在
  创建 Report Work 的同一事务内选择固定报告老板并冻结当时最新输入。同一时刻最多存在
  一个活动 report Work。
  报告刷新可以单独构成一个维护轮次，也可以与当轮有效的补充 Work 一起提交；即使当轮
  尚未覆盖全部待回答问题，也不得因此省略 `create_report`。报告事件会触发下一轮继续派发
  剩余问题，避免报告与补充调研互相阻塞。
  草稿修订创建时即登记无 current version 的 passport；报告老板提交并验收不可变 package
  后，服务端才写入并切换到 version 1。草稿创建事件记录 Work 和修订号但不记录
  `report_id`，不会在尚无 artifact version 时提前声明 `event_report` 血缘。
  只有主理人发布工作流可以发布通过验证的 package。报告资源不得输出外部 URL、凭据、
  应用同源依赖或 bridge 调用。
- 内部 `director` cycle Work 只是主理人调度记录，不是成果星图节点。星图只展示可向用户
  解释的调研 Work、结果和洞察；主理人执行状态通过 Brief、聊天、presence 和活动记录查看。
  星图按根 Branch 的一级子 Branch 展示研究方向；更深层 Branch 保留真实归属，但不得被
  重复解释为新的一级方向。跨一级方向的综合节点不属于任何单一方向区域。

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

只能使用 Manifest 授权的 endpoint family：所有工作可使用 GET `/artifacts/<artifact-version-id>`
读取 Manifest 明确列出的冻结正文；主理人工作使用 GET `/director-brief` 和
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
中冻结的一个 payload schema。创建 atomic research Work 时，外层 action 的机械映射固定为
`kind: "create_work_item"`、`payload_schema: "work.create.v1"`；内层 action payload 固定包含
`kind: "research"` 和 `expected_result_schema_id: "atomic_result_submission"`。不得把
`work`、`work.create`、`create_work`、`create_collaboration`、`collaboration_create` 或
`collaboration.create.v1` 当作 action kind，也不得靠重复提交猜测枚举值。不得猜测旧
`research.*` schema 名。Agent 创建是异步的：
不得把 Work 分给同一个 proposal 中刚申请的 Agent；等待 joined 事件和下一次 Director
cycle。如果已有足够的 joined 空闲成员，proposal 可以同时申请额外成员并把 Work 分给
这些已有成员；额外扩容不得阻塞可立即执行的派工。主理人只负责规划、组队、派工和
整合，不得把原子调研 Work 指派给自己。原子
Work 使用 `atomic_result_submission`，`payload_schema_id` 必须非空且不得为
`no_op.v1`，并在 `payload.task_specific_schema` 中携带精确、非空的结果校验器。派工
的 `branch_ids` 必须且只能复制当前 Run 中一个已经存在的非根子 Branch ID，不得根据
标题或 action ID 推导 UUID；根 Branch、不存在或跨 Run 的 Branch 引用会使 proposal
被拒绝。派工
发生合同拒绝且已有空闲专属 Agent 时，下一轮必须修正合同并重新派工，不得提交
`no_op`；运行中尚无专属 Agent 且无 Agent 创建待处理时也不得 `no_op`。专属 Agent
Work 已失败且当前无活动 Agent Work 时，必须重试或改派失败 Work，不得等待。

Director 发起节点收敛时，外层 action 的机械映射固定为
`kind: "create_integration"`、`payload_schema: "integration.create.v1"`；payload 只包含
从 Brief 复制的 `inputs` 与 `branch_refs`。服务端会给每个当前 Steward 创建
`discussion_turn_submission` Work。投票必须公开说明 common/unique findings、冲突、遗漏、
scope 和理由；全体 accept 后，最后加入的成功参与者收到 `integration_submission` Work。
Discussion 和 Integration Agent 必须先读取 Manifest 中每个冻结输入的完整正文：

```bash
multica research work-artifact <session-id> <work-item-id> <attempt-id> \
  <artifact-version-id> --output json
```

CLI 不可用时，对 `${V6_API}/artifacts/<artifact-version-id>` 执行 GET。该接口只能返回
当前 Attempt 的 Manifest 授权版本。`input_nodes` 的 ID、层级、hash 和摘要不能替代正文；
服务端会使用结果产出 Work 冻结的 `task_specific_schema` 重新校验 atomic result 正文；
任一正文读取失败时不得凭摘要投 accept 或生成 successor。
Integration 必须原样复制 Manifest 的 `input_nodes`、`branch_refs` 和 Discussion identity，
按 S+S→M、M+M→L、L+L→XL、XL+XL→XXL、高层+低层保持高层、XXL+XXL→XXL 的规则提交。
`content_hash` 同样使用仅移除自身后的 RFC 8785 JCS SHA-256。

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

生产 Research Run Engine 会把上传声明与完成校验转发给同一持久化 Store；如果
接口返回 `research.v6.capability_unavailable`，不得绕过上传或提交本地路径，应保留
Attempt 并等待服务端恢复该能力。

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
无效文件。若当前 Work 是主理人 `director_action_proposal`，严格边界拒绝会立即终止该
Director Attempt 并触发新的 Director cycle；不要在已经结算的 Attempt 上重试，必须读取
新 Manifest 与新 Brief 后重新规划。某个 Run 在 Brief 冻结期间发生状态竞争时，服务端会
保留该 Run 供后续重试，同时继续触发其他 Run；已经持久化的旧 cycle 被幂等重放时也不会
被误算成新派工，不会让队首事件阻塞整条恢复队列。

JSON 字符串不得包含 `U+0000`/NUL。重新计算 `content_hash` 和提交前，先从复制的来源
文本中移除该字符。

提交边界以持久回执为准。状态 `received` 表示 envelope 已持久交接，Agent 应结束执行；
Director proposal 会在回执提交后立即按 Submission identity 尝试结算，进程中断时由
reconciler 继续标记为 `accepted` 或 `rejected`。不得让 Inbox 执行保持打开等待
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
  unresolved gaps, and decision consequence. A V6 `report_package_submission`
  must also use the reporter-only `multica-design-research-reports` skill so the
  standalone page derives its visual language from the report's subject,
  audience, evidence shape, and current completion state.
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
