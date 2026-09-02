# Graph Memory 分层导航与 Spreadsheet Skill Evolution 实施计划

> 状态：Draft；规格已确认，代码实施尚未授权
>
> 日期：2026-09-02
>
> 对应规格：[`../specs/2026-09-02-graph-memory-layered-skill-graph-navigation-spec.zh-CN.md`](../specs/2026-09-02-graph-memory-layered-skill-graph-navigation-spec.zh-CN.md)
>
> 基线：计划编写时最新 `lrm/dev` detached HEAD 为 `1f31c899f`；实施分支开工时必须重新审计最新代码和 migration，不能机械沿用本文估算编号。

## 1. 目标与交付方式

本计划在同一 release milestone 交付两条可独立启停的能力链：

```text
A. Graph Navigation v2
   AtomKind/NodeRole/Pattern role
   → eligible-before-rank Graph/Atom/Skill retrieval
   → Profile/Topic/Pattern/Skill 分层导航

B. Spreadsheet Agent Skill Evolution
   eligible observable trajectory
   → Pattern consolidation
   → atomic SkillCandidate
   → immutable Spreadsheet assertion evaluation
   → human canary/promotion
   → Skill Catalog active-safe version
   → provider materialization / fence / rollback
```

Navigation 可以独立上线；Evolution 依赖 Navigation v2、provenance、audit ledger 和隔离 evaluator。所有 Evolution gates 默认关闭。任何 Phase 都不得影响既有 Skill Catalog CRUD、`agent_skill` binding、grant 或 provider-native Skill 投递。

本计划只写实施顺序和验收，不授权代码修改、commit 或 rollout。每个 Slice 严格执行：

```text
Test first → Implement → Targeted Validate → Review gate
```

## 2. 当前代码基线与复用边界

### 2.1 Graph Memory

当前核心：

```text
server/internal/memorygraph/types.go
server/internal/memorygraph/atom.go
server/internal/memorygraph/atom_index.go
server/internal/memorygraph/retriever.go
server/internal/memorygraph/store.go
server/internal/memorygraph/graph.go
server/internal/memorygraph/consolidate.go
server/internal/memorygraph/explore_tools.go

server/internal/service/graph_memory_recall.go
server/internal/service/graph_memory_recall_execute.go
server/internal/service/graph_memory_agent_run.go
server/internal/service/graph_memory_agent_tool_ledger.go
server/internal/service/graph_memory_agent_gateway.go
server/internal/service/graph_memory_agent_execution.go
```

已知差距：`Node` 尚无 canonical `NodeRole`；Atom 当前实现只接受 `fact|preference|fallback`；`GraphView` 只描述 scope；Graph retrieval 当前可在候选 Top-K 后过滤；公共 `MemoryRef` 只有 `graph_node|staging_atom`；Skill 尚未进入 Graph corpus。

### 2.2 Trajectory、DAG 与 retention

```text
server/migrations/026_task_messages.up.sql
server/pkg/db/queries/task_message.sql
server/internal/handler/daemon.go
server/internal/service/task.go
server/internal/service/interaction_dag_sanitize.go
server/internal/service/interaction_dag_publisher.go

server/migrations/314_pi_provider_call_ledger.up.sql
server/migrations/476_universal_interaction_dag.up.sql
server/migrations/486_*.sql
server/internal/service/memory_retention.go
server/internal/service/memory_archive.go
server/internal/handler/memory_retention.go
server/internal/scheduler/jobs_graph_memory.go
```

当前 `task_message` 可保存 text/thinking/tool use/tool result；provider-emitted thinking 通常为 `diagnostic_only`。DAG sanitizer 已有 redaction、binary rejection、字段上限和 artifact placeholder，但现有 retention 尚未证明统一覆盖 `task_message`、`pi_provider_call`、`agent_execution` 与所有 DAG trajectory。Evolution eligibility 必须建立在这些现有 SoT 之上，不能创建第二套 raw trajectory。

### 2.3 Skill Catalog 与 materialization

