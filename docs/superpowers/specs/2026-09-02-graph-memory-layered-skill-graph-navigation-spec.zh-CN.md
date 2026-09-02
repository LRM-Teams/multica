# Graph Memory 分层导航与 Skill Graph 规格

- 日期：2026-09-02
- 状态：设计已确认；实施待排期
- 范围：在 Universal Interaction DAG → Atom → Graph pipeline 之上，定义分层 Topic/Profile 投影、七类 AtomKind、Skill Graph、typed `MemoryRef` 以及按 Agent / AtomKind / Skill 的安全检索过滤。
- 上位规格：`2026-08-27-universal-interaction-dag-graph-memory-pipeline-spec.zh-CN.md`（下称 *universal spec*）
- 关联规格：`2026-08-25-graph-memory-agent-mode-spec.zh-CN.md`、`2026-08-31-research-graph-memory-unification-spec.zh-CN.md`
- 替代关系：本规格替代 `2026-09-01-graph-memory-past-bench-recovery-spec.zh-CN.md` §3 B6 中的 AtomKind 枚举及其 `procedure`→skill-proposal 路由前提；PAST-Bench 的其余恢复项不受影响。

## 1. 问题与决策

现有 universal spec 已把 Segment、Atom、版本化 Graph node、typed ref、Interaction DAG traversal、scope/retraction 设计为统一管线，但仍缺少四项能力：

1. Agent 只能在 Graph node / Atom / Segment 间按拓扑探索，缺少 Topic/Profile 的高层摘要入口，无法在“全局理解”和“原始证据核验”之间显式导航。
2. `Atom.kind` 有字段但没有稳定语义分类；PAST-Bench 临时提出的四类枚举不足以表达事件、决策和约束，且把 procedure 混入事实层会混淆可撤回知识与可执行能力。
3. Skill 已是 `skill`、`skill_file`、`agent_skill`、grant/promotion 审计组成的一等业务实体，却不在 Memory Graph 内；系统无法回答 Skill 从何而来、适用于谁、能否组合、是否被替代或有哪些证据支持。
4. 当前混合检索以 scope 为主要过滤维度，尚未将来源 Agent、AtomKind、Skill/Skill 关系、子图及层级作为 first-class、server-authoritative filter。

本规格确认以下设计：

- Memory Pyramid 的查询语义以 **分层视图** 实现：Topic/Profile 是基于下层受控投影的可重建超节点（hypernode），不是新的事实源。
- Atom 是最小独立事实单元，采用固定七类 `AtomKind`。
- Skill 是 Memory Graph 的一等 `NodeRole=skill` 节点，也是由关系型 Skill Catalog 管理的可执行 artifact；二者通过稳定 ID 和版本/hash 双向关联。
- `MemoryRef.kind` 说明受控解析与允许操作，不说明对象是否属于 Graph。`kind=skill` 的对象仍在图拓扑中，但必须使用 Skill resolver 和 grant/binding 规则。
- 每次 Search、Explore、Evidence、Citation、Skill view/attach 都由服务端按当前 ACL、scope、retraction 和版本重新授权；模型或客户端提供的 filter 不可扩大数据可见性或能力权限。

## 2. 目标与非目标

### 2.1 目标

- 支持从 Profile/Topic 摘要下钻至 Graph node、Atom、Segment/Evidence，并可从低层证据回到高层上下文。
- 固化 `fact | event | instruction | preference | decision | constraint | fallback` 的 AtomKind 契约。
- 将 Skill 的来源、版本、适用性、评估、组合和替代关系纳入 Graph，同时保留 Skill Catalog 作为执行 artifact、授权和绑定的权威。
- 让检索可按来源 Agent、AtomKind、Skill、Skill 适用/绑定 Agent、子图、层级及关系过滤；所有过滤先缩小可见语料，再进行排序。
- 保持 universal spec 的 Workspace/Project/Channel 隔离、version pin、watermark、retraction fence、Atom consumption 和 v1/v2 兼容契约。
- 为后续学习型导航策略提供可审计的层级、filter、view、evidence、stop-reason 和成本信号；本规格不要求立即采用 RL。

### 2.2 非目标

- 不用 Topic/Profile/Skill View 覆盖或替代 Segment、Atom、Graph version、Skill Catalog 的事实/执行权威。
- 不把任意历史消息、任意 Atom 或 Agent 自身复述自动升级为 Skill。
- 不因 Search 命中 Skill 就赋予读取完整 `SKILL.md`、执行、安装、绑定或 promotion 权限。
- 不把 `AtomKind` 当成 Graph Node 的唯一类型；一个 Graph node 可由多类 Atom 支撑。
- 不引入原生 Hypergraph 数据库。第一版以普通 Graph 中的 view/supernode + membership edge 编码超边。
- 不允许运行时临时拼接多个 Skill artifact 后作为未验证的 Composite Skill 执行。

## 3. 术语、权威与层次模型

```text
Universal Interaction DAG / sanitized Segment        不可变事件事实与证据
  ↓ atomization
Atom                                                   最小可检索、可消费陈述
  ↓ consolidation
Graph Node                                             版本化语义知识与关系
  ↓ materialization
Topic/Profile View (hypernode)                         可重建的高层导航投影

Segment / Atom / Graph Node / Evaluation
  ↓ curation + human/policy gate
Skill artifact (skill + skill_file)                    可执行能力的权威实体
  ↕ stable ID + version/hash projection
Skill Node (NodeRole=skill)                            可探索的能力知识节点
```

### 3.1 权威边界

| 对象 | 权威来源 | 可否直接执行/改变授权 | 是否可由 Graph 重建 |
|---|---|---:|---:|
| Segment / Interaction Edge | Universal Interaction DAG | 否 | 否 |
| Atom | Atom ledger + Segment provenance | 否 | 是 |
| Graph Node | 已发布 Graph generation | 否 | 是 |
| Topic/Profile View | 已发布 Graph/Atom/Segment 的受控投影 | 否 | 是 |
| Skill artifact | `skill` / `skill_file` / grant / binding 表 | 是，仍受显式授权 | 否 |
| Skill Node | Skill artifact + evidence/evaluation 的图投影 | 否 | 是 |

Graph failure 不得阻塞 DAG 或 Skill artifact 的既有 CRUD；Skill Catalog/绑定 failure 不得绕过 Graph ACL 或让候选 Skill 自动可执行。

### 3.2 NodeRole

Graph Node 增加结构角色（role），与 `AtomKind` 正交：

```text
memory   由 Atom consolidation 的普通知识节点
entity   人、组件、项目、工具等实体锚点
daily    时间窗口摘要
topic    主题聚合超节点
profile  user / agent / channel / project 的高层投影
skill    一个 Skill artifact 的能力知识投影
```

既有 Node 无 role 时按 `memory` 解释。`role` 不授予可见性或操作能力；Node 的 `visibility`、来源 closure 与 retraction fence 始终优先。

### 3.3 Topic/Profile View（超节点）

`topic` 与 `profile` 节点代表一组成员共同形成的语义单元，允许成员重叠，因此在语义上是超边/超节点；物理实现使用普通 node 加显式 membership edge。

每个 View 至少记录：

```text
view definition / generator policy version
member typed refs（Atom、Graph node、Segment 或下层 View）
member/source watermark 与 Graph version
scope、visibility、temporal/epistemic status
summary、created/updated time、provenance closure
```

