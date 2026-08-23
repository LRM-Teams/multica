# 罗纳尔多分层调研系统开发规格

状态：设计冻结；用户可创建 V6，产品化收口（消息路由、首页分流、回滚、Monitor、评测接入、来源持久化、事件重建）已接线

日期：2026-08-14

适用范围：Research Run V6、Web/Desktop Research Constellation 详情页、Director Runtime、动态 Run Team、HTML Report

版本决定：本规格按 ADR-0017 原位替换从未生产启用的旧 V6。V1–V5 合同、Prompt hash、历史 Run 和兼容读取保持不变。

机器载荷和跨对象不变量见 [`docs/research-run-v6-contract.md`](../../research-run-v6-contract.md)，数据库表、约束、事务和恢复见 [`docs/research-run-v6-storage-contract.md`](../../research-run-v6-storage-contract.md)，HTTP/Realtime/Report origin 见 [`docs/research-run-v6-http-contract.md`](../../research-run-v6-http-contract.md)，文件级开发顺序见 [`实施计划`](../plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md)。前三份合同与本规格共同构成目标 V6 的规范；出现歧义时，本规格决定产品语义，合同文档决定可执行边界，实施计划不得修改语义。

## 1. 结论

罗纳尔多是用户为单个 Research Run 选择的唯一 Research Director。新 Run 启动时只有罗纳尔多，不预建 Scout、Reader、Validator、Reporter 或其他固定成员。罗纳尔多根据当前 Goal 和研究图动态创建、配置、分配、改派、闲置及归档 Agent。

所有研究事实写入现有 PostgreSQL。Agent 会话、聊天、画布和 Director 上下文都不是事实来源。S 级结果按语义融合为 M、L、XL、XXL；每次融合和更新都创建不可变 successor，输入节点只允许被一个 canonical successor 吸收。罗纳尔多每次从各 Branch 的顶部内容和运行控制摘要重建上下文，不依赖永不重置的模型会话。

调研完成时，罗纳尔多安排任意 Agent 生成独立、精美、可包含 JavaScript 的静态 HTML 报告。报告挂在 Goal 上，通过隔离弹窗展示，不是研究节点，也不吸收任何 Branch 内容。

## 2. 当前最大问题

当前生产默认仍是 `research-run-v5`。现有代码已经有 Task、Attempt、Evidence、Artifact Passport、Inquiry、Insight、Integration、Dispute 和 Report 的数据库基础，但没有把以下行为接成一个生产 Module：

1. Director 无法只读取各 Branch 顶部结果并长期维持全局判断；现有快照和固定流程仍可能把低层内容带入上下文。
2. `research_fleet` 与固定 task kind/role 假设不符合“Run 只有罗纳尔多、其他成员全部动态创建”。
3. 旧 V6 规定固定 Plan/Task/Report/Evaluation envelope、至少两输入 Insight 和独立评审角色，不符合新的 promotion、assimilation、Discussion 和罗纳尔多验收模型。
4. `research_graph_node` 仍容易被当成研究事实；目标态中它只能是可重建 Projection。
5. 现有报告以 Markdown 为权威阅读面；目标态要求独立的 HTML 页面、沙箱 JavaScript 和 Goal 弹窗。

证据位置：

- `server/migrations/274_research_run_backend.up.sql`
- `server/migrations/318_research_artifact_passport.up.sql`
- `server/migrations/348_research_inquiry_graph.up.sql`
- `server/migrations/350_research_integration_dispute_graph.up.sql`
- `server/internal/researchrun/`
- `docs/research-run-v6-contract.md`，现已由 ADR-0017 原位替换为冻结的 Ronaldo V6 目标合同

## 3. 目标和边界

### 3.1 必须实现

- 用户用自然语言持续修改、补充、否定或颠覆 Goal；每条用户消息都由罗纳尔多判断影响范围。
- 罗纳尔多拥有 Research 模块内的最高语义决策权，可以创建任意研究任务和任意 Agent 配置。
- Run 级动态团队，少于 20 人时偏向创建，20–49 人提高创建门槛，50 人硬上限。
- Research、Match、Discussion、Integration、Director、Report 都是可恢复的持久 Work Item。
- S/M/L/XL/XXL 分层、promotion、assimilation、single-successor absorption、版本和来源图。
- Branch Frontier、每 Branch 最多一个当前 XXL、跨 Branch 共享节点和多 Branch 共用 XXL。
- 无收益、错误、弯路、停止和争议的持久记录及用户可见 Projection。
- Director Brief、上下文轮换、事件水位、重启恢复和 `awaiting_director`。
- Goal 根节点、语义缩放星图、未吸收 S 微型圆点、节点检查面板和报告弹窗。
- HTML Report revision、资源固化、JavaScript 沙箱、罗纳尔多验收和版本历史。

### 3.2 不包含

- 不删除或改写 V1–V5 历史行为。
- 不建立第二个数据库或图数据库。
- 不把普通 Channel/Group Chat 变成 Research Discussion 的事实来源。
- 不要求一个永久模型会话，也不为整个 Run 设置 token 总额度。
- 不允许 Agent 绕过服务端事务、workspace 权限、Artifact Passport、DAG、50 人上限或报告沙箱。
- 不把 Report、视觉聚类、collapsed path 或密度点阵写回 canonical 研究图。

## 4. 领域模型

### 4.1 Goal、Run 和 Director

- **Goal**：图上的根节点，内容来自当前 Research Contract revision。Goal 不属于 S/M/L/XL/XXL。
- **Research Run**：单个调研任务的持久执行边界，也是 50 个 Agent 上限的计数边界。
- **Research Director**：用户选择并固定到 Run 的罗纳尔多。只有用户可以替换 Director。
- **Director Cycle**：罗纳尔多读取一个冻结 Director Brief、提交 Action Proposal、服务端执行并记录结果的一次决策周期。