```text
server/migrations/008_structured_skills.up.sql
server/migrations/261_skill_grant_level.up.sql
server/pkg/db/queries/skill.sql
server/internal/handler/skill.go
server/internal/handler/skill_promote.go
server/internal/service/evolution_skill_catalog.go
server/internal/service/evolution_version.go
server/internal/daemon/shared_skills.go
server/internal/daemon/execenv/bound_skills_mirror.go
server/internal/daemon/execenv/context.go
server/internal/daemon/agent/skills/providers.go
```

`skill/skill_file/agent_skill/grant_level/skill_promotion` 继续是 artifact、binding 与授权权威。开工前必须审计现有 `evolution_version` 是否满足 immutable artifact version/active-safe pointer；不满足时扩展，禁止再造语义重叠的版本表。

### 2.4 Spreadsheet

当前仓库未发现可直接复用的 canonical Workbook model、XLSX parser/writer、formula engine、Spreadsheet evaluator 或 benchmark adapter。可以复用通用 storage/attachment、artifact 生命周期、sandbox、evaluation runner/grader 的模式，但不能直接复用带 Research 领域语义的表和类型。

新增代码建议分层：

```text
server/internal/skillevolution/                 通用领域模型、状态机、gate、Orchestrator
server/internal/skillevolution/spreadsheet/     Spreadsheet domain adapter/assertion/diff
server/internal/service/skill_evolution*.go     ACL、跨域协调、outbox/reconciliation
server/internal/handler/skill_evolution*.go     capability-gated HTTP
server/pkg/db/queries/skill_evolution.sql        稳定单表/读模型查询
```

## 3. 开工 Gate

### Gate G0 — 最新基线与 writer inventory

开工分支必须：

1. 从实施当日最新 `lrm/dev` 创建干净分支，记录 commit；不得覆盖当前 detached 文档 worktree或原始 dirty worktree。
2. 重新检查最高 migration。文档基线最高为 `490_universal_dag_legacy_backfill`，本文 `491+` 仅是建议占位。
3. 列出所有 Skill content/version、grant、binding、promotion 和 provider materialization writer。
4. 列出 `task_message`、provider call、DAG Segment、Graph Atom/Node、retention/retraction writer。
5. 列出 Graph Search/Explore v1/v2 reader 和当前 filter/rank 顺序。
6. 证明旧应用镜像可在新增 schema 上运行，所有新 writer/reader 默认关闭。

未完成 inventory 前不得新增 production writer。

### Gate G1 — ADR：权威、事务与包边界

冻结以下决策：

- Pattern、SkillCandidate、AssertionManifest、EvaluationRun、Approval/Deployment/Rollback 的 SoT；
- Skill artifact version 与 active-safe pointer 复用/扩展方案；
- final activation 的 DB transaction、锁顺序、CAS 和 outbox；
- Evolution key 与单 active run 唯一约束；
- public `MemoryRef` 与内部 `SkillEvolutionRef` 边界；
- Pattern Graph projection 的 identity/version/retraction closure；
- evaluator package 与 Spreadsheet adapter seam；
- rollback、writer epoch、旧 writer fail-closed 和 migration cutover。

### Gate G2 — ADR：Spreadsheet evaluator 与供应链

在添加依赖前冻结：

- pinned Python/container 和 XLSX/formula engine；
- canonical workbook diff 规则；
- Excel/LibreOffice compatibility shadow 方式；
- approved dependency allowlist、精确版本、registry、malware/static scan；
- sandbox filesystem/network/process/CPU/memory/time envelope；
- benchmark fixture、lineage split、hidden oracle 与 artifact storage。

新增依赖必须使用精确版本；异常或近似包名需人工确认。

### Gate G3 — Feature flags 与 policy

建议全局开关（最终命名由配置约定决定）：

```text
MULTICA_GRAPH_NAVIGATION_V2_ENABLED
MULTICA_PATTERN_CONSOLIDATION_ENABLED
MULTICA_SKILL_CANDIDATE_GENERATION_ENABLED
MULTICA_SKILL_SHADOW_EVALUATION_ENABLED
MULTICA_SKILL_RUNTIME_PROMOTION_ENABLED
MULTICA_SPREADSHEET_SKILL_EVOLUTION_ENABLED
```