不变量：

1. View 是 materialized projection；不得成为无来源的独立事实源。
2. 任何成员 retraction、scope 缩窄、版本失效或 provenance 缺失，都使受影响 View 从当前 Search 隐藏、标记 stale/tentative 或重算；不得继续以旧摘要泄露正文。
3. View 的可见性不得宽于任何会影响其对外摘要的成员 source。不可见成员要么被调用者视图排除后重新 materialize，要么不返回该 View。
4. `profile` 必须有明确 owner kind/id（`user | agent | channel | project`）；禁止把多个协作者混成一个未标识的“workspace 用户画像”。

## 4. AtomKind 契约

```go
type AtomKind string

const (
    AtomFact        AtomKind = "fact"
    AtomEvent       AtomKind = "event"
    AtomInstruction AtomKind = "instruction"
    AtomPreference  AtomKind = "preference"
    AtomDecision    AtomKind = "decision"
    AtomConstraint  AtomKind = "constraint"
    AtomFallback    AtomKind = "fallback"
)
```

| Kind | 定义 | 最低要求 / 生命周期 |
|---|---|---|
| `fact` | 可由来源支持的状态或陈述 | 可 supersede、invalidate 或 retract |
| `event` | 在时间、参与者或状态变化上有边界的发生事项 | 必须保留/引用时间与来源，不得无证据改写成稳定偏好 |
| `instruction` | 有权主体明确要求 Agent 如何行动 | 记录 authority、scope、priority、有效期与冲突关系 |
| `preference` | 主体的偏好、习惯或工作方式 | 保留主体、置信度和时间；不能由单次 event 无依据泛化 |
| `decision` | 已确认的项目/团队/主体决策 | 必须有可验证 durable evidence；无确认只能为 fact/event/proposal，不得 promotion |
| `constraint` | 技术、合规、预算或流程上的限制 | 必须可显式失效、替代或限定 scope，避免过期硬约束 |
| `fallback` | 无法安全归类但仍可发布的保守 Atom | 不得充当高置信 decision/instruction/constraint 的唯一依据 |

Atomizer 只能提出 `body`、`kind`、source refs 和辅助 metadata；Server 验证 source refs 位于 Segment range、kind 合法、scope 继承、tool trust、长度/数量和敏感信息策略。

`procedure` 不再是 AtomKind。多步骤轨迹可以产出多个 `event`、`instruction`、`decision`、`constraint` 等 Atom；是否形成 Skill candidate 由独立 curation/proposal 流程根据一组 provenance refs、可验证 outcome 和 policy 判断，不能从单个 `procedure` 标签直接自动建 Skill。

Graph node 可保存 `derived_atom_kinds[]`，它是 atom provenance 的派生集合而非单一分类；例如一个架构节点可同时包含 `decision`、`constraint` 与 `fact`。

## 5. Skill Graph

### 5.1 双重表示

每个可执行 Skill 保留现有关系型实体：

```text
skill / skill_file / agent_skill / grant_level / channel_id / skill_promotion
```

其 `skill_id`、artifact version/content hash、grant/binding 是读取全文、安装、绑定、promotion、撤销的权威。对于可进入 Graph 的 Skill，系统建立 `NodeRole=skill` 的 Skill Node：

```text
node_id, skill_id, artifact_version/content_hash
capability summary、输入/输出契约、已审核 capability metadata
applicability/evaluation summary、scope-safe provenance refs
visibility、temporal/epistemic status、Graph version
```

Skill Node 的 searchable body 默认仅含安全的名称、描述、frontmatter 和经审核的 capability summary；不得默认 embedding 完整 `SKILL.md`、支持脚本、路径、密钥或未审查 prompt。完整 artifact 读取走单独 Skill resolver，并再次验证 grant level、channel scope、当前 Agent binding 和请求者权限。

### 5.2 关系与组合

Skill Graph 至少支持下列显式关系；关系必须有来源、actor/policy、版本和 scope-safe provenance：

```text
Skill --derived_from--> Segment | Atom | Evidence
Skill --validated_by--> Evaluation | Outcome | Benchmark
Skill --applicable_to--> Agent | TaskType | Project | Channel
Skill --requires--> Tool | Capability
Skill --uses--> Tool
Skill --composes_with--> Skill
CompositeSkill --composed_of--> Skill
Skill --refines|supersedes|conflicts_with--> Skill
Skill --recommended_for--> Topic | Profile | Task
```

`composes_with` 仅表示协作/推荐关系，不产生可执行 artifact。只有经验证、显式生成和授权的 Composite Skill 才是新的 `skill` 行与 `NodeRole=skill` 节点；其 artifact 必须有自己的 `SKILL.md`、输入输出契约、组件清单、冲突/权限处理和测试/评估。Graph traversal 不得临时拼接组件 Skill 后绕过上述门槛执行。

### 5.3 生成、升级与撤回

- 从 trajectory/Atom 产生的 Skill candidate 默认 `needs_review`，不得自动写为可绑定 artifact 或提升 grant。
- candidate 的 evidence 必须引用已清洗、未撤回、调用者/curator 有权读取的 Segment/Atom/Outcome；Memory Agent derivative 复述不可单独充当 ground truth。
- Skill artifact 内容或支持文件变化必须产生新 artifact version/hash；对应 Skill Node 在新 Graph version 更新或 supersede，旧 node 保留可审计历史但不作为默认当前候选。
- Skill、source Segment、Atom 或任一支持 evidence 被撤回时，Skill Node/Evaluation/Recommendation 立即受同一 retraction fence 约束；artifact 是否删除或下架仍由 Skill Catalog 授权策略决定，但 Graph 不得继续返回失效证据摘要。

## 6. Typed MemoryRef 与操作能力

`MemoryRef` 是结构化、opaque-ID-safe 的引用；`kind` 选择 resolver 和受控操作，不授予能力：

```text
graph_node   已发布 Graph node（包括 role=memory/topic/profile/entity/daily）
staging_atom watermark 内 active、未消费的 Atom
skill        Graph 中 role=skill 的节点所投影的 Skill artifact
```

最小 wire shape：

```json
{"kind":"graph_node","id":"<opaque node id>","graph_identity":{"workspace_id":"…","kind":"project|channel|research","owner_id":"…"}}
{"kind":"staging_atom","id":"<opaque atom id>","segment_id":"<opaque segment id>"}
{"kind":"skill","id":"<skill UUID>","graph_node_id":"<opaque node id>","artifact_version":"<hash-or-version>"}
```

规则：

1. `skill` ref 的拓扑邻居由其 `graph_node_id` 解析；artifact 内容、文件、绑定、attach、grant/promotion 由 `skill_id` 的 Skill resolver 解析。
2. `skill` ref 可被 Search、Explore、Evidence/metadata view 引用；它不能让模型直接写 Skill、读取完整 artifact、调用工具或改变 `agent_skill`。
3. 任何 ref 的 caller-provided graph identity、version、scope、owner、channel、artifact hash 均为诊断字段；Server 从 canonical plan/DB 重新解析并校验。
4. v1 继续仅接受/返回 node ID。v2 才返回上述 typed ref；一个 run 不可混用 v1/v2。

## 7. Search、过滤与分层导航

### 7.1 请求语义

v2 Search/Explore plan 允许以下语义过滤；Server 可以因调用方权限或 capability 降级/拒绝，但不可按调用方字段扩大 corpus：

