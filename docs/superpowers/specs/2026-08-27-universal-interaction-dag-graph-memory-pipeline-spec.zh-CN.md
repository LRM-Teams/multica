# Universal Interaction DAG 与 Graph Memory Pipeline 规格

- 日期：2026-08-27
- 状态：设计已确认；实施待开始
- 范围：把所有 Workspace 的所有 Task 交互持久化为 Universal Interaction DAG，并为 Graph Workspace 建立安全、可遍历、可整理、可训练的 Atom → Graph pipeline
- 配套计划：`docs/superpowers/plans/2026-08-27-universal-interaction-dag-graph-memory-pipeline-plan.zh-CN.md`
- 前置规格：
  - `2026-08-17-graph-memory-scope-design.md`
  - `2026-08-21-graph-memory-backtest-explore-redesign-spec.zh-CN.md`
  - `2026-08-24-graph-memory-recall-continuation-spec.zh-CN.md`
  - `2026-08-25-graph-memory-agent-mode-spec.zh-CN.md`

## 1. Problem Statement

当前 Interaction DAG 与 Graph Memory 之间存在结构性断层：

1. Interaction DAG 只覆盖 AReaL training task 和 non-training env-dispatch task；普通 Agent task、Legacy Workspace task 和无 Project scope 的 task 不形成统一、可审计的交互轨迹。
2. Segment 只在 delegation 或 terminal 时关闭，并由 caller 以“一 Task 最多一个 Segment”保护；resident 或长生命周期 Task 在第一次输出后的后续输入、输出无法形成新的 generation。
3. Graph ingest 由 detached goroutine best-effort 调用；Server 重启、临时存储故障或摘要失败可能永久漏写，且没有 durable outbox、dead-letter 和可重放状态。
4. Graph staging 保存 Segment 摘要但没有原子事实层。一个 Segment 内多个事实无法独立消费；任意 Graph node 引用整个 Segment 都可能让尚未整理的事实失去可见性。
5. Hybrid Retriever 当前把 pinned Graph version 的所有 node 与目录内所有 staging 混在同一索引。Staging 没有读取 Segment scope sidecar，因而在共享 Project Graph 中可能绕过 exact-channel `GraphView`。
6. Staging 可以作为 seed 被读取和 citation，但不能 expand。Interaction DAG edge 与 Memory Graph edge 是两套互不相连的拓扑，Memory Agent 无法从最近 Segment 沿 `continues/responds_to/delegates_to/mentions` 事实关系继续探索。
7. Consolidation 每次读取全部 staging，缺少 durable atom coverage ledger。成功 winner、失败 candidate、未切换 TTT 与 active staging 状态之间没有原子消费边界。
8. Project-visible promotion 的语义授权、证据要求、来源可见性和纠错路径不完整。模型输出可能事实上承担 ACL 扩权，而 Server 只进行形状校验。
9. Channel rebind 或 standalone channel 绑定 Project 后，Graph 数据可能继续留在旧目录或需要多 Graph 联邦读取；当前 Agent gateway 只打开一个 Graph directory，无法完整实现 lineage 语义。
10. Source deletion、历史 Graph version、归档 evidence、query trace 与派生 Graph node 之间没有端到端 retraction contract。
11. Interaction trajectory、Memory Explore、Consolidation candidate 与训练 reward 没有统一的 eligibility、selection、revision、deletion 和 exactly-once 交付模型。
12. 现有 Graph Memory Agent 可以长期运行，但“持续 session”与“单次 trajectory 无限运行”没有被清晰分离；没有 checkpoint rollover 的硬边界会放大循环、成本和恢复风险。

团队需要一个统一 pipeline：Interaction DAG 是 Workspace 级不可变事件事实源；Graph Memory 是有 scope 的投影；最近事实以 active Memory Atom 低延迟可搜、可沿 DAG 探索；Consolidation 把 Atom 原子消费为版本化 Graph；训练只消费显式选择、可审计、有 reward 的 trajectory。

## 2. 目标与非目标

### 2.1 目标

- 所有 Workspace、所有 Task 都形成 Universal Interaction DAG，不依赖 `memory_type`、AReaL 或 env-dispatch。
- 长生命周期 Task 按 durable inbound/outbound 边界形成连续 generation，sequence 无重叠、无缺口。
- Segment close 与 durable outbox 同事务；sanitization、atomization、publish 可恢复、幂等、可告警。
- Graph Workspace 只投影有 canonical Project/Channel scope 的已发布 Segment；Legacy 与无 scope Task 仍完整保留 DAG。
- 以 Memory Atom 作为 active/consumed 最小单位；同一 Segment 的不同事实可独立进入 Graph。
- 默认 Search 同时覆盖当前有效 Graph node 与 active Atom，并在每层重新执行 scope/status filter。
- Memory Agent 可沿 Interaction DAG 事实边和 Memory Graph 语义边 Explore，同时保留 typed ref、来源、方向和水位线。
- Consolidation winner、Graph version、node→atom provenance 与 atom consumption 原子提交。
- Channel 内容只有通过可验证 durable evidence 和 Server policy 才能生成 Project-visible 派生节点。
- Channel migration 只搬 channel-owned 内容，不携带旧 Project 共享知识，并保留历史 citation redirect。
- Source deletion 可以撤销 trajectory、atom、citation、当前与历史 Graph 内容。
- Tenant-local 与 pooled training 分开授权；selection manifest、reward ledger、删除 ledger 可重放。
- 使用 shadow telemetry 决定生产数值，安全硬上限从第一阶段即存在。

### 2.2 非目标

- 不把普通频道每条 raw message 直接变成独立 Graph node。
- 不让 Memory Agent、Atomizer、Consolidator 或调用方自报 Workspace、Graph owner、scope、version 或授权。
- 不把 Interaction DAG 的事实边物理复制成 Memory Graph semantic edge。
- 不让 channel rebind 自动把 channel-visible 内容 promotion 为 project-visible。
- 不把“已为 Memory 保存”解释为“已被某次训练选中”或“允许跨 Workspace pooled training”。
- 不承诺 shared model 对单样本即时、精确 unlearning。
- 不在本规格中固定最终 production quota/reward 权重；先提供保守 bootstrap 与硬上限，再由 shadow evidence 冻结。
- 不要求 v1 node-only 客户端理解 Atom、Segment、watermark 或 DAG traversal；新能力进入 versioned v2 contract。

