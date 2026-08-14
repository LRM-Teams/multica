# 罗纳尔多分层调研系统实施计划

状态：计划冻结；仅文档，未授权修改运行时代码

日期：2026-08-14

产品语义以 [`../specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](../specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md) 为准，机器载荷以 [`../../research-run-v6-contract.md`](../../research-run-v6-contract.md) 为准，持久化以 [`../../research-run-v6-storage-contract.md`](../../research-run-v6-storage-contract.md) 为准，HTTP/Realtime/Report origin 以 [`../../research-run-v6-http-contract.md`](../../research-run-v6-http-contract.md) 为准。本计划只规定如何分批实现和验收，不增加产品规则。

## 1. 开工条件

当前不能直接开始实现。文档冻结时的仓库状态是：

- 工作树位于 `dev@d460f3ee0b91`，`origin/dev@d0db8c57fc31` 已领先 19 个提交。
- 工作树存在未完成的 merge/rebase，以及 `researchrun` 和旧实施计划中的未解决冲突。
- `origin/dev` 已出现 386 号 migration，并有两个 385 号 migration 文件；V6 migration 不得沿用本文档阶段曾估算的固定编号。

首个实施分支必须先完成以下只读/版本控制检查，且不得用 V6 工作覆盖冲突中的用户改动：

1. 确认冲突所有者，完成或终止现有 merge/rebase。
2. 更新到实施当日的 `origin/dev`，记录最终 base commit。
3. 审计 migration 编号、`research_*` 表、V6 半成品和首页规格的最新变化。
4. 对本规格、目标 Schema 和最新代码做一次差异评审；只允许因代码路径变化调整实施文件名，不允许静默改变产品语义。
5. 给每个实施 Slice 建立独立提交或 PR，V6 在最后启用前始终保持 unsupported。

## 2. 实施边界

### 2.1 必须保留

- V1–V5 decoder、Prompt hash、result golden、既有 Run 和旧 Markdown Report 阅读路径。
- React Query 管理服务端状态；Zustand 只保存选择、viewport、筛选、弹窗等客户端状态。
- `packages/core` 不依赖 `react-dom`、`localStorage` 或 `process.env`。
- `packages/ui` 不依赖 `@multica/core`；`packages/views` 不依赖 `next/*` 或 `react-router-dom`。
- Handler 只做认证、限流、严格解码和 HTTP 映射；canonical transaction 不进入 Handler。
- Agent、Inbox、对象存储、模型调用是 Adapter Seam；研究领域规则留在 `server/internal/researchrun`。

### 2.2 禁止顺手实施

- 不迁移 V1–V5 Run 到 V6。
- 不让旧 `research_fleet` 成为 V6 团队事实来源。
- 不在客户端推断 absorption、tier、Frontier、terminal cascade 或报告输入。
- 不把 `research_graph_node`、视觉 cluster、collapsed path 或密度聚合当 canonical 事实。
- 不在行为落地前修改 builtin Research Skill，避免 Agent 学到尚不可用的命令。
- 不在激活前把默认 orchestrator 从 V5 改为 V6。

## 3. 目标 Module 与依赖方向

`researchrun.Engine` 继续是外部 Deep Module。建议把现有过宽 `ResearchRun` Interface 拆成由消费者定义的窄 Interface，但所有实现仍由同一个 Engine 组合：

| 消费者 | Interface | 允许的方法 |
| --- | --- | --- |
| 用户 HTTP | `ResearchRunControl` | create/read/message、pause/resume/cancel、replace Director |
| Agent HTTP | `ResearchRunSubmission` | fetch Manifest、submit typed envelope、idempotent replay |
| scheduler | `ResearchRunReconciler` | reconcile due Work Items/outbox/Director cycles |
| Projection HTTP | `ResearchRunProjection` | snapshot、slice、delta、detail、report metadata |
| Report origin | `ResearchReportDocumentReader` | 按 immutable report revision 读取已发布 package |

内部依赖固定为：

```text
HTTP / scheduler
    -> researchrun application Interface
        -> Director / Context / Team / Work Item / Knowledge Graph /
           Discussion / Steering / Report / Projection Modules
            -> PostgreSQL transaction + Artifact Passport + Run Event + outbox
            -> Agent / Inbox / Model / Object Storage Adapters
```

Handler 不得直接查询 V6 canonical 子表。现有 `server/internal/handler/research_v6_projection*.go` 中的 SQL 和图拼装应迁到 Projection Module；Handler 只保留参数校验和响应映射。

## 4. HTTP 和运行时表面

既有用户消息入口继续作为唯一 Steering 输入；前端不新增 merge/stop/replan 业务命令。V6 目标路由固定如下，具体 workspace 鉴权沿用现有 middleware：

| Actor | Method/Path | 作用 |
| --- | --- | --- |
| 用户 | `POST /api/research/sessions` | 创建 V6 Run 时显式提供 `director_agent_id`，事务中只创建该 Director membership |
| 用户 | `POST /api/research/sessions/{id}/messages` | 自然语言消息，可带 `selected_research_refs[]` |
| 用户 | `PUT /api/research/sessions/{id}/director` | 用户以 expected state version 显式替换或恢复 Director |
| 用户 | `GET /api/research/v6/runs/{runId}/projection/snapshot` | pinned Snapshot 首屏/分页 |
| 用户 | `GET /api/research/v6/runs/{runId}/projection/slice` | viewport/展开 Slice |
| 用户 | `GET /api/research/v6/runs/{runId}/projection/deltas` | 连续 Delta |
| 用户 | `POST /api/research/v6/runs/{runId}/projection/resume` | 恢复或要求 resync |
| 用户 | `GET /api/research/v6/runs/{runId}/projection/nodes/{nodeId}` | 检查面板详情 |
| 用户 | `GET /api/research/v6/runs/{runId}/reports` | Goal 的 Report revision metadata |
| 用户 | `GET /api/research/v6/runs/{runId}/reports/{reportId}` | 报告详情、审阅、input refs 和 sandbox URL |
| Agent | `GET /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/manifest` | 中断后重新取得该 Attempt 的冻结 Manifest |
| Director | `GET /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/director-brief` | 分页读取同一冻结 Brief |
| Director | `POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/director-brief-acks` | 处理页面后幂等记录 page hash/watermark |
| Agent | `GET /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/catalog` | 遍历 Manifest 授权的同级目录和相关高层候选摘要 |
| Agent | `POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/catalog-acks` | Agent 处理页面后幂等推进 reviewed watermark |
| Agent | `POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/report-uploads` | 为 Report Attempt 创建 path/hash/size/MIME 绑定的上传 capability |
| Agent | `POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/report-uploads/{uploadId}/complete` | 服务端重新读取并确认上传资源 |
| Agent | `POST /api/agent/research/sessions/{id}/work-items/{workItemId}/attempts/{attemptId}/submission` | 提交九类合同中的 Agent/Director envelope |

Submission endpoint 先根据 Work Item 的 `expected_result_schema` 选择根 Schema，再执行 task-specific second validator。Principal 必须同时匹配 workspace、Run team membership、Work Item、Attempt、Inbox delivery 和 Manifest hash；只靠 body 中的 Agent ID 不授权。

独立报告 origin 使用不可变 canonical path，例如 `https://<report-origin>/research/{reportId}/{packageHash}/index.html`；实际查看 URL 附带短期签名 capability，ID/hash 本身不授权。该 origin：

- 不接收主应用 Cookie；不启用 credentialed CORS。
- 验证签名、到期时间、Report revision 和允许的查看模式；无效 capability fail closed。
- 只服务 published revision 和经 Director 审阅所需的短期 draft capability。
- 每次响应都返回固定 CSP、`X-Content-Type-Options: nosniff`、严格 MIME 和 immutable ETag。
- 开发环境使用独立 host/origin，不以主应用同源路径替代安全测试。

## 5. Slice 0：基线、合同替换和永久关闭门

目标：让代码只认识新 Ronaldo V6 合同，但仍不能创建 V6 Run。

修改：

- `docs/contracts/research-run-v6.schema.json`：用已冻结的 `research-run-v6-director.schema.json` 原字节替换；目标文件的 `$id` 已是正式 V6 地址，替换后 hash 必须保持不变。
- `server/internal/researchrun/v6_contract_test.go`：更新 hash、九个 envelope 清单、strict unknown-field 和 unsupported 断言。
- `server/internal/researchrun/planner_v6.go`、`result_v6_plan.go`、`result_v6_plan_materialize.go`、`v6_integration_result.go` 及其测试：删除旧 V6 decoder/materializer 或明确隔离为不可达历史测试，不能与新 envelope 混用。
- `server/internal/researchrun/v6_activation.go`：所有新门未提供前继续 fail closed。
- 新增 `server/internal/researchrun/contract_v6.go`、`contract_v6_test.go`：统一严格解码、内容规范化、hash 和二次 Schema 分派。
- 新增九类正例 fixture 和每类 unknown/missing/null/trailing/oversize/hash-mismatch 负例；文档 fixture 作为 golden 输入，不复制第二份含义不同的数据。

完成条件：

- V1–V5 golden 完全不变。
- 新 Schema hash 与 `docs/research-run-v6-contract.md` 一致。
- `ensureSupportedOrchestratorVersion("research-run-v6")` 仍返回 unsupported。
- 旧 V6 固定 Plan/Reporter/Validator 假设不再被任何新 V6 decoder 引用。

## 6. Slice 1：持久化骨架

目标：先建立可恢复事实，不接模型和 UI。

文件：

- `server/migrations/<N+0..N+8>_research_v6_*.up.sql/.down.sql`：按 storage contract 的九个 migration slice 创建。
- `server/internal/migrations/research_v6_*_schema_test.go`：约束、索引、append-only、scoped FK、up/down/up 和重复编号检查。
- `server/pkg/db/queries/research.sql`：只加入单表或稳定读模型查询；需要锁顺序和多表原子写的 SQL 保持在 `researchrun` transaction Implementation 附近。
- `server/pkg/db/generated/*`：只由项目 sqlc 命令生成，不手改。
- `server/internal/researchrun/types.go`：新增领域 ID、enum 和只读 DTO；V1–V5 类型不加可选 V6 字段。

先实现数据库能机械保证的规则：

- active Director、active team membership、active Steward、同 Branch current XXL 唯一。
- single-successor absorption、Discussion frozen-input 唯一、Report revision/hash 唯一。
- scoped composite FK、状态 CHECK、append-only trigger、50 人上限。
- Run Event sequence、idempotency key 和 outbox intent 与 canonical row 同事务。

完成条件：fresh/up/down/up 通过；旧应用镜像可以在新 Schema 上运行；migration 不创建 V6 Run、不伪造历史 Director/Steward/Absorption。

## 7. Slice 2：通用 Work Item 与恢复

目标：Research、Match、Discussion、Integration、Director、Report、Review 共用一个 lease/retry 面。

新增：

- `server/internal/researchrun/work_item.go`
- `server/internal/researchrun/postgres_work_item.go`
- `server/internal/researchrun/work_manifest.go`
- `server/internal/researchrun/postgres_work_manifest.go`
- `server/internal/researchrun/work_catalog_v6.go`
- `server/internal/researchrun/postgres_work_catalog_v6.go`
- `server/internal/researchrun/work_item_recovery_test.go`
- `server/internal/researchrun/work_item_fault_injection_test.go`

修改：

- `server/internal/researchrun/api.go`：增加窄 Submission/Reconciler Interface。
- `server/internal/researchrun/execution.go`、`postgres_store.go` 或实施基线中的等价文件：V6 转到新 Work Item，V1–V5 保留旧 Task Attempt。
- `server/internal/handler/research_run_http.go`、`server/cmd/server/router.go`：接入 Manifest 和通用 Submission endpoint。
- `server/internal/handler/research_dispatch.go`：Inbox context 绑定 `run_id/work_item_id/attempt_id/manifest_id/manifest_hash`。
- `server/internal/scheduler/jobs_research_run.go`：reconcile 到期 lease、未知提交结果和 outbox。

状态转换必须按 expected version/CAS 执行。commit 后响应丢失时，同一 `client_request_id + content_hash` 返回原结果；同 request ID 不同 hash 返回 conflict。lease 转移不复制 accepted Result。

完成条件：七类 Work Item 可在服务重启和 Agent 中断后恢复；同级/高层候选目录分页和 reviewed watermark 可恢复；before-commit、after-commit-unknown、重复同载荷和重复异载荷矩阵通过。

## 8. Slice 3：动态 Team、Director 和 Context Compiler

目标：Run 从“只有用户选定的罗纳尔多”开始，罗纳尔多能基于有限 Brief 安排全部后续工作。

新增：

- `server/internal/researchrun/director.go`
- `server/internal/researchrun/postgres_director.go`
- `server/internal/researchrun/director_brief.go`
- `server/internal/researchrun/context_compiler.go`
- `server/internal/researchrun/team_v6.go`
- `server/internal/researchrun/postgres_team_v6.go`
- `server/internal/researchrun/director_prompt_v6.go`
- 对应 unit/integration/fault-injection tests

外部 Seam：

- `AgentLifecycleAdapter`：创建、检查、归档 Agent，不接受领域状态写入。
- `InboxDispatchAdapter`：只投递已提交的 Work Item/Manifest。
- `DirectorModelAdapter`：输入冻结 Brief，输出 strict Action Proposal。

规则：

- 0–19 active membership 时允许罗纳尔多按判断创建；20–49 必须在 Proposal 中提供额外理由；第 50 个后数据库和服务双重拒绝。
- 不创建固定角色；capability、模型、提示和工具由 action payload 指定。
- Brief 分 Research/Control 两部分，正文只来自 Branch Frontier 的 bounded summary；terminal 只进聚合，absorbed child full content 不进 Brief。
- Overview、Page、watermark 和 changed-page 审阅进度持久化；模型会话轮换不改变输入事实。
- Director 不可用进入 `awaiting_director` 并通知用户，不自动替换。

完成条件：0→N 动态团队、20/50 边界、Director quota/offline、会话重置、数百 Branch 分页和 context byte/token bound 均有回归。

## 9. Slice 4：Result、Tier、Absorption、Match 与 Discussion

目标：完成从 Result S 到 XXL 的 canonical 压缩路径。

新增：

- `server/internal/researchrun/result_node_v6.go`
- `server/internal/researchrun/knowledge_graph_v6.go`
- `server/internal/researchrun/postgres_knowledge_graph_v6.go`
- `server/internal/researchrun/match_v6.go`
- `server/internal/researchrun/discussion_v6.go`
- `server/internal/researchrun/postgres_discussion_v6.go`
- `server/internal/researchrun/integration_v6.go`
- `server/internal/researchrun/postgres_integration_v6.go`
- `server/internal/researchrun/tier_property_test.go`
- `server/internal/researchrun/absorption_race_integration_test.go`

接受顺序：严格 envelope → principal/Manifest → Artifact Passport access → evidence ref → state/version → canonical Result + Version + Branch binding + Event。成功前不得把 Work Attempt 变成 succeeded。

Integration 顺序：锁 Run → 按 UUID 锁 inputs → 校验 fresh/current/hash/Goal/Branch → 校验同级 promotion 或低级 assimilation → 写 successor/version/derivation → 占用每个 direct successor slot → 更新 Frontier/Steward → Event/outbox → commit。

Discussion 只保存结构化 turns/votes/decision；普通 Channel 消息可以显示通知，但不作为事实。全部接受才 integrate，全部拒绝回到 Match Decision，mixed/uncertain 交给 Director，证据冲突创建 Dispute。

完成条件：

- promotion 至少两个 fresh 同级输入且不能跳级。
- assimilation 保持目标 tier；XXL successor 仍为 XXL。
- 同一输入并发吸收只有一个成功，失败者拿到 successor ref。
- 新 Agent 被接受为最后加入者时取得 Steward；离线后由 Director 指派替代。
- absorbed 节点从默认 Frontier 消失且永不自动恢复。

## 10. Slice 5：Steering、Goal revision 和失效传播

目标：每条用户消息都产生可审计判断，包括 no-op。

新增：

- `server/internal/researchrun/steering_v6.go`
- `server/internal/researchrun/postgres_steering_v6.go`
- `server/internal/researchrun/terminal_propagation_v6.go`
- `server/internal/researchrun/steering_v6_integration_test.go`

修改：

- `server/internal/handler/research_ops.go` 的消息创建路径：消息先持久化，再通过 outbox 触发 Steering Work Item。
- `server/internal/researchrun/api.go`：用户面只暴露自然语言消息/替换 Director，不暴露语义命令。
- 前端 composer 请求类型：增加可删除的 `selected_research_refs[]`，不增加 stop/merge/replan 枚举。

每次 Assessment 冻结 message ID、Goal version 和 event watermark。Goal 修改产生新 revision；被影响的 Discussion/Work 按 CAS 变 stale/cancelled。高层 terminal 向 active descendants 传播取消；低层未吸收 terminal 不改高层；已吸收输入被否定时创建 challenge/review，不恢复旧输入。

完成条件：每种补充、否定、颠覆、继续研究和 no-op 都有持久 Assessment；重复处理不产生第二次 cascade。

## 11. Slice 6：HTML Report package 与独立 origin

目标：保存、验证、审阅和发布真正的静态 HTML 报告。

新增：

- `server/internal/researchrun/report_v6.go`
- `server/internal/researchrun/report_package_v6.go`
- `server/internal/researchrun/postgres_report_v6.go`
- `server/internal/researchrun/report_sandbox_policy.go`
- `server/internal/researchrun/report_v6_security_test.go`
- `server/internal/handler/research_report_v6.go`
- `server/internal/handler/research_report_document.go`

对象存储 Seam：定义窄 `ReportPackageStorage`，只允许 immutable put、verified read/head；用 `server/internal/storage` 的 Local/S3 实现做 Adapter。不能在领域 Module 依赖 S3 类型，也不能复用可覆盖 key。

渲染 Seam：定义 `ReportRenderAdapter`，在无主应用凭据、无网络、有限 CPU/内存/时间的浏览器进程中加载同一 compiled HTML，输出截图 Artifact、console/布局/超时诊断和实际 CSP 结果。Ronaldo 的 Review Work Item Manifest 同时包含 plain text、outline、引用、输入摘要、截图和诊断；仅审查 Agent 自报的 HTML 文本不满足验收。

建议 package 接受方式：Agent 先通过 Work Item 绑定的受限 upload session 上传 package 资源，Submission 只引用 upload resource ID、规范 path/hash/size/MIME 和 manifest；服务端从 upload session 解析 object key/generation，重新读取并验证 bytes，编译成单一自包含 HTML 后才接受 draft。禁止信任 Agent 自报 hash 或任意 object key。

验证器检查：

- 单一 HTML 文档、UTF-8、字节/DOM/script/font/image 上限。
- 所有资源为 package 内 data/blob 表达，不含外部 URL、`srcdoc` 嵌套逃逸、base URL、form、object/embed、worker 和网络 API。
- CSP 由服务端生成，script/style hash 与实际 inline bytes 一致；禁止 `unsafe-eval` 和 `unsafe-inline`。
- 编译器拒绝 inline event handler、style attribute、路径穿越、重复/大小写冲突 path 和未被 manifest 覆盖的字节。
- exact Goal/input snapshot、引用、plain-text fallback、outline 和 package hash 可重现。

Director review 只能产生 `needs_research`、`needs_revision`、`technical_failure` 或 `published`。publish 使用 expected report version CAS；已发布 revision 永不改 bytes。

完成条件：Web/Electron/直接打开三条路径都无法访问主应用 origin、Cookie/storage/API、parent DOM、network、popup/download/top navigation/worker；对象丢失只标记 technical unavailable，不能在旧 revision 下重生成。

## 12. Slice 7：服务端 Projection

目标：从 canonical 状态重建默认图，不把全文塞进 Snapshot。

新增或迁移到：

- `server/internal/researchrun/projection_v6.go`
- `server/internal/researchrun/postgres_projection_v6.go`
- `server/internal/researchrun/projection_v6_snapshot.go`
- `server/internal/researchrun/projection_v6_slice.go`
- `server/internal/researchrun/projection_v6_delta.go`
- `server/internal/researchrun/projection_v6_detail.go`
- `server/internal/researchrun/projection_v6_rebuild_test.go`

修改：

- `server/internal/handler/research_v6_projection.go`
- `server/internal/handler/research_v6_snapshot_page.go`
- `server/internal/handler/research_v6_slice.go`
- `server/internal/handler/research_v6_delta.go`
- `server/internal/handler/research_v6_node_detail.go`
- `server/internal/handler/research_v6_projection_detail.go`

默认 Snapshot 只包含 Goal、Branch 顶部 M+、高层 terminal、全部 unabsorbed/terminal Work/Result S、bounded summaries 和 report metadata。absorbed 节点由 one-layer Slice 展开。`collapsed_path`、density cluster 和屏幕 LOD 只存在 Projection。

Projection stable ID 必须来自 `(run_id, entity_kind, entity_id, revision where needed)`，不能来自数组位置或布局。Delta 只由 committed Run Event 生成；sequence 缺口、hash 不符或 retention 超窗要求 resync。

完成条件：数据库重建得到相同 hash；分页不重复/不遗漏；相同 canonical snapshot 顺序稳定；1k/10k/50k S 的 payload、查询计划和内存指标有基准。

## 13. Slice 8：Core 客户端合同

目标：前端只消费目标 Projection，不混用旧 V6 类型。

修改：

- `packages/core/types/research-v6.ts`
- `packages/core/research-v6/schemas.ts`
- `packages/core/research-v6/registry.ts`
- `packages/core/adapters/v6.ts`
- `packages/core/api/research-v6.ts`
- `packages/core/hooks/research-v6/**`
- `packages/core/hooks/research-v6-live/**`
- `packages/core/hooks/research-v6-slice/**`

新增：

- report metadata/query hooks。
- node-detail and one-layer expansion query hooks。
- selected node ref 的 Zustand client store；不得复制服务端 node lifecycle。

React Query cache key 必须包含 workspace/run/snapshot/viewport or expansion identity。WS 事件只 invalidate/推进 Projection client，不直接把 canonical entity 写进 Zustand。未知 enum 降级为 generic visual + diagnostic，不导致页面崩溃。

完成条件：schema fixture、snapshot/delta gap、workspace/run identity、out-of-order buffer、resync、unknown-kind 和 SSR 环境测试通过。

## 14. Slice 9：星图、检查面板和聊天引用

实现前先按 `impeccable` 要求读取 craft floor；UI 完成后运行 detector 和真实浏览器 QA。

主要修改：

- `packages/views/research/components/research-session-page.tsx`
- `packages/views/research/components/research-constellation-workspace.tsx`
- `packages/views/research/components/research-node-detail.tsx`
- `packages/views/research/graph-model/{types,model,positioner}.ts`
- `packages/views/research/lib/build-d5-session-canvas.ts`
- `packages/views/research/lib/node-visuals.ts`
- `packages/views/research/lib/canvas-keyboard-nav.ts`
- `packages/views/research/components/research-d5-layout.css`
- `packages/views/research/components/research-chat-drawer.tsx` 及 composer adapter
- `packages/views/locales/{zh-CN,en}/research.json`

建议新增：

- `research-v6-s-node.tsx`：颜色、轮廓、中心纹和 motion 的单一映射。
- `research-v6-semantic-label.tsx`：实际屏幕尺寸/碰撞/选择驱动的标签。
- `research-v6-density-layer.tsx`：远景视觉聚合，保留真实 node identity。
- `research-v6-expansion-layer.tsx`：一次只展开一层 derivation。
- `research-selected-ref-chip.tsx`：聊天框节点引用。

交互断言：

- S 本体无文字；原因只在点击后的右侧检查面板显示。
- running 有呼吸效果，reduced motion 为静态双环；语义不能只靠颜色或动效。
- pointer hit area ≥24×24，touch ≥40×40；所有节点可键盘访问并有可访问名称。
- 默认隐藏 S 连线，选中后显示；吸收动画失败不改变 canonical state。
- M+ 标签按屏幕空间出现，点击后聚焦；Goal fit 全图；展开一次只请求一层。
- 首页和详情按 orchestrator version 分流；V1–V5 仍展示旧 Fleet/阶段，V6 不显示固定角色和预计人数。

完成条件：组件/a11y/reduced-motion/semantic zoom/selection/large-canvas tests 通过；Web 和 Desktop 使用同一 `packages/views` Implementation。

## 15. Slice 10：Report modal

修改：

- `packages/views/research/components/research-node-report-modal.tsx`
- `packages/views/research/report/**`：保留旧 Markdown reader，增加 V6 HTML metadata/history shell。
- `packages/views/research/components/research-session-goal-card.tsx` 或 Goal detail 等价入口。

新 modal 只把 immutable sandbox URL 交给 `<iframe sandbox="allow-scripts">`。不得使用 `allow-same-origin`、WebView、preload 或通用 `postMessage` bridge。关闭时卸载 iframe；超时/高负载由 watchdog 卸载并展示可重试错误。Report 不是 graph node，Goal 只显示交付 metadata/history。

完成条件：1440×900、1920×1080、窄屏、长报告、目录、SVG、动画、旧 Markdown fallback 和关闭释放均有自动化/截图证据。

## 16. Slice 11：激活、运维和文档同步

最后一个 Slice 才允许修改 supported/default version。修改：

- `server/internal/researchrun/v6_activation.go` 及 audit tests。
- Run create/version selection、首页兼容 projection、指标和告警。
- `server/internal/service/builtin_skills/multica-research-fleet/SKILL.md`。
- `server/internal/service/builtin_skills/multica-research-fleet/references/research-fleet-source-map.md`。
- `docs/engineering-principles.md`：把已落地规则从 `仅文档` 改为 `可执行`，逐项列出装置。
- 本计划 verification log：只记录实际执行结果。

`AssessV6Activation` 至少检查：migration、Schema hash、V1–V5 golden、九 envelope、恢复矩阵、single-successor race、Director context bound、50 人上限、Projection rebuild/大图、Report sandbox/Web/Electron、`RESEARCH_REPORT_ORIGIN` 与主应用不同源、builtin Skill/source map 与默认版本回滚。

发布顺序：

1. 迁移和 V6-disabled server。
2. 能识别 V6 但默认 V5 的 Web/Desktop。
3. 内部/fixture V6 Run。
4. activation audit。
5. 只对新 Run 打开 V6；旧 Run 固定原版本。

回滚只关闭新建 V6。已创建 V6 Run 进入 paused/maintenance，不能用 V5 decoder 读取，也不能删掉已写事实。

## 17. 验收矩阵

| 面 | 必须证明 | 失败即阻止激活 |
| --- | --- | --- |
| Contract | 九 envelope strict、二次 validator、hash 固定 | 是 |
| Compatibility | V1–V5 golden/hash/旧 Report 不变 | 是 |
| Database | scoped FK、append-only、single successor、up/down/up | 是 |
| Recovery | before commit、unknown commit、replay、lease takeover | 是 |
| Director | Brief 上下文有界、分页全覆盖、会话轮换等价 | 是 |
| Team | 初始仅 Director、20 门槛、50 上限、归档/接替 | 是 |
| Graph | promotion/assimilation、XXL、跨 Branch 去重、terminal 排除 | 是 |
| Steering | 每条消息有 Assessment、cascade 幂等、challenge 不复活 | 是 |
| Projection | rebuild hash、连续 Delta、Slice、50k S 基准 | 是 |
| UI | 全部 unabsorbed S、状态编码、键盘、reduced motion | 是 |
| Report | package hash、CSP、独立 origin、攻击负例、旧版可复现 | 是 |
| Operations | Director quota 通知、对象丢失、暂停/恢复/回滚 | 是 |

实现结束至少执行：

```bash
make test
pnpm test
pnpm typecheck
pnpm lint
pnpm react:doctor
make check
```

数据库集成测试必须使用隔离数据库并记录 migration base。前端必须在真实 Web 和 Desktop 表面验证；fixture 截图不能替代真实 API/WS 路径。

## 18. 开发交接顺序

后端开发先拿以下四份文件：

1. 产品规格：`docs/superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`
2. 机器合同：`docs/research-run-v6-contract.md` 和 `docs/contracts/research-run-v6-director.schema.json`
3. 存储合同：`docs/research-run-v6-storage-contract.md`
4. 传输合同：`docs/research-run-v6-http-contract.md`
5. 本实施计划

只给主规格不足以开工：它没有固定 Schema hash、字段级数据库约束、当前冲突基线、文件所有权、Slice 退出条件和激活顺序。开发人员应先完成 Slice 0 的基线审计，再按 Slice 提交；任何需要改变产品语义的问题必须回写主规格或 ADR，不能只在代码评审评论中决定。