每层还需 workspace DB gate。所有默认 false；后级开关不得隐式打开前级 writer。

## 4. Migration 切片（编号实施时重算）

| 建议编号 | 目标 | 主要对象 |
|---|---|---|
| 491 | Graph/Atom 兼容元数据 | 七类 AtomKind validation/backfill state、NodeRole/search metadata；若 Graph file model 不需 DB 列则只保存 migration audit/state |
| 492 | Evolution core | `skill_evolution_run`、`skill_pattern`、Pattern evidence/revision、candidate 与 candidate artifact/ref |
| 493 | Evaluation | assertion manifest/assertion、EvaluationRun、逐 assertion result、dataset/environment identity、contamination status |
| 494 | Decision/deployment | approval、deployment/promotion、rollback、idempotency、active-run unique constraint、outbox/reconciliation state |
| 495 | Spreadsheet profile | Workbook artifact manifest、lineage/fingerprint、canonical diff/assertion outcome metadata；大文件继续走 object storage |
| 496 | Retention/backfill | diagnostic thinking expiry/class、trajectory eligibility/backfill checkpoint、legacy Skill projection checkpoint |

每个 migration 必须有 `.up.sql/.down.sql`、fresh/up/down/up、scoped FK、CHECK/unique/index、append-only/immutability trigger 和旧镜像兼容测试。Append-only audit ledger 的 down migration只用于未启用环境；生产回滚优先关 reader/writer，不删除审计数据。

## 5. Phase 0 — 合同、flags 与失败测试

### Slice 0.1 — Domain contracts 与 strict decoder

**Test first**

- 为 Pattern、Candidate、Manifest、Evaluation、Approval、Deployment、Rollback、EvolutionRun 定义 strict JSON/Go contracts；未知 enum/field、missing/null、oversize、hash mismatch 拒绝。
- Golden 覆盖 spec 的状态机与 `SkillEvolutionRef`，证明公共 `MemoryRef` 枚举不变。
- Spreadsheet manifest fixture 覆盖 value/formula/type/style/structure/output-path assertion。

**Implement**