## 3. Solution

建立一个深模块 `UniversalInteractionDAG` 作为 Task lifecycle、Graph projection、Explore 和 training 的唯一 Interaction DAG seam。Caller 只提交 canonical Task/message 事件，不自行计算 generation、sequence range、scope、publish 状态或 edge。

整体数据流：

```text
Task/message transaction
  → UniversalInteractionDAG close/open generation
  → segment metadata + ingest outbox
  → sanitize six-field trajectory
  → atomize trusted content
  → publish segment + atoms + publish_seq
  → Graph Workspace scope projection
  → class-aware Search(Graph nodes + active atoms)
  → typed Explore(Graph edges + Interaction DAG edges)
  → Consolidation winner atomically consumes atoms
  → Dive/backtest delayed reward
  → explicit training selection/export
```

Interaction DAG 是 Workspace 级 event SoT；Graph directory、Graph version、staging Atom 和 Project-visible node 都是可重建、受 scope 约束的投影。Graph failure 不得破坏 DAG；关闭 Graph Search 不得停止 durable DAG write；关闭 training 不得影响 Memory。

### 3.1 现有两套 DAG 的收敛

当前代码中的两套表承担不同角色，实施后不得长期保留两个可独立写入的事实源：

- `interaction_dag_segment` / `interaction_dag_edge` 演进为 Universal Interaction DAG canonical store，承载 Workspace identity、Task generation、message range 和 durable event edge。
- `interaction_dag_run_segment` / `interaction_dag_causal_edge` 继续作为 env-dispatch / Mixed-RL frozen run snapshot projection，保留 provider-call ownership、reward、freeze guard 与 canonical manifest 语义，但不再接受绕过 Universal DAG 的独立事实写入。

迁移顺序：先为 frozen run row 增加 canonical Universal Segment/Edge mapping；新 writer 只写 Universal DAG，再由同事务或幂等 projector构建 run snapshot；Assembled DAG、diagnosis 与通用 Memory reader迁到 Universal DAG；env-dispatch freeze/export reader继续读 frozen projection。旧 row backfill mapping 后关闭长期 dual-write。任何 projection mismatch 使 freeze/export fail closed，不回退旧独立 writer。

这不是删除 Mixed-RL snapshot：snapshot 仍是训练 run 的不可变交付物；被删除的是“它可以与 Universal DAG 各自描述不同交互事实”的双 SoT 状态。

## 4. User Stories

1. 作为 Workspace 成员，我希望所有 Agent Task 都留下统一交互 DAG，以便任务是否使用 AReaL 不再决定可审计性。
2. 作为 Legacy Workspace owner，我希望 Task 也进入 Interaction DAG，但不启动 Graph staging，以免意外改变 Memory 产品模式。
3. 作为 Graph Workspace owner，我希望有 Project 或 Channel scope 的 Task 自动形成 Graph staging，以便最近工作可被召回。
4. 作为无 scope Task 的执行者，我希望轨迹仍进入 DAG，但不被猜测性写入某个 Graph。
5. 作为 resident Agent，我希望每次 durable input/output 都形成连续 generation，以免首次回复后的工作丢失。
6. 作为审计者，我希望同 Task generation 的 message sequence 无重叠、无缺口并可幂等重放。
7. 作为用户，我希望 streaming chunk 和中间 tool event 不产生大量空 Segment。
8. 作为平台管理员，我希望 Server 重启不会丢失待 ingest Segment。
9. 作为平台管理员，我希望 transient、deterministic、authorization 和 redaction failure 有不同重试/终态。
10. 作为安全管理员，我希望 sanitizer 失败永不回退保存或发送未清洗正文。
11. 作为安全管理员，我希望 tool input/output 只有通过 trust class、redaction 和 size limit 后才能形成 Atom。
12. 作为 Memory 用户，我希望一个 Segment 中多个事实可独立检索和整理。
13. 作为 Memory 用户，我希望寒暄或无长期价值轨迹允许产生零 Atom。
14. 作为 Memory 用户，我希望尚未整理的新事实立即可搜，而不是等待 consolidation。
15. 作为 Memory 用户，我希望已整理事实优先通过 Graph node 呈现，避免 raw staging 重复淹没 Top-K。
16. 作为历史查询用户，我希望 superseded/invalidated node 不抢占默认结果，但可以显式沿历史关系查看。
17. 作为频道成员，我希望 raw channel Atom 只在来源频道可见。
18. 作为 Project 成员，我希望 Project-only Task 的 Atom 可在 Project 内共享。
19. 作为 DM 成员，我希望 DM 内容即使绑定 Project 也不自动扩散。
20. 作为 Memory Agent，我希望命中 Atom 后能沿其所属 Segment 的事实边继续探索。
21. 作为 Memory Agent，我希望区分 Graph node、Atom 与 Segment，不用猜 ID 类型。
22. 作为 Memory Agent，我希望知道邻居来自 Interaction DAG 还是 Memory Graph，以及边方向和类型。
23. 作为 Memory Agent，我希望 consumed Segment 仍可通过 DAG/evidence 查看，但不参与默认 Search。
24. 作为 Memory Agent，我希望单 trajectory 有硬预算，长期 session 通过 checkpoint 滚动继续。
25. 作为 Memory Agent，我希望 rollover 后读取最新 Graph/version/watermark，同时旧 ref 重新校验。
26. 作为安全管理员，我希望每次 Search、view、expand、evidence、citation 和 submit 都重新检查 ACL。
27. 作为 Project 成员，我希望 channel 内容只有在具备 durable evidence 时才成为 Project Memory。
28. 作为频道成员，我希望 promotion 创建新的派生节点，不修改或公开原始私密 Segment。
29. 作为 Project owner，我希望可以立即 retract 错误记忆。
30. 作为普通成员或 Agent，我希望可以提交 correction candidate，但不能直接删除共享知识。
31. 作为跨频道读者，我希望 citation 不泄露无权访问的来源频道、作者或消息。
32. 作为原频道成员，我希望在权限仍有效时按需下钻查看受限 evidence。
33. 作为频道成员，我希望 Channel 换绑 Project 后自己的历史 channel Memory 仍可用。
34. 作为旧 Project 成员，我希望迁出的频道内容不再出现在旧 Project Search。
35. 作为新 Project 成员，我希望迁入内容保持 channel-only，除非之后独立 promotion。
36. 作为审计者，我希望旧 citation 经 canonical redirect 解析，而历史事件 provenance 不被改写。
37. 作为删除请求者，我希望删除源消息后所有可反推正文的 trajectory、Atom、citation 和 Graph 派生物被撤销。
38. 作为审计者，我希望删除后仍保留不可逆 hash、时间和 reason，而不是保留正文。
39. 作为 Workspace owner，我希望配置 trajectory、trace、archive 和 training retention。
40. 作为安全管理员，我希望 archive restore 重新检查当前 ACL，并有短期授权与审计。
41. 作为 Workspace owner，我希望 tenant-local 与 pooled training 独立开关。
42. 作为训练管理员，我希望 `eligible`、`selected`、`exported`、`consumed` 和 `retracted` 是不同状态。
43. 作为训练管理员，我希望训练 manifest 固定样本、policy、sanitizer、hash 和目的。
44. 作为训练管理员，我希望 managed Memory Agent 的派生回复不被当作独立 ground truth。
45. 作为 RL 训练者，我希望 Explore reward 来自独立 Dive Agent 的 relevance、groundedness、completeness 和真实轮次成本。
46. 作为 RL 训练者，我希望 Judge failure 表示 unavailable，而不是伪造 reward=0。
47. 作为 Consolidation 训练者，我希望 reward 衡量相对 baseline 的质量与效率增益，而不是只奖励 winner 身份。
48. 作为训练审计者，我希望 reward revision 不覆盖已消费历史值。
49. 作为训练审计者，我希望 evaluation、hidden holdout 和 safety canary 分离。
50. 作为成本管理员，我希望超预算先关闭 reranker/embedding并延迟 atomization，而不是丢 DAG。
51. 作为运维人员，我希望 shadow rollout 对泄漏、sequence、redaction、删除和成本有硬门。
52. 作为旧客户端维护者，我希望 v1 node-only contract 在兼容期内不因 v2 上线改变含义。
53. 作为新客户端维护者，我希望 v2 使用 typed refs、watermark、evidence 和 checkpoint contract。
54. 作为 UI 用户，我希望 citation 明确标识 Consolidated Memory、Recent Unreviewed Observation、Historical Evidence 与 Restricted/Retracted Source。
55. 作为后台维护者，我希望历史 backfill 明确标记 approximate boundary，不伪造不存在的 generation 或 edge。

