# Goal × Work Graph Runtime 实施计划

状态：已实现，等待 PR CI

日期：2026-08-06

设计依据：`docs/superpowers/specs/2026-08-06-agent-work-graph-runtime-spec.zh-CN.md`

## 目标

在不破坏现有 Adaptive Channel Goal、Issue assignment 和 Research Run 的前提下，建立一套服务端可调度的通用 Work Graph。Goal 继续表达“要达成什么、什么算成功”，Work Graph 表达“当前如何拆解和执行”，Issue/Run Node 是可审计工作单元，Agent Job/Runtime 承载实际执行。

## 已拍板边界

1. Goal 是目标契约的唯一真相源：objective、success criteria、人工范围和生命周期仍由 `channel_goal` 管理。
2. Work Graph 是执行计划的唯一真相源：节点、依赖、角色、预算、调度、图版本、产物有效性和核验由图内核管理。
3. Goal version 与 graph revision 分开；目标契约变化才增加 Goal version，仅执行方案变化只增加 graph revision。
4. 一个 Goal 可以直接由单一持久 Agent Job 完成，不强制建图；只有准入结果为 `GRAPH` 才创建 Work Graph。
5. Work Graph 同时允许 `channel_goal` 或根 `issue` 作为主 anchor，但每张图只有一个 canonical anchor。频道长期目标优先使用 Goal，独立工作优先使用根 Issue。
6. 现有 `channel_goal_subgoal` 不发展成第二套调度内核。迁移完成后，Goal 页面中的子目标应成为 Work Graph Node 的投影；过渡期不做无约束双向同步。
7. Work Graph 完成只能触发 Goal 完成候选；Goal 仍需 success criteria、Evidence、开放节点和人工/发布 Gate 全部通过才能完成。

## 交付切片

### Slice 0：准入行为和契约

- 在 Issue assignment 的实质执行前加入 Work Decomposition Gate。
- 明确 `DIRECT | GRAPH | PROPOSE_GRAPH` 三种语义，禁止简单任务建图。
- 更新内置 Issue 与 multi-agent coordination Skill 及 source map。
- 增加 Prompt 回归，覆盖普通直接执行、可分解任务、强共享上下文任务、权限边界和 planner 不重复实现委派节点。
- 在 `docs/engineering-principles.md` 登记 Goal × Work Graph 权责边界。

完成条件：现有执行路径不增加工具或模型调用；Prompt 能稳定给出建图判断原则，但不假装已有服务端图 API。

### Slice 1：最小持久化内核和原子建图

- 增加 `work_graph`、`work_graph_revision`、`work_graph_node`、`work_graph_edge` 最小 schema。
- anchor 使用 `anchor_kind + anchor_id`，并由服务端校验 workspace、Goal/Issue 身份和权限。
- 第一版每个 Run Node 必须绑定 Issue；节点创建和 Issue 创建在同一事务内完成。
- 增加原子 `issue graph create` Agent API/CLI，支持临时 ID、`depends_on`、幂等键、循环检测和整体回滚。
- 强制 `expected_graph_version`，并记录 revision reason、author 和 topology digest。
- 增加 PostgreSQL 集成测试：重复请求、部分失败、跨 workspace、环、超节点/深度预算均不得留下残图。

完成条件：一次可靠创建 2～10 个节点；同一幂等键重试返回原映射；失败零残留。

### Slice 2：ready-node 调度和 Agent Job

- 以服务端状态机计算 ready node，不让 Prompt 手动维护串行链。
- 并行节点自动 enqueue；强依赖满足后下游自动 ready。
- 节点 claim 幂等并受每图、每 Agent 和全局并发限制。
- Run Node 绑定独立 Issue Agent Job；provider session 仍只是 Job 的当前执行容器。
- 增加 Graph Delta 事件，planner 仅在失败、重规划、失效、裁决、Gate 或可交付时被唤醒。
- 取消、替代或 Goal pause/cancel 时执行可观察的 stop/checkpoint 协议，禁止孤儿 Runtime。

完成条件：并行和串行调度均由服务端推进；planner 无需轮询或手动 promote backlog。

### Slice 3：Goal 生命周期和完成 Gate

- Goal `paused/resumed/cancelled/completed` 与图运行状态按明确状态表联动。
- Goal intent/version 变化触发图影响分析和新 revision，而不是覆盖历史。
- Graph 汇总产生 Goal progress/current step/blocker 候选；写入继续服从现有 Goal CAS 和权限。
- 图可交付不直接完成 Goal；最终完成继续调用 Goal 的 criteria + evidence + open work Gate。
- Goal 卡片中的 Subgoal 逐步切换为 Work Graph Node 投影，避免两个依赖图成为真相源。