- 新建 `server/internal/skillevolution/types.go`、`contract.go`、`contract_test.go`。
- 新建 `server/internal/skillevolution/spreadsheet/manifest.go` 和 fixture 目录。
- 扩展 `server/internal/memorygraph/types.go`：`NodeRole`、`DerivedAtomKinds`、Pattern/Skill scope-safe metadata；既有 node 默认 `memory`。
- 扩展 `server/internal/memorygraph/atom.go` 七类 AtomKind；保留旧数据读取兼容。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/memorygraph -run 'Test.*(Contract|AtomKind|NodeRole|Manifest)'
```

### Slice 0.2 — Feature gates 与 no-op 基线

**Test first**

- 所有全局/Workspace gates 默认关闭；关闭时零新表写入、零 scheduler/model call、v1 响应字节/语义不变。
- Navigation 开、Evolution 关时 v2 可工作；Evolution 任一级关停不影响 accepted Skill materialization。
- 配置 env override、上限 clamp 和未知值 fail closed。

**Implement**

- 扩展 `server/internal/service/graph_memory_config.go` 或新增 `skill_evolution_config.go`。
- 新增 gate status DTO，不暴露隐藏对象存在性。

**Validate**

```bash
cd server
go test ./internal/service -run 'Test.*(GraphNavigation|SkillEvolution).*Gate'
```

## 6. Phase 1 — Navigation v2 与 eligible-before-rank

### Slice 1.1 — NodeRole/AtomKind publication 与迁移

**Test first**

- 七类 AtomKind 全部可发布；未知 kind、旧 `rule/procedure` 静默映射拒绝。
- `rule` backfill 必须显式选择 instruction/constraint；`procedure` 进入 candidate re-evaluation 或 fallback。
- Node 多 `derived_atom_kinds`、role 缺失兼容为 memory；Pattern 不进入普通 task corpus。

**Implement**

- `server/internal/memorygraph/atom.go`、`types.go`、publication/serialization tests。
- 对应 migration/backfill job；记录 checkpoint/reason，不自动生成 Skill。

**Validate**

```bash
cd server
go test ./internal/memorygraph ./internal/service -run 'Test.*(AtomKind|NodeRole|Backfill|Publication)'
```

### Slice 1.2 — Eligible corpus before rank

**Test first**

- ACL/scope/retraction/watermark/status、result kind、node role、source agent、AtomKind、Skill/Pattern purpose 在 BM25/vector Top-K 前裁剪。
- 不可见高分候选不能占满 K；rank 后 defense-in-depth recheck。
- Graph/Atom/Skill/Pattern partition 隔离，deterministic fusion/tie-break，embedding/reranker降级不放宽权限。

**Implement**

- 重构 `server/internal/memorygraph/retriever.go`、`atom_index.go`，在 loader/index selector 层形成 eligible set。
- 扩展 `GraphView` 或引入 server-canonical `RetrievalPlan`，禁止直接信任 caller filters。
- Skill summary loader只加载 reviewed summary，不加载完整 `SKILL.md`。

**Validate**

```bash
cd server
go test ./internal/memorygraph -run 'Test.*(Eligible|FilterBeforeRank|Hybrid|Partition|TieBreak)'
```

### Slice 1.3 — v2 refs 与分层 Explore

**Test first**

- Profile/Topic/Pattern/Skill → Graph Node → Atom → Segment/Evidence 双向导航。
- Pattern role只有 evolution capability 可读；forged purpose/ref/graph identity/version fail closed。
- v1 node-only run不返回 Atom/Skill/Pattern/DAG capability。

**Implement**

- `server/internal/memorygraph/explore_tools.go`；
- `server/internal/service/graph_memory_recall*.go`；
- `graph_memory_agent_gateway.go`、`graph_memory_agent_run.go`、tool ledger DTO；
- internal `SkillEvolutionRef` resolver，不改公共 `MemoryRef.kind`。

**Validate**

```bash
cd server
go test ./internal/memorygraph ./internal/service -run 'Test.*(Explore|TypedRef|PatternPurpose|V1V2)'
```

## 7. Phase 2 — Evolution ledger、trajectory eligibility 与 retention

### Slice 2.1 — Append-only ledger 与 run state

**Test first**

- migrations 492–494 的约束、单 active evolution key、revision/hash、append-only Evaluation/Decision、同 key同 payload replay、异 payload conflict。
- Run 状态合法转换；terminal run不能复活；stale base/source使 candidate stale。
- Workspace/scoped FK 防跨 tenant 关联。

**Implement**

- migrations + `server/pkg/db/queries/skill_evolution.sql` + `make sqlc`。
- `server/internal/skillevolution/store.go`、`postgres_store.go`、`run.go`、`idempotency.go`。

**Validate**

```bash
make sqlc
cd server
go test ./internal/skillevolution/... -run 'Test.*(Schema|Run|AppendOnly|Idempotency|Scope)'
```

### Slice 2.2 — Observable trajectory projector

**Test first**

- 从 `task_message`/DAG Segment 只导出 observable allowlist；thinking/scratchpad/hidden oracle 始终排除。
- tool result redaction、长度、binary、artifact backing blob、ACL/retraction/retention/data residency fail closed。
- `evolution_eligible` 必须在 run-start 固定；事后追认拒绝。
- Outcome 分类不把 infra/policy/unsupported 计为 agent failure。

**Implement**

- 新建 `server/internal/skillevolution/trajectory.go`、`trajectory_projector.go`。
- 复用 `interaction_dag_sanitize.go`，不复制第二套 secret redactor。
- 在 source run/Segment metadata 中增加用途/eligibility/lineage，或建立不改变 raw SoT 的 scoped eligibility ledger。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/service -run 'Test.*(Trajectory|Eligibility|Thinking|Outcome|ArtifactRef)'
```

### Slice 2.3 — Retention 与历史清理

**Test first**

- diagnostic thinking 默认最多 30 天且不可 export；Workspace 可缩短，不能延长绕过 policy。
- sweep 覆盖 `task_message` thinking、provider ledger、DAG trajectory/evidence artifact；legal hold/source deletion 优先级正确。
- Pattern/candidate ref 不延长 source 正文；删除后仅保留允许的不可逆 audit hash。
- Archive restore 重放 deletion/retraction fence，不复活正文/旧 grant。