## 5. 核心不变量

1. `workspace_id` 是 Interaction DAG tenant identity，所有 Segment 与 Edge 必须属于同 Workspace。
2. Interaction DAG 记录所有 Task；Graph projection 只处理 `memory_type=graph` 且有 canonical scope 的已发布 Segment。
3. Generation range 只能由 Server 在 Task cursor row lock 内分配。
4. Segment metadata/outbox 与 Task message boundary 在同一事务提交。
5. 从 canonical `task_messages` 派生出的 DAG trajectory副本，在成为pipeline可读payload、发送给Atomizer/Embedding/Reranker/Dive/Consolidator或进入Search/training前必须完成deterministic redaction；本规则不改变业务Task原本的provider执行和消息持久化contract。
6. Atom scope 只能继承 Segment；Atomizer 无权 promotion。
7. `channel_id != null` 的原始 Atom 永远 exact-channel；`channel_id == null && project_id != null` 才默认 project-visible。
8. Typed ref 不能改变授权；每次解析后都按 canonical current ACL 重验。
9. Staging watermark、edge cursor 和 Graph version 在一个 Explore trajectory 内固定。
10. Interaction DAG Edge 只表达 durable event relation，不由 LLM 推断。
11. Active Atom 的消费只在 winning/current Graph version 发布时生效。
12. Project promotion 必须创建派生 node，引用可验证 durable evidence，并通过 Server policy。
13. Project-visible node 不授予其私有 source 的读取权限。
14. Channel migration 不迁移旧 Project project-visible node 或其他频道内容。
15. Source retraction 优先于历史版本可重放性；被撤销正文在所有版本 fail closed。
16. `recorded_for_dag`、`eligible_for_memory`、`eligible_for_training` 与 `selected_for_training` 互不推导。
17. Judge unavailable 不等于数值 0；缺失 reward 不进入训练。
18. Memory Agent session 可持续；每个 trajectory 必须有 Server hard budget 和 terminal checkpoint/submit。

## 6. Universal Interaction DAG

### 6.1 Identity 与 schema

Interaction DAG 从 Project-root 改为 Workspace-root：

```text
Segment
- workspace_id required
- segment_id required, workspace-unique
- agent_run_id required
- generation required
- project_id_at_event nullable
- channel_id_at_event nullable
- issue_id nullable
- route_generation_at_event nullable
- memory_type_at_event required
- graph_projection_eligible_at_event required
- start_seq/end_seq reference canonical task_messages.seq
- close_action_kind: message | reaction | terminal | metadata_only
- canonical_action_id nullable; message/reaction必填，terminal/metadata_only为空
- visible_action_key required and workspace-unique for non-metadata close
- trajectory_source
- derivative
- trainable_eligible
- publish_status/content_status
- publish_seq nullable until published
- sanitizer_version/policy_version
- created_at/published_at/retracted_at

Edge
- workspace_id required
- edge_seq required, workspace-monotonic
- src_segment_id/dst_segment_id
- type: continues | responds_to | delegates_to | mentions
- direction derived at read time
- trigger_message_id nullable
- created_at
```

`project_id_at_event`、`channel_id_at_event` 与 `route_generation_at_event` 是不可变 provenance；Channel migration 只改变 current Graph projection mapping，不改历史 Segment。

Universal DAG同时保存Segment→provider-call association（`owned|shared_producer|audit`、call ordinal、run/run-agent identity）。`close_action_kind + canonical_action_id + association`足以确定性投影现有Mixed-RL `run_segment.kind/canonical_action_id`、provider-call owner/shared关系和`channel_message|reaction|session_continuation`causal edge。Projection不得从trajectory自然语言反推这些字段。

### 6.2 Canonical sequence 与 Generation 状态机