### 4.2 Work S 和 Result S

- **Work S**：Agent + Task + Attempt 的当前执行投影。
- **Result S**：Attempt 成功后接受的不可变 Atomic Research Result。
- 两者在 UI 都显示为 S，但数据库身份不同，通过 `produced_by` 关联。
- Work 成功后，UI 中同一圆点原位过渡为 Result S；数据库不得原地把 Attempt 改成 Result。
- 失败、取消或丢失且没有结果的 Attempt 作为终止 Work S 保留。重试创建新的 Work S。

### 4.3 Insight 和 Tier

| Tier | 产生规则 | 后续更新 |
| --- | --- | --- |
| S | 一个被接受的 Atomic Research Result | 可被任意相关更高节点 assimilation |
| M | 至少两个 fresh S promotion | `M + 相关低层输入 → M successor` |
| L | 至少两个 fresh M promotion | `L + 相关低层输入 → L successor` |
| XL | 至少两个 fresh L promotion | `XL + 相关低层输入 → XL successor` |
| XXL | 至少两个 fresh XL promotion | `XXL + 相关输入 → XXL successor` |

Promotion 不允许跳级。补充输入可以参与内容，但不能替代两个同级 fresh 输入的晋级条件。数量只触发 Match，不保证存在语义收益。

### 4.4 Branch 和 Frontier

- Branch 是显式、持久的研究方向，保存 objective、scope、parent、进入/退出条件、状态、创建原因和终止原因。
- Branch Frontier 是该 Branch 当前 fresh、未吸收的 Result/Insight 集合，可以同时存在多个互不包含的节点。
- 每个 Branch 最多有一个当前有效 XXL。历史 XXL revision 和终止 XXL 可以有多个。
- 同一个 Insight 或 XXL 可以同时属于多个 Branch；生成报告时按 canonical node ID 去重。
- Branch 重组、合并或拆分由罗纳尔多决定，不能由视觉聚类或字符串相似度自动完成。

### 4.5 Report

- Report 是挂在 Goal 上的独立交付物，不是节点。
- 每个 revision 记录精确 Goal version、各 Branch 当前 XXL；没有 XXL 的 Branch 记录其 maximal Frontier inputs；同时记录内容哈希、资源清单、作者和验收决定。
- Report 不吸收、不隐藏、不修改任何 Research node。
- Goal 面板默认打开最新 published Report，并列出全部历史 revision。

## 5. 不变量

1. PostgreSQL canonical tables + append-only events 是唯一事实来源。
2. 每个 accepted content node 最多有一个直接 canonical successor。
3. Absorption 成功后输入立即从默认图和 Branch Frontier 消失，但数据库、Passport、Version、Derivation 和用户展开历史永久保留。
4. 两个 Integration 同时吸收同一输入时，先提交的事务成功；后提交者得到 `stale_input`，必须改用 successor 重算。
5. 已吸收节点永不自动恢复。上层节点后来错误时，旧输入只在展开来源中显示；需要挽救时创建 Review Work Item 和新的 Result。
6. 同一 evidence independence key 只计一次，不因跨 Branch 共享 Insight 重复计权。
7. 任何 Insight 内容更新都产生 successor，旧版本 `superseded`，不原地覆盖。
8. 同一精确 Goal/Branch/input revision 组合最多一个 active Discussion。
9. 终止节点永久保留，但不参与后续 Match、Discussion、Promotion、Director Research Brief 或报告输入。
10. Director 不可用时不得自动指定继任者。
11. Report 只有罗纳尔多可以从 draft 变为 published。
12. Report 页面不得接触主应用 origin、Cookie、storage、API、Node/Electron preload 或外部网络。
13. 罗纳尔多发布报告前，Branch 中所有 material unabsorbed content 必须被吸收、明确排除、终止或列为未解决缺口；存在 XXL 时报告不得绕过它重复读取已压缩的低层内容。

## 6. 端到端运行

1. 用户选择罗纳尔多并提交初始 Goal。
2. 服务端创建 Run、Contract revision、Director assignment 和首个 Director Work Item。
3. 罗纳尔多读取 Director Brief，自行建立 Branch、创建 Agent、定义 Mission Prompt/模型/工具并分配 Research Work Item。
4. Agent 从冻结 Manifest 恢复任务，提交严格 Result Envelope。
5. 服务端先接受 Research Result；Match 或 Integration 失败不得回滚已接受 Result。
6. 结果 Agent读取同级目录和所属 Branch 的更高节点，提出不融合、promotion、assimilation 或 dispute candidate。
7. 相关 Steward 进入持久 Discussion。全体同意则 Integration；全体拒绝则记录 Match Decision；混合、无法判断或证据冲突则邀请罗纳尔多。
8. Integration 原子创建 successor、Derivation、Absorption、Branch binding、Steward assignment、Event 和 Projection outbox。
9. Frontier、争议、Agent、失败、用户消息或报告状态变化触发新的 Director Cycle。
10. 罗纳尔多判断 Goal 已满足后创建 Report Work Item。报告不足则继续调研；报告展示不足则继续修改；验收通过后发布并把 Run 标记 completed。
11. completed 后用户继续对话时，罗纳尔多可以创建新 Contract revision 并恢复 running；旧报告和完成判断不改写。

## 7. 罗纳尔多运行模型

### 7.1 权限

罗纳尔多可以在 Research 模块内提出以下 Action：

