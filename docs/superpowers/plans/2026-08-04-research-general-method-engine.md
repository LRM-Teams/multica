# 通用调研方法引擎实施记录

状态：已实现，待提交 PR

日期：2026-08-04

## 目标

让 Research Run 的规划结果成为后续检索、精读、核验、反证、综合和审查共同遵守的持久方法工件；方法契约必须适用于行业、竞品、技术选型、政策、市场、尽调、事实核查和学术问题，不把论文写作规范当作默认流程。

## 已完成：参考实现核对

- [x] 完整克隆 `Imbad0202/academic-research-skills`，核对提交 `112a869f5205943728791d84602fe43fd3e1438b`。
- [x] 盘点仓库组成：提示词与协议、JSON Schema、质量定义、评测、失败路径、脚本和示例；该仓库不是可直接替换 Multica Research Run 的运行时。
- [x] 阅读其总体架构、模式注册、深度调研与学术流水线入口。
- [x] 阅读研究问题、研究架构、综合、来源核验、反方审查和总编辑角色契约。
- [x] 阅读方法模式、来源质量、领域证据画像、跨角色质量定义、状态机、Claim 核验和失败恢复协议。

## 采用的机制

1. **持久方法蓝图**：规划不只产生任务列表，还明确决策问题、方法理由、分析方法、证据要求、纳入/排除标准、来源策略、反证策略、停止条件、未知项和规划风险。
2. **问题决定方法**：不同目标可以选择比较、测量、机制分析、时间序列、案例、事实核查、风险分析或混合方法；不强制套用学术范式。
3. **范围继承**：子问题和后续任务读取同一目标版本、计划版本与方法蓝图，避免静默扩大范围或改变判断标准。
4. **反证前置**：规划时定义什么证据会削弱或推翻当前判断，`counter_search` 据此执行，不把反证降级为报告末尾的免责声明。
5. **停止条件可审计**：停止由问题覆盖、证据充分度、反证处理和边际信息增益共同决定，不按来源数量或篇幅机械完成。
6. **失败后从工件恢复**：重试或重新规划读取已接受的方法版本和证据账本，不依赖聊天历史重建意图。

## 补充参考图的取舍

两张参考图把协作系统拆成组织/任务图、运行图、知识图、能力图，以及“计划—执行—观察—评价—改进”反馈循环。映射到 Research Run 后采用以下关系：

1. `research_task`、`research_task_dependency`、Attempt 构成执行图；它回答谁按什么依赖做什么，以及运行状态是什么。
2. Question、Source Snapshot、Observation、Claim、Evidence Link 构成证据图；它回答结论来自哪里，支持和反证关系是什么。
3. `research_decision`、质量审查和引用审查构成评价反馈图；评价结果只能触发重新综合或重新规划，不能直接伪造证据或改写完成状态。
4. Fleet role 与 Agent 状态是能力路由的现有 Adapter；Research Run 只声明所需能力，不再实现一套 Agent 身份、权限或技能系统。
5. Research Method 用 goal/plan 版本连接上述三张图。重新规划产生新版本，旧任务、方法、证据和报告保留为审计历史。

不采用图中的自修改 Prompt、自动升级 Agent、通用组织权限图、CI/CD 健康监控和另建统一图数据库。这些分别属于 Agent 自进化、组织权限和运维 Module；放进调研后台会扩大 Interface、复制现有能力，并让研究状态与平台状态互相污染。

## 不采用或改写的机制

- FINER、ontology、epistemology、IRB、PRISMA、CONSORT、STROBE、APA、期刊投稿和 DOI/arXiv 规则只适用于部分学术任务，不进入通用默认协议。
- 固定论文阶段和每阶段人工批准会让普通业务调研停滞，不作为所有任务的状态机。
- 固定“论文 > 官方 > 新闻 > 社区”来源等级无法表达来源对具体 Claim 的适配性；后续改成证据用途与 Claim 风险共同决定要求。
- 大量单用途角色增加浅 Interface 和交接损耗；Multica 保留 ResearchRun 深 Module，用持久工件连接现有 Agent 能力。

## 已确认的现有缺陷

`PlanProposal` 已包含 `inclusion_criteria`、`exclusion_criteria`、`source_strategy`、`uncertainties` 和 `planning_risks`，但 `AcceptResult` 接受计划时只持久化问题和任务。这些字段只留在原始 Attempt Result 中，`RunSnapshot` 不返回，后续任务 Prompt 也不包含。因此规划 Implementation 的方法判断没有穿过任务执行 Seam。

## 实施步骤

- [x] 新增 `research-run-v3`，保留 v1/v2 的结果、Prompt 和质量行为。
- [x] 定义并验证通用 `ResearchMethod`；v3 计划缺少任何必需方法字段时拒绝接受。
- [x] 接受计划或重新规划时，把方法作为当前 goal/plan 版本的 append-only `research_decision` 工件持久化。
- [x] `TaskContext` 加载当前方法，所有 v3 任务 Prompt 明确展示并要求继承。
- [x] v3 保留 v2 的报告结构、Claim 引用、独立审查和作者归属约束。
- [x] 增加单元测试和 PostgreSQL 集成测试，覆盖版本兼容、持久化、重新规划版本和 Prompt 继承。
- [x] 更新 Research Run 权威设计文档、共享响应 Schema、v3 节点结果名称和有界监控标签。
- [ ] 提交非草稿 PR 到 `dev`。

## 验证记录

- [x] `go test ./internal/researchrun ./internal/metrics`
- [x] `@multica/core` 全量测试：117 files / 1065 tests。
- [x] `@multica/views` 全量测试：480 files / 4144 passed / 2 expected fail / 5 skipped。
- [x] `@multica/core` 与 `@multica/views` TypeScript typecheck。
- [x] 全新工作树数据库 `multica_worktree_526` 从 001 到 282 完整执行迁移。
- [x] 使用真实 PostgreSQL 执行计划持久化、Result 重放、重新规划方法版本和完整交付集成用例。
- [x] Handler 的新建 Fleet Prompt 和内置 Research Fleet Skill 用例。
- [x] `pnpm react:doctor`：扫描 2 个受影响前端文件，0 个问题。
- [x] `make check` 的 TypeScript typecheck、TypeScript 全量测试、全量 Go 测试通过。
- [ ] `make check` 的 Playwright 阶段未通过：运行中 Docker daemon 退出，PostgreSQL 连接被重置，随后登录、导航和列表用例批量超时。另有失效夹具仍写入已删除的 `agent.visibility` 字段，以及研究列表断言仍要求旧相对时间文案。这些不在真实用户执行路径，本改动未修改产品逻辑迁就它们。

## 后续候选改动

第一项 PR 提交后继续核对并修复：

1. 来源质量从固定 `source_class` 权重改为“来源对 Claim/方法的适配性 + 独立性 + 可追溯性 + 时效性”。
2. 任务图根据方法蓝图生成验收条件，阻止同义问题和重复节点。
3. 质量审查增加范围偏移、方法遵循、反证覆盖和决策可用性，不用篇幅代替深度。