`task_messages.seq` 是唯一 Segment range sequence。Channel message、provider notification、daemon stream 或 UI event 不能直接推进 DAG cursor；它们必须先通过现有 Task message persistence seam形成 canonical `task_message`。Agent 的 visible action 只有在对应 user-visible message/comment/result与task message已经在同一业务 transaction持久化后，才能关闭 generation。

```text
no open generation
  + first canonical task_message → open generation N at start_seq

open generation
  + additional inbound/tool messages before visible output
  → append to same generation; do not close

open generation
  + persisted visible outbound/delegation/final
  → close [start_seq,end_seq] including that outbound
  → create segment metadata + outbox
  → no open generation

no open generation
  + next canonical inbound → open generation N+1

no open generation
  + canonical visible outbound/reaction without prior inbound
  → atomically open at that action's task_message seq and close the same one-message generation
  → each consecutive visible outbound gets its own generation

cancel/fail/terminal
  + non-empty open generation → close with terminal metadata
  + no canonical messages → create one metadata-only Segment with content_status=empty
```

Resident steering/batched directed messages在第一次 visible outbound前都属于同一 generation，并按task message seq保序。多个 inbound不会产生空 Segment。System/cron Task 若没有用户 inbound，以第一条canonical system/provider task message开启；完全没有message仍产生metadata-only terminal Segment，以满足“所有Task进入DAG”，但不产生Atom。

Retry child使用新的`agent_run_id`并通过`responds_to/continues`等durable linkage关联；不得复用失败parent的cursor。Duplicate boundary使用`(workspace_id, agent_run_id, generation)`与visible action idempotency返回同一Segment。

Streaming delta、typing、reaction和未单独持久化的tool event不关闭generation。Tool call/result若存在canonical task message，保留在当前generation内。

### 6.3 Edge 生成

- `continues`：同一 Task generation N → N+1。
- `responds_to`：触发消息所在 Segment → 处理该 directed input 的 Segment。
- `delegates_to`：发出 durable delegation 的 Segment → child Task 首 Segment。
- `mentions`：发出 mention 的 Segment → 被唤醒 Agent 首响应 Segment。
- `completion` 是 Segment terminal metadata，不创建悬空 Edge。

Edge 可以在目标正文 publish 前存在，但只有两端都 published、落在 trajectory edge watermark 内且当前 caller 可见时才能返回。

## 7. Durable Publish Pipeline

### 7.1 状态机

```text
pending → processing → published
                    ↘ redaction_failed
                    ↘ rejected_scope
                    ↘ dead_letter
published → retracted
```

Task transaction 只负责 generation、Segment metadata 与 outbox，不同步调用模型或文件存储。Canonical业务消息继续遵循既有Task/provider contract；下列步骤处理的是从`task_messages`派生、将进入DAG/Memory/training的副本：

1. 从 canonical `task_messages` sequence range 构造六字段 trajectory：`sequence/type/tool/content/input/output`。
2. 在任何pipeline外部模型调用或pipeline readable payload发布前，执行deterministic redaction、size cap、binary rejection、artifact externalization。
3. 按 Workspace data-processing policy 选择 Atomizer provider；不可越权切换 provider/region。
4. 生成 Atom 与 source message sequence refs。
5. 在 publish transaction 中写 payload、Atom、`publish_seq` 和 Graph projection request。
6. 只有 transaction 完成后读路径可见。

### 7.2 Failure semantics

- transient provider/storage：Shadow bootstrap在24小时内最多10次指数退避，单次lease有stale reclaim；telemetry冻结production policy前不得放宽。
- deterministic sanitizer/schema：直接 `redaction_failed`，不重复消耗。
- authorization/scope：`rejected_scope`。
- 达到 retry policy：`dead_letter`，进入 Workspace health，可幂等 replay。
- 任何失败不得阻塞业务 Task terminal，也不得回退未清洗正文。

### 7.3 Provider policy

Redaction 在外部模型调用前完成。Atomizer、Embedding、Reranker、Dive、Consolidation 都继承 Workspace provider/region policy；cache 不跨 Workspace 复用原文或 embedding。Provider 不可用按降级层级处理，不得偷偷换供应商。

## 8. Memory Atom 与 Graph Projection

### 8.1 Atom contract

Atom 是 Segment 内最小可独立 Search/consume 的 statement：

```text
Atom
- atom_id: segment-scoped stable hash
- workspace_id/segment_id
- body
- kind
- source_message_seqs
- source_tool/tool_trust_class
- content_hash/artifact_ref
- visibility/channel_id/project_id inherited from Segment
- publish_seq
- active/retracted state derived from ledger
```

Atomizer 输出候选 body、kind 和 source refs；Server 验证 refs 在 Segment range 内、数量/长度预算、scope 继承和 tool trust。无记忆价值内容可产生零 Atom。Extractor 失败时可发布一个显式 `fallback` Atom，但不得声称完整结构化覆盖。

### 8.2 Graph eligibility

Graph projection eligibility 在Segment事件发生时冻结，不能由后来Workspace配置切换追溯改变：

- `memory_type_at_event!=graph`：不创建Graph projection；后来切到Graph也不自动回放。
- `memory_type_at_event=graph` + no Project/Channel：不创建Graph projection。
- `memory_type_at_event=graph` + Channel：写事件时canonical route指定Graph，Atom `visibility=channel`。
- `memory_type_at_event=graph` + Project-only：写Project Graph，Atom `visibility=project`。
- Managed Memory Agent derivative Segment：进入DAG，默认不生成Atom。

Legacy→Graph、Graph→Legacy或route变更只影响切换transaction之后的新Segment。任何旧数据导入必须走显式、owner授权、限速且标记`legacy_backfill`的job；默认空图/不自动回填语义保持有效。Backfill按执行时重新做scope、sanitization和eligibility校验，不能简单翻转历史row字段。

## 9. Retrieval 与 Explore v2

### 9.1 Default Search corpus

```text
current/pinned Graph version 中可见且 current 的 Graph nodes
+
watermark 内可见、未消费、未撤销的 active Atoms
```

Superseded、historical、invalidated node 不作为默认 seed；可由 history mode 或关系遍历访问。Consumed Atom 不参加默认 Search，但 source 保留供 evidence/DAG。