```text
result_kinds: graph_node | staging_atom | skill
node_roles: memory | entity | daily | topic | profile | skill
source_agent_ids: 产生 Segment/Atom/Node 证据的 Agent
atom_kinds: 七类 AtomKind
related_skill_ids: 与给定 Skill 有图关系的对象
skill_applicable_to_agent_ids: Skill 适用对象
skill_assigned_to_agent_id: 当前已通过 agent_skill 绑定的 Skill
skill_grant_levels: agent | channel | workspace
root_ref + max_traversal_depth + relation_types: 受限子图
levels / temporal / epistemic / evidence policy: 层次、当前性和证据门槛
```

`source_agent_ids`、`skill_applicable_to_agent_ids`、`skill_assigned_to_agent_id` 必须分别实现，禁止使用含糊的单一 `agent_id`：前者是证据来源，后两者分别是能力推荐和实际授权绑定。

`atom_kinds` 直接过滤 Atom；对 Graph Node 使用 `derived_atom_kinds` 做 provenance-derived match。Skill Node 的 `derived_from` evidence 可受 `atom_kinds` 过滤，但 Skill artifact 本身不伪装为 Atom。

`result_kinds=[skill]` 只返回 Skill Node/Skill ref；`related_skill_ids` 返回与目标 Skill 存在授权可见关系的 Graph Node、Atom、Topic/Profile 或其他 Skill，而不是强制返回 artifact 全文。

### 7.2 检索顺序与语料

```text
canonical caller/task/plan
  → ACL, current scope, retraction, watermark, status, filter eligibility
  → eligible Graph/Atom/Skill corpus
  → 各 corpus 内 BM25 + vector retrieval
  → deterministic class-aware fusion / optional bounded rerank
  → defense-in-depth ACL + resolver recheck
  → typed refs + scope-safe metadata
```

必须先形成 eligible corpus 再排名；不得先对全局 Top-K 排名后仅过滤，以免不可见或不符合子图条件的候选占满 K 并使可见结果不足。所有 provider 故障维持 existing fail-closed/降级规则：embedding 失败降级 BM25，reranker 失败降级 deterministic fusion，ACL/retraction/identity 失败不降级为放宽读取。

Graph/Atom/Skill 使用逻辑隔离索引或等价的严格 partition；Skill corpus 默认只索引经审核 summary。跨 corpus 融合必须返回 class、score components、filter explanation 和是否发生降级的审计元数据。

### 7.3 导航

分层导航与拓扑导航同时可用：

```text
Profile / Topic View ↔ Graph Node ↔ Atom ↔ Segment / Evidence
Graph Node / Skill Node ↔ hierarchy、semantic、Skill relation edges
Atom / Segment ↔ Interaction DAG durable edges
```

现有 `start / explore / redirect / submit / checkpoint` 生命周期保持。v2 additive 地支持：

- Search/start 返回 `abstraction_level`、`node_role`、`derived_atom_kinds`、`source_class` 和 filter-safe score components；
- Explore 可从 Topic/Profile/Skill Node 沿 membership、hierarchy、semantic、Skill relation、DAG edge 进入相邻 typed refs；
- Evidence 仅从已授权 ref 下钻清洗后的 Segment chunk/Atom provenance；
- redirect 可在同一 pinned plan 内对新的 query/filter 获取 fresh seed，但不能修改 scope/version/watermark；
- submit/checkpoint 记录 viewed refs、applied filters、停止原因、工具/令牌/延迟成本，供审计与后续导航策略训练。

## 8. 安全、不变量与兼容性

1. Skill Node、Topic/Profile View、derived AtomKind、filter metadata 全是派生信息；不能修改 source ACL、grant、binding 或 retraction 状态。
2. Channel-private Atom/Segment/Skill evidence 不得因被 Topic/Profile/Skill Node 引用而变为 Project-visible；任何 promotion 继续遵守 universal spec 的 durable evidence 与 Server policy。
3. 所有 Search、view、expand、evidence、citation、Skill detail/attach、submit 都重新校验当前 ACL、retraction、scope、Graph version、artifact version 与 watermark。
4. `agent_skill` 绑定和 `skill.grant_level` 是能力交付权威；Graph relationship `applicable_to`、`recommended_for` 仅提供建议，不产生绑定。
5. source Agent filter 只缩小已授权语料，不可用于推断不可见 Agent、频道、Skill 或统计计数。
6. Topic/Profile/Skill Node 的 citation 必须区分“摘要/推荐/能力 metadata”与“原始证据”；跨 scope 仅返回 scope-safe 描述和来源数量。
7. Source/Skill artifact 版本切换、删除或撤回应保留历史 audit hash/reason，但不允许旧正文绕过 fence 被读取。
8. 既有 `fact`、`preference`、`fallback` Atom 直接映射到新枚举；旧 `rule` 必须经 backfill policy 显式映射为 `instruction` 或 `constraint`；旧 `procedure` 不映射为 AtomKind，而进入受控 Skill candidate re-evaluation 或保守 fallback，禁止静默提升。

## 9. 验收标准

1. Atomizer 只能发布七类 AtomKind；未知 kind、越界 source ref、scope 不一致和伪造 trust metadata 被拒绝。
2. `decision` 缺少 durable evidence 不得作为 Project-visible decision 或 Skill promotion 的唯一依据；`fallback` 不得满足该门槛。
3. 同一 Graph Node 能记录多个 `derived_atom_kinds`，且过滤该集合不会将未匹配 source 伪装为支持证据。
4. Topic/Profile View 始终携带成员 typed refs、Graph/Atom watermark、generator policy 和 provenance closure；其成员变化/retraction 后不返回过期正文。
5. 同一私有 Atom 被不同 scope Topic/View 引用时，未获来源权限的 caller 不能发现该 Atom、其来源身份、成员计数或摘要内容。
6. `profile` 无明确 owner kind/id 时无法发布；不同 Agent/Channel/Profile 不被隐式合并。
7. 每个 Skill artifact 可投影为一个 version/hash 对应的 Skill Node；Skill Node 可沿支持、替代、组合、评估、适用关系导航。
8. Search 命中 Skill 不能自动读取完整 `SKILL.md`、支持文件、绑定 Agent、提升 grant 或执行 Skill；这些动作分别经 Skill resolver 与授权接口检查。
9. Composite Skill 必须有独立 artifact、组件清单、输入输出/权限契约和评估记录；仅有 `composes_with` 边的 Skill 不可作为组合 artifact 执行。
10. 被撤回的 source evidence、Skill version 或其依赖 closure 不再出现在默认 Search/Explore/Citation；所有 reader 经同一 fence 验证。
11. `source_agent_ids`、`skill_applicable_to_agent_ids`、`skill_assigned_to_agent_id` 分别返回正确集合，且不互相替代。
12. `atom_kinds` 正确过滤 active Atom；Graph Node 的结果仅在实际 provenance 含目标 kind 时匹配。
13. `result_kinds=skill` 只返回 Skill ref；`related_skill_ids` 能返回授权可见的关联 Atom/Node/View/Skill。
14. 子图/层级/关系 filter 在排序前裁剪语料；不可见或过滤掉的全局高分项目不会使可见 Top-K 被人为缩短。
15. Graph、Atom、Skill 三类语料的融合结果携带 ref kind、role/class、score components、filter/降级审计信息，且 deterministic tie-break。
16. v2 能从 Profile/Topic → Graph Node → Atom → Evidence 下钻，并从 Skill Node 遍历其关系；每一步重验 ACL/version/watermark。
17. v1 node-only endpoint 与 run 在兼容期保持语义不变；未协商 v2 的客户端不能获得 Atom、Skill、View 或 DAG capability。
18. Search/Explore trace 记录 applied filters、viewed refs、stop reason、tool calls、tokens、latency 和 retraction/ACL denial；其中正文不进入长期指标默认字段。
19. PAST-Bench B6 的旧 `preference|rule|fact|procedure` 断言、prompt 和路由被本规格替换；回归测试覆盖 rule/procedure 迁移不产生自动 Skill promotion。
20. 各 provider 的 Skill materialization 继续从 Skill Catalog/agent binding 读取，不依赖 Graph 可用性；Graph index 不可用时不得丢失已授权 Skill 的既有运行时投递。