- 创建、取消、停止、重试、改派任何 Work Item；
- 创建、配置、闲置、恢复、归档 Run Agent；
- 创建、修改、拆分、合并、暂停、终止 Branch；
- 创建 Match、Discussion、Dispute、Integration 和 Review；
- 指定或转移 Steward；
- 裁决冲突，或创建新 Agent/任务获取辅助证据；
- 修改 Goal/Contract revision；
- 创建、拒绝、修改、发布 Report；
- 暂停、恢复、完成或终止 Run。

服务端只执行机械不变量：actor 身份、workspace、expected state/version、DAG、single successor、50 人上限、幂等、权限、Manifest、Artifact Passport、报告沙箱和事务恢复。

### 7.2 Director Brief

每次 Cycle 从数据库编译两部分：

**Research Brief**

- 当前 Goal/Contract revision；
- 每个 Branch 的 objective、scope、状态和 Frontier `brief_summary`；
- 每个 Branch 当前 XXL；
- 跨 Branch 共享节点去重信息；
- 重要不确定性、未解决争议和报告缺口；
- 终止方向只给聚合总结，例如“调研过 X，发现 Y 不符，已停止”，不注入终止节点全文。

**Control Brief**

- 最新用户消息及关联的选中节点 revision；
- Run Agent、空闲/工作/异常状态和当前计数；
- active Work Item、Discussion、Dispute、Integration、Report；
- Director 不可用、provider quota、任务失败、取消确认和最近事件；
- 上次审阅 watermark 后的变化。

Branch 过多时使用持久 Overview + 分页 Frontier Page。Overview 包含总数、状态、Agent、争议和变化范围；同一 Brief 的页面共享 set hash，每页有 page hash。罗纳尔多处理后显式确认页面，服务端保存 watermark；未确认完整页集时拒绝 Action Proposal。下一轮优先读取发生变化的页。系统保证逻辑全覆盖，不把所有全文塞进一次模型调用。

### 7.3 上下文轮换

- Research Director 是持久身份，不是 provider conversation ID。
- 每次 Cycle 都可使用新模型会话。
- 会话接近模型上下文阈值时自动轮换，不影响 Run。
- 任何依赖旧会话但未写入数据库的决定均视为不存在。
- 产品不限制整个 Run 的 token 总量；单次调用仍必须符合模型上下文窗口和 provider 限制。

### 7.4 触发和合并

触发条件包括 Frontier 变化、Discussion escalation、Steward 丢失、Agent failure、用户消息、Goal revision、团队容量变化、Report draft、provider/quota 异常和交付条件变化。短时间内的多个事件合并到一个 through-event watermark，避免重复 Cycle。

## 8. 动态 Run Team

### 8.1 创建规则

- Run 初始只有罗纳尔多。
- 不存在必要成员、固定角色或固定报告 Agent。
- 罗纳尔多决定 Agent 名称、使命、Prompt、模型、工具、权限、预期结果和生命周期。
- `<20`：没有合适空闲 Agent 时偏向创建。
- `20–49`：必须在 Team Formation Decision 中说明能力缺口、并行收益或独立性需求。
- `50`：硬拒绝新建，罗纳尔多必须复用、改派、等待或归档后再创建。
- 计数是一个完整 Run 的 active team membership，不是单个 leaf task，也不是 workspace fleet。

### 8.2 生命周期

- absorbed 后，Steward 若不负责其他 Frontier node，则回到 Run idle pool。
- 罗纳尔多可改派空闲 Agent，也可 soft archive 长期无用 Agent。
- Run 完成后动态 Agent 默认 soft archive；罗纳尔多可明确保留。
- archive/delete 不删除 Task、Attempt、Result、Discussion、Decision、Steward 和 Report attribution。
- Agent 离线、中断或超时触发 Steward Recovery；罗纳尔多选择现有或新 Agent，从数据库重建上下文。

### 8.3 Director 异常

罗纳尔多停止、离线、重复失败、token/provider quota 用光或无法恢复时：

- Run 进入 `awaiting_director`；
- 立即通知用户；
- 已提交结果和事件继续持久化；
- 不创建新的顶层裁决、团队或报告发布动作；
- 用户恢复原 Director 或明确选择新 Director 后继续。

## 9. Agent Runtime Protocol

### 9.1 不可变 System Protocol

所有动态 Agent 共享一份短而不可变的 Runtime Protocol：

- 只执行 assigned Work Item；
- 从 Manifest 恢复，不依赖旧聊天；
- 所有引用使用同 Run 的稳定 ID/version/hash；
- 按 strict Result Envelope 提交；
- Match、Discussion、Integration 和 Decision 必须持久化，私聊不能推进 canonical state；
- 不提交隐藏 chain-of-thought、provider token stream、credential 或无界 tool log；
- 失败、部分完成和不确定性必须显式记录。

### 9.2 Director Mission Prompt

罗纳尔多控制每个 Agent 的 Mission Prompt、persona、领域方法、模型、工具和任务特定 JSON Schema。系统不硬编码 Scout/Reader/Validator/Reporter 的语义。

### 9.3 最小 Atomic Result Envelope

每个 Research Atomic Result 至少包含：

- `work_item_id/task_id/attempt_id/agent_id`；
- `goal_version`、Branch refs 和 input Manifest hash；
- `catalog_summary`：同级目录的一句话摘要；
- `brief_summary`：目标、结论、适用范围、主要证据、不确定性；
- `objective`、`conclusion`、`content`；
- evidence/source/claim refs；
- uncertainty、conflicts、open questions；
- conclusion/integration/termination proposals；
- related-node Match proposals；
- `client_request_id`、schema version、content hash。

三层内容必须在写入时生成并持久化，读取时不得临时重新总结。