Graph node 与 Atom 分路 BM25/vector 召回，先 scope/status filter，再用 deterministic RRF/class/freshness fusion，可选 bounded reranker。Embedding/reranker failure 分别降级为 BM25/deterministic fusion。Active Atom 使用 Workspace-configurable freshness half-life；Graph node 不按统一年龄衰减，而依赖 temporal/epistemic status。

### 9.2 Typed references

v2使用结构化`MemoryRef`，不使用分隔符字符串，因此现有包含冒号的Segment/Node ID不需要转义或猜测：

```text
MemoryRef
- kind: graph_node | atom | segment
- id: opaque original ID
- graph_identity: {workspace_id, kind, owner_id} only for graph_node
- segment_id: required for atom
```

Wire JSON示例：`{"kind":"atom","id":"<opaque>","segment_id":"multica:<task>:<generation>"}`。Server按`kind`选择resolver，其他字段组合不合法即fail closed；caller不得通过ref内graph identity取得授权。

v2 response始终返回`source_class`、canonical structured ref、scope-safe metadata和score components。v1继续只接受/返回node ID，不隐式暴露DAG能力。

### 9.3 Path 与 capability matrix

- Agent mode五个native tools继续使用`start/explore/redirect/submit/checkpoint`语义；只有daemon/runtime声明`memory_explore_v2`且Server返回同capability时，payload切到structured refs、多Graph plan、Atom/DAG traversal和watermark。Server与daemon/tool prompt必须同一release slice升级。
- 未协商v2 capability的Agent mode继续node-only v1，不能看到Atom或Interaction DAG；不得在同一run混用v1/v2。
- Inject recall可复用v2内部Search engine获取Graph+active Atom，但对daemon保持现有bounded injection response；它不暴露interactive DAG expand。
- 外部/管理client通过独立versioned v2 HTTP contract访问typed Search/Evidence/History；v1 endpoint含义不变。
- 08-24“工具协议不变”只约束其P0/Continuation范围；本规格以显式capability negotiation替代该限制，不做静默wire变更。

### 9.4 Explore plan 与 watermark

Trajectory start 由 Server 返回：

```text
trajectory_id
canonical graph identities + versions
segment_publish_seq_max
interaction_edge_seq_max
query
seed typed refs
hard budgets
```

Agent 不提供 owner/version/watermark。Checkpoint 后新 trajectory 获取最新 plan，继承 bounded prior，但所有 ref 重新 canonical resolve 与 ACL 校验。

### 9.5 Traversal

- Graph ref：遍历 hierarchy、typed relation、entity 与允许的 embedding neighbors。
- Atom ref：可查看 sibling Atom、所属 Segment、canonical Graph replacements，并以 Segment 为 anchor 遍历 Interaction DAG。
- Segment ref：返回 scope-safe summary、consumed/retracted 状态、Interaction DAG neighbors 和 canonical Graph replacements。
- Interaction neighbor 返回 `edge_source=interaction_dag`、`edge_type`、`direction`。
- Consumed Segment 可以通过 DAG/evidence 访问，但不重新成为默认 seed。
- 不可见 endpoint 按不存在处理，不暴露 edge 或计数。

### 9.6 Evidence

`/evidence` 只允许从 caller 已授权的 Graph/Atom/Segment ref 下钻真实 provenance。先返回 staging/Atom summary；需要完整证据时按 bounded chunk 读取 sanitized trajectory。Archive restore 需要显式 reason、短期授权、当前 ACL 和 audit，不重新进入默认 Search。

### 9.7 Runtime budget

Memory Agent resident session可长期运行；单trajectory必须受rounds、fanout、distinct views、tool calls、token、wall-clock和Workspace quota硬限制。Shadow bootstrap hard caps为：`max_rounds=6`、`max_neighbors_per_expand=8`、`max_distinct_segments_viewed=32`、`max_atoms_per_segment_response=8`、`max_tool_calls=32`、`max_trajectory_tokens=32000`、`max_wall_clock_seconds=600`；同时应用现有Workspace token/hour、node/minute和continuous-turn ceiling中更严格者。所有计数由Server ledger计算，Agent自报不生效。达到边界自动checkpoint；频道仍活跃则开新trajectory。

这些是shadow安全上限，不是最终product default。Production冻结必须同时满足：跨scope/deletion/sanitizer canary为0失败；P95 trajectory未达到hard cap的比例有稳定余量；found/groundedness不低于旧路径；Workspace token与latency预算达标。任何放宽都需要versioned profile与新的canary evidence。

## 10. Consolidation 与 Atom Consumption

Consolidation 只读取固定 `input_atom_publish_seq_max` 内的 active Atom。Candidate Graph version 记录 parent version、input manifest 和 operations。

Winner 发布 transaction 同时提交：

1. Current Graph version pointer。
2. Graph node → Atom/Segment provenance。
3. 每个 Atom 的 coverage/consumed ledger。
4. Active index generation。
5. Consolidation outcome、backtest stats 和 reward input manifest。

Candidate failure、operation rejection、TTT loser 或 no-switch 不消费 Atom。一个 Graph node 只覆盖显式 `atom_refs`；引用 Segment 不代表覆盖全部 Atom。

Trigger 使用新增 active Atom 数、query 数、最大等待时间和 operation/token budget，不再反复把全部历史 staging 放入 prompt。

## 11. Project Promotion 与纠错

Consolidation LLM 可以提出 `visibility=project` 的新派生 node，但必须引用 Atom/evidence。Server 验证：

- 所有来源属于同 Workspace 和目标 Project lineage。
- Source 未撤销且 caller/consolidation run 有权读取。
- 无 secret、credential、高敏 PII 或禁止外扩分类。
- Durable evidence 类型有效：human confirmation、formal decision、allowlisted read-only observation、successful non-rolled-back outcome、multi-source observation。
- Human confirmation 引用真实 message，作者在事件时有 Project/Channel 权限；低置信度保持 channel scope。
- 输出是派生摘要，不复制私有 raw body。

错误 Project node：owner/admin 可立即 retract；普通成员和 Agent 可提交 correction candidate；新 durable evidence 可 supersede。所有操作保留 policy/version/reason/provenance audit。

跨频道 citation 只显示 Project node、公开 evidence 类型与来源数量。只有仍拥有 source Channel 当前权限的人可查看具体 source。

## 12. Channel Migration