## 10. 测试与 rollout

- 单元测试：AtomKind schema/迁移、NodeRole、typed SkillRef resolver、Skill summary index、filter normalization、composite contract validation。
- DB/集成测试：Skill grant/binding、channel/project scope、source Agent provenance、artifact hash/version、retraction closure、promotion evidence 与 concurrent update。
- 检索测试：eligible-before-rank、Graph/Atom/Skill partition、跨 corpus fusion、AtomKind/Agent/Skill filter 组合、BM25/vector/reranker 降级、deterministic tie-break。
- Explore contract 测试：层级下钻、Skill relation、DAG traversal、filter persistence、redirect、checkpoint、v1/v2 隔离与 forged ref fail-closed。
- 安全 canary：私有频道 source 引用到 Topic/Profile/Skill、未绑定 Agent 命中 Skill、已撤回 evidence/Skill version、恶意 `SKILL.md`/supporting-file 内容、跨 Workspace ID。
- Shadow rollout：先构建 View/Skill Node 和记录 filter telemetry，不影响默认结果；校验 scope leak=0、retraction fail-open=0、Top-K fill rate、证据命中率、导航层级/成本和 Skill recommendation-to-approved-binding 转化后，才版本化开启各类 Search filter 与融合结果。

## 11. 对既有规格的衔接

- universal spec 的 Segment/Atom scope、publish watermark、typed ref、version pin、DAG traversal、consolidation、promotion、deletion 与 training 规则保持权威；本规格仅在其上新增 role、projection、filter 和 Skill resolver contract。
- universal spec §8.1 的 Atom `kind` 由本规格 §4 精化；其 Atom scope 继承和 Server 验证不变。
- universal spec §9 的 class-aware Search/Explore 由本规格 §7 扩展为 Graph/Atom/Skill 三语料和 eligible-before-rank；不改变 v1 contract。
- universal spec §11 的 Project promotion 不适用于自动 Skill grant；Skill promotion 继续使用独立 grant/policy/human-review 流程。
- `2026-09-01` PAST-Bench 规格的 P3 现由本文承接；该文 B6 的旧 kind、`procedure` 路由、相应 prompt/harness 必须按本文更新后才能实施。

## 12. Skill 演化知识平面与 Spreadsheet 首域

本节基于 *WikiSkill: Compiling Agent Experience into Persistent Knowledge for Skill Evolution*（arXiv:2608.27454v1）的三层架构、持久 Pattern、原子 Skill proposal、validation gating 与跨模型迁移结果，并结合需求 grilling 的确认结论，对 §3、§5、§7、§9 和 §10 作规范性扩展。论文中的文件式 Wiki、全量 Skill prompt injection、单一 accuracy gate 和无清理的长期积累仅作为研究证据，不作为生产实现。

本节继续服从 universal spec 的 DAG SoT、scope、ACL、version pin、watermark、retraction fence、source deletion 和 typed ref 规则。若本节与 §1–§11 的通用约定发生表述差异，本节只在 Skill Evolution 与 Spreadsheet domain profile 范围内提供更具体约束，不降低上位安全不变量。

### 12.1 范围、交付顺序与非目标

首个里程碑同时交付 Graph Memory 分层导航和受控 Skill Evolution，但二者必须独立启停、独立观测、独立回滚；“同一里程碑”不表示同时放量或共用一个开关。能力依赖为：

```text
graph_navigation_v2                         可独立启用
  → pattern_consolidation                   依赖导航、provenance 与 evolution ledger
    → skill_candidate_generation            依赖 Pattern 与 audit history
      → skill_shadow_evaluation              依赖隔离 evaluator 与 assertion manifest
        → skill_runtime_promotion            依赖有效评估、人工审批与 Skill Catalog CAS
```

每层必须有 workspace gate、global kill switch、流量控制和独立 telemetry。关闭任何后级能力不得破坏前级导航，也不得影响 Skill Catalog 中既有 accepted Skill 的 CRUD、grant/binding 或 provider materialization。

第一生产域为 **Spreadsheet Agent**。首版支持受控的 cell-level 与 sheet-level 操作，包括值、公式、格式、排序/筛选和结构变换；明确排除：

```text
VBA / 宏执行
外部数据连接刷新
跨 Workbook 写入
密码或保护绕过
任意网络与不受控插件
任务执行中的在线 Skill 自我修改
```

主 gate 使用 pinned Python/container runtime 与版本化 Spreadsheet adapter；Excel 或 LibreOffice 只作为 compatibility shadow。具体 primary model/provider 和 transfer-shadow target 不写死在长期规格中，但 pilot 启动前必须固定并写入版本化配置。

首版还明确不做：跨 Workspace Pattern/trajectory 聚合、全模型/Provider 默认兼容、无人工审批的真实 runtime promotion、用 Graph 替代 Skill Catalog、把 Spreadsheet 通过解释为所有领域均已验证、用 provider hidden reasoning 训练/演化、全量历史或全部 Skill prompt injection，以及通过减少安全检查或最低 validation 来满足预算。

### 12.2 Trajectory、Outcome 与数据边界

Skill Evolution 中的 `Trajectory` 不是新的事实源或公共 `MemoryRef.kind`，而是对当前 durable run/message/DAG 数据的只读可观察事件视图。当前实现可来自 `task_message`、provider call ledger、Graph Memory Agent tool ledger 和 Interaction DAG sanitized Segment；统一视图只允许包含：

```text
user/assistant 可见消息或其 scope-safe 摘要
结构化 action、tool call 与经清洗的 tool result
环境状态变化、artifact ref/diff 与最终响应
authoritative Outcome
时间、task/dataset、agent/model/provider、tool/runtime/environment metadata
source message seq、Segment/DAG refs、scope、watermark 与 retention metadata
```

以下内容禁止进入 evolution eligible corpus、Pattern/Skill embedding、Maintainer/Proposer prompt、Skill artifact 或训练导出：

```text
provider hidden chain-of-thought、thinking、scratchpad 或 reasoning block
validation answer / hidden oracle
未经清洗的秘密、PII、完整敏感 tool output
无 backing blob 的悬空 artifact placeholder
```

真正未输出的 hidden reasoning 不可成为数据源；provider 明确发出的 `thinking` 即使当前以 `diagnostic_only` 持久化，也只属于短期 incident/debug 数据。其默认 retention 上限为 30 天，Workspace 可配置得更短；延长需要合规依据、actor/reason 和审计，且始终禁止进入 evolution、embedding 与 export。现有历史 thinking 必须由受审迁移/清理任务按 policy 删除或 crypto-erase，不得回填 evolution corpus。