## 10. Match、Discussion 和 Integration

### 10.1 节点发现

- Result Agent 可逻辑遍历全部同级 fresh 节点，目录分页并保存 watermark；不是把所有全文注入上下文。
- Agent 同时读取自己 Branch Frontier 中比自己高的相关节点，用于 assimilation。
- 目录项至少包含 ID、revision、tier、Branch、catalog summary、scope、status、Steward 和 content hash。
- 需要详情时按 ID/version 读取 brief 或 full representation。

### 10.2 Match Decision

Match Decision 固定到 node revisions + Goal version + Branch scope。未变化的组合不重复比较。任一输入、Goal 或 Branch scope 改变后旧决定失效。

拒绝原因至少包括：

- `unrelated`
- `no_semantic_gain`
- `duplicate`
- `blocked_by_scope`
- `insufficient_evidence`

### 10.3 Discussion

- “拉小群”实现为 Research Discussion，不使用普通 Channel。
- 输入节点、revision、content hash、Goal version、Branch scope、event watermark 和 participant set 在开始时冻结。
- 相同 scope/version 只能有一个 active Discussion；不同 Branch 可以并行。
- 多节点候选只建立一个多方 Discussion，不拆成两两私聊。
- 只需当前节点 Steward 参加。Assimilation 时是高层 Steward 与新 Agent；不召回历史 contributor。
- 最后加入并成功完成 Integration 的 Agent 成为 successor Steward；只有该 Agent 已离线、归档或在提交时失去资格，才由罗纳尔多立即指派替代者。
- 全部同意：提交 Integration。
- 全部拒绝：记录 Match Decision，不融合。
- 混合意见或无法判断：邀请罗纳尔多。
- 存在事实/证据冲突：同时创建 Dispute。
- 输入 revision 变化：Discussion 变为 `stale_input`，旧 turns 保留，使用最新输入继续或重开。

Discussion 对用户显示完整可见 turns、结构化贡献、投票和决定；不保存隐藏推理。

### 10.4 Promotion 和 Assimilation

- Promotion 需要至少两个 fresh 同级输入，输出高一级；XXL + XXL 输出仍为 XXL。
- Assimilation 允许低层节点更新相关高层节点而不升级 tier，例如 `M(v1) + S3 → M(v2)`。
- 默认从最低相关上层节点开始 assimilation，再把变化向上重新判断；重大反证可直接 challenge 高层。
- Integration 提交时锁定全部 input successor slot、Branch state version 和 Goal version，在一个事务内完成 successor、Derivation、Absorption、Steward、Event 和 outbox。

## 11. 状态和终止

状态维度必须分开：

| 维度 | 状态示例 |
| --- | --- |
| execution | `pending/running/succeeded/failed/cancelled/lost` |
| conclusion | `proposed/accepted/challenged/refuted/invalid` |
| integration | `unmatched/candidate/discussing/absorbed/excluded` |
| termination reason | 见下表 |

标准 `reason_code` 决定 UI 颜色、图案和动效；`reason_detail` 保存罗纳尔多给出的具体说明。罗纳尔多不能用任意字符串直接创建视觉类型，无法归类时使用 `other`。

| reason_code | 语义 | 默认视觉编码 |
| --- | --- | --- |
| `invalid_direction` | 方向错误 | 红色 + X 中心纹 |
| `dead_end` | 死胡同 | 紫红 + 封闭中心 |
| `no_semantic_gain` | 无新增收益 | 冷蓝 + 空心圆 |
| `duplicate` | 内容重复 | 青灰 + 双点纹 |
| `out_of_scope` | 超出当前 Goal | 靛色 + 断开括号纹 |
| `stopped_by_user` | 用户意图导致停止 | 琥珀 + 缺口外圈 |
| `stopped_by_director` | 罗纳尔多判断停止 | 中性灰 + 横线中心纹 |
| `resource_failure` | provider/tool/quota 等异常 | 橙色 + 破损外圈 |
| `superseded` | 被新 revision 替代 | 石板色 + 双环；默认历史中显示 |
| `other` | 尚未归类 | muted + 菱形中心纹 |

终止记录永久保留，供用户核查；不再进入 Match、Discussion、Director Research Brief 或 Report inputs。终止内容在 Director Brief 中只有聚合总结。

## 12. 用户 Steering 和失效传播

### 12.1 用户输入

- 用户只在聊天框用自然语言与罗纳尔多对话。
- 每条用户消息创建 Steering Assessment；不得用关键词或固定语义规则代替罗纳尔多判断。
- 用户选中节点后发送消息时，UI 自动附带可见、可删除的 node ID/revision/summary 引用标签；它是上下文，不是命令。
- 罗纳尔多可以判断为 no-op、局部补充、Branch 停止、Branch 新建、Goal revision、全图复核、Agent/Task 调整或 Report 重做。
- 判断错误时通过新的 Assessment 和 Decision 更正，不改写历史。

### 12.2 传播规则

- 高层节点或 Branch 被用户/罗纳尔多判为终止：停止该分支全部 active descendant Work、Match、Discussion 和 Integration；完成历史保留。
- 低层未吸收 S 的失败、取消或错误不影响现有高层节点。
- 已吸收低层内容后来被 refute：依赖高层标为 `challenged`，由 Steward/罗纳尔多创建 repair/review；不自动恢复旧 S，也不自动删除高层。
- Goal revision 后，罗纳尔多逐 Branch 判断继续、改变方向、停止、补充或重新验证。服务端只按罗纳尔多提交的影响集执行 versioned transition。

## 13. PostgreSQL 持久化设计

### 13.1 复用现有表

