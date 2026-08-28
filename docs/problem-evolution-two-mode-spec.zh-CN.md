# Multica 多模式解题自进化 Spec

> 状态：Draft
>
> 目标分支：`dev`（实现走 feature 分支，不直接提交 `dev`）
>
> 适用范围：Evolution Center、Go Server、Multica daemon、运行时模型、评测器与实时事件
>
> 参考实现：[bingreeky/JIT](https://github.com/bingreeky/JIT)（JIT-Agent）与 [LRM-Teams/self-harness 的 reward-only 分支](https://github.com/LRM-Teams/self-harness/tree/feature/reward-only-feedback)

## 0. 结论

Multica 增加一个“解题进化”能力，支持三种相互独立但共享基础设施的运行模式：

1. **答案进化（solution evolution）**：用户提供问题；用户提供评分方法，或由系统生成评分规则并由用户确认。系统直接进化本次任务的候选答案，最终交付一个答案。
2. **每题 harness 进化（JIT task harness + reward-only）**：用户提供当前任务及其隐藏答案或 verifier。系统参考 JIT-Agent 为这道题现场生成多个任务专用 harness，再通过静态门槛、任务匹配选择和 reward-only 执行反馈选择、修复或重启。harness 默认只服务当前任务，不宣称具备跨任务泛化能力。

3. **持久 harness 工程化（AHE，模式 C）**：用户提供一个任务集引用。系统在 `evaluate → analyze → improve` 外层循环里进化一个带版本的 harness 工作区，每次修改必须给出可证伪的预期影响，下一轮用实测 flip 判定并按判定保留或回滚。完整定义见第 20 章。

模式 A 与模式 B 都必须在开始前具备：

- 明确的任务输入；
- 已就绪并冻结的评分契约；
- 运行预算、停止条件和运行时；
- 可持久化、可回放的候选谱系与评测事件。

用户不必手写复杂评分规则。系统可以根据问题自动生成评分草案，但必须先展示评分维度、权重和验证方式，再由用户确认或使用明确标记的“一键采用并开始”。评分契约冻结后，本次运行中不得静默修改。

## 0.1 落地约束（与仓库现状对齐后的修订）

本节是对第 1 版设计的强制修订。后续章节已按本节改写；如出现冲突，以本节为准。

### 0.1.1 命名空间：一律使用 `problem_evolution` 前缀

仓库中 `evolution_*` 命名空间已被“技能/记忆进化”域占用，包括表 `evolution_unit_submission`、`shared_evolution_unit`、`evolution_unit_delivery`、`evolution_model_runtime_config`、`evolution_training_example`、`evolution_model_eval_run`，以及 `server/internal/service/evolution.go`、`server/internal/handler/evolution*.go` 和前端 `packages/views/evolution/`（当前 Evolution Center 的记忆与 curation 页面）。

因此本能力不得复用裸 `evolution_` 前缀：

- 数据表：`problem_evolution_run`、`problem_evolution_evaluator_contract`、`problem_evolution_candidate`、`problem_evolution_candidate_edge`、`problem_evolution_evaluation`、`problem_evolution_event`、`problem_evolution_artifact`；
- Go package：`server/internal/problemevolution`，handler 文件前缀 `problem_evolution_*.go`；
- HTTP 路由：`/api/problem-evolution/...`；
- WebSocket 事件：`problem_evolution_run:*`；
- daemon capability：`problem_evolution_v1`（遵循现有 `memory_curation_v1`、`restricted_execution_profiles_v1` 的带版本后缀约定）；
- 前端目录：`packages/views/problem-evolution/`，页面仍从 Evolution Center 入口进入。

页面归属（Evolution Center 子路由）不变，只是持久化与代码命名与既有进化域彻底分开。

### 0.1.2 第一阶段不执行用户提供的 verifier 代码

仓库现有的隔离设施是 daemon 的 `restricted_execution_profiles_v1`（`server/internal/daemon/execution_profile.go`，为受限模型调用设计）与 `server/internal/service/diagnosis_sandbox.go`。两者都不是可以安全执行任意用户代码的容器级沙箱。

因此第一阶段 verifier 类型收窄为：

1. 平台内置确定性评测器：JSON schema 校验、正则与精确匹配、编译/构建检查、内置测试 runner（固定命令与固定参数模板）；
2. 平台内置工具验证：计算器、静态检查、来源核验；
3. rubric judge（模型调用，走既有受限执行档位）。

“用户上传任意 verifier 代码/测试”从本 spec 移出，作为独立立项，前置条件是先完成隔离技术选型（容器 / seccomp / gVisor 级别）与威胁模型评审。在此之前，第 4 章“用户上传 verifier 或测试”仅指上传**内置 runner 可解释的数据文件**（期望输出、样例、schema），不含可执行代码。

### 0.1.3 隐藏答案 secret 子系统按新建子系统对待

第 9.8 条要求的“独立 secret storage + 短时单用途 capability”在仓库中不存在：现有只有 `agent_credential` 表和 `server/internal/secretscoped/filter.go`（输出过滤器，不是 secret store）。它必须作为独立 PR 与独立评审对待，工作量包含存储、签发端点、吊销、审计和泄漏 sentinel 测试，不能塞进 harness 生成的同一个 PR。

### 0.1.4 不在第一阶段抽取 Research 的 graph primitives

`packages/views/research/` 与 `server/internal/researchrun` 是成熟且测试极重的面。AGENTS.md 要求“避免宽泛重构”“优先复用现有模式而非引入并行抽象”，而在只有一个新消费者时做公共抽取会让两侧同时回归。

第一阶段做法：

- 本能力自建薄画布，直接依赖 `@xyflow/react`（版本走 pnpm catalog）；
- 只复用叶子级共享组件：证据 badge、节点详情抽屉、错误边界、reduced-motion 与大图性能开关的通用 hook（若已在 `packages/ui` 或 `packages/views/common`）；
- 不修改 `packages/views/research/` 任何文件；
- 待本能力画布稳定、且确实出现第三个消费者后，再单独立项抽取 shared graph primitives。

### 0.1.5 范围与流程

- `apps/mobile/` 不在范围内。本能力不向 `@multica/core` 导出移动端消费的运行时代码。
- 所有新增 UI 文案进 `packages/views/locales/`，命名与中文措辞先对照 `apps/docs/content/docs/developers/conventions.zh.mdx`。
- 本文档需在 GitHub Issues 建跟踪 issue。AGENTS.md 指向的 `docs/engineering-principles.md` 在当前仓库中**不存在**（`docs/agents/domain.md` 与 `docs/multi-agent-collaboration-principles.md` 均引用了它），因此本能力的持久规则改为落一条 `docs/adr/` 记录，待该索引文件补齐后回填。
- 模型 preset：`deepseek-v4-flash` 已存在于 `packages/views/runtimes/utils.ts` 的定价目录（`deepseek-chat` / `deepseek-reasoner` 为其别名），可作为可选 preset；仍不写死为唯一模型，默认继承用户选择的 runtime 模型。

## 1. 背景与问题

现有 Evolution Center 主要展示智能体记忆、skill、协作和运行指标；Research 已具备问题输入、多人运行、证据来源、报告、实时图谱与无限画布。当前缺少的是：

- 围绕一个用户问题现场生成多条候选分支；
- 用稳定的评分契约选择、修复和组合候选；
- 让用户看到候选从失败、挑战、修复到交叉组合的证据链；
- 在有隐藏答案的情况下，为当前题生成专用 harness，并只用安全 reward 做选择或有限反馈，避免泄露答案或 verifier 文本；
- 把最佳候选、失败版本、评分、成本和谱系作为平台实体长期保存。

## 2. 产品目标

### 2.1 目标

1. 用户可以在一个向导中选择“进化本次答案”或“为当前任务定制 harness”。
2. 用户能在开始前理解并确认“系统如何判好坏”。
3. 运行过程中实时展示代数、分支、父子关系、机制引用、评分和失败状态。
4. 所有节点都能解释“为什么保留、为什么淘汰、相对父代改善了什么”。
5. 答案进化最终交付一个针对本次问题的答案。
6. 每题 harness 进化最终交付当前任务的答案、胜出 harness 和完整版本谱系。
7. reward-only 模式下，进化器无法读取隐藏答案、测试名称、测试路径、verifier 文本、期望值或任意自由文本 verifier payload。
8. Web 与 Desktop 共享同一业务页面、数据契约和查询逻辑。

### 2.2 非目标

1. 不展示模型私有思维链；只展示可审计的产物、评分、引用、变更摘要和失败分类。
2. 不把 LLM judge 分数宣传为客观正确率。
3. 不默认用 64 个候选处理普通交互式问题。
4. 不允许任意用户 verifier 代码执行：第一阶段完全不支持（见 0.1.2），任何阶段都不得在 Go Server 进程内执行。
5. 不把隐藏答案放入普通 task context、WebSocket payload、日志或进化器可读工作目录。
6. 不把 Research 与解题进化强行合并成同一个数据库实体，也不在本 spec 内重构 Research 的图谱代码。
7. 不覆盖 `apps/mobile/`。

## 3. 核心概念

### 3.1 运行（evolution run）

一次从固定输入和固定评分契约开始，到预算耗尽、达到目标或用户停止为止的完整实验。

### 3.2 候选（candidate）

- 答案进化模式：候选是一个答案产物，可包含 Markdown、代码、附件和证据引用。
- 每题 harness 进化模式：候选是当前任务专用的 harness 快照。第一阶段使用结构化模块契约，而不是允许 meta-agent 输出任意程序。

### 3.3 评分契约（evaluator contract）

在运行开始前冻结的评测定义，包括：

- 评测器类型；
- 输入、输出和产物 schema；
- 评分维度和权重；
- 分数范围和通过阈值；
- 超时、异常和缺失输出如何计分；
- 使用哪些隐藏资源；
- 哪些反馈允许回传给 producer、debugger 和 evolver；
- 评测器版本与内容哈希。

### 3.4 行为分数向量（behavior profile）

用于判断候选能力是否互补：

- 答案进化：每个评分维度或验收点的分数向量；
- 每题 harness 进化：当前任务多个 verifier 维度、子目标或 rollout 的 reward 向量；只有一个二值 reward 时，向量可以退化为单值。

父代选择不只看平均分，还要寻找“总分不差、擅长点不同”的候选。

## 4. 统一开始门槛

运行只有在以下条件全部满足时才能从 `draft` 进入 `queued`：

1. `problem_spec` 非空并通过 schema 校验；
2. `evaluator_contract.status = frozen`；
3. 运行时在线且具备 `problem_evolution` capability；
4. 模型、并发数、候选预算、wall-clock 预算和最大单候选时间已确定；
5. 产物类型与交付格式已确定；
6. reward-only 模式的隐藏资源已完成隔离检查；
7. 用户有工作区运行权限，预算预估未超过配额。

评分规则可以通过三种方式产生：

1. 用户直接选择内置确定性 evaluator；
2. 用户上传内置 runner 可解释的评测数据（期望输出、样例、schema、匹配规则）；第一阶段不接受可执行 verifier 代码，见 0.1.2；
3. 系统根据问题生成 rubric 草案，用户确认后冻结。

不允许“先运行，跑到一半再让 judge 改规则”。

## 5. 模式 A：答案进化

### 5.1 适用场景

- 写方案、分析报告、研究结论；
- 数学、逻辑或规划问题；
- 单次代码生成；
- 有明确格式和验收标准的内容生成；
- 用户关心的是本次最终答案，而不是长期改造一个智能体。

### 5.2 用户输入

必填：

- 问题或任务描述；
- 期望交付类型；
- 评分契约，或允许系统生成评分草案。

选填：

- 背景资料、附件、工作区上下文；
- 必须满足和不得违反的约束；
- 可使用的工具、仓库和来源范围；
- 运行时、模型、深度档位和成本上限；
- 人工提供的参考答案。参考答案存在时仍不得直接注入候选生成上下文，除非用户明确选择“参考改写”而不是“独立求解”。

### 5.3 评分器类型

按可信度优先级使用：

1. **确定性验证**：内置测试 runner、schema、编译、求解器检查、精确规则；
2. **工具验证**：运行代码、计算器、静态检查、事实来源核验；
3. **参考答案比较**：只允许 verifier 读取参考答案；
4. **rubric judge**：按多个维度分别评分并给出短理由；
5. **组合评分**：确定性结果作为硬门槛，rubric 只比较已通过硬门槛的候选。

系统自动生成的 rubric 至少包含：

- `dimension_id`；
- 名称和可操作的判定标准；
- 权重；
- `0..1` 分数定义；
- 验证方法；
- 失败是否为硬失败；
- 可向后续候选公开的反馈摘要范围。

### 5.4 默认进化流程

```text
问题 + 冻结评分契约
  → 生成验收点与基础候选
  → 并行分支：Elite / Repair / Challenger / Diverse
  → 逐候选评测并形成行为分数向量
  → 质量 + 行为互补 + 产物 shingles 选择父代
  → Refine / Repair / Restart / Crossover
  → 达到目标、停滞或预算耗尽
  → Final Synthesizer 基于最佳候选和压缩差异生成最终答案
  → 最终答案再次通过同一冻结评分契约
```

默认多样性信号：

- 行为互补占多样性判断的 75%；
- 产物 shingles 新颖性占 25%；
- 最终选择还必须加入质量分，75/25 不是最终总分权重。

给下一代模型的父代反馈只包含：

- 两个父代的总分和维度摘要；
- 各自更强的维度数量；
- 共同薄弱的维度；
- 最大的匿名分数差；
- 机制级变更摘要；
- 有界的失败分类。

不发送完整 evaluator 日志或完整私有轨迹。

### 5.5 默认预算

- `fast`：4 个初始候选，最多 2 代，候选上限 8；
- `standard`：4 条分支，最多 4 代，候选上限 16；
- `deep`：4 条分支，最多 8 代，候选上限 32；
- `custom`：管理员允许后可配置到 64 或更高。

普通交互默认使用 `standard`，避免用户等待一个完整 benchmark 规模的运行。

### 5.6 交付物

- 最终答案；
- 最终分数卡和限制说明；
- 被采用的候选谱系；
- 主要证据与引用；
- 仍未解决的验收点；
- Token、成本、耗时和候选统计；
- 可导出到频道、issue 或 Research Report 的产物。

## 6. 模式 B：JIT 式每题 harness 进化 + reward-only

### 6.1 适用场景

- 用户有一道具体任务，以及隐藏答案或可执行 verifier；
- 任务的完成过程、工具使用和状态组织会显著影响结果；
- 一个固定通用 harness 很难覆盖该任务的结构；
- 用户希望为当前任务临时构造最合适的 memory、planning、action 和 capability orchestration；
- 隐藏答案不能污染 harness 生成和修复过程。

此模式把“单题特化”定义为目标，不把它称为通用训练。任务结束后，胜出 harness 默认随 run 归档，不自动替换任何全局智能体配置。

### 6.2 与 JIT-Agent 的关系

直接复用的 JIT 思想：

- meta-agent 根据当前 task spec、protocol、tool/skill registry 和少量历史 harness 参考，现场生成任务专用 harness；
- harness 使用固定结构化接口，不让 meta-agent 输出任意自由形态 agent 程序；
- 每题生成 N 个候选，温度必须允许候选产生差异；
- 候选先经过“文件/模块是否完整”的免费静态门槛；
- 可使用 meta-model logprob 或 model-as-judge 做执行前选择；
- 生成、选择、执行产物分开保存，可恢复、可重放；
- meta-model、execution model 和 judge model 是三个独立角色。

Multica 的扩展：

- 上游 JIT 对低分候选不会按 benchmark reward 修复，只在生成或执行异常时 repair；
- Multica 增加一个显式、可关闭的 `reward_only_feedback` 阶段：选中 harness 执行失败后，meta-agent只能依据 reward、agent-owned trace 和白名单元数据进行有限轮 repair 或 regenerate；
- 页面必须标记这是“JIT-inspired reward-only extension”，不能把低分 repair 描述成上游 JIT 的原始行为。

### 6.3 Harness 结构化契约

第一阶段候选至少包含以下模块：

- `memory`：当前任务需要保留、压缩和淘汰什么状态；
- `planning`：如何拆解任务、更新计划和判断完成；
- `action`：每一步如何选择模型调用、工具调用、子智能体或交付；
- `capability_policy`：哪些 tools/skills 可以在什么条件下使用；
- `prompt`：任务专用但不含隐藏答案的执行提示。

Multica 可以把这些模块实现为 Go/JSON/YAML 契约，不要求照搬 JIT 的五个 Python/YAML 文件，但必须做到：

- 静态可校验；
- 执行权限可限制；
- 模块差异可展示；
- 不能读 verifier secret；
- 能被当前 runtime 的 agent CLI 执行。

### 6.4 重要语义

“任务 + 答案”中的答案只属于 verifier，不属于 meta-agent、producer、debugger 或执行 harness。

reward-only 阶段允许 meta-agent 看到：

- `PASS / FAIL / TIMEOUT` 或数值 reward；
- 当前 harness 自己产生的轨迹；
- 白名单数值/布尔过程元数据，例如是否完成、是否异常、测试总数、通过数、失败数、跳过数和耗时；
- 基于上述安全数据生成的压缩分析；
- 当前题内历史 harness 的分数、模块差异和失败分类。

reward-only 阶段禁止 meta-agent 看到：

- 标准答案、reference answer、expected value；
- 测试名称、测试路径、隐藏断言；
- verifier message、trace、payload、stdout；
- 原始 verifier 目录或其绝对路径；
- 任何可能携带答案的自由文本 verifier 字段；
- 由人工 reviewer 从隐藏答案中转述出的提示。

这与 `self-harness/feature/reward-only-feedback` 的隔离边界一致：安全反馈采用字段白名单，而不是字段黑名单。

#### 6.4.1 安全反馈的带宽预算

白名单本身也是信道。逐轮回传细粒度计数（例如每轮的通过/失败/跳过数）在多轮反馈下等价于让模型对 verifier 做迭代二分探测，因此白名单必须附带带宽限制，而不只是字段限制：

- 默认只回传单调量：当前运行内**历史最好**的通过数与总数，不回传每轮逐次计数；
- 每轮反馈允许的数值字段总数不超过 6 个；
- 通过数按桶投影（默认 4 桶：0、少数、多数、全部），不回传精确整数；除非 evaluator 契约显式声明 `metrics_precision = exact` 且已启用最终盲验隔离；
- 反馈轮数上限与带宽档位一起记录在 `feedback_policy`，并写入运行报告；
- 无论哪种档位，`TIMEOUT` 与异常分类只回传枚举值，不回传计数明细。

`feedback_policy` 必须显式声明本次运行使用的带宽档位（`bucketed`（默认）/ `exact`），页面在使用 `exact` 时展示“已对当前 verifier 高带宽自适应搜索”的说明。

### 6.5 用户输入

必填：

- 当前任务；
- 隐藏答案数据、内置确定性 verifier 配置或冻结的 rubric evaluator（不含用户可执行代码，见 0.1.2）；
- 当前 runtime 可用的 tool/skill registry；
- execution model；
- 候选 harness 数、执行候选数、最大反馈轮数和总预算。

选填：

- 一个可作为 fallback 的 seed harness；
- 允许检索的历史 harness 描述或代码数量；
- meta-model、selector/judge model；
- 必须保留的安全规则和禁止能力；
- harness module allowlist；
- selection 策略和成本优先级。

### 6.6 默认流程

```text
当前任务 + tools/skills + 隐藏 verifier
  → 检索少量历史 harness 描述
  → Meta-agent 生成 N 个任务专用 harness
  → 静态完整性、安全和权限检查
  → Task-fit judge/logprob 预选 K 个
  → Execution model 使用 K 个 harness 独立解题
  → Verifier 返回 reward-only 结果
  → 有候选通过：按 reward、成本和稳定性选择胜者
  → 全部失败：基于自己的轨迹 + 安全反馈做有限轮 repair/regenerate
  → 重新静态检查并执行
  → 达到通过、反馈轮数或预算上限
  → 输出最终答案 + 胜出 harness + 谱系
```

建议默认 `N=4`、预选 `K=2`、`max_feedback_rounds=2`。相比只执行一个 harness，执行两个能利用真实 reward；相比执行全部候选，成本更可控。

### 6.7 选择策略

支持：

- `select_then_execute`：最接近上游 JIT，先按 logprob/judge 选一个再执行，成本最低；
- `shortlist_then_execute`：先预选 K 个，再用真实 verifier reward 选胜者，默认；
- `execute_all_then_select`：所有静态合格候选都执行，质量信号最强、成本最高。

执行前 selector 只判断“哪个 harness 更可能适合任务”，不能访问隐藏答案。执行后 selector 只能读取 reward 和 safe metrics。

### 6.8 Reward-only 反馈闭环

低 reward 不附带答案提示。Repair/Regenerate 输入只包含：

- harness 模块摘要；
- execution trace 的有界摘要；
- `PASS/FAIL/TIMEOUT`；
- allowlisted safe metrics；
- “在哪个执行阶段失效”的结构化分类；
- 与同题其他 harness 的匿名行为差异。

若 reward 是单个二值值且 trace 没有可诊断信号，系统优先 regenerate 不同 harness 结构，而不是让模型凭空猜测 verifier。

### 6.9 任务特化、过拟合边界与复用

- 单题运行是正常模式，不显示“过拟合警告”；
- UI 显示“当前任务专用，不代表跨任务能力提升”；
- 胜出 harness 默认 scope 为 `run_only`；
- 按题生成 harness 本身是任务特化，不是需要消除的问题；但反复依据同一个 verifier reward 自适应修改，仍可能形成 verifier overfitting 或 reward hacking；
- 能拆分 verifier 时，搜索阶段只读取 `development_probe` 的安全 reward，最终候选只允许一次 `final_confirmation` 盲验；两者使用不同隐藏样例、随机种子或扰动；
- 无法拆分 verifier 时，限制反馈轮数、对 reward 做低带宽投影，并在报告中标注“已对当前 verifier 自适应搜索”；
- 要获得最接近上游 JIT、也最适合公开 benchmark 对比的结果，应使用 `select_then_execute`，并关闭低 reward repair；
- 用户可以手动“保存为 harness 模板”，但保存的是结构和适用标签，不携带任务内容、答案或 verifier 信息；
- 自动进入历史参考库前必须经过去敏、去任务常量和人工/策略审核；
- 如果用户真正要进化通用 harness，应创建独立的 benchmark evolution 流程，使用多任务开发集和 held-out 集，不与本模式混淆。

### 6.10 指标

- 当前任务是否通过；
- 最佳 reward；
- 首次通过所需 harness 数和反馈轮数；
- selector top-K 命中率；
- execution steps、Token、成本和耗时；
- 静态无效率、执行异常率、TIMEOUT 率；
- 最终盲验是否通过，以及搜索 reward 与最终盲验结果的差距；
- repair 与 regenerate 的成功率。

### 6.11 交付物

- 当前任务最终答案或产物；
- 胜出 task-specific harness 快照和内容哈希；
- N 个初始候选、预选、执行、repair/regenerate 的完整谱系；
- reward-only 隔离审计结果；
- selector 理由、真实 reward 和成本对比；
- 可重放当前 run 的固定 evaluator、模型和 harness 版本；
- 可选的“保存为去敏 harness 模板”操作。

## 7. 两种模式的产品入口

### 7.1 路由

- `/{workspaceSlug}/evolution/solve`：运行列表和新建入口；
- `/{workspaceSlug}/evolution/solve/{runId}`：单次运行大画布；
- Web 使用 Next.js route wrapper；
- Desktop 使用相同 shared view，并在 Desktop router 增加对应 workspace route；
- 共享视图代码位于 `packages/views/problem-evolution/`（见 0.1.1），不放进现有 `packages/views/evolution/`。

`solve` 段用于把本能力与 Evolution Center 现有的记忆/技能进化页面在 URL 上分开；它是 workspace 内路由，不涉及 AGENTS.md 的全局 root 路由命名限制。

Evolution Center 保留一个入口卡片和最近运行摘要，不把完整解题画布塞进现有指标页。

### 7.2 新建向导

第一步选择：

- **进化本次答案**：最终得到本次问题的最佳答案；
- **为当前任务定制 harness**：最终得到本题答案和本题专用 harness。

第二步配置任务：

- 答案模式：问题、附件、交付格式和约束；
- 每题 harness 模式：当前任务、隐藏答案/verifier、tool/skill registry 和可选 seed harness。

第三步确认评分：

- 展示评分维度、权重、硬门槛、超时处理和反馈范围；
- reward-only 显示“evolver 能看到 / 不能看到”的隔离清单；
- 用户确认后冻结 evaluator。

第四步配置预算并开始：

- 运行时和模型；
- 深度档位；
- 并发数、候选数或迭代数；
- 成本预估和停止条件。

### 7.3 运行详情画布

页面布局：

```text
┌─ 顶栏：问题、模式、状态、代数/迭代、最佳分、成本、停止 ─────────────┐
├─ 左栏：阶段、分支、事件时间线 ─┬─ 中央：无限画布候选谱系 ─┬─ 右栏：节点详情 ┤
│                               │                           │                │
│ baseline / generation        │ parent → child            │ 分数卡         │
│ repair / challenger          │ reference ⇢ mechanism     │ 证据与引用      │
│ evaluation / synthesis       │ failed / pruned / elite    │ 变更与失败摘要   │
├───────────────────────────────┴───────────────────────────┴────────────────┤
└─ 底栏：当前最佳答案或胜出 task-specific harness、未解决风险、导出操作 ───┘
```

### 7.4 节点和边

答案模式节点：候选答案。

每题 harness 模式节点：seed、初始候选、shortlist、executed、repair/regenerate 和 winner harness。

边类型：

- `parent`：父代生成子代；
- `repair`：针对失败修复；
- `refine`：同一路线改进；
- `restart`：新算法或新组件层级；
- `crossover`：组合两个候选；
- `reference`：只读取机制，不直接继承；
- `rollback`：退回当前题内历史最佳 harness；
- `selected`：评测后成为下一轮基线。

节点颜色表达 lane，边样式表达关系，红色虚线表达失败、超时或被剪枝。

### 7.5 可展示证据

- 产物摘要与可下载产物；
- 固定 evaluator 的分数卡；
- 相对父代的结构化差异；
- 来源、引用和工具验证结果；
- reward-only 的安全反馈摘要；
- PASS/FAIL/TIMEOUT、异常分类和允许的过程计数；
- Token、成本、耗时和模型版本；
- 选择、剪枝、回滚和停止原因。

不得展示：

- 私有思维链；
- 完整内部模型消息；
- reward-only 隐藏答案和 verifier 自由文本；
- 未经过滤的环境变量、路径和日志。

## 8. 平台架构

```text
Shared Evolution Views
  ├─ React Query：run / snapshot / candidate / evaluator / artifact
  ├─ Zustand：画布视口、选择、筛选和抽屉状态
  └─ @xyflow/react：谱系画布
             │
             ▼
Go Server
  ├─ 权限、配额、API、持久化和 WebSocket invalidate 事件
  ├─ evaluator contract 冻结与版本管理
  ├─ secret/verifier capability 管理
  └─ daemon run claim / heartbeat / result callbacks
             │
             ▼
Multica daemon
  ├─ problem evolution orchestrator
  ├─ producer / debugger / evaluator / communicator
  ├─ runtime model or CLI invocation
  ├─ isolated verifier runner
  └─ candidate artifacts and incremental events
```

### 8.1 复用边界

第一阶段按 0.1.4 执行：**不修改 `packages/views/research/`，不做 graph primitives 抽取。**

复用的是模式与叶子组件，不是 Research 的实现：

- 通过 pnpm catalog 直接依赖 `@xyflow/react`（与 Research 同版本，但各自持有自己的画布实现）；
- 复用已在 `packages/ui` 或 `packages/views/common` 的叶子组件与 hook：证据 badge、详情抽屉、错误边界、reduced-motion 与大图性能开关；
- 复用 WebSocket → React Query invalidation 模式（模式复制，不共用代码）；
- 复用 Web/Desktop shared view 的路由组织方式。

明确不复用：

- `research_session` 及 researchrun v6 的任何数据表与状态机；
- Research 专用 node/edge type 作为本能力的持久化契约；
- Research 的 canvas、graph-model、semantic-lod、star-graph 等内部模块（不 import，不改）。

后续若出现第三个图谱消费者，再单独立项抽取 shared graph primitives，由 Research 与本能力分别提供 adapter。本 spec 不承担该重构。

### 8.2 执行位置

Go Server 不直接执行模型调用和用户 verifier。长任务由 daemon 领取并执行。

在 daemon 增加 `problem_evolution_v1` capability，注册进 `daemonRegistrationCapabilities`（`server/internal/daemon/daemon.go`），常量定义在 `server/pkg/protocol/messages.go`，并参考 memory curation 的 claim、heartbeat、timeout、result callback 和 zombie recovery 模式。新增 daemon 协议字段使用 camelCase JSON。调度器以 Go package 形式实现；第一阶段可以复刻现有 Python 启发式算法，但持久化和进程生命周期由 Multica 管理。

### 8.3 模型选择

- 默认继承用户选择的 runtime/agent 模型；
- 可提供 `deepseek-v4-flash` preset（已存在于定价目录，见 0.1.5）；
- 不在数据库、页面或代码中写死 Lenovo API key；
- producer、debugger、judge、communicator 可以使用同一模型，也允许管理员分别配置；
- evaluator 使用模型时必须记录 model/version/prompt hash。

## 9. 数据模型

### 9.1 `problem_evolution_run`

- `id`
- `workspace_id`
- `created_by`
- `mode`：`solution | task_harness_reward_only`
- `title`
- `problem_spec`
- `artifact_type`
- `status`
- `stage`
- `runtime_id`
- `model_config`
- `budget_config`
- `stop_config`
- `evaluator_contract_id`
- `best_candidate_id`
- `final_candidate_id`
- `graph_version`
- `started_at / finished_at / created_at / updated_at`
- `failure_reason`

### 9.2 `problem_evolution_evaluator_contract`

- `id`
- `workspace_id`
- `mode`
- `status`：`draft | validating | frozen | invalid`
- `version`
- `contract`
- `content_hash`
- `secret_ref`
- `feedback_policy`
- `created_by`
- `frozen_at / created_at / updated_at`

### 9.3 `problem_evolution_candidate`

- `id`
- `run_id`
- `generation`
- `lane`
- `operator`
- `status`
- `score`
- `behavior_profile`
- `feasible`
- `artifact_ref`
- `artifact_hash`
- `summary`
- `change_summary`
- `failure_class`
- `runtime_seconds`
- `token_usage`
- `cost`
- `evaluation_index`
- `created_at / updated_at`

`score` 与 `behavior_profile` 的 JSONB 结构固定并带版本，前端 zod schema 与之一一对应：

```json
{
  "schema_version": 1,
  "total": 0.82,
  "scale": "unit_interval",
  "hard_gate_passed": true,
  "dimensions": [
    { "dimension_id": "correctness", "score": 0.9, "weight": 0.5, "hard": true },
    { "dimension_id": "coverage", "score": 0.7, "weight": 0.5, "hard": false }
  ]
}
```

```json
{
  "schema_version": 1,
  "kind": "dimension_vector",
  "entries": [
    { "key": "correctness", "value": 0.9 },
    { "key": "coverage", "value": 0.7 }
  ]
}
```

约束：

- `total` 与 `entries[].value` 一律归一到 `0..1`；原始分数放在 evaluation 记录里，不放候选表；
- `kind` 允许 `dimension_vector`（模式 A）与 `reward_vector`（模式 B），二值 reward 退化为单元素向量；
- `schema_version` 递增时必须同时更新前端 zod schema 和读路径的向后解析，旧 run 不回填；
- 空评测（未评测、失败）使用 `null`，不使用 `total = 0`，避免与真实 0 分混淆。

### 9.4 `problem_evolution_candidate_edge`

- `id`
- `run_id`
- `from_candidate_id`
- `to_candidate_id`
- `edge_type`
- `metadata`
- `created_at`

### 9.5 `problem_evolution_evaluation`

- `id`
- `run_id`
- `candidate_id`
- `case_id`
- `rollout_index`
- `reward`
- `verdict`
- `safe_metrics`
- `safe_feedback`
- `verifier_version`
- `started_at / finished_at`
- `error_type`

`safe_feedback` 必须由 feedback policy 投影生成，不能直接保存原始 verifier payload。

### 9.6 `problem_evolution_event`

- `id`
- `run_id`
- `seq`
- `event_type`
- `candidate_id`
- `actor_type / actor_id`
- `payload`
- `created_at`

`(run_id, seq)` 唯一，用于断线重放和幂等投影。

`seq` 由 **Go Server 在写入事务内分配**，daemon 不分配 seq。原因：模式 A 与模式 B 都会并行产生候选，daemon 侧自行分配就必须自建串行化，且崩溃恢复后容易出现空洞或重号。

具体契约：

- daemon 上报事件时携带 `client_event_id`（daemon 生成的 UUID）与 `run_id`；
- Server 在同一事务内取 `max(seq) + 1` 并插入，`(run_id, client_event_id)` 建唯一索引作为幂等键；
- 重试相同 `client_event_id` 返回已分配的 `seq`，不产生新事件；
- 允许 daemon 批量上报，Server 按数组顺序在单事务内连续分配，保证批内顺序稳定；
- 事件语义顺序若与 seq 无关（例如两个并行候选的进展），前端不得依赖 seq 推断因果，只用于重放与去重。

#### 9.6.1 `graph_version` 与实时一致性

- `graph_version` 由 Server 拥有，在任何影响图结构的写入事务内单调 +1，daemon 与前端都不得写；
- WebSocket 事件 payload 必须携带触发时的 `graph_version`；
- 前端保存已渲染的 `graph_version`：收到的事件版本不大于当前版本时**丢弃**，不发起请求；
- snapshot 响应也返回 `graph_version`，前端只在响应版本大于当前版本时替换图；否则丢弃该响应（处理“invalidate 先到、旧 snapshot 后到”的乱序）；
- 版本跳跃（收到的版本远大于本地）不做增量补齐，直接重取 snapshot。

### 9.7 `problem_evolution_artifact`

- `id`
- `run_id`
- `candidate_id`
- `kind`
- `storage_ref`
- `content_hash`
- `mime_type`
- `size_bytes`
- `visibility`
- `created_at`

### 9.8 隐藏答案与 verifier

隐藏资源不得放在上述普通 JSON 字段中。它们存入独立 secret storage，并通过短时、单用途、绑定 `run_id + evaluator_contract_id + verifier_version` 的 capability 提供给 verifier runner。

producer、debugger、communicator、浏览器和普通 daemon task token 均不能获取该 capability。

该 secret storage 在仓库中尚不存在（现有只有 `agent_credential` 表与 `server/internal/secretscoped/filter.go` 输出过滤器），按 0.1.3 作为独立 PR 立项，交付范围包含：存储与加密、签发端点、TTL 与单次使用、吊销、审计事件、以及以哨兵值贯穿 query/trace/WS/日志/artifact/页面的泄漏测试。模式 B 的实现以此为前置依赖。

## 10. API 草案

### 10.1 用户 API

- `POST /api/problem-evolution/runs/drafts`
- `PATCH /api/problem-evolution/runs/{runId}`
- `POST /api/problem-evolution/runs/{runId}/evaluator/generate`
- `POST /api/problem-evolution/runs/{runId}/evaluator/validate`
- `POST /api/problem-evolution/runs/{runId}/evaluator/freeze`
- `POST /api/problem-evolution/runs/{runId}/start`
- `POST /api/problem-evolution/runs/{runId}/stop`
- `GET /api/problem-evolution/runs`
- `GET /api/problem-evolution/runs/{runId}`
- `GET /api/problem-evolution/runs/{runId}/snapshot`
- `GET /api/problem-evolution/runs/{runId}/events?after_seq=`
- `GET /api/problem-evolution/runs/{runId}/candidates/{candidateId}`
- `GET /api/problem-evolution/runs/{runId}/artifacts/{artifactId}`
- `POST /api/problem-evolution/runs/{runId}/export`

所有 response 必须经过前端 zod schema 解析并提供 malformed-response fallback。

### 10.2 daemon API

- heartbeat capability：`problem_evolution_v1`
- pending payload：run、frozen evaluator metadata、非 secret 输入、预算、claim token
- `POST /api/daemon/problem-evolution/runs/{runId}/events`
- `POST /api/daemon/problem-evolution/runs/{runId}/artifacts`
- `POST /api/daemon/problem-evolution/runs/{runId}/heartbeat`
- `POST /api/daemon/problem-evolution/runs/{runId}/complete`
- `POST /api/daemon/problem-evolution/runs/{runId}/fail`
- `POST /api/daemon/problem-evolution/runs/{runId}/release`

verifier secret capability 由独立 endpoint 仅签发给已 claim 的 verifier execution，不与 pending payload 一起下发。

## 11. 实时事件

WebSocket 事件采用 invalidate 信号，不直接把 server state 写入 Zustand：

- `problem_evolution_run:changed`
- `problem_evolution_run:graph_updated`
- `problem_evolution_run:candidate_updated`
- `problem_evolution_run:score_updated`
- `problem_evolution_run:artifact_updated`
- `problem_evolution_run:completed`

payload 至少包含 `workspace_id`、`run_id`、`graph_version` 和可选 `candidate_id`。客户端收到事件后失效对应 React Query key；画布视口和选择仍由 Zustand 保存。

## 12. 状态机

### 12.1 Run

```text
draft
  → validating_evaluator
  → ready
  → queued
  → running
  → synthesizing
  → completed

queued/running/synthesizing → stopping → cancelled
任意执行态 → failed
```

### 12.2 Candidate

```text
planned → producing → validating → evaluating
  → selectable → elite / selected / pruned
  → failed / timeout / infeasible
```

### 12.3 Evaluator

```text
draft → validating → frozen
draft/validating → invalid
```

`frozen` evaluator 不可原地修改；变更必须创建新版本和新 run，或明确重启当前 draft。

冻结的强制方式不只在 start 时校验：

- 冻结时计算并持久化 `content_hash`；
- run 启动时校验 hash，与 run 记录绑定 `evaluator_contract_id + content_hash`；
- **每次评测执行前**重新校验当前契约 hash 等于 run 绑定的 hash，不一致直接把该次评测标记为 `error_type = evaluator_contract_drift` 并让 run 进入 `failed`；
- daemon 侧 pending payload 携带 hash，回传结果时带回，Server 校验回传 hash；
- 数据库层对 `status = frozen` 的行加触发器或应用层 guard，拒绝 `contract` 与 `content_hash` 的 UPDATE。

## 13. 停止条件

任一条件满足即可停止：

- 达到目标分、目标 verifier reward 或通过判定；
- 候选/迭代预算耗尽；
- wall-clock 或成本预算耗尽；
- 连续 `patience` 代没有最小改进；
- 所有新候选失败；
- verifier 连续基础设施异常；
- 用户停止；
- runtime 离线且超过恢复期限。

停止必须记录结构化 `stop_reason`，不得只写一段日志。

### 13.1 停止时限（可测数值）

“有界时间”必须是具体数字，否则验收无法测：

- 用户点击停止后，Server 在同一请求内把 run 置为 `stopping`，并停止派发新候选；
- daemon 在下一次 heartbeat（心跳间隔 ≤ 10s）收到 stop 信号后，**5s 内**停止启动新候选；
- 已在执行的候选给予 `graceful_drain = 60s` 完成或安全取消；
- 从点击停止到 run 进入 `cancelled` 的上限为 **90s**；超时则强制置为 `cancelled` 并把未收敛候选标记 `aborted`；
- daemon 心跳丢失 `heartbeat_timeout = 60s` 后触发 zombie recovery，run 转 `failed` 或按 `stop_config` 重新排队；
- 上述数值可在 `stop_config` 覆盖，但必须有值，且报告中展示实际生效值。

## 14. 权限、安全与审计

1. 所有查询按 `workspace_id` 隔离。
2. 创建和查看普通运行需要工作区成员权限；上传评测数据和导出 harness 需要更高权限。
3. 第一阶段只运行内置 evaluator（见 0.1.2），执行走 daemon 现有 `restricted_execution_profiles_v1` 档位：禁网络、限定工作目录、限定 CPU/内存/时间。用户可执行 verifier 代码在完成独立的隔离选型与威胁模型评审前不得启用。
4. reward-only 使用字段白名单投影 + 带宽预算（见 6.4.1），新增字段默认不可见。
5. secret 读取、evaluator freeze、运行开始、停止、导出和 harness 应用都写审计事件。
6. 日志清洗绝对路径、环境变量、token 和 hidden-answer 内容。
7. 答案模式的 LLM judge 必须记录 prompt hash 和模型版本，便于重放。
8. 页面明确区分“客观验证”“参考答案比较”“LLM 评审分”。

## 15. 成本与配额

开始前展示：

- 最大候选/迭代数；
- 模型调用次数上限（默认每道题 100 次，可配置）；
- 最大并发；
- 每候选 timeout；
- 预计 Token 和成本区间；
- 金额上限（默认无限，可配置）；
- verifier sandbox 资源上限。

运行中展示实际消耗，并在达到软阈值时通知；硬阈值必须停止新候选，只允许正在运行的候选完成或安全取消。

### 15.1 与现有计费/配额的接入

不新建独立计费口径，接现有 `billing` / `plan-billing`：

- Token 与成本按现有 usage attribution 归属到 workspace 与发起人，run 与候选 ID 作为归因维度写入既有 usage 记录；
- 启动前的预估走现有配额检查；超出计划额度时 start 返回明确的配额错误，不进入 `queued`；
- 模型调用次数默认上限为每道题 100 次，可在页面创建/编辑运行时配置；
- 金额上限默认为无限（`0` 表示无限），用户可配置正数美元上限；
- 金额限制启用时，软阈值默认为本次运行金额预算的 80%，硬阈值为 100%；金额无限时不触发金额阈值；
- 硬阈值触发与用户主动停止走同一条 `stopping` 路径和同一组时限（见 13.1），`stop_reason` 区分 `budget_exhausted` 与 `user_stopped`。

## 16. 实施拆分

原 5-PR 拆分过粗：PR-1 单个包含 7 张表、冻结状态机、全套 API、权限与测试；PR-4 又把 secret 子系统、沙箱、harness 生成和泄漏测试塞在一起。改为“先纵向最小切片，再横向补能力”，模式 B 全程在 feature flag 后。

### PR-0：文档与登记

- 本文档提交；
- 落一条 `docs/adr/` 记录（`docs/engineering-principles.md` 当前缺失，见 0.1.5）；
- 建 GitHub 跟踪 issue，把后续 PR 号回写到本文档；
- 模型 preset 已确认为 `deepseek-v4-flash`，8.3 据此定稿。

### PR-1：纵向最小切片（端到端可用）

目标是一条能跑通并合并的最小链路，而不是完整契约面。

- 迁移：只建 `problem_evolution_run`、`problem_evolution_evaluator_contract`、`problem_evolution_candidate`、`problem_evolution_event`；
- 只支持模式 A + 单一内置确定性 evaluator（schema/精确匹配），不含 rubric 生成；
- 只支持 4 个初始候选、单代、无 crossover/repair 分支；
- daemon `problem_evolution_v1` capability、claim/heartbeat/complete/fail、Server 侧 seq 分配与幂等键；
- 前端：列表 + 详情静态画布（自建薄 xyflow 画布）+ 节点详情；
- 测试：evaluator freeze guard、seq 幂等、`graph_version` 乱序丢弃、malformed-response fallback。

### PR-2：评分契约完整化

- rubric 草案生成、校验、确认与冻结；
- 组合评分与硬门槛；
- `score` / `behavior_profile` schema v1 与前端 zod；
- 每次评测前的 contract hash 漂移校验。

### PR-3：完整候选谱系与模式 A 进化算子

- `problem_evolution_candidate_edge`、`problem_evolution_evaluation`、`problem_evolution_artifact`；
- Elite / Repair / Challenger / Diverse 四条分支与 crossover；
- 行为互补 + shingles 多样性选择；
- Final Synthesizer 与最终答案复评；
- 停止条件、13.1 时限、zombie recovery、artifact 回传。

### PR-4：隐藏答案 secret 子系统（独立前置）

- secret storage、短时单用途 capability 签发与吊销；
- 绑定 `run_id + evaluator_contract_id + verifier_version`；
- 审计事件与日志清洗；
- 贯穿 query/trace/WS/日志/artifact/页面的哨兵泄漏测试。

不含 harness 生成，可与 PR-3 并行。

### PR-5：模式 B — JIT 式每题 harness（feature flag）

- 结构化 harness 契约与静态门槛；
- meta-agent 生成 N 个候选、task-fit 预选 K 个、执行、winner selection、题内 rollback；
- `select_then_execute` 与 `shortlist_then_execute`；
- 依赖 PR-4。

### PR-6：Reward-only 反馈闭环

- 白名单投影 + 6.4.1 带宽预算；
- 有限轮 repair/regenerate；
- development probe 与 final confirmation 的隔离；
- 上游 JIT 行为与 Multica 扩展在 UI/事件中的显式区分。

### PR-7：交付与运营

- 最终答案 / 胜出 harness 导出与模板去敏；
- 15.1 的计费与配额接入、审计；
- 导出到频道、issue 和 Research Report；
- 运行对比与复现。

#### PR-7 已实现部分的口径

导出 `GET /api/problem-evolution/runs/{runId}/export` 返回一个自洽的交付包：

- 冻结的评分契约与其 `content_hash`，且必须与 run 上钉的 hash 一致；
- 候选、谱系边、逐次评测记录；边引用的候选必须都在包内，否则读者无法只用交付包重建图；
- artifact 只给 `storage_ref` + `content_hash`，正文不入包；
- secret 只给审计汇总（用量、签发/兑换/拒绝计数与拒绝原因分布），密文与明文都不入包；
- `result.scope_claim`：结论可推广到多远，由平台判定，取值 `search_sample_only`（未盲验）、`search_sample_only_large_overfit_gap`（缺口 > 0.15）、`this_task_only`（模式 B 已盲验）、`this_problem_blind_validated`；
- `reproduction`：`replayable` 与 `missing_for_replay`。缺 `evolver_version`、`evaluator_content_hash` 或 `search_seed` 时必须明说不可精确重放，不允许默认可复现。

运行对比 `GET /api/problem-evolution/runs/{runId}/compare/{otherRunId}`：

- 评分契约 hash、模式、evolver 版本、benchmark 开关任一不同即 `comparable = false`，此时不给出优劣，只列差异；不同 rubric 的分数不在一个坐标系里，硬排名等于编造结论；
- 两边都有盲验成绩时以盲验判定，`preference_basis = blind_validation`；只有搜索成绩时判定仍给出，但 `preference_basis = search_score_only`，供读者判断这个偏好有多少证据。

## 17. 验收标准

### 17.1 共用

- 未冻结 evaluator 时 Start 按钮不可用，API 也拒绝启动；
- 运行重载或断线后能从 snapshot + events 恢复同一图谱；
- 用户能停止运行：daemon 在收到 stop 后 5s 内不再启动新候选，run 在 90s 内进入 `cancelled`（见 13.1）；
- 冻结契约被篡改时，评测以 `evaluator_contract_drift` 失败，不会静默使用新契约；
- 事件重复上报同一 `client_event_id` 不产生新 `seq`；
- 旧版本的 WS 事件或 snapshot 响应被丢弃，画布不回退到旧图；
- 每个节点都有确定状态、父代关系、评分、成本和选择原因；
- Web 与 Desktop 使用 shared view，不复制业务组件；
- 不展示私有思维链。

### 17.2 答案进化

- 用户只输入问题时，系统能生成可编辑 rubric 草案；
- rubric 确认后冻结并用于全部候选和最终答案；
- 至少产生 Elite、Repair、Challenger、Diverse 四类分支；
- Crossover 能接收匿名压缩互补摘要；
- 最终答案必须重新评测并展示未解决项。

### 17.3 JIT 式每题 harness 进化

- 隐藏答案 sentinel 不出现在 evolver query、trace bundle、WebSocket、日志、artifact 或页面；
- verifier 新增未知字段时默认不进入 safe feedback；
- 默认档位下不回传逐轮精确通过数：反馈中的通过量为桶化值，且每轮数值字段不超过 6 个（见 6.4.1）；
- `metrics_precision = exact` 时页面必须展示高带宽自适应搜索说明；
- evolver 只能修改 allowlisted harness 根目录；
- 当前任务的 verifier reward/通过判定是首要选择指标；多 rollout 时通过稳定性和成本用于同分排序，TIMEOUT 算失败；
- 单任务运行显示“当前任务专用，不代表跨任务能力提升”的 scope 标签；
- verifier 可拆分时，搜索反馈与最终盲验使用隔离的隐藏样例、随机种子或扰动；不可拆分时必须显示自适应搜索说明；
- 上游 JIT 行为与 Multica 的低 reward repair 扩展在 UI 和事件中明确区分；
- 至少支持 `select_then_execute` 和 `shortlist_then_execute`；
- 最终能下载胜出 task-specific harness，并能回放任一候选版本；
- 保存为模板前会移除题目常量、隐藏答案痕迹和 verifier 信息。

## 18. 默认产品决策

1. 页面属于 Evolution Center，但使用独立子路由和全屏画布。
2. 两种模式共享 run、图谱、事件、artifact、成本和权限基础设施，使用不同 orchestrator。
3. 答案进化默认 `standard` 预算 16 个候选。
4. 每题 harness 默认优化当前任务的 verifier reward/通过判定；多 rollout 时以通过稳定性作为次级指标。
5. 系统可自动生成评分规则，但必须在开始前冻结。
6. DeepSeek V4 Flash 作为可选 preset，不写死为平台唯一模型。
7. reward-only 的答案和 verifier 采用 capability 隔离与字段白名单反馈。
8. 每题 harness 默认 `N=4`、shortlist `K=2`、最多 2 轮 reward-only feedback。
9. 每题 harness 默认 scope 为 `run_only`，不得标记为“通用解题器已提升”。
10. 持久化、代码与路由统一使用 `problem_evolution` / `problem-evolution` 前缀，不复用既有 `evolution_*` 命名空间。
11. 第一阶段不执行用户提供的 verifier 代码；只支持内置 evaluator。
12. 第一阶段不改动 `packages/views/research/`，本能力自建薄画布。
13. 事件 `seq` 由 Server 分配，`graph_version` 由 Server 拥有并用于丢弃乱序更新。
14. reward-only 反馈默认桶化、低带宽，`exact` 档位需显式开启并在页面标注。
15. 停止与预算耗尽走同一条 `stopping` 路径，时限为固定可测数值。
16. 模式 B 全程置于 feature flag 之后，依赖独立的 secret 子系统 PR。
17. 进化算法以外部可执行程序形式接入（见 §19），Multica 不在仓库内重写算法。

## 19. 外部自进化程序的接入方式

### 19.1 选型结论

进化算法不进入 Multica 仓库。它作为**外部可执行程序**被 daemon 启动，通过文件 + stdout NDJSON 与 Multica 交换数据。

仓库已有四条可选通路：

1. **daemon capability + heartbeat 意图**：Server 建运行意图 → daemon 心跳领取 → 本机执行 → 结果回调。先例是 memory curation（`server/pkg/protocol/messages.go` 的 `DaemonHeartbeatPendingMemoryCuration`、`server/internal/daemon/memory_curation.go`）。daemon 本身已经是"起子进程并解析输出"的执行器（`server/pkg/agent/` 下所有 provider CLI 都是 `exec.CommandContext`）。
2. **共享 sandbox 节点 + `exec` 作业**：`sandbox_node` / `sandbox_node_token` / `sandbox_workspace_binding` / `sandbox_instance` / `sandbox_job`（migration 140），连接器 `server/cmd/multica/cmd_sandboxd.go` 通过外向 WSS/HTTPS 领取作业并 `docker exec` 执行。具备镜像隔离、资源 `limits`、workspace 绑定、job token 和快照。
3. **外部服务直连 HTTP API**：先例是 `db_bridge/`（独立 Python 服务 + `multica_auth.py`）与 `mf_cli/`。
4. **在 Go 中重写算法**：语义最干净，但算法仍在快速迭代，重写会立即过期。

**结论**：控制面用通路 1，不可信执行用通路 2，通路 3 仅用于算法自测阶段，不采用通路 4。

这同时修订 0.1.2：用户提供的 verifier 代码不是"永久不支持"，而是"只允许在 sandbox 节点内执行"；第一阶段仍只上内置 evaluator，把 sandbox 执行留给 PR-5/PR-6。

### 19.2 三层分工

| 层 | 位置 | 职责 |
| --- | --- | --- |
| 控制面 | Go Server（in-tree） | run/候选/事件/评分契约持久化、契约冻结与 hash 校验、`seq` 分配、`graph_version`、WS invalidate、权限、配额、审计 |
| 编排面 | daemon capability `problem_evolution_v1`（in-tree，薄） | 领取意图、准备工作目录与输入、启动外部程序、读取 NDJSON 并转发、超时、停止信号、zombie recovery、artifact 上传 |
| 算法面 | 外部程序（独立 repo） | 候选生成、评分调用、修复、选择、harness 生成 |

编排面不含任何进化算法；算法面不访问 Multica 数据库、不调 Multica HTTP API、不认识 workspace 概念。

### 19.3 调用契约

daemon 以子进程方式启动外部程序：

```bash
<evolver_path> --input <workdir>/input.json --workdir <workdir>
```

- `<evolver_path>` 由 daemon 配置提供（与 `Config.Agents[provider].Path` 同样的配置风格），不由 Server 下发，避免 Server 指定任意可执行路径；
- `<workdir>` 由 daemon 创建，per-run 唯一，进程只允许在其中写入；
- 进程环境经过清洗：不注入隐藏答案、不注入 Multica token、不注入 secret capability；
- stdout 只允许 NDJSON 事件；stderr 视为日志，按行截断后进入 diagnostic log；
- 一次调用完成一个"批"（默认一代候选）。多代由 daemon 多次调用，reduce 状态由 Multica 持有，外部程序不做跨调用状态假设。

#### 19.3.1 `input.json`

```json
{
  "schema_version": 1,
  "run_id": "0f3a...",
  "mode": "solution",
  "generation": 0,
  "problem": {
    "statement": "……题目正文……",
    "artifact_type": "markdown",
    "constraints": ["必须给出复杂度分析"],
    "attachments": [
      { "name": "data.csv", "path": "inputs/data.csv", "content_hash": "sha256:…" }
    ]
  },
  "evaluator": {
    "contract_id": "9c21...",
    "content_hash": "sha256:…",
    "kind": "builtin_deterministic",
    "dimensions": [
      { "dimension_id": "correctness", "weight": 0.5, "hard": true, "criteria": "……" },
      { "dimension_id": "coverage", "weight": 0.5, "hard": false, "criteria": "……" }
    ],
    "pass_threshold": 0.8,
    "invoke": {
      "transport": "cli",
      "command": ["multica", "problem-evolution", "evaluate"],
      "input_path": "eval/request.json",
      "output_path": "eval/result.json"
    }
  },
  "budget": {
    "candidates": 4,
    "max_parallel": 2,
    "candidate_timeout_seconds": 600,
    "batch_timeout_seconds": 1800
  },
  "model": {
    "provider": "codex",
    "model": "gpt-5-codex",
    "thinking_level": "medium"
  },
  "feedback": {
    "parents": [
      {
        "candidate_id": "c-parent-1",
        "score_total": 0.71,
        "dimension_summary": [{ "dimension_id": "coverage", "score": 0.4 }],
        "failure_class": "missing_requirement",
        "change_summary": "……"
      }
    ],
    "shared_weak_dimensions": ["coverage"],
    "policy": { "bandwidth": "bucketed" }
  },
  "output": {
    "artifact_dir": "artifacts",
    "candidate_manifest": "candidates.json"
  }
}
```

约束：

- `evaluator.invoke` 是**外部程序调用评测的唯一入口**。外部程序不实现评分，只把候选写入 `input_path` 并读取 `output_path`；这样隐藏答案永远不经过外部程序；
- 第一阶段 `transport` 只支持 `cli`，由 daemon 提供该命令的 wrapper；后续新增 `sandbox_job` transport 对应通路 2；
- `feedback.parents` 已经是投影后的安全摘要，外部程序不得期待原始日志；
- `attachments[].path` 与 `output.*` 均为相对 `<workdir>` 的路径。

#### 19.3.2 stdout 事件（NDJSON）

每行一个 JSON 对象，字段：

```json
{ "schema_version": 1, "client_event_id": "uuid", "event_type": "candidate_started", "candidate_id": "c1", "at": "2026-08-27T06:00:00Z", "payload": {} }
```

第一阶段的 `event_type` 白名单：

| event_type | 必需 payload | 语义 |
| --- | --- | --- |
| `batch_started` | `{ "planned_candidates": 4 }` | 本次调用开始 |
| `candidate_started` | `{ "lane": "diverse", "operator": "baseline", "parent_ids": [] }` | 新候选开始 |
| `candidate_artifact` | `{ "kind": "answer", "relative_path": "artifacts/c1.md", "content_hash": "sha256:…", "size_bytes": 1234 }` | 候选产物就绪 |
| `candidate_scored` | `{ "score": { … §9.3 结构 … }, "behavior_profile": { … } }` | 评测完成 |
| `candidate_failed` | `{ "failure_class": "timeout", "message": "有界短文本" }` | 候选失败 |
| `candidate_finished` | `{ "status": "selectable" }` | 候选终态 |
| `progress` | `{ "note": "有界短文本", "tokens": 0, "cost": 0, "model_calls": 0 }` | 进度与用量；`model_calls` 是外部进化程序上报的累计模型调用数 |
| `batch_finished` | `{ "best_candidate_id": "c1", "produced": 4 }` | 本次调用结束 |

规则：

- `client_event_id` 由外部程序生成（UUIDv4），daemon 与 Server 用它做幂等；`seq` 由 Server 分配（见 9.6）；
- 未在白名单中的 `event_type` 被 daemon 丢弃并计数，不透传；
- 单行上限 64 KiB，超限截断并转为 `progress` 事件记录截断；
- `message` / `note` 类自由文本上限 1 KiB，且经过 `secretscoped` 过滤后才落库；
- 事件不携带模型思维链、完整 prompt 或 evaluator 原始输出。

#### 19.3.3 产物契约

- 候选产物写入 `<workdir>/<output.artifact_dir>/`，通过 `candidate_artifact` 事件声明相对路径与 `content_hash`；
- 外部程序**不直接调用 Multica API 上传**，由 daemon 校验 hash 后上传为 `problem_evolution_artifact`，从而不绕过权限层；
- 声明的路径必须位于 `artifact_dir` 之内（daemon 侧做路径逃逸校验），符号链接一律拒绝；
- 单产物默认上限 16 MiB，单批总量默认上限 128 MiB，超限该候选标记 `artifact_too_large`。

#### 19.3.4 退出码

| 退出码 | 含义 | run 侧处理 |
| --- | --- | --- |
| `0` | 本批正常完成 | 按事件推进 |
| `10` | 本批全部候选失败，但程序本身正常 | 记录 `all_candidates_failed`，按停止条件判断是否继续 |
| `20` | 输入不合法（`input.json` 无法解析或 schema 不匹配） | run 直接 `failed`，`failure_reason = evolver_input_rejected` |
| `30` | 基础设施异常（模型不可用、评测入口不可用） | 可重试，重试上限由 `stop_config` 控制 |
| 其他非 0 | 未知错误 | run `failed`，保留 stderr 尾部 |
| 被信号终止 | 超时或停止 | 按 13.1 的 `stopping` 流程处理 |

### 19.4 停止与超时

- daemon 收到 stop 后先向子进程发 `SIGTERM`，`graceful_drain = 60s` 后 `SIGKILL`（Windows 走既有 `force_kill_process.go` 路径）；
- `batch_timeout_seconds` 由 daemon 用 `context.WithTimeout` 强制，语义与 memory curation 的 `MemoryCurationRunTimeout` 一致；
- 每个 runtime 同时只允许一个 run 的批次在执行（照抄 `activeCurationRuns` 的单飞模式）。

### 19.5 版本钉死与可重放

run 记录必须保存：

- 外部程序的版本标识（`--version` 输出或镜像 tag）与 `content_hash`（可获得时）；
- `input.json` 的 `schema_version` 与 evaluator `content_hash`；
- 模型 provider/model/thinking level；
- 事件白名单版本。

缺少版本标识时 run 仍可运行，但报告中标注"不可精确重放"。参考 `skills-lock.json` 的钉版做法。

### 19.6 落地顺序

1. **契约先行（不改 Multica 仓库）**：外部 repo 实现薄壳——读 `input.json`、调用已有进化循环、打 NDJSON、按 19.3.4 返回退出码。用手写 `input.json` 自测。
2. **Multica PR-1**：最小切片（4 张表 + capability + 启动外部程序 + 静态画布）。
3. **后续 PR**：完整谱系、secret 子系统、sandbox 内 verifier、模式 B。

第 1 步与第 2 步可并行，唯一耦合是本章的契约。

## 20. 模式 C：持久 harness 工程化（AHE）

参考 [self-harness / Agentic Harness Engineering](https://github.com/LRM-Teams/self-harness/tree/feature/reward-only-feedback)。

### 20.1 为什么单列一种模式

模式 B 与模式 C 都在“改 harness”，但工作单元不同，因此不能共用骨架：

| 维度 | 模式 B（JIT，按题定制） | 模式 C（AHE，持久工程化） |
|---|---|---|
| 工作单元 | 单题 | 一个任务集，每轮跑 N 个任务 |
| 候选物 | 一次性 harness，用完即弃 | 带版本的 harness 工作区，跨轮继承 |
| 进化循环 | 生成多个 → 选 K 个 → 执行 | `evaluate → analyze → improve` 外层迭代 |
| 反馈 | reward-only（PASS/FAIL + 数值） | per-task reward + trace 摘要（原始 trace 常 >10M token） |
| 停止条件 | 候选数 / 代数 / 成本 | `target_pass_rate` / `max_iterations` / 成本 |
| 成果归属 | 只属于本次 run | 满足 held-out 门槛后可提升为 workspace 级可复用 harness |
| 反过拟合手段 | 盲验隔离 + 有限修复轮 | 预测影响 + 下一轮 flip 证伪 + 回滚，叠加 held-out 任务集 |

模式 C 的证伪机制正是本文档反复要求的“不许自封通用能力提升”的证据形式：每次修改必须预先声明“哪些任务会转 pass、哪些有风险”，下一轮实测 flip 用来判定这条声明成立与否，不成立就回滚。因此模式 C 不需要新的原则，只需要把已有原则落到新的表结构上。

### 20.2 harness 工作区的七个组件

进化对象是 harness 而非模型，工作区按组件切分，每个组件独立成文件并纳入版本控制：

1. `systemprompt.md`
2. `agent.yaml`（agent 配置）
3. `tool_descriptions/`
4. `tools/`（工具实现）
5. `middleware/`
6. `skills/`
7. `sub_agents/`

外加一份长期记忆 `LongTermMemory.md`。平台不解释这些文件的内容，只负责：按组件粒度存储版本、记录每轮 diff、保证可回滚、保证同一版本可以被重新执行。

### 20.3 任务集：只存外部引用

平台**不存题目内容**。任务集实体只记录：

- `source`：外部数据集标识（如 harbor-datasets 的 repo + revision）；
- `dataset_ref`：sandbox 节点上可解析的目录或镜像坐标；
- `task_names`：参与本 run 的任务名单；
- `holdout_task_names`：held-out 名单，见 20.6；
- `rollouts_per_task`：每任务 rollout 次数。

理由与隐藏答案一致：题目与验证材料留在执行侧，平台只保存引用、reward 与摘要。任务内容不进平台数据库，也就不存在“平台侧泄漏题目”这一类风险。

### 20.4 数据表（新增）

命名沿用 `problem_evolution_` 前缀。

#### `problem_evolution_task_set`

| 列 | 说明 |
|---|---|
| `id` / `workspace_id` | 主键与租户 |
| `source` / `dataset_ref` / `dataset_revision` | 外部数据集引用 |
| `task_names` JSONB | 参与任务名单 |
| `holdout_task_names` JSONB | held-out 名单，run 开始后不可修改 |
| `rollouts_per_task` | 每任务 rollout 次数，>=1 |
| `created_at` / `updated_at` | |

#### `problem_evolution_harness_version`

| 列 | 说明 |
|---|---|
| `id` / `run_id` / `workspace_id` | |
| `iteration` | 产出该版本的轮次 |
| `parent_version_id` | 继承关系，构成 harness 谱系 |
| `components` JSONB | 组件名 → artifact 引用 + content_hash |
| `content_hash` | 整个工作区的稳定哈希，用于钉版与去重 |
| `rolled_back` BOOL | 是否因证伪被回滚 |
| `promoted_scope` | `run` 或 `workspace`，默认 `run` |

#### `problem_evolution_iteration`

| 列 | 说明 |
|---|---|
| `id` / `run_id` | |
| `iteration` | 轮次，run 内唯一 |
| `input_version_id` / `evolve_version_id` | 本轮被评测的版本、本轮产出的版本（错位两代） |
| `stage` | `evaluating` / `analyzing` / `improving` / `settled` |
| `pass_rate` / `holdout_pass_rate` | 本轮成绩 |
| `cost` / `tokens` | 成本 |

#### `problem_evolution_task_result`

| 列 | 说明 |
|---|---|
| `id` / `iteration_id` / `run_id` | |
| `task_name` / `rollout_index` | |
| `split` | `search` 或 `holdout` |
| `reward` | 数值 reward |
| `verdict` | `pass` / `fail` / `error` / `timeout` |
| `trace_ref` / `trace_digest_ref` | 原始 trace 与摘要的 artifact 引用；平台不存正文 |

#### `problem_evolution_change_record`

这是模式 C 的核心表：把“声明—证伪”关系落到数据上。

| 列 | 说明 |
|---|---|
| `id` / `iteration_id` / `harness_version_id` | |
| `component` | 被修改的组件 |
| `failure_evidence_ref` | 失败证据（trace 摘要引用） |
| `root_cause` | 根因，文本，长度受限 |
| `fix_summary` | 针对性修改说明 |
| `predicted_pass_task_names` JSONB | 预测会转 pass 的任务 |
| `predicted_risk_task_names` JSONB | 预测有风险的任务 |
| `observed_flips` JSONB | 下一轮实测 flip |
| `verdict` | `pending` / `confirmed` / `refuted` / `inconclusive` |
| `action` | `kept` / `reverted` / `revised` |

`verdict` 与 `action` 由平台在下一轮成绩落库后计算，不接受进化程序自报，理由与模式 A/B 中“选择权归平台”一致。

### 20.5 迭代协议

沿用第 19 章的进程边界：daemon 启外部程序，NDJSON 上报，平台判定。新增事件类型（仍走白名单）：

- `iteration_started`：`{iteration, input_version_hash}`
- `task_result`：`{task_name, rollout_index, split, reward, verdict, trace_ref}`
- `analysis_ready`：`{digest_ref}`（只报引用，不报正文）
- `change_proposed`：`{component, root_cause, fix_summary, predicted_pass, predicted_risk, evidence_ref}`
- `harness_version_ready`：`{iteration, components, content_hash, parent_version_hash}`
- `iteration_finished`：`{iteration, pass_rate}`

平台侧约束：

1. `change_proposed` 缺少 `predicted_pass` 与 `root_cause` 一律拒收——没有可证伪的声明，就没有“证据支撑的修改”。
2. `task_result` 的 `split` 为 `holdout` 时，只允许在 20.6 规定的时机出现；搜索轮出现 holdout 结果即拒收并计入审计。
3. 进化程序不能自报 `verdict` / `action` / `promoted_scope`。
4. trace 正文不入库：只接受 artifact 引用，正文留在执行侧存储。

### 20.6 held-out 与提升门槛

- 任务集划分为 search 与 holdout 两份，`holdout_task_names` 在 run 开始时冻结，之后不可修改（与 evaluator 契约冻结同理）。
- 搜索轮**只能**看到 search 任务的 reward 与摘要；holdout 只在 run 收尾执行一次。
- 允许把胜出 harness 提升为 workspace 级可复用（`promoted_scope = workspace`），但必须同时满足：
  1. holdout pass rate 不低于基线 harness 的 holdout pass rate；
  2. holdout 与 search 的成绩差（过拟合缺口）不超过配置阈值，默认 0.15；
  3. 至少一条 `change_record` 的 `verdict = confirmed`。
- 任一条不满足，只能停留在 `run` 范围。报告里必须显示 search 与 holdout 两个数字，不允许只报搜索成绩。

### 20.7 停止条件（模式 C）

| 条件 | 默认值 |
|---|---|
| `target_pass_rate` | 0.9 |
| `max_iterations` | 10 |
| `max_cost_usd` | 按预算档，standard 档 50 |
| 连续无提升轮数 | 3 |

停止路径复用第 13.1 节的数值：daemon 5s 内停止产出新任务，90s 内 run 必须落到终态。

### 20.8 执行与隔离

- 每个 rollout 在 sandbox 节点的独立环境里执行（对应上游的 E2B sandbox），沿用现有 `sandbox_node` 的 exec job 与 node token；
- 并发上限由 sandbox 节点容量决定，run 侧必须配置 `max_parallel_rollouts`，默认 4，避免把节点打满导致整轮卡死；
- 外部程序拿不到平台凭据，环境变量按 19.3 的清洗规则处理。

### 20.9 实施拆分（在 PR-7 之后）

- **PR-8**：任务集与 harness 版本表 + 外部引用校验 + 版本钉死与回滚。
- **PR-9**：迭代协议（新事件类型、iteration/task_result 落库、并发上限、成本汇总）。
- **PR-10**：change_record 证伪闭环（预测入库、下一轮 flip 归因、`kept`/`reverted` 判定）。
- **PR-11**：held-out 隔离与提升门槛（冻结名单、收尾单次盲测、workspace 级提升审批）。
- **PR-12**：画布与报告（harness 版本谱系、组件 diff、search/holdout 双数字、变更—证伪时间线）。

### 20.10 当前实现补充

模式 C 的第一版已落地：

- `task_harness_persistent` 运行模式、外部任务集引用与 held-out 名单冻结；
- harness version、iteration、task result、change record 四类持久化数据；
- `iteration_started`、`task_result`、`analysis_ready`、`change_proposed`、
  `harness_version_ready`、`iteration_finished` 事件及服务端投影；
- 服务端计算变更是否被下一轮确认/证伪，进化程序不能自报结论；
- 只有同时具备 held-out 成绩、过拟合缺口不超过 0.15、至少一条 confirmed
  变更时，才能把 harness version 提升到 workspace 范围；
- 页面可创建模式 C 运行，并配置外部任务集、搜索任务和盲验任务。

计费侧已接入本仓现有的云 billing 代理边界：

- 若配置了云 billing，start 前调用余额接口做预检；
- 本仓新增 `problem_evolution_usage` 归因表，按 run、provider、model 记录累计
  model calls、input/output tokens 与 cost，并用幂等 upsert 防止重试重复计费；
- 自托管且未配置云 billing 时，仍使用本地模型调用/金额预算；
- 当前云端仓库没有公开的扣费/usage-ingest 契约，因此不会凭空调用未定义的
  billing endpoint；云端正式扣款由 billing 服务消费该归因记录或后续明确契约。