Channel 换绑 Project 或 standalone Channel 首次绑定 Project 时：

- 只迁移 `channel_id=<current channel>` 的 channel-owned active Atom、consumed source、channel-visible Graph node、channel daily node与两端都属于该频道的Graph edge。
- 不迁移旧Project project-visible node、project daily node、其他频道node、仅因被本频道引用过的共享知识，或跨向旧Project node的semantic edge；后者只保留受限historical provenance，不复制target。
- Source blob字节不重复复制；新projection增加blob ref。Graph directory内的source/staging/atom索引重建为新owner identity。
- Graph-local query log、backtest window、candidate version、consolidation trace和reward trace保持事件时旧Graph audit，不复制为新Project训练数据。
- Citation snapshot不改写；旧ref通过migration ledger重定向。Memory Agent checkpoint/prior中的ref在下次trajectory rollover时canonical resolve。
- 新副本继续`visibility=channel`，不自动promotion。
- 旧ref变为不可搜索migration tombstone/redirect；历史manifest不改写。
- Binding transaction切换单一新写主并记录migration generation/source watermark。
- Worker复制watermark前内容；迁移期间安全双读并按canonical ID去重；完成后关闭旧read projection。
- Interaction DAG Segment/Edge、training manifest与reward ledger不复制、不修改，只更新current Graph projection mapping。
- Retraction/deletion同时解析old/new canonical refs。

“Never delete daily node”只约束知识历史不能无审计消失；channel daily node迁移为新canonical copy并在旧Graph留下tombstone/redirect满足该要求，不能继续作为旧Project可搜索副本。

## 13. Retention、Deletion 与 Encryption

Workspace可在平台上限内配置trajectory、trace和archive retention。Shadow bootstrap contract为：sanitized trajectory热存90天、加密归档1年、full query/Explore trace加密热存30天；Workspace可缩短，不能在无额外审批时超过平台上限。Shadow后可依据成本和恢复证据版本化调整新数据policy，但不能无迁移说明改变既有retention承诺。

Graph/source body 使用独立可撤销 encrypted blob；manifest 保存 ref/hash。PostgreSQL `retraction_registry` 是所有DB、file、blob、archive、citation、Search、Explore和training reader共享的最高优先级fence；任何content resolve先查fence，再读projection。Source deletion：

1. 业务删除transaction必须同步写retraction fence，并利用预维护的reverse provenance index把所有直接/传递依赖Graph node写入`quarantined_pending_recompute`；两者与消息tombstone同事务或同一数据库原子procedure完成。若无法建立fence/隔离闭包，删除请求失败，不向用户报告成功。Outbox只负责后续blob erase/recompute，不能延迟read fence。
2. Trajectory chunk、Atom退出Search/Explore/training selection。
3. Citation标记`source_retracted`。
4. 所有引用被删source的Graph node先整体进入`quarantined_pending_recompute`并从current Search/Explore隐藏；不能在recompute完成前继续返回可能包含被删信息的旧body。
5. 仅由该来源支持的Graph node tombstone；多来源node在新Graph version重算并移除被删provenance后才重新发布。
6. 释放blob refs，无其他合法引用后cryptographic erase。
7. 旧Graph version、frozen export与archive restore都必须查同一fence，只返回`content_retracted`，不得读取旧正文。
8. 未消费training export立即撤销；已消费记录进入tenant rebuild或pooled deletion/unlearning ledger。
9. Audit只保留不可逆hash、时间、actor和reason。

## 14. Training 与 Reward

### 14.1 Eligibility 与 selection

所有安全发布的非derivative trajectory自动`training_eligible`，但不自动被训练消费。状态分离：

```text
recorded → sanitized → eligible → selected → exported → consumed
                                            ↘ retracted
```

Tenant-local与pooled training独立配置。新建Workspace可以按产品默认创建`tenant_training_enabled=true`，但既有Workspace migration必须写`tenant_training_status=pending_owner_ack`并保持selection关闭，直到owner/admin通过CAS确认grant；不得由数据库default静默激活历史Workspace。无论新旧Workspace，在本规格rollout的reward shadow/calibration global kill switch通过前都不能实际selection/export。Pooled始终必须owner/admin明确opt-in。Legacy与Graph Workspace遵循同一training治理，但Graph projection eligibility不由training设置改变。

每次selection manifest必须固化`training_grant_id`、grant policy version、actor、granted_at、tenant/pooled purpose及当时Workspace配置；grant撤销后尚未消费manifest失效，已消费样本进入第13/14节删除处理。Manifest同时固定Segment、hash、sanitizer/policy和scope。

Selection 排除 retracted/redaction_failed、低质量、异常高频、near-duplicate、untrusted tool、Memory Agent derivative 和 reward unavailable。单 Agent/Channel/Workspace 有采样上限。

Tenant adapter 删除时销毁或从 clean checkpoint 重建。Pooled 已消费样本进入 deletion/unlearning ledger并从后续训练排除；产品不宣称即时精确遗忘。

### 14.2 Explore reward

独立 ephemeral Dive Agent 使用 pinned evidence 与白名单 trajectory 评分：

```text
overall = min(relevance, groundedness, completeness)
reward = overall - w_round * server_counted_rounds
```

Judge infrastructure failure → `reward_status=unavailable`，不训练；scope/invalid input → rejected；Agent 自身预算违规可给 deterministic negative reward。Dive model、prompt、policy、input manifest 与 raw dimensions写 reward ledger。

### 14.3 Consolidation reward

Consolidation candidate 先过 Graph invariant、scope、coverage、recall 和 safety hard gate。Reward 使用相对 parent/baseline 的可比较增益：

```text
quality_delta = recall_delta + coverage_delta - regression_penalty
efficiency_delta = baseline_rounds - candidate_rounds
                   - absolute_embedding_cost
                   - absolute_node_churn
                   - absolute_edge_churn
reward = w_quality * quality_delta + w_efficiency * efficiency_delta
```

Winner 身份不自动等于高 reward；hard gate failure 是 deterministic negative。Reward 归 candidate consolidation trajectory，不复制给 source Atom。

### 14.4 Evaluation 与交付

Evaluation window、hidden holdout 和 safety canary 使用稳定、可审计划分。Shadow 阶段只记录 raw reward components，不更新模型；权重经 offline replay、人类抽检和 holdout 相关性校准后，以 `reward_policy_version` 启用。