| 现有表 | 目标用途 |
| --- | --- |
| `research_session` | Run root、Goal/State version、Director 和生命周期 |
| `research_contract_revision` | Goal/Contract revisions |
| `research_task` / `research_task_attempt` | Research Task 与执行 Attempt |
| `research_result_artifact` | Result S 原始 strict envelope 与 Passport |
| Source/Observation/Claim/Evidence tables | 证据事实 |
| `research_branch` | Branch 规范记录 |
| `research_insight` | M/L/XL/XXL 内容节点 |
| `research_integration_round` / `research_insight_derivation` | Integration 和来源 |
| `research_dispute*` / `research_deliberation*` | 争议兼容数据，迁入通用 Discussion |
| `research_report` | Report revision envelope |
| `research_run_event` | committed event sequence |
| Artifact Passport/Version/Manifest tables | 访问、版本、来源和冻结上下文 |
| `research_graph_node/edge` | 仅 V1–V5 兼容 Projection；不得成为 V6 canonical state |

### 13.2 必须新增或扩展的规范表

以下名称是开发合同，迁移实现不得另建同义平行表：

1. `research_director_assignment`
   - Run、Director Agent、generation、assigned_by、reason、active interval。
   - 一个 Run 只允许一个 active assignment。
2. `research_director_cycle`
   - trigger event range、Brief manifest/hash、model session、Action Proposal、execution outcome、review watermark、failure。
3. `research_steering_assessment`
   - user message、selected refs、Goal before/after、affected Branch/node refs、interpretation、actions、Director cycle。
4. `research_team_membership`
   - Agent、formation Decision、Mission Prompt revision、model/tools/config、state、joined/left、terminal reason。
5. `research_work_item`
   - kind=`research|match|discussion|integration|director|report|review`、typed target、assignee、state、priority、lease、attempt budget、idempotency key、input watermark。
6. `research_result_node`
   - 一对一绑定 `research_result_artifact`；保存三层摘要、objective、conclusion、scope、uncertainty、conflicts、open questions、全局 conclusion/integration/termination 状态。
7. `research_insight_version`
   - logical Insight family、revision、tier、三层内容、scope、状态、content hash、created by Integration。
   - 现有 `research_insight` 作为 family/root 或迁移为当前版本索引，不在一行原地覆盖内容。
8. `research_node_branch`
   - typed content node 与 Branch 的多对多绑定；不保存分支级 absorption 状态。
9. `research_node_absorption`
   - input artifact/version、successor Insight/version、Integration、absorbed_at。
   - input version 唯一约束实现 single successor。
10. `research_node_steward_assignment`
    - node/version、Agent、generation、assigned/released、reason；一个 fresh node 只允许一个 active Steward。
11. `research_match_decision`
    - canonical candidate set hash、Goal version、Branch scope hash、decision/reason/detail、input revisions。
12. `research_discussion`
    - kind、scope hash、status、input watermark、Director escalation、stale reason。
13. `research_discussion_input/participant/turn/vote`
    - 冻结输入、participants、完整可见 turns、结构化 vote 和 contribution。
14. `research_branch_frontier`
    - Branch 与 fresh content version 的当前索引；由 Absorption 事务维护，可从历史重建。
15. `research_director_brief_page`
    - Research/Control page、through event、page key、content hash、summary bytes、reviewed watermark。
16. `research_report_input`
    - Report revision 与精确 Goal/Branch/node versions；共享节点唯一去重。
17. `research_report_resource`
    - immutable HTML package resource、MIME、byte hash、size、inline mapping。
18. `research_report_review`
    - Director decision、result=`published|needs_research|needs_revision|technical_failure`、reason、follow-up refs。
19. `research_work_catalog_page`
    - Attempt、同级/高层候选视图、tier/Branch scope、through event、page key/hash、cursor 和 reviewed watermark；Agent 中断后从已审页继续。

### 13.3 现有表修改

- `research_session.fleet_id` 对 V6 变为可空兼容字段；V6 team 不从 `research_fleet_member` 推导。
- `research_session.status` 增加 `awaiting_director`。
- `research_task.kind` 不再作为 V6 固定角色枚举。V1–V5 保留旧 CHECK；V6 使用 `task_type`/schema ref 表达 Director 自定义任务。
- `research_branch` 增加 goal version、scope、state version、reason code/detail 和 Director/Attempt provenance。
- `research_report` 增加 lifecycle status、对象存储 key/generation、HTML document/package hash、plain-text fallback、outline/citation metadata、parent revision 和 published_at；不可变 package bytes 放在现有持久对象存储，不复制进数据库大字段。
- 所有会进入 Agent/Director Manifest、被其他研究对象引用或承载内容/决定的新对象必须创建 Artifact Passport/Version，并使用 scoped composite FK。membership、lease、pointer、分页确认和 Projection bookkeeping 只记录运行/读取状态，不伪装成研究 Artifact；其可见页面仍由 Context Manifest、page hash 和输入 Artifact Version 固定。

### 13.4 事务、恢复和事件

- 所有写操作通过 `beginResearchTx/commitResearchTx` 和稳定 operation label。
- 每个 operation 进入 before-commit rollback、after-commit-unknown、幂等重放和 reconcile matrix。
- Integration、high-level cascade、Director assignment 和 Report publish 必须各自单事务提交 canonical row + Event + outbox。
- 外部 Agent 创建、Inbox dispatch、HTML resource upload 使用 durable outbox；数据库先记录 intent，再执行 Adapter。
- lease 到期只允许其他 worker 接管 Work Item，不允许复制 accepted result。

## 14. 后端 Module 设计

Research Run 继续是外部 Deep Module。Handler、scheduler 和 CLI 只依赖窄用例 Interface，不获取子表 Store。