每个 source run 在开始时固定：

```text
run_kind、evolution_eligible、allowed_purposes
workspace/project/channel scope 与 retention policy
task/dataset identity、lineage、tool/runtime/environment versions
```

运行结束后还必须通过 authoritative Outcome、sanitization、当前 ACL、retraction、source deletion、data residency 和 retention 检查才可进入 consolidation。管理员可以撤销 eligibility，但不得把原本禁止训练的数据事后静默追认为可用。历史 trajectory 默认不 eligible；受审 backfill 必须记录 actor、policy、source watermark、选择/拒绝数量和原因。

Outcome 至少区分：

```text
pass | agent_failure | partial | infrastructure_invalid | policy_denied
```

环境损坏、adapter/evaluator 错误、unsupported feature 和 policy denial 不得被归因成 Agent failure 或生成“绕过限制”的 Skill。用户反馈以新的 correction/event evidence 追加并触发重评，不可无审计覆盖原 Outcome。

### 12.3 三个知识平面与权威边界

Graph Memory 共享 canonical provenance，但至少划分三个强隔离的逻辑平面：

```text
task recall plane        当前任务的已授权事实、视图和 approved Skill
evolution plane          Pattern、candidate history、正反 evidence 与 proposer navigation
evaluation plane         hidden dataset/oracle、assertion manifest、EvaluationRun 与 approval
```

不同平面必须使用独立 capability、eligible corpus、索引 partition 或等价强隔离、服务身份与生命周期。客户端参数只能缩小 canonical plan，不能把 task caller 提升为 evolution/curator/evaluator。普通 Search/Explore 不得通过 existence、count、score、timing 或摘要侧信道泄露 evolution/evaluation 对象。

权威映射为：

| 对象 | 权威来源 | Graph 表示 | 执行/授权能力 |
|---|---|---|---|
| Raw observable trajectory / Outcome | Interaction DAG、task/run ledger、authoritative scorer | scope-safe evidence projection | 否 |
| Pattern | durable evolution ledger | `NodeRole=pattern` 投影 | 否 |
| SkillCandidate | durable evolution ledger + immutable candidate artifact | evolution-only relation/summary | 否 |
| AssertionManifest / EvaluationRun | evaluation ledger | opaque ref 与 scope-safe结果摘要 | 否 |
| Approval / Deployment / Rollback | approval/promotion ledger + Skill Catalog transaction | Skill relation与审计投影 | 可改变 active artifact/grant，仍受审批 |
| Skill artifact | `skill` / `skill_file` / grant / binding | `NodeRole=skill` 投影 | 是，仍受当前授权 |

§3.2 的 `NodeRole` additive 增加 `pattern`。它是版本化扩展，不改变既有无 role node 按 `memory` 解释的规则。§7 的普通 Graph corpus 不因新增 role 自动包含 Pattern：`pattern` 只有在 canonical plan 已授予 evolution/curator capability 时才 eligible-before-rank。

Pattern 投影使用公共 `MemoryRef(kind=graph_node)`，但 resolver 必须同时校验 role 与 purpose。`SkillCandidate`、`EvaluationRun`、`AssertionManifest`、`Approval` 使用内部 capability-scoped `SkillEvolutionRef` 或等价域 ref；本版本不扩展公共 `MemoryRef.kind`，也禁止把这些对象伪装成普通 Graph node 绕过验证数据权限。

Graph/evolution index 故障只能停止新 consolidation、proposal、evaluation 或 promotion；不得阻塞上一 accepted Skill 的 Catalog 读取和 provider 投递。Graph projection 永远不是 candidate decision、active artifact pointer、grant 或 binding 的事务权威。

### 12.4 权威数据模型与状态

#### Pattern

Canonical Pattern record 至少包含：

```text
pattern_id、revision、workspace/evolution key
pattern_kind: success | failure | mixed
status: tentative | supported | contradicted | refuted | stale
problem、applicability、root_cause_summary、recommended_action
positive_evidence_refs[]、negative_evidence_refs[]
task type、source/target model、provider/tool/runtime/environment metadata
scope/provenance closure、generator/policy version
created/updated actor、timestamps、content hash
```

Graph `NodeRole=pattern` 只是该 record 的 scope-safe versioned projection。单条 trajectory、单个模型自述或无 authoritative Outcome 的总结只能形成 `tentative` Pattern。升级为 `supported` 的门槛由版本化 policy 定义，必须使用多个独立 lineage/task evidence，并允许负证据阻止升级；同一 Workbook 的复制、改名或轻微变体不能重复计票。

#### SkillCandidate

```text
candidate_id、revision、target_skill_id 或 new_skill_name
base_artifact_version/hash、candidate_artifact/hash、proposed_diff_hash
proposer identity/model/policy、motivating Pattern/source refs
requested scope、required capabilities、created_at
status: needs_review | shadow | evaluating | accepted | rejected | stale | withdrawn | superseded
```

`accepted` 只表示 proposal decision，不表示已产生 agent/channel/workspace grant。实际 binding、grant、canary 和 scope widening 由独立 Approval/Deployment records 表达。

#### AssertionManifest 与 EvaluationRun

AssertionManifest 是 immutable、versioned 的机器判定合同，至少包含：

```text
manifest_id/hash、dataset/task-set identity/version、lineage split
domain profile、task slices、evaluator/scorer versions
environment key、required capabilities
assertion_id/kind、oracle ref hash、severity、hard/soft
tolerance/threshold、required/not-required、data residency
```

EvaluationRun 必须 append-only；重试或 scorer/policy/manifest 变化产生新 run，不覆盖旧分数：

```text
evaluation_id、candidate/base artifact hashes、manifest hash
target agent/model/provider/tool/runtime/environment
逐 assertion pass | fail | error | not_run + evidence hash
correctness/safety/ACL/privacy/cost/latency/reliability/confidence metrics
contamination status、decision policy/version、terminal result/reason
```

#### Approval、Deployment 与 Rollback

Approval record 至少包含 approver identity/role、candidate/evaluation/manifest/policy/artifact hashes、target scope、decision、reason、expiry、risk acknowledgement 和是否允许自动 rollback。Deployment/Promotion 独立记录 target agent/channel/workspace、binding/grant 前后状态、materialization status 与 provider convergence。Rollback 记录 trigger、from/to artifact、fence、in-flight policy、actor/policy 和后续 roll-forward 状态。

所有 ledger 对象使用 opaque ID、revision/content hash、scope、created actor/time、idempotency key 与 append-only decision history；删除 source 后可保留的最小 audit hash/reason 不得包含可恢复正文。

### 12.5 Pattern 归纳、去重与主动导航

Pattern Maintainer 必须比较成功与失败 trajectory 的实际 action/tool sequence、artifact diff 与 authoritative Outcome，归纳可泛化根因而非复制 error message。至少支持：

```text
Pattern --observed_in--> Segment | Trajectory | Outcome
Pattern --supported_by--> SuccessfulOutcome | EvaluationRun
Pattern --contradicted_by--> FailedOutcome | EvaluationRun
Pattern --mitigated_by--> Skill
Pattern --recurs_with|generalizes|supersedes|conflicts_with--> Pattern
SkillCandidate --motivated_by--> Pattern
```