**Implement**

- 扩展 `memory_retention.go`、`memory_archive.go`、handler 和 `jobs_graph_memory.go`，必要时新增专用 retention job/DLQ。
- 迁移 496 与 backfill checkpoint；dry-run/report 模式先上线。

**Validate**

```bash
cd server
go test ./internal/service ./internal/scheduler ./internal/handler -run 'Test.*(Retention|Thinking|Archive|Restore|Deletion)'
```

## 8. Phase 3 — Pattern consolidation 与主动 Proposer

### Slice 3.1 — Canonical Pattern + Graph projection

**Test first**

- 成功/失败 action sequence 对照生成 tentative Pattern；多个独立 lineage + policy 可 supported；负证据可 contradicted/refuted。
- fingerprint/lineage + semantic/applicability/root-cause 去重；merge 可逆；conflict 不覆盖。
- Pattern source retract/scope缩窄后 projection隐藏/降级，count/score不泄露。

**Implement**

- `server/internal/skillevolution/pattern.go`、`pattern_fingerprint.go`、`pattern_policy.go`。
- `server/internal/service/skill_evolution_pattern_projection.go` + outbox consumer。
- Graph node `NodeRole=pattern` 和 relation edges；写路径使用 Graph mutation lock/coordinator。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/service ./internal/memorygraph -run 'Test.*Pattern'
```

### Slice 3.2 — Maintainer 与 Proposer capability

**Test first**

- Maintainer 只能写 Pattern proposal；Proposer只能写一个 atomic candidate/no_action。
- Proposer主动读取 index→historical rejection→Pattern→少量 evidence；不能读 hidden validation/thinking。
- 相同 rejected fingerprint 无实质变化不重复评估。
- budget超限返回 checkpoint/no_action，不削减 hard gate。

**Implement**

- `maintainer.go`、`proposer.go` 与窄 tool interfaces；模型 adapter 放 service seam。
- prompt/response strict contract、tool step/evidence byte/token budget。
- 角色使用独立 service identity/capability，即使底层模型相同。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... -run 'Test.*(Maintainer|Proposer|Rejected|Budget|Capability)'
```

### Slice 3.3 — Manual Orchestrator

**Test first**

- curator manual create pin 全部 inputs；同 evolution key 并发拒绝。
- crash、response loss、restart 从 checkpoint恢复；旧 owner/lease不能推进新 run。
- no_action/rejected/cancelled/failed/stale/fenced 终态稳定。

**Implement**

- `orchestrator.go`、`reconciler.go`；
- `server/internal/service/skill_evolution_orchestrator.go`；
- scheduler只做 reconciliation，首 milestone 不做自动周期触发。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/service ./internal/scheduler -run 'Test.*SkillEvolution.*(Orchestrator|Recovery|Lease)'
```

## 9. Phase 4 — Spreadsheet domain profile 与 evaluator

### Slice 4.1 — Workbook artifact、lineage 与 canonical diff

**Test first**

Fixtures 至少覆盖：

- value/formula/type 变化；公式文本不同但语义等价；错误引用；cache未刷新；
- style/merge/named range/sheet structure 保真；
- collateral mutation、跨 range/sheet/workbook、非法 output path；
- corrupt XLSX、unsupported UDF/宏/外部引用、engine mismatch；
- 相同模板改名/改少量值仍归同 lineage group。

**Implement**

- `server/internal/skillevolution/spreadsheet/artifact.go`、`fingerprint.go`、`diff.go`、`formula.go`。
- 大 Workbook 存 object storage；DB只存 immutable ref/hash/size/MIME/scan/retention。
- pinned sandbox adapter；无网络、限定工作目录/output path。

**Validate**

```bash
cd server
go test ./internal/skillevolution/spreadsheet/... -run 'Test.*(Workbook|Diff|Formula|Lineage|OutputPath)'
```

### Slice 4.2 — Assertion manifest 与 scorer

**Test first**

- manifest immutable hash；baseline/candidate同 manifest；修改 threshold 新版本。
- required assertion缺失、oracle不可读、evaluator error、environment mismatch → fail closed。
- outcome `pass|agent_failure|partial|infrastructure_invalid|policy_denied` 正确。
- hidden oracle不进入 Agent/Proposer/tool ledger普通响应。

**Implement**

- `spreadsheet/assertion.go`、`evaluator.go`、`scorer.go`。
- EvaluationRun 逐 assertion结果和 evidence hash；LLM judge只作为辅助 adapter。

**Validate**

```bash
cd server
go test ./internal/skillevolution/spreadsheet/... ./internal/skillevolution -run 'Test.*(Assertion|Evaluator|Scorer|Oracle|Outcome)'
```

### Slice 4.3 — Baseline/candidate/shadow matrix

**Test first**

- regression、hidden held-out、fresh shadow按 lineage隔离。
- primary target与transfer-shadow target独立结果；未经验证为 unknown/shadow。
- hard gate不能被总分覆盖；统计/成本阈值读取 pinned policy。

**Implement**

- `evaluation_matrix.go`、`gate.go`、`compatibility.go`。
- 先支持单 primary + 单 transfer shadow；不构建通用 benchmark平台。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... -run 'Test.*(Gate|Matrix|Transfer|Contamination|NonInferiority)'
```