| Module | Interface | 隐藏的 Implementation 复杂度 |
| --- | --- | --- |
| `directorModule` | `RunDirectorCycle`, `ReplaceDirector` | Brief、Action Proposal、事件合并、上下文轮换、异常 |
| `contextCompilerModule` | `CompileDirectorBrief`, `CompileWorkManifest` | 分页、token budget、授权 representation、水位 |
| `teamModule` | `ApplyTeamActions`, `RecoverSteward` | 20/50 规则、Agent Adapter、membership、archive |
| `workItemModule` | `Claim`, `Complete`, `Fail`, `Cancel`, `Reconcile` | lease、retry、Inbox identity、outbox |
| `knowledgeGraphModule` | `AcceptResult`, `Promote`, `Assimilate`, `Challenge` | tier、DAG、single successor、Branch Frontier、Steward |
| `discussionModule` | `Open`, `AppendTurn`, `Vote`, `Escalate`, `Close` | frozen inputs、participant、stale、Dispute |
| `steeringModule` | `AssessUserMessage`, `ApplyAssessment` | Goal revision、影响集、cascade、audit |
| `reportModule` | `CreateDraft`, `ValidatePackage`, `Review`, `Publish` | HTML package、CSP、resource、input snapshot、revision |
| `projectionModule` | `Snapshot`, `Slice`, `Delta`, `NodeDetail` | semantic LOD、collapsed path、stable ID、pagination |

Seam 只放在 provider/Agent creation、Inbox dispatch、object storage、HTML serving 和 model invocation 等外部 Adapter 边界。PostgreSQL 事务和领域规则保持在 `server/internal/researchrun`，提高 Locality；不得重新引入一个覆盖所有用例的全能 Store Interface。

## 15. API 和消息合同

### 15.1 用户面

- 用户仍通过 Research message/chat endpoint 发送自然语言。
- 消息 payload 可附 `selected_research_refs[]`，每项包含 stable ID、kind、revision 和 display summary。
- 前端不得发送 stop/merge/replan 等业务命令模拟用户判断。

### 15.2 Director/Agent 面

- Director Action Proposal 和 Agent Result 使用新的 strict V6 schema，所有 owned object `additionalProperties: false`。
- Action Proposal 是 generic action list，不固定 Agent 角色或研究方法。
- 服务端返回逐 action accepted/rejected/conflict 结果；部分 mechanical rejection 不允许静默执行其余语义依赖动作。

### 15.3 Projection 面

- Snapshot 固定 `snapshot_id + through_event_sequence`，支持 Branch、tier、status、viewport Slice 和 one-layer expansion。
- Delta 只来自 committed `research_run_event`，sequence 必须连续；缺口重新获取 Snapshot。
- Node Detail 必须携带当前画布的 `snapshot_id`，按该快照返回 brief/full/history/discussion/report refs；不得为详情请求新建或静默切换 Snapshot，也不在初始 Snapshot 携带全部正文。

### 15.4 Report 面

- Goal detail 返回 Report metadata 和历史，不内联 HTML。
- Report document 由独立资源 origin 返回；canonical path 绑定 immutable revision/package hash，实际 `sandbox_url` 使用短期签名 capability，并带 CSP、ETag 和 content hash。ID/hash 本身不授权读取。
- 旧 Markdown Report 继续使用现有 reader；新 V6 优先打开 HTML Report modal。

## 16. 星图 Projection 和交互

### 16.1 默认图

默认显示：

- Goal 根节点；
- 每个 Branch 的 Frontier M/L/XL/XXL；
- 每个当前和历史终止的高层节点；
- 全部未吸收 Work S 和 Result S，包括运行、待融合、错误、停止、无效、弯路、无收益；
- 已吸收 S/M/L/XL/XXL 默认隐藏。

Goal 到 Branch Frontier 使用明确标记的 `collapsed_path` Projection edge。它不是 canonical edge，必须包含 hidden count、snapshot ID 和可展开标识。跨 Branch 共享节点只画一次。

### 16.2 S 微型圆点

- S 节点本体不显示文字。
- running 使用呼吸外圈；pending、terminal、failed 使用各自图案。
- 状态同时依赖颜色、外圈和中心纹，不只依赖颜色/动效。
- `prefers-reduced-motion` 下 running 改为静态双环。
- 可见圆点可以很小，但 pointer/keyboard hit area 不小于 24×24px；触摸面不小于 40×40px。
- 悬停不显示错误原因文字。点击后才在右侧检查面板显示。

### 16.3 布局和连线

- S 围绕所属 Branch Frontier 的相关上层节点布局；没有上层时围绕 Branch/Goal 区域。
- 默认不显示 S 连线，避免线团。
- 选中 S 时高亮它到 Branch、Task、候选节点和 successor/history 的关系。
- 吸收动画表现输入节点向 successor 汇聚后消失；动画结束前 canonical state 已提交，动画失败不能回滚数据。
- Work S 成功后原位过渡为 Result S；失败旧 Attempt 保留，retry 显示新圆点。

### 16.4 语义缩放

- 是否显示 M/L/XL/XXL 文字由屏幕实际尺寸、碰撞和当前选择决定，不按 tier 固定。
- 点击 M+ 自动聚焦放大并显示等级与摘要。
- 点击 Goal fit 全局，各节点按可读空间决定标签。
- S 永不在节点上显示原因文字。
- 大图远景允许视觉密度聚合：相同 Branch/状态的点形成数量点阵；放大恢复真实节点。聚合只属于 Projection，不创建节点。

### 16.5 展开和检查面板