完成条件：Goal 与图不会出现一个 completed、另一个仍在运行的无解释分叉。

### Slice 4：Artifact、核验和最小重算

- 增加 Artifact revision/digest、verification attempt 和 Evidence 引用。
- 支持一个独立 verifier 及 `bounded/blind/replication/sealed` 上下文策略。
- verdict 只能是 `PASS | FAIL | BLOCKED`，并绑定不可变 Artifact revision。
- 分开 execution、validity、review 三类状态。
- 上游或 Goal 契约变化时计算影响闭包；旧 PASS 自动 stale，但不盲目全图重跑。
- 第一版由 planner 确认最小重算集合，高成本重算必须重新过预算 Gate。

完成条件：产物变化后旧 PASS 不可用于交付，历史仍可审计，未受影响节点可以复用。

### Slice 5：共享前端

- `packages/core` 增加兼容解析 schema、query/mutation 和 WS invalidation。
- `packages/views` 增加 Goal/Issue 共用的摘要卡和只读主干图；Web/Desktop 共用实现。
- 展示执行、有效性、审阅三类状态，以及 revision、Artifact、Evidence、阻塞和替代关系。
- 实时变化保持节点身份和布局稳定；第一版不提供任意拖拽编辑图。
- API 新字段全部 parse-with-fallback，并覆盖 malformed/旧服务响应。

完成条件：用户能实时理解“在做什么、为什么等待、什么失效、下一步是什么”，旧桌面客户端不会因新响应崩溃。

## 迁移策略

1. 采用 expand-first migration，只新增表和 nullable/有默认值字段，不在首轮删除 `channel_goal_subgoal`。
2. Work Graph 上线前不双写；切换投影时明确单向来源，并用对账指标观察差异。
3. 旧 Goal、旧 Issue 和没有图的 Agent Job 行为保持原样。
4. Research Run 先通过接口适配复用调度内核，稳定后再迁移存储；不同时维护两个新增 scheduler。
5. 每个切片独立可回滚，数据库迁移满足旧一版应用仍可运行。

## 测试与评测

- 准入表：问候、天气、小修复必须 DIRECT；独立前后端、迁移依赖链、多方向调研和高风险交付按契约建图。
- 后端：事务、CAS、幂等、DAG、预算、并发、恢复、取消、失效传播和完成 Gate 的单元/数据库集成测试。
- Runtime：独立 context scope、Graph Delta、Job rollover 和无兄弟私有会话回归。
- 前端：API 漂移、摘要状态、图投影、实时 invalidation、权限和可访问性测试。
- 每个切片先跑聚焦测试；阶段完成前执行 `make check`，前端变更另跑 `pnpm react:doctor`。

## 风险控制

- 不在 Slice 0 暗示不存在的 CLI/API；Prompt 只能进行判断和使用现有 Issue 能力。
- 不用 `channel_goal_subgoal_dep` 临时冒充完整 DAG 后再维护双内核。
- 不让 Agent prose 直接改变 canonical ready/completed 状态。
- 不让图状态继承或扩大 Goal 创建者、频道和 workspace 权限。
- 不在首版自动大规模重算、自进化、训练模型或开放不可观察的 provider subagent。

## 开工记录

- [x] 从最新 `lrm/dev` 的 `623d97e3f` 创建独立 worktree：`/home/jianghp3/gaia/multica-worktrees/goal-work-graph-runtime`。
- [x] 创建分支：`feat/goal-work-graph-runtime`。
- [x] 保留原工作区所有未提交内容，不修改其索引、分支或文件。
- [x] 完成 Slice 0 的 Prompt、Skill、source map 和回归测试。
- [x] 完成 Slice 1 的 expand-first migration、原子创建、幂等与 DAG 校验。
- [x] 完成 Slice 2 的 ready-node 调度、Issue/Agent Job 联动和状态回写。
- [x] 完成 Slice 3 的 Goal 生命周期、上下文摘要与完成 Gate。
- [x] 完成 Slice 4 的 Artifact revision、独立核验、失效传播与最小重算。
- [x] 完成 Slice 5 的兼容 schema、实时刷新、Goal 摘要和只读节点视图。
- [x] 本地通过 Go build/vet/cursor-deadlock、聚焦 PostgreSQL 集成测试、前端 9 项 build/typecheck/lint、React Doctor 和完整前端测试。