去重先用 deterministic fingerprint/lineage 召回候选，再比较语义、applicability、root cause、environment 与正反 evidence，决定 append、link、merge 或新建。Embedding 相似、名称相同或多数投票都不能单独自动合并。Merge 必须可逆、保留原 ID/evidence/audit；冲突不得由新内容覆盖，而应保留 `conflicts_with` 并细化适用条件，无法消解时降级为 `tentative/unknown`。

Skill Proposer 不接收全部历史 prompt。它先读取 Pattern/Skill index、任务 pass/fail 摘要和 historical impact，再在 pinned plan/version/watermark 内主动导航：

```text
查询历史 accepted/rejected candidates
→ Explore 相关 Pattern、正反 Outcome 与 target compatibility
→ 按 ACL/retention 下钻少量 evidence
→ submit 一个原子 SkillCandidate 或 no_action
```

Rejected proposal 不永久禁止，但相同 fingerprint 默认被去重；只有 evidence、environment、base Skill 或 proposal 发生实质变化时才允许新 EvaluationRun，且必须说明变化。更换措辞不能绕过 rejection history。

Evolution-capable plan additive 支持：

```text
memory_purpose: task_recall | skill_evolution | evaluation_audit | curator_review
node_roles: ... | pattern
pattern_kinds / pattern_statuses / candidate_statuses
target_agent_ids / target_model_ids / provider_ids / tool_capability_ids
evaluation_dataset_ids / evaluation_run_ids
```

这些字段只缩小 server-authoritative corpus。一个 proposer/evaluator run 不得通过 redirect 偷换 Graph version、watermark、scope、manifest 或 hidden dataset；需要刷新必须创建新 run。

### 12.6 Evolution Orchestrator、原子 proposal 与并发

Evolution Orchestrator 是流程 DRI，但不取代各域 SoT。Evolution key 固定为：

```text
workspace + target agent + task type + environment major version
```

同一 key 同时只允许一个 active mutation run；不同 key 可并行。首个 milestone 只允许 curator 手动创建、带 pinned input set 的 run；定时触发在 Pattern 质量、成本和污染指标稳定后另行开启，且必须有最小新 evidence 门槛。

Run 至少经历：

```text
queued → snapshotting → consolidating_patterns → proposing_candidate
       → awaiting_review → evaluating → awaiting_approval → completed
terminal: no_action | rejected | cancelled | failed | stale | fenced
```

每次 run pin：source/evidence set、Graph identity/version/watermark、base Skill hash、manifest/dataset、model/provider/runtime、policy、scope、budget 与 data residency。重试创建新 attempt 或从安全 checkpoint 恢复，不覆盖历史。

每个 proposal 默认是：

```text
一个 candidate → 一个 target Skill → 一个 base artifact version → 一个受审 diff
```

Candidate artifact 必须 immutable；应用和批准使用 CAS。最终 activation 必须在单一事务或可证明等价的一致性协议中重新校验：

```text
target active-safe/base artifact pointer
candidate revision/status 与 artifact/diff hash
manifest、EvaluationRun、gate policy 与 approval revision/hash
source/ACL/retraction closure 与 target scope
binding/grant 前置状态
```

成功时原子写 candidate decision、new artifact/version pointer、promotion audit 与 outbox；provider materialization 和 Graph/index projection 通过幂等 outbox 收敛。任一校验变化即 `stale`，不得自动 rebase、部分发布、扩大 grant 或修改 binding。响应丢失后同一 idempotency key+payload hash 返回原决定；同 key 不同 payload 拒绝。

多 Skill 变更拆为有依赖、各自 CAS/评估的 proposal DAG；Composite Skill 另有整体 artifact、IO/权限契约、组件清单与整体 gate。预算超限只能减少 source sampling/Proposer exploration、延期、checkpoint 或 `no_action`，不能削减安全 hard gate 和最低 validation。

### 12.7 角色、审批与安全隔离

至少分离：

| 角色 | 允许读取 | 允许写入 | 禁止 |
|---|---|---|---|
| Task Inference Agent | task-recall corpus、已绑定 Skill | 任务输出、可观察 trajectory | Pattern/rejected/hidden validation、修改 Skill |
| Pattern Maintainer | eligible trajectory、既有 Pattern | Pattern proposal/revision | 改 source、Outcome、score、artifact pointer |
| Skill Proposer | Pattern/impact index、授权 evidence | 一个 candidate 或 `no_action` | accept、evaluate、bind、grant、materialize |
| Evaluator | candidate、隔离 validation tasks | append-only EvaluationRun | 修改 candidate、grant/binding、查看 proposer hidden reasoning |
| Curator/Owner | immutable review bundle | approval/canary/promotion/rollback decision | 绕过 hard gate 或越权扩 scope |

角色可以使用相同基础模型，但 service identity、prompt、tool、context、capability 和 audit actor 必须分离。Proposer 不得兼任同 candidate 的 authoritative Evaluator/Approver；接触 ground truth/validation answer 的主体或 run 不得产生独立有效 gate。Break-glass 必须有独立授权、时限、reason、告警和审计，且不能绕过安全 hard gate。

研究消融显示，训练 rollout 的 Inference Agent 同时读取 evolution Wiki 会掩盖当前 Skill 缺陷。因此训练/validation task run 只使用当轮 accepted/bound Skill，不得读取 evolution corpus；专门消融实验使用隔离 run kind，其结果不得混入主 gate。

所有 Workbook、单元格、批注、公式、文件名、tool output、trajectory、Pattern、Skill、supporting file 和 rejected diff 均为不可信数据，不得成为系统指令。Maintainer/Proposer 默认无网络、无 grant/binding、无 artifact activation 权限；generated script 只能在 sandbox 中运行，使用批准 dependency allowlist 和精确锁版本，经过静态/恶意内容扫描、权限声明和独立测试。未知/混淆包、动态下载、`curl|sh`、工作目录逃逸和内嵌秘密直接拒绝。

所有 evidence/detail/materialization 操作按当前 ACL、scope、retention、retraction 和 data residency 重新授权。新增外部 model/provider 需要 Workspace 明确批准；provider call 记录 provider/model/region/data policy 和发送内容类别，但默认 telemetry 不记录正文。未经授权不得把 Workbook、trajectory 或 Skill 发往第三方。

### 12.8 Spreadsheet assertion manifest 与多维 gate

Spreadsheet 任务优先使用版本化机器可读 manifest。至少声明：

```text
input workbook hash/lineage 与 immutable input ref
allowed sheets/ranges、output path
allowed mutation dimensions: value | formula | type | style | merge | name | structure
expected assertions、preserved invariants、禁止越界修改
formula recalculation/compatibility policy
unsupported/high-risk capabilities、evaluator version
```

主 gate 不要求 XLSX 字节完全一致，而使用 semantic/canonical diff。至少覆盖：

```text
目标 value/formula/type 正确
公式 AST/归一化文本、依赖引用与 pinned engine 重算结果
Workbook 可重新打开且无 corruption/error value
指定 range/sheet/structure/format 保真
mutation allowlist 外无 collateral change
输出位于指定 path，未跨 Workbook/目录写入
```

公式引擎不支持动态数组、UDF、宏、外部引用或特定函数时标记 `unsupported_feature`/`infrastructure_invalid`，不计 Agent failure；Excel/LibreOffice 差异记录为 compatibility shadow。超长 tool output 保存清洗摘要和 content-addressed artifact ref；ref 必须有真实 backing blob，并受 ACL、malware scan 与 retention 控制。