- 点击 XXL 一次只展开其有效 XL constituents；点击 XL 展开 L，以此类推。
- XXL assimilation revision 的组成视图展示当前有效 XL constituents；revision history 单独显示变更链。
- 右侧检查面板显示状态、reason detail、Agent、Task、目标、结论摘要、证据、时间、Discussion、版本和来源。
- 点击空白关闭；点击另一节点切换；键盘 focus 和 selection 行为一致。
- 面板只提供查看、展开、筛选和定位，不提供停止、纠正、融合等业务按钮。

### 16.6 高层终止节点

- 高层节点终止后仍留在当前图，使用弱化样式和明确错误图案。
- 罗纳尔多创建纠正 Branch 后，旧终止节点与新 active Frontier 同时显示。
- absorbed descendants 不恢复；active unabsorbed descendant Work 被停止后以 terminal S 显示。

## 17. HTML Report

### 17.1 生成和验收

1. 罗纳尔多判断当前 Goal 信息足够，创建 Report Work Item 和任意 Report Agent。
2. Agent 提交完整 HTML package、元数据、目录、引用、plain-text fallback 和 input refs。
3. 服务端执行机械验证：完整文档、资源 hash、引用存在、包体上限、CSP、无外部请求、无危险 sandbox capability。
4. 罗纳尔多查看真实渲染截图和内容，判断：
   - `needs_research`：信息不足，继续调研；
   - `needs_revision`：内容或视觉不足，继续修改；
   - `technical_failure`：包或沙箱失败，重新生成；
   - `published`：满足当前 Goal。
5. rejected draft 永久保留，但 Goal 默认只打开 latest published。

### 17.2 展示

- 点击 Goal 打开 Goal 面板；选择报告后打开接近全屏的 modal。
- modal 内使用独立 origin 的 sandboxed iframe。未部署第二 HTTPS origin 时，已登录成员读取 `GET .../reports/{id}/compiled`，前端用 `blob:` + `sandbox="allow-scripts"` 展示；禁止 `srcdoc`、禁止把带 cookie 的 API URL 当 iframe `src`、禁止把 compiled HTML 塞进 JSON detail。独立 origin 配好后仍走 capability URL。
- 没有独立 renderer 时，Director 仍可在 HTML/hash/CSP 校验通过后审到 `published`，跳过截图；有 renderer 时必须看真实渲染截图。
- Report 可以包含动画、图表、目录和前端筛选 JavaScript。
- 报告关闭后不得继续在后台运行高负载脚本。

### 17.3 沙箱合同

- iframe 只允许 `sandbox="allow-scripts"`；禁止 `allow-same-origin`、forms、popups、downloads、top navigation 和 storage access。
- Report URL 自身也返回 CSP `sandbox allow-scripts`，直接打开不能逃离沙箱。
- `default-src 'none'`、`connect-src 'none'`、`object-src 'none'`、`frame-src 'none'`、`worker-src 'none'`、`base-uri 'none'`、`form-action 'none'`。
- Script body 由服务端计算 hash 并写 `script-src`；禁止 `unsafe-eval` 和未声明外部脚本。
- HTML、CSS、JS、图片和字体先组成不可变 package；服务端验证后把脚本和样式内联、把图片和字体嵌入 data，生成单一 compiled HTML。发布页只返回该文档，运行时不得请求 package 子资源、CDN 或任意 URL。
- CSP 的 `script-src` 和 `style-src` 只列出服务端从 compiled bytes 计算的 hash；`img-src data:`、`font-src data:` 只允许文档内嵌资源。
- 不提供通用 `postMessage` bridge；如果未来增加受控消息，必须固定 origin、schema 和 allowlist。
- Web 和 Electron 使用同一合同；Electron 不使用带 preload/Node integration 的 webview。
- 对包体、DOM 节点、script bytes、初始化时间和持续 CPU 占用设置服务端/客户端上限，超限终止 iframe 并显示可重试错误。

## 18. 失败和恢复场景

必须通过以下场景：

1. Agent 在提交 Result 前中断：Attempt lost/failed，retry 产生新 Work S。
2. Agent 在数据库 commit 后、收到响应前中断：相同 request ID 幂等返回同一 Result。
3. Integration commit outcome unknown：reconcile 根据 absorption unique slot 收敛到一个 successor。
4. Steward 离线：Frontier 不丢失，罗纳尔多从 DB 指派新 Steward。
5. Director 会话重置：新会话从 Director Brief 继续。
6. Director provider quota 用尽：`awaiting_director` + 用户通知，不自动替换。
7. 服务重启：Work Item lease 过期后接管；不重复 Agent、Result、Discussion 或 Report。
8. Goal 在 Discussion 中改变：Discussion `stale_input`，原 turns 保留。
9. 高层 Branch 被用户否定：active descendants 停止，absorbed history 不恢复。
10. 两个 Integration 抢同一 S：一个成功，另一个 stale 并重定向 successor。
11. Report JS 尝试 fetch、storage、top navigation、popup 或 parent DOM：全部失败。
12. 用户在 completed 后继续对话：新 Contract revision，旧 Report 仍可打开。

## 19. 兼容和发布

- V1–V5 Run 固定原 orchestrator version，不迁成 V6。
- 旧 `research_fleet`、固定 Task kind、Markdown Report 和 S1–S4 stage 继续服务旧 Run。
- V6 新 Run 不创建固定 Fleet，不展示虚构的“预计 Agent 数”或固定角色；首页需按 orchestrator version 做兼容 Projection。
- `docs/superpowers/specs/2026-08-14-research-home-command-center-spec.zh-CN.md` 中固定 Fleet/阶段展示对 V6 的冲突内容由本规格覆盖，旧 Run 仍按原规格显示。
- V6 schema、prompt、result、gate、builtin skill 和 source map 必须在同一实现系列中更新。
- 省略 `orchestrator_version` 时默认 V5；显式指定 V6 和主理人可以创建 V6 Run。`AssessV6Activation` 审计是否具备把省略版本的默认值切到 V6 的条件，不关闭显式 V6 创建。
- 默认版本激活只影响新 Run。工作区 release control 可独立关闭新的 V6 创建并暂停现有 V6 Run；回滚不用 V5 decoder 读取已创建的 V6 Run。