## 10. Phase 5 — Artifact version、approval、promotion 与 rollback

### Slice 5.1 — Immutable candidate artifact

**Test first**

- `一个 candidate → 一个 target Skill → 一个 base artifact version`；跨 Skill side effect 拒绝。
- SKILL.md/frontmatter/IO/capability/provenance manifest strict；supporting scripts hash/scan/dependency lock。
- Candidate不能修改 grant/binding/provider runtime。

**Implement**

- 复用或扩展 `evolution_version.go`；新增 candidate artifact store。
- Skill summary只索引 reviewed name/description/frontmatter/capability summary。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/service -run 'Test.*(CandidateArtifact|SkillManifest|Dependency|AtomicDiff)'
```

### Slice 5.2 — Final activation CAS + outbox

**Test first**

并发/fault matrix：

```text
before commit
commit success + response loss
stale base/candidate/evaluation/approval
source retract between approval and activation
duplicate same key/payload
duplicate different payload
outbox/provider/index failure
```

任何失败都不能部分更新 active pointer、decision、grant/binding 或 materialization。

**Implement**

- Catalog/evolution ledger事务中的 lock order与CAS；
- decision/promotion/outbox同事务；
- provider/Graph projection幂等 consumer + reconciliation。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/service -run 'Test.*(Activation|CAS|Outbox|Fault|Reconcile)'
```

### Slice 5.3 — Approval 与 scope widening

**Test first**

- agent/channel/workspace审批矩阵、SoD、自批拒绝、双人高风险审批。
- 每次扩域需要目标环境新评估；accepted不自动 binding。
- immutable review bundle hash与审批内容匹配；expired approval拒绝。

**Implement**

- approval service/API；复用 `skill_promote.go` 的权威 grant路径，禁止旁路。
- break-glass 有 lease/reason/alert，仍不能越过 hard gate。

**Validate**

```bash
cd server
go test ./internal/skillevolution/... ./internal/handler ./internal/service -run 'Test.*(Approval|Promotion|Scope|Separation)'
```

### Slice 5.4 — Materialization fence 与 rollback

**Test first**

- 每次 materialization重验 active-safe/artifact/grant/binding/ACL/retraction/runtime。
- hard safety事件立即 fence；confirmed regression回上一 accepted-safe。
- binding history保留；provider收敛超时告警；Graph outage不阻塞rollback/runtime。
- in-flight run不混用版本，安全 fence优先。

**Implement**

- 扩展 `shared_skills.go`、`bound_skills_mirror.go`、`context.go` 和 provider adapter。
- active-safe resolver、fence cache（authoritative recheck优先）、rollback/reconciliation。

**Validate**

```bash
cd server
go test ./internal/daemon/... ./internal/service ./internal/skillevolution/... -run 'Test.*(Skill.*Material|Fence|Rollback|Convergence)'
```