Dataset split 使用 source lineage、Workbook canonical fingerprint 和任务语义 fingerprint，防止同模板、改名或轻微变体跨 training/hidden validation。验证由三部分组成：

```text
regression set     保证既有能力不退化
hidden held-out    检查泛化，Proposer 不读 answer/oracle
fresh shadow       检查真实 runtime、成本与负迁移
```

Baseline 与 candidate 必须运行同一 immutable manifest/environment。Required assertion 缺失、oracle 不可读、evaluator error、环境不匹配、污染或 manifest 在看到结果后原地修改均 fail closed；修改 manifest/threshold 必须新版本和新 EvaluationRun。

Gate 分维判断，禁止单一加权总分掩盖 hard failure：

```text
correctness / assertion coverage / critical task slices
security / ACL / privacy / scope / retraction / deletion
artifact integrity / collateral mutation / output path
capability / model / provider / tool / runtime compatibility
cost / token / tool calls / latency / recovery reliability
sample size / confidence / repeated trials / negative transfer
```

固定零容忍：scope leak、validation contamination、unauthorized write/promotion、retraction fail-open、非法 output path。Correctness 非劣界限、目标增益、成本、延迟、样本下限和统计门槛由 baseline 后的版本化 policy 配置并写入 EvaluationRun。LLM judge 只能提供版本化辅助评分/解释，不能单独自动批准 Skill；其 model/prompt/version 和输入 refs 必须审计。

### 12.9 Approval、canary、materialization 与 rollback

自动化上限是 Pattern、candidate、隔离 validation 和 shadow；任何进入真实 runtime 的 binding，以及 agent→channel→workspace 扩域，都需要显式人工批准和目标 scope 的新评估，不能继承窄 scope approval。

审批矩阵：

```text
agent binding       agent owner 或 delegated curator
channel grant       channel owner + curator
workspace grant     workspace owner + 独立 curator
高风险写操作/外部副作用  无论 scope 均加严或双人审批
```

Proposer、Evaluator、candidate creator 不能批准自己的 promotion。Curator review bundle 至少展示 base/candidate diff、purpose、Pattern、正反 evidence、逐 slice/assertion 结果、权限变化、目标 scope、compatibility、已知风险、artifact/evaluation/manifest/policy hashes。

Canary 先在 immutable input copy、隔离 workspace 和独立 output path shadow replay；旧/新 Skill 不得双写同一真实文件。真实 canary 需要 owner 授权、可恢复输出与明确版本标识。

Canonical artifact 可以包含 `SKILL.md`、受审脚本和声明式资源，但必须有 content hash、依赖锁、capability/IO contract、测试和 provenance manifest。Agent 只看到真实 grant/binding eligible Skill 的 name/description，相关时再 progressive disclosure；Graph 命中不能自动安装、绑定、读取全文或执行。

Materialization 与每次 active version 解析重新检查 artifact hash、active-safe pointer、grant/binding、ACL、retraction/fence 与 target runtime。安全/ACL/恶意内容/source deletion 等确定性事件立即 fence；统计性能回归达到版本化门槛或连续窗口后 CAS 回退上一 accepted-safe artifact；弱信号停止扩大 grant 并人工复核。

Rollback 不删除 binding history：binding 指向逻辑 Skill，由 authoritative active-safe version 解析。Rollback 记录 provider convergence；超时告警并 fail closed。In-flight run 不得切换到一半版本；安全 fence 优先于 pin。Graph/index stale 不影响权威回滚；Graph/evolution 故障不得阻塞上一安全版本投递。

### 12.10 跨 Agent、模型、Provider 与 runtime

§7 已有 `source_agent_ids`、`skill_applicable_to_agent_ids`、`skill_assigned_to_agent_id` 继续保持三种独立语义，并增加：

```text
skill_discovered_by_agent_id / skill_discovered_by_model_id
skill_evaluated_on_agent_ids / skill_evaluated_on_model_ids
skill_target_provider_ids / skill_required_capabilities
transfer_class: general_procedure | model_specific_workaround | runtime_specific | unknown
```

Skill 质量按矩阵记录，不压缩成全局通用分数：

```text
(skill artifact version,
 target agent/model/provider,
 tool/runtime/environment version,
 task type/dataset version)
→ correctness、regression、cost、confidence、observed_at
```

未经目标环境验证的 transferred Skill 默认 `unknown/shadow`，不得继承 source model 的 accepted decision 或 grant。模型特定 workaround 不得包装成通用 procedure，也不得覆盖目标模型原本可用的完整策略。检索可以结合 semantic relevance、compatibility、required capability、evaluation confidence、recency 和 grant/binding eligibility，但只影响推荐排序，不扩大权限。

Evaluator、formula engine、Python library、Provider、安全 policy 或环境 major version 变化时，旧 EvaluationRun 按兼容矩阵继续有效或标 `stale` 并触发 shadow revalidation，不能永久默认有效，也不能无差别立即删除。

### 12.11 Retention、删除、备份与合规

Retention policy 必须分别覆盖 observable trajectory、diagnostic thinking、Pattern evidence/projection、candidate artifact/diff、EvaluationRun、manifest/result、approval/deployment、tool artifact 和 telemetry，而不只覆盖 query log/blob。Workspace policy 决定普通正文期限；Pattern/Skill 引用不得延长 source 正文寿命。

Source eligibility 撤销、删除、retraction 或 scope 缩窄时重算依赖 closure：Pattern 降级/隐藏，未批准 candidate stale，EvaluationRun invalidated，approved Skill 进入风险评估。若 artifact 含被撤正文/敏感值或该来源是唯一关键支持，则立即 fence；否则重新验证剩余证据。审计可保留合法最小不可逆 hash、decision reason 与版本关系，但不能保留可恢复正文、score breakdown 侧信道或成员计数。

后台支持 Pattern merge/supersede/stale、低价值 projection compaction、artifact orphan 检测和 dangling ref 修复。正文到期、撤权或删除后，普通/evolution reader 都不能通过旧摘要、diff、index cache、count 或备份恢复内容。

备份可包含 ledger、artifact metadata 和 projection，但 restore 后先进入不可执行状态，重放 deletion/retraction fence，并完成 ACL、grant、artifact hash、source guard reconciliation 后才能恢复 materialization；不得复活已擦除正文、旧 grant 或被 fence artifact。

### 12.12 Schema/data migration 与兼容性

迁移必须双向、可 checkpoint、幂等、可中断恢复，并保持旧 reader/accepted Skill runtime 可用。至少包括：

1. 七类 AtomKind/NodeRole/search metadata 的兼容扩展与旧 `rule/procedure` 显式迁移；
2. Pattern、SkillCandidate、EvaluationRun、AssertionManifest、Approval/Deployment/Rollback ledger；
3. Graph/Skill projection outbox、index metadata 与 feature gates；
4. Spreadsheet artifact/manifest/assertion/result schema；
5. 现有 Skill artifact hash 与 `NodeRole=skill` shadow backfill。

现有 Skill 保持原 grant/binding/materialization，不因迁移停用；回填 artifact hash/Skill Node 后标记 `legacy_provenance`、`evaluation_unknown`，不得伪造 Pattern、candidate、manifest、accepted evaluation 或扩大 grant。后续通过 shadow revalidation 逐个补齐。