## 20. 测试和验收

### 20.1 数据库和领域

- migration fresh/up/down/up；旧 V1–V5 fixture 完整读取。
- single successor、同 Branch 当前 XXL 唯一、active Director 唯一、active Steward 唯一、50 membership cap。
- promotion/assimilation/XXL ceiling/property-based DAG 测试。
- concurrent absorption、Discussion uniqueness、Goal revision stale、report publish CAS。
- before-commit、after-commit-unknown、idempotent replay 和 reconcile recovery matrix。
- terminal exclusion、absorbed input 不恢复、cross-Branch shared node/evidence dedupe。

### 20.2 Director 和 Runtime

- Director Brief 不包含 absorbed child full content 或 terminal detail。
- 数百 Branch 分页、watermark、changed-page 优先和 context byte/token bound。
- model session rotation produces equivalent Action input facts。
- dynamic Agent 0→N、20 threshold、50 rejection、archive/reassign/recovery。
- every user message creates Steering Assessment，包括 no-op。
- arbitrary Mission Prompt/task schema 不绕过 immutable Runtime Protocol。

### 20.3 UI

- 默认图包含全部 unabsorbed S，absorbed S 不出现。
- running breath、reduced-motion 静态双环、reason color+pattern、keyboard focus 和 screen-reader name。
- 24/40px hit target、右侧检查面板、selected edge、message node chip。
- semantic zoom label collision、Goal fit、M+ focus、one-layer expansion。
- 1k/10k/50k S 的远景密度、viewport Slice、内存、帧率和点击定位。
- 高层终止 + 新纠正 Frontier 并存。
- Web/Desktop parity；未知 enum 和 malformed Projection 安全降级。

### 20.4 Report 安全和视觉

- sandbox escape、same-origin、Cookie/storage/API、fetch/XHR/WebSocket/Beacon/image exfil、popup、download、top navigation、postMessage、worker、object/embed 全部负向测试。
- Electron 无 preload/Node API。
- 直接打开 Report URL 仍受 CSP sandbox。
- script hash、resource hash、immutable cache、旧 revision 可重现。
- 1440×900、1920×1080、窄屏 modal 截图；长报告、目录、表格、SVG、动画和关闭后的资源释放。

### 20.5 完成命令

实现完成前至少执行：

```bash
make test
pnpm test
pnpm typecheck
pnpm lint
pnpm react:doctor
make check
```

前端修改完成后执行 impeccable detector，并用真实 Web/Desktop 浏览器完成截图和交互 QA。没有迁移恢复证据、V1–V5 golden、V6 activation audit、报告沙箱负向测试和真实大图性能结果，不能宣称完成。

## 21. 实施顺序

1. 冻结新 V6 JSON Schema、Runtime Protocol、Action/Result Envelope 和状态枚举。
2. 添加迁移、约束、Passport/Version、事件和 recovery matrix。
3. 实现 Work Item、Knowledge Graph、Discussion、Team、Director Brief、Steering 和 Report Module。
4. 接入 dynamic Agent Adapter、Inbox dispatch、Director cycles 和 failure recovery。
5. 实现 V6 Snapshot/Slice/Delta/Detail Projection。
6. 实现星图默认视图、semantic zoom、S 编码、检查面板和节点引用聊天上下文。
7. 实现 HTML Report package、独立资源 origin、sandbox modal 和 Ronaldo review。
8. 更新首页 V6 Projection、builtin skill、source map、docs 和兼容 schema。
9. 通过全套测试和 activation audit 后，只对新 Run 启用 V6。

## 22. 被否决的方案

- **全局唯一 XXL**：不符合每个 Branch 独立压缩。目标态允许多个 Branch XXL，最终 Report 汇总。
- **同一 S 被多个 Branch 分别吸收**：会重复内容和证据。目标态只允许一个 successor，其他 Branch 使用上层节点。
- **终止节点聚成一个异常节点**：隐藏用户要求看到的工作量。目标态保留全部未吸收 S，远景聚合只影响渲染。
- **每个 Branch 强制一个 Frontier node**：会丢失暂时无法融合但都有效的内容。目标态 Frontier 是集合。
- **固定 Scout/Reader/Validator/Reporter**：限制罗纳尔多按问题组织团队。目标态只有不可变 Runtime Protocol，没有固定角色。
- **永久 Director 模型会话**：长期必然超过上下文窗口且无法恢复。目标态每 Cycle 从 DB 重建。
- **报告作为全局 XXL 或 graph node**：会把交付格式与研究事实混在一起。目标态 Report 只挂 Goal。
- **任意同源 HTML/JS**：会形成持久 XSS 和数据外传。目标态允许 JS，但只能在独立沙箱运行。
- **为旧未发布 V6 再建 V7**：保留无生产兼容价值的废弃协议。ADR-0017 允许原位替换一次。

## 23. 冻结状态

本规格没有未决产品问题，当前允许拆分实施任务和评审合同。运行时代码、SQL migration、生成代码和前端资源只有在用户另行授权后才开始修改。开发必须先解决当前工作区未完成的 merge/rebase、更新到当时的 `origin/dev`，再按文件级实施计划复核路径与 migration 编号。