## 11. Phase 6 — Control API 与 Evolution Explorer

### Slice 6.1 — Capability-gated API

建议路由（最终按现有 router约定调整）：

```text
POST /api/workspaces/{workspaceId}/skill-evolution/runs
POST /api/.../runs/{runId}/cancel
GET  /api/.../runs/{runId}
GET  /api/.../patterns
GET  /api/.../candidates/{candidateId}
POST /api/.../candidates/{candidateId}/evaluate
POST /api/.../candidates/{candidateId}/approvals
POST /api/.../deployments/{deploymentId}/rollback
```

**Test first**

- workspace/role/capability、strict body、idempotency、rate/quota、opaque ID；
- forged purpose/ref/dataset/score存在性不泄露；
- handler不直接拼跨表事务，只调用窄 service interface。

**Implement**

- `server/internal/handler/skill_evolution*.go`、router wiring、OpenAPI/schema；
- current ACL/retraction recheck on every detail/evidence/mutation。

**Validate**

```bash
cd server
go test ./internal/handler -run 'Test.*SkillEvolution'
```

### Slice 6.2 — Curator Evolution Explorer

当前可复用前端区域：

```text
packages/core/evolution/
packages/views/evolution/
packages/views/evolution/components/graph-memory-cards.tsx
packages/views/evolution/components/skills/evolution-review-section.tsx
```

**Test first**

- Pattern→evidence、candidate diff、逐 assertion结果、compatibility、approval/rollback history；
- 默认列表无正文/hidden answer/thinking；detail走短期resolver capability；
- review bundle immutable hash、risk acknowledgement、scope审批、rollback confirmation；
- legacy/flag-off workspace不显示或不可操作。

**Implement**

- core types/API/query keys；Evolution Explorer route/components；locale/a11y/loading/error/empty states。
- React Query 管服务端状态；客户端只存筛选/选中/弹窗。

**Validate**

```bash
pnpm --filter @multica/core test
pnpm --filter @multica/views test
pnpm typecheck
```

### Slice 6.3 — Metrics、alerts 与 runbooks

**Test first**

- telemetry无 Workbook值、thinking、hidden answer、完整diff/tool output；
- scope/retraction、contamination、越权、异常promotion、CAS conflict、queue lag、materialization lag、rollback、negative transfer告警分类；
-普通rejection不告警风暴。

**Implement**

- structured metrics/events；DLQ/reconciliation dashboards；owner通知；runbooks：fence、rollback、provider lag、retention failure、restore/re-fence。

## 12. Phase 7 — Migration、shadow 与 rollout

### Slice 7.1 — Legacy Skill shadow backfill

**Test first**

- 现有 `skill/skill_file/agent_skill/grant` 完全保留；回填 hash/Skill Node 后 runtime输出不变。
- 标记 `legacy_provenance/evaluation_unknown`；不生成 Pattern/candidate/approval，不扩大 grant。
- checkpoint/restart/idempotent、scope/retraction正确。

**Implement**

- dry-run report → shadow backfill job → projection compare → checkpoint。
- 历史 trajectory另走显式审批 backfill；默认不执行。

### Slice 7.2 — Rollout 阶段

```text
R0  flags off，schema/contracts only
R1  Navigation v2 shadow compare
R2  Pattern shadow（无 candidate）
R3  Candidate shadow（无 evaluation/promotion）
R4  isolated Spreadsheet evaluation
R5  curator review only
R6  single-agent canary
R7  channel canary
R8  workspace promotion
```

每阶段独立退出条件和 kill switch。立即停止扩量：

- scope leak、validation contamination、未授权写入/promotion、retraction fail-open、非法 output path 任一非零；
-旧 Skill投递受影响；
- CAS/outbox无法证明 exactly-once effect；
- rollback/provider convergence不可观测；
-跨模型负迁移无法被 compatibility gate拦截；
- thinking/hidden oracle进入 evolution/export/telemetry。

### Slice 7.3 — Backup/restore 演练