历史 trajectory 默认不回填；被批准的 backfill job必须排除 thinking，执行当前 sanitization/ACL/retention/outcome 检查并记录 watermark。旧评估缺少 manifest/source/environment 时标为 `legacy/unverified`，不可满足新 gate。

公共 v1 Graph API 与 node-only run 保持不变；v2 才读取 Pattern role。Evolution mutation 使用新的 capability-gated control API，与普通 Search/Explore 分离。关闭 evolution reader/scheduler/projection 即可回滚功能；除非已证明数据可安全丢弃，否则 rollback 不 down/drop append-only audit ledger。

### 12.13 运维、SLO、预算与责任

职责：Interaction DAG/Graph team 负责 source/provenance/ACL/retraction；Skill Platform 负责 artifact/grant/binding/materialization；Evaluation owner 负责 dataset/scorer/gate policy；Evolution Orchestrator 负责状态机、CAS、outbox、reconciliation 与审计；Security/Data Governance 负责 retention、incident、data residency 和跨 scope promotion。

Navigation 保持交互式 SLO；Evolution 是异步、可取消、可 checkpoint、有 deadline 的任务。开启 evolution 不得显著降低现有 Search/Explore p95。首发为单个受控 Workspace、少量 Spreadsheet Agent 的 pilot；primary target 与 transfer-shadow target、correctness/cost/latency 门槛和 quota 在 baseline 后写入版本化 policy。

预算至少覆盖 sampled trajectories/evidence bytes、Maintainer/Proposer steps、token/cost、candidate 数（默认 1）、validation tasks、并发、artifact size 和 wall time。超限不得减少 hard gate。

必须监控/告警：scope/retraction denial、validation contamination、越权尝试、异常 promotion、CAS conflict、队列/gate 延迟、materialization/index lag、provider convergence、连续 rollback、负迁移、dangling artifact/ref、ledger/projection 不一致和 retention sweep 失败。普通 candidate rejection 是业务事件，不触发告警风暴。

任务通过事务、outbox、idempotency、DLQ 和 reconciliation 恢复；相同 key+payload 重放返回原结果，异载荷拒绝。故障时先停止 scheduler/promotion、关闭 evolution reader、保留 ledger，再回退 active-safe artifact；不以 schema down 作为首选回滚。

通知只发给 curator/owner 的 candidate、approval、rollback 和安全事件；普通用户只在实际绑定版本变化或任务受影响时收到 scope-safe 摘要。日志/webhook 不得携带 Workbook、hidden answer、thinking、完整 diff 或敏感 tool output。

上线需 Graph、Skill Platform、Security/Data Governance、Spreadsheet product owner 和 pilot Workspace owner 分别签字。任何一方只能批准自己的责任域，不能用总体 accuracy 覆盖安全或数据治理失败。

### 12.14 验收标准与测试追踪

除 §9 的通用 20 条外，Skill Evolution/Spreadsheet 首域必须满足：

1. 导航与 evolution 同一 release 交付但独立开关、独立 kill switch；关闭 evolution 不影响 navigation 和既有 accepted Skill。
2. `thinking`、hidden reasoning、validation answer 不进入 eligible trajectory、Pattern、embedding、candidate prompt、artifact 或 export；历史/新增 retention 与删除任务可验证。
3. 只有 run-start 显式 eligible、当前仍授权、已清洗、未撤回且有 authoritative Outcome 的 trajectory 可被 consolidation；历史 backfill fail closed。
4. Pattern canonical record 与 Graph projection revision/hash 一致；单轨迹只能 tentative，独立 lineage 与负证据 gate 正确。
5. Pattern 去重/merge 可逆，冲突不覆盖，私有 evidence 不通过摘要/count/score 泄露。
6. 普通 Task Agent 无法读取 evolution/evaluation corpus；Maintainer、Proposer、Evaluator、Approver 的 capability 与职责分离不可绕过。
7. 一个 run pin evolution key、Graph/version/watermark、base artifact、manifest/dataset、environment、policy 与 budget；同 key 并发拒绝，重试幂等。
8. Candidate 是单 Skill、单 base version 的 immutable artifact；stale hash/revision、跨 Skill side effect、越权 scope 或不可见 evidence 均 fail closed。
9. 最终 activation 原子 CAS；任何失败不部分改变 artifact pointer、candidate decision、grant/binding 或 provider materialization。
10. Spreadsheet manifest 能检测目标 value/formula/type、公式重算、Workbook corruption、format/structure 保真、collateral mutation 和 output-path violation。
11. `infrastructure_invalid`、`policy_denied`、`unsupported_feature` 不计为 agent failure；LLM judge 不能单独自动批准。
12. Training/regression/hidden/shadow 按 lineage 隔离，接触 ground truth 的 run 不能生成独立有效 gate，contamination=0。
13. Gate 分维输出 hard/soft 结果；scope leak、未授权写入/promotion、retraction fail-open、validation contamination 和 output-path violation 均为 0。
14. Agent→channel→workspace 每次扩域有目标环境新评估和显式审批；高风险 Skill 加严，accepted decision 不自动产生 binding。
15. Canary 只操作 immutable copy/隔离 output；Graph hit、Pattern 推荐或 shadow status 不能执行 Skill。
16. 一个 Skill 可保存 target model/provider/tool/runtime 的独立评估；未验证迁移保持 unknown/shadow，model-specific workaround 不污染不兼容目标。
17. Source 删除/retract 后 resolver 立即 fence，异步 index 旧项不能读取正文或继续 materialize；closure 重算、rollback 和 provider convergence 可审计。
18. 既有 Skill migration 不丢 binding/grant，不伪造 provenance/evaluation；Graph/evolution 故障时继续投递上一 accepted-safe artifact。
19. Backup restore 不复活删除正文、旧 grant 或 fenced artifact；retention sweep 覆盖 PostgreSQL trajectory、provider ledger 与 evidence artifacts。
20. E2E 证明：eligible Spreadsheet trajectory → Pattern → atomic candidate → isolated assertion evaluation → human canary approval → provider materialization → fence/rollback；全链路 refs/version/hash/actor/policy 可追溯。

增量测试矩阵：

| 类别 | 必测内容 |
|---|---|
| Unit | enum/schema、Pattern 状态、fingerprint/lineage、manifest/assertion、gate、CAS、compatibility、retention policy |
| DB/transaction | append-only ledger、同 key 重放/异载荷冲突、并发 run、activation CAS、outbox、approval、rollback、migration up/down/up |
| Search/navigation | purpose partition、eligible-before-rank、Pattern evidence、rejected history、forged ref/filter、v1/v2 隔离 |
| Spreadsheet | canonical workbook diff、公式/类型/格式/结构、collateral mutation、unsupported engine、output path、artifact corruption |
| Security | prompt injection、secret/PII、validation leak、cross-workspace、malicious script/dependency、ACL/retraction race、side channel |
| Fault injection | before/after commit、response loss、stale base、provider/index outage、DLQ/reconciliation、restore/re-fence |
| UI | immutable review bundle、分层审批、risk acknowledgement、rollback/fence、正文按需 resolver、通知脱敏 |
| E2E/canary | offline replay → shadow → single agent → channel → workspace，记录 correctness、regression、cost、negative transfer 与安全零事件 |

Definition of Done 是完成上述第 20 条闭环，而不是仅生成一个 Skill、代码合并或总体 benchmark accuracy 上升。