Delayed reward 通过 immutable ledger/outbox exactly-once-effect 交付。相同 identity 重放值必须一致；重评创建 revision，不覆盖已消费记录。

## 15. API、UI 与可观测性

- v1 保留 node-only contract 与兼容期。
- v2 提供 typed Search/Explore/Checkpoint/Evidence/History contract。
- UI citation 标识 `Consolidated memory`、`Recent unreviewed observation`、`Historical/superseded evidence`、`Restricted source`、`Retracted source`。
- Workspace health 展示 segment publish backlog、redaction failure、DLQ、atomization lag、migration、consolidation、reward unavailable 和 deletion backlog。
- Query/trace 长期指标只保留 token、latency、result class、匿名计数与 hash；不默认具有 training eligibility。
- 所有 provider、archive restore、evidence access、promotion、retraction、migration 和 reward revision 有审计记录。

## 16. Error Handling 与降级顺序

资源不足或 provider outage 时：

1. Durable Segment metadata/outbox 不丢。
2. Deterministic security redaction 不跳过。
3. Sanitized trajectory publish 必须最终完成或进入可见 health terminal state。
4. Atomization 可延迟；失败使用显式 fallback Atom。
5. Embedding 可延迟；Search 降级 BM25。
6. Reranker 最先关闭；使用 deterministic fusion。
7. Consolidation 可延迟；active Atom 继续受 scope 可搜并产生 health warning。
8. Training selection/export 可完全关闭，不影响 DAG、Memory 或 Task。

任何 ACL、sanitizer、deletion canary failure 自动关闭受影响读路径，但保持 durable DAG write，修复后 replay。

## 17. Acceptance Criteria

### Universal DAG

1. 所有新 Task 不论 Workspace memory type、Project、Channel、AReaL 或 env-dispatch 都创建 Workspace-scoped DAG lifecycle。
2. Project/Channel 可空，Workspace 不可空；跨 Workspace Segment/Edge 写入由 DB 与 module 双重拒绝。
3. Durable inbound/outbound 状态机生成连续 generation；stream/tool chunk 不额外关闭。
4. Concurrent/replayed boundary 不产生重复 generation；range 无重叠、无缺口。
5. Segment close metadata 与 outbox 同事务；业务 Task terminal 不等待模型 pipeline。
6. `continues/responds_to/delegates_to/mentions` 只由 durable event linkage创建并幂等。
7. Memory Agent derivative Task 进入 DAG 且标记 derivative。

### Publish 与 Atom

8. 未完成 redaction 的正文不进入可读 store、外部 model、Search、Graph 或 training。
9. Pipeline 只有完整 publish transaction 后分配 `publish_seq`。
10. Transient、deterministic、scope 与 DLQ 状态可区分、告警、重放。
11. Atom source sequence 全部位于 Segment range，scope 与 Segment完全一致。
12. Segment 可产生零个、一个或多个 Atom；Atom identity 重试稳定。
13. Graph staging 只处理 Graph Workspace 且有 Project/Channel scope 的 Segment。
14. Legacy、无 scope、derivative Segment 不产生默认 Graph Atom。

### Search 与 Explore

15. 默认 Search 仅含 pinned/current有效 Graph node与watermark内 active Atom。
16. Consumed Atom、retracted content、superseded/invalidated node不成为默认 seed。
17. Graph 与 Atom 分路召回；embedding/reranker失败分别降级，结果仍受 scope filter。
18. Project Graph 中频道 A 的 Atom不能被频道 B Search、view、expand、evidence、submit或citation发现。
19. v2 所有 ref typed；unknown/forged type fail closed。
20. Trajectory 固定 Graph versions、publish watermark、edge cursor与硬预算。
21. Atom 可沿所属 Segment 的 Interaction DAG expand；Graph node可沿Memory Graph边expand。
22. Interaction edge返回source/type/direction；不可见endpoint不泄漏存在性。
23. Consumed Segment可由授权DAG/evidence读取，但不进入普通Search。
24. Checkpoint rollover获取最新plan并重新校验prior ref。
25. 单trajectory预算耗尽不会终止resident session；自动checkpoint并可继续。

### Consolidation 与 Promotion

26. Consolidation只读取固定watermark内 active Atom。
27. Winner Graph version、provenance、atom consumption与active index原子提交。
28. Loser、失败、no-switch、rejected op不消费 Atom。
29. Node只消费显式 atom refs，不因 segment ref吞掉 sibling事实。
30. Project promotion创建新派生node，不修改原channel Atom/node。
31. Promotion缺失durable evidence、scope、provenance或security gate时拒绝并审计。
32. 跨频道citation不暴露私有source identity/body；原频道授权者可受控下钻。
33. Owner/admin retract立即从current view隐藏；correction/supersede保留历史关系。

### Migration 与 Deletion

34. Channel migration只复制本频道channel-visible内容，禁止复制旧Project共享或其他频道内容。
35. 迁移后scope仍为channel；promotion独立执行。
36. Migration使用单写主、generation、水位线、安全双读和canonical去重。
37. 旧ref通过授权redirect解析；Interaction DAG历史provenance不改写。
38. Source deletion立即使trajectory、Atom、citation和受影响历史Graph body不可读。
39. 单来源node撤销；多来源node重算并移除被删provenance。
40. Blob无合法引用后可cryptographic erase；audit不保留正文。
41. Archive restore重验当前ACL并记录reason、actor、TTL和访问对象。

### Training 与 Reward

42. `eligible`不自动变成`selected/exported/consumed`。
43. Tenant与pooled training独立配置；pooled默认非隐式授权。
44. Selection manifest可重放并排除retracted、derivative、unavailable和poisoned样本。
45. Dive独立评分三维，overall取最低维并减Server轮次成本。
46. Judge失败记录unavailable且不交付数值0 reward。
47. Consolidation reward使用baseline delta与绝对成本，winner身份不直接奖励。
48. Reward只归实际trajectory；revision不覆盖已消费记录。
49. Holdout与safety canary不被平均分抵消。
50. Delayed reward outbox幂等交付并可恢复。

### Rollout 与兼容