- 备份 ledger/artifact metadata/projection；
- restore环境默认不可执行；
-重放 deletion/retraction，reconcile ACL/grant/hash/source guard；
-证明不复活已删除正文、旧 grant 或 fenced artifact；
-证明 accepted-safe artifact在 Graph index不可用时仍可投递。

## 13. 全局验证矩阵

### Server/DB

```bash
make sqlc
make migrate-up
make migrate-down
make migrate-up
cd server
go test ./internal/memorygraph ./internal/skillevolution/... ./internal/service ./internal/handler ./internal/scheduler ./internal/daemon/...
```

### Frontend

```bash
pnpm --filter @multica/core test
pnpm --filter @multica/views test
pnpm typecheck
pnpm lint
```

### Repository gate

```bash
make test
make check
# 或仓库当前推荐：
ENV_FILE=.env.worktree bash scripts/check.sh
```

若全量验证因环境/时间不可执行，PR必须记录未执行项、原因、等价定向检查和后续 owner；不得用未验证状态扩大 rollout。

## 14. Requirement → Slice 追踪

| Spec §12 | 主要 Slice |
|---|---|
| 12.1 scope/flags/Spreadsheet | 0.2、4.x、7.2 |
| 12.2 trajectory/thinking | 2.2、2.3 |
| 12.3 planes/authority | G1、1.3、2.1 |
| 12.4 models/states | 0.1、2.1、4.2、5.x |
| 12.5 Pattern/navigation | 3.1、3.2 |
| 12.6 Orchestrator/CAS | 2.1、3.3、5.2 |
| 12.7 roles/security | 3.2、5.3、6.1 |
| 12.8 Spreadsheet/gate | 4.1–4.3 |
| 12.9 promotion/rollback | 5.3、5.4、7.2 |
| 12.10 transfer | 4.3 |
| 12.11 retention/restore | 2.3、7.3 |
| 12.12 migration | 1.1、7.1 |
| 12.13 operations | 6.3、7.2 |
| 12.14 acceptance | 全 Phase E2E |

## 15. 回滚策略

按影响从轻到重：

1. 关闭 promotion/candidate/evaluation/Pattern writer flags；
2. 停止 scheduler/model call/outbox consumer，保留 ledger；
3. 关闭 evolution reader/UI，保留 Navigation v2；
4. 关闭 Navigation v2 reader，回到 v1；
5. CAS 回退 affected Skill 到上一 accepted-safe artifact并等待 provider convergence；
6. 清理可重建 Graph/index projection；
7. 仅在未产生生产审计数据且验证安全时执行 schema down。

禁止通过删除 candidate/evaluation/approval ledger、恢复旧 grant、忽略 source fence或启用临时未验证 Skill完成回滚。

## 16. Definition of Done

只有以下全部成立，首个 milestone 才完成：

1. v1语义不变；Navigation与Evolution独立开关/回滚。
2. 七类 AtomKind、NodeRole/Pattern、Graph/Atom/Skill eligible-before-rank 与分层Explore可用。
3. Observable trajectory不含 thinking/hidden oracle；retention/backfill/删除有实际覆盖测试。
4. Pattern正反evidence、去重/冲突/retraction正确且不泄露。
5. 单 Skill candidate、immutable manifest/evaluation、最终activation CAS和outbox通过并发/fault矩阵。
6. Spreadsheet evaluator检测公式、类型、格式/结构、collateral mutation、corruption和output path。
7. 普通 Agent、Maintainer、Proposer、Evaluator、Approver权限不可互换；validation contamination=0。
8. 人工 canary/promotion、target matrix、provider materialization、fence/rollback可审计并收敛。
9. Legacy Skill不丢grant/binding，不伪造provenance/evaluation。
10. E2E 完成：

```text
eligible Spreadsheet trajectory
→ Pattern
→ atomic candidate
→ isolated assertion evaluation
→ human canary approval
→ provider materialization
→ monitored fence/rollback
```

11. Pilot owner与Graph、Skill Platform、Security/Data Governance、Spreadsheet product owner完成签字。
12. 未实现任何首版非目标：在线自改、跨Workspace聚合、宏/外部连接/任意网络、全模型默认兼容、无人审批promotion、hidden reasoning演化。