51. Shadow阶段验证sequence、outbox丢失率、跨频道canary、sanitizer fail-open、deletion和成本。
52. 任一安全canary失败关闭相关读路径而非DAG写入。
53. v1 contract在兼容期内保持node-only语义；v2独立发布。
54. Historical backfill在实时shadow稳定后限速执行，标记approximate且默认不进入training selection。
55. Production quota、freshness、retention和reward权重只有在shadow evidence后版本化冻结。
56. Universal DAG是唯一live writer事实源；Mixed-RL run segment/causal edge只由canonical mapping投影，projection mismatch使freeze fail closed。
57. 多个inbound、resident steering或batch在首个visible outbound前属于同一generation；cancel/fail/empty system task的Segment语义按状态机可重放。
58. Legacy时期Segment不会因Workspace后来切到Graph自动进入Graph；只有显式owner授权backfill可投影。
59. Agent native tools只有在`memory_explore_v2`双向capability协商后使用structured refs；v1 run不可中途切换。
60. `MemoryRef`是结构化对象；opaque ID中任意冒号或分隔字符不影响解析。
61. Channel migration对source、Atom、daily node、Graph node/edge、citation、query/backtest、checkpoint和training/reward工件逐类遵循第12节语义。
62. Retraction fence生效后，所有受影响派生node在recompute前隔离；旧version、archive和frozen export不能读取正文。
63. Shadow阶段执行明确hard caps、retry与retention bootstrap；放宽前必须满足第9.7节安全与质量冻结门槛。
64. Universal Segment保存close action identity与provider-call association，可确定性重建Mixed-RL frozen run projection。
65. 无open generation时visible outbound原子open-and-close；连续outbound不会悬空或错误合并。
66. 删除成功返回前，retraction fence与全部已知依赖node quarantine已原子生效。
67. Shadow对每trajectory强制32 tool calls、32000 tokens与600秒wall-clock上限，并取Workspace ceiling更严格值。
68. 既有Workspace在owner/admin确认tenant training grant前不进入selection；manifest固化grant identity/version/actor。

## 18. Testing Decisions

- 以 `UniversalInteractionDAG`、Graph projection、typed Explore v2、Consolidation publish 与 Reward ledger 的公开 interface为主要测试 seam；不让 handler/daemon 各自复制状态机测试。
- DB integration tests验证 row lock、generation/range、workspace identity、outbox原子性、publish watermark、edge gating和migration fencing。
- Property tests覆盖任意并发/replay下range不重叠、typed ref不可越权、retraction后所有读取fail closed。
- Scope matrix至少覆盖：same channel、same project different channel、different project、standalone、rebind history、DM、project-only、unscoped、cross-workspace forged ID。
- Retrieval tests分别验证Graph/Atom候选、class fusion、freshness、status、fallback和consumed exclusion。
- Explore contract tests验证typed refs、DAG双向边、canonical replacement、budget/checkpoint、watermark和unavailable endpoint。
- Consolidation tests用多Atom Segment证明partial consumption；用loser/no-switch/failure证明不消费。
- Promotion tests使用prompt injection、secret、无效evidence、删除source、低权限author和合法durable decision。
- Migration tests在并发新写入、失败重试、重复worker、删除和旧citation解析下验证无split-brain。
- Retraction tests从source message贯穿trajectory、Atom、Search、Graph current/history、citation、archive和training manifest。
- Reward tests验证原始维度、unavailable、negative budget、baseline delta、holdout、revision conflict与exactly-once effect。
- Rollout canary使用真实scope隔离与删除路径；“无crash”不构成上线证据。

## 19. Rollout

1. Schema与Universal DAG shadow write；所有读路径保持旧行为。
2. 验证generation、sequence、redaction、publish、DLQ与provider policy。
3. Graph Workspace启用active Atom shadow index，对比旧Search但不影响结果。
4. 启用v2 Atom Search与citation标识。
5. 启用Interaction DAG traversal与checkpoint rollover。
6. 启用Atom-based Consolidation consumption与Project promotion policy。
7. 启用Channel migration与canonical redirect。
8. 启用deletion/retraction、archive restore和历史blob erase gate。
9. Reward shadow只记录components；校准后启用tenant training selection。
10. Pooled training保持显式opt-in并独立gate。
11. 实时路径稳定后，限速回填历史Task为一个`legacy_backfill` Segment，boundary标记approximate。

## 20. Out of Scope

- 把 Interaction DAG 本身转换成 LLM 推断的 semantic knowledge graph。
- 自动迁移旧 Project 的 project-visible知识到新 Project。
- 对 deleted pooled sample 承诺即时精确weight unlearning。
- 在 v1 contract 中混入 typed refs。
- 在 shadow 前固定不可调整的生产数值。
- 用 Memory Agent自己的公开复述循环生成新的Graph evidence。
- 用 source Segment 的 reward 代替实际 policy trajectory reward。

## 21. 对既有规格的替代关系

本规格保留既有 Graph scope 的 Workspace/Project隔离、exact-channel visibility、server-authoritative route与每步filter，但替代以下旧假设：

- Interaction DAG不再只属于env-dispatch/training Project，而是所有Workspace Task的事实层。
- “一Task一Segment”替换为“一Task多generation、每次durable可见行动关闭”。
- Graph staging不再best-effort file write，而由durable publish pipeline驱动。
- 默认Search不再索引全部staging，而索引active Atom。
- Staging不再是不可expand叶子；Atom通过所属Segment联邦遍历Interaction DAG。
- Memory Agent“不设固定轮数”改为“resident session持续、单trajectory有硬预算并checkpoint rollover”。
- Standalone Channel绑定Project后不再永久留在原Graph；channel-owned内容安全迁移。
- Judge failure不再写数值0 reward，而是unavailable。

其他未冲突的inject/agent互斥、resident steering、Graph version pinning、Dive三维评分、consolidation TTT与citation snapshot语义继续有效。

## 22. Further Notes

- 本规格是跨多个phase的umbrella contract；每个phase必须产生可独立验收的软件，不能以“后续phase会补安全”为由提前开放读路径。
- 数值bootstrap必须有安全硬上限，但production默认值通过shadow telemetry和reward calibration确定。
- `docs/engineering-principles.md` 在当前checkout不存在；实施前应由仓库维护者恢复或确认其替代文档，避免新全局contract缺少规则索引。
- 本规格与配套计划不授权commit、部署、production migration或Computer/Agent restart。
