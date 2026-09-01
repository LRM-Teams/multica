# EvoAgentBench / PAST-Bench：Graph Agent Memory 与 Legacy Memory 对比评测规格

- 日期：2026-08-25
- 状态：设计已确认；待实现外部 Harness 与 Smoke 验证
- 评测对象：Multica `Graph Agent Memory On` 与 `Legacy Memory On`
- 基准：EvoAgentBench、PAST-Bench
- 首要约束：优先且尽量只调用 Multica 现有 API；默认不修改 Multica 产品代码
- 前置规格：Graph Memory Agent Mode、Graph Memory Reviewer、Graph Memory Scope、Agent Memory
- 参考调研：仓库根目录的四基准对比文档

## Problem Statement

团队需要定量比较 Multica 开启 Graph Agent Memory 与使用 Legacy Memory 时的任务精度、记忆精度、Token 消耗和时间成本，并使用 EvoAgentBench 与 PAST-Bench 覆盖两种互补能力：前者测历史任务中程序性能力向相关新任务迁移，后者测持久状态在跨会话中的真实改善及其机制证据。

这不是一个普通的两组任务成功率测试。Graph Agent Memory 与 Legacy Memory 的产品路径并非只替换存储后端：Graph Agent Mode 会 provision 独立的频道 Memory Agent，增加模型调用、异步探索、Graph ingest/consolidation/Dive、频道协作和额外 Token；Legacy Memory 则主要通过已有 agent/project/channel/workspace 记忆加载和渲染提供上下文。因此主实验只能回答“完整生产系统哪个表现更好、成本如何”，不能把全部差异归因于 Graph 数据结构或检索后端。

评测还必须处理三个协议风险：

1. PAST-Bench 要求每个 evaluation episode 从 fresh session 开始，同时只允许持久化状态跨会话保留；普通频道历史或 provider session 泄漏会伪造 memory 收益。
2. EvoAgentBench 要求 evolution state 只来自 train 侧；test 侧不能使用 curator-side Ability 标签进行路由，也不能让前序 test task 的写回污染后序 test task。
3. Graph Memory 的 Memory Agent、整理、Dive 和失败重试成本必须完整计入。只统计主 Agent 会系统性低估 Graph 的 Token 和时间。

最新产品约束是避免修改 Multica。评测应首先通过外部 Harness 编排现有 workspace、project、channel、thread、Graph profile、channel mode、consolidation、lineage、usage 和消息 API 完成。若当前公开 API 无法同时满足 fresh-session、read-only/off、状态冻结或逐 run telemetry，Harness 必须在 Smoke 阶段停止或将结果明确降级为“协议有偏差的系统级评测”，而不是暗中修改 Server 或把近似结果宣称为官方兼容分数。

## Solution

构建一个独立于 Multica 产品代码的评测 Harness。Harness 固定 EvoAgentBench 与 PAST-Bench 上游版本，将官方任务转换为 Multica 频道交互，创建隔离的 workspace/project/channel，配置 Graph Agent 或 Legacy Memory，驱动 train/learn/update/eval/control 序列，等待可观测的系统稳定点，采集主 Agent、Memory Agent 和后台记忆处理成本，运行官方或等价 grader，并输出可复现的逐任务记录和汇总报告。

主实验全量比较 `Legacy On` 与 `Graph Agent On`，每组运行 3 个固定但不同的 seed。PAST-Bench 另在四个能力类别中各预注册 2 个 family，共 8 个 family，增加 persistence-off 归因子集。评测包含两条轨道：

- **自然学习轨**：两套系统接收相同 train/learn/update 输入，各自决定写入、整理和召回什么，测端到端系统效果与成本。
- **等价预置轨**：只使用允许的 train/learn/update 数据生成 canonical memory，冻结 provenance 和内容 hash，再通过现有 API 向两套系统提供语义等价状态，尽量隔离检索与应用能力。若现有 API 不能进行不泄漏 test 的等价预置，该轨在 Smoke 阶段标记为 unsupported，不得用不等价手工注入代替。

Graph 组固定使用 `graph_memory_mode=agent`；不运行 Graph Inject 消融。Graph 的 TTT、recall TTT 和 consolidation TTT 关闭，探索/整理并行度固定为 1。主 Agent 在两组中使用完全相同且明确锁定的 provider、model、thinking、instructions、tools、附件权限、超时和重试策略；Graph Memory Agent 使用计划部署的明确版本模型，其额外调用作为 treatment 成本。

结果采用描述性报告，不设置统一胜负阈值，也不合成任意加权总分。任务质量、memory 机制质量、Token 和时间分别呈现；仅报告 Pareto 关系、paired difference、置信区间和 win/tie/loss，不事后定义“胜出”。

## User Stories

1. 作为评测负责人，我希望用 EvoAgentBench 全量 supported split 比较两套 Memory 系统，以便知道历史任务经验能否迁移到实例不重复的新任务。
2. 作为评测负责人，我希望用 PAST-Bench 全量 26 个 family、204 个 episode 比较两套系统，以便覆盖 Memory、Procedural Reuse、Information Gathering 和 Update。
3. 作为产品负责人，我希望主实验比较完整 Graph Agent Memory 与完整 Legacy Memory，以便结果反映真实部署系统而不是人为裁剪的后端。
4. 作为研究人员，我希望报告明确声明主实验不是纯 backend 因果实验，以免把 Memory Agent 的规划和额外推理错误归因给 Graph 数据结构。
5. 作为研究人员，我希望不使用 Graph Inject 作为额外实验臂，以便控制本轮范围与成本。
6. 作为研究人员，我希望每个配置运行 3 个固定但不同的 seed，以便估计模型采样和工具波动。
7. 作为统计分析人员，我希望同一任务在 Graph 与 Legacy 间配对，并交错两组运行顺序，以便降低服务负载、网页变化和时间漂移偏差。
8. 作为成本管理员，我希望 Graph Memory Agent、ingest、consolidation、Dive、TTT、重试和失败调用的 Token 全部计入，以免 Graph 成本被低估。
9. 作为成本管理员，我希望 input、output、cache read、cache write Token 分项报告，以便理解成本来自上下文、生成还是缓存。
10. 作为性能工程师，我希望记录端到端 wall-clock、queue、主 Agent、Memory Agent 和 memory-ready 等待时间，以便定位延迟来源。
11. 作为性能工程师，我希望时间是次要指标且主实验低并发，以便优先获得可解释的精度和 Token 数据。
12. 作为 PAST-Bench 使用者，我希望 evaluation episode 的易失上下文被清空，以便提升只能来自持久化状态而不是旧聊天历史。
13. 作为 PAST-Bench 使用者，我希望同一 family 的 learn/update 状态能在后续 episode 中保留，以便测试真实跨会话改善。
14. 作为 PAST-Bench 使用者，我希望 cold score 只作校准，persistence-off 才作为 matched baseline，以便遵守基准口径。
15. 作为 PAST-Bench 使用者，我希望每类选择 2 个预注册 family 做 On/Off 归因，以便在预算可控时获得最小归因证据。
16. 作为 PAST-Bench 使用者，我希望同时报告 task score、self-evolution gap 和 Mech，以便区分“做对了”与“通过预期持久化路径做对了”。
17. 作为 EvoAgentBench 使用者，我希望所有 evolution state 只来自 528 个 train task，以免 test 泄漏。
18. 作为 EvoAgentBench 使用者，我希望 267 个 test task 不把结果写回 evolution state，以免前序测试题帮助后序测试题。
19. 作为 EvoAgentBench 使用者，我希望 curator-side test Ability 标签只用于运行后的离线评分，绝不用于执行时路由，以免把诊断上界当作可部署方法。
20. 作为模型负责人，我希望主 Agent 两组完全同模、同 thinking、同 tools 和同 prompt，以便唯一主要 treatment 是 Memory 系统。
21. 作为模型负责人，我希望所有模型写入完整版本而不是 `latest` 或 provider 默认，以便结果可复现。
22. 作为 Graph Memory 负责人，我希望 Graph 整理和探索并行度为 1，以便成本和行为稳定可解释。
23. 作为 Graph Memory 负责人，我希望在正式任务前验证频道处于 effective Agent mode 且 managed Memory Agent 为 active，以免把 blocked/empty Graph 当成 Graph 结果。
24. 作为 Legacy Memory 负责人，我希望 Legacy workspace 中 managed Graph Memory Agent 为 inactive，以免两条路径混用。
25. 作为 Harness 开发者，我希望优先使用现有 Multica API 作为唯一产品 seam，以便不把 benchmark 逻辑耦合进 Multica Server。
26. 作为 Harness 开发者，我希望每条请求、任务、episode、seed、backend、track 和模型配置都有稳定关联 ID，以便追踪和重跑。
27. 作为 Harness 开发者，我希望状态隔离使用独立 workspace/project/channel，而不是依赖清理共享生产状态，以便避免跨臂污染。
28. 作为 Harness 开发者，我希望 Smoke 验证频道会话边界、Graph scope、Legacy scope 和跨 episode 保留行为，以便在大规模运行前发现协议不兼容。
29. 作为 Harness 开发者，我希望缺失 Token usage 被标记为 unknown 而不是 0，以免产生虚假的成本优势。
30. 作为 grader 维护者，我希望 grader 与 worker/Memory Agent 使用独立固定模型，以免自评和模型族偏差。
31. 作为 grader 维护者，我希望 pairwise 输出交换 A/B 顺序各判一次，以便检测顺序偏差。
32. 作为审计人员，我希望开放式评分随机人工审计 10%，且每个 benchmark 至少 50 个样本，以便估计 judge-human agreement。
33. 作为审计人员，我希望 grader 看不到 backend、workspace、Agent 名称和 memory trace，以便保持盲评。
34. 作为安全管理员，我希望所有代码执行、SWE 修复和网页研究运行在无生产密钥的一次性 staging/sandbox 中，以免 benchmark 影响生产或泄露数据。
35. 作为安全管理员，我希望不使用生产 workspace、生产 Memory DB 或真实用户数据，以便满足隔离要求。
36. 作为复现人员，我希望固定上游 commit、数据 manifest、容器 digest、grader prompt hash 和模型版本，以便复现实验。
37. 作为复现人员，我希望保存原始 JSONL、任务级表、配置 manifest、memory snapshot/version、统计报告和复现实验命令，以便独立核验。
38. 作为项目负责人，我希望先 Smoke、再单 seed Pilot、最后补齐 3 seeds，以便尽早暴露成本或协议问题。
39. 作为项目负责人，我希望 Smoke 和 Pilot 后分别看到预计 Token、费用、耗时和失败率，并在确认后才继续，以便控制外部模型开销。
40. 作为 Multica 维护者，我希望评测默认不修改 Multica 产品代码，以免为了 benchmark 引入长期维护接口。
41. 作为 Multica 维护者，我希望现有 API 若不足，Harness 明确报告 unsupported/protocol deviation，而不是绕过权限、直接改数据库或伪造官方兼容性。
42. 作为读者，我希望最终报告区分任务精度、memory 精度和迁移/持久化收益，以免把不同概念都称为 accuracy。
43. 作为读者，我希望 Evo 四领域、PAST 四能力分别报告原生指标和 macro-average，以免大领域按样本数支配总分。
44. 作为读者，我希望看到 Graph 与 Legacy 的 paired difference、95% bootstrap CI 和 win/tie/loss，以便判断差异稳定性。
45. 作为决策者，我希望结果只做描述性比较，不用事后加权总分或临时上线阈值，以便避免指标选择偏差。

## Acceptance Criteria

1. Harness 不修改 Multica 产品代码、不直连或写入 Multica 数据库、不绕过现有权限模型；任何例外都需要新的显式批准和独立规格。
2. EvoAgentBench 固定官方论文版本对应的 supported split：528 train、267 test；记录四领域 task IDs 和上游 commit。
3. PAST-Bench 固定 26 family、204 episode 的官方 manifest；记录四能力 family/episode IDs 和上游 commit。
4. 主实验包含 Legacy On 与 Graph Agent On 的全量自然学习轨，均运行 3 seeds。
5. 等价预置轨只在现有 API 能证明语义等价、provenance 仅来自允许学习数据时运行；否则明确标为 unsupported。
6. PAST 归因子集在看结果前从四类中各固定 2 个 family，包含普通和困难/控制 family；完整 episode 序列均运行。
7. 主 Agent 两组的 provider/model/thinking/instructions/tools/MCP/custom args/env、上下文限制、超时、重试、附件和任务顺序一致。
8. Graph 使用 Agent mode；Graph Inject 不进入本规格；recall TTT、consolidation TTT 与总 TTT 关闭，相关并行度固定为 1。
9. Graph managed Memory Agent 的实际 runtime/model/thinking 必须能通过现有 API 解析并锁定；空值回落 provider 默认时 Smoke 失败。
10. 每个实验单元使用隔离资源；无 benchmark、backend、track、seed 或 family 间状态污染。
11. Smoke 必须证明每个 PAST evaluation episode 不继承易失 provider session 或旧频道上下文，同时能读取允许的持久状态。
12. Smoke 必须证明 Evo test 不写回 train snapshot；无法通过现有 API 实现时，不得宣称严格 Evo ability-transfer 协议兼容。
13. persistence-off 必须禁止访问同 family 的既有持久状态；若只能近似为空 workspace/new channel，报告必须标出偏差。
14. Graph/Legacy 的 API 请求、运行状态、输出、grader 结果和 usage 以不可变 run ID 关联。
15. Token 总成本包含主 Agent、Memory Agent、记忆整理、judge、失败和重试；无法归属的 usage 为 unknown。
16. 时间报告至少包含端到端 p50/p95、失败率和 timeout rate；不进行高并发吞吐排名。
17. Evo 按四领域报告原生分数；PAST 按 family 后按能力 macro-average，并报告 Mech。
18. 不生成跨 Evo 与 PAST 的统一总分；不预设 Graph/Legacy 胜负阈值。
19. 自动 judge 盲评且固定版本；pairwise 交换顺序；人工审计满足 10% 且每基准至少 50 样本，或采用已确认的降级审计范围并显式说明。
20. Smoke 出现状态串扰、模型无法锁定、usage 账目不闭合、grader 泄漏或稳定点不可判定时，停止进入 Pilot。
21. Pilot 和 Full 均在估算 Token、费用、耗时和失败率得到用户确认后启动。
22. 结果报告必须列出所有协议偏差，并根据偏差将结果标为 official-compatible、partially-compatible 或 system-comparison-only。

## Implementation Decisions

### 1. 产品边界与模块

- 新增的是**外部评测 Harness**、benchmark adapter、grader adapter、usage collector、统计分析和报告生成器；Multica Server、Daemon、数据库 schema 和 UI 默认不改。
- 现有 Multica HTTP API 是唯一产品 seam。Harness 不依赖私有 helper、内部表或直接文件系统 mutation。
- Harness 由四层组成：资源编排层、benchmark driver、观测/计量层、离线评分与报告层。层间通过稳定 run manifest 和 JSONL 事件合同连接。
- 不发布新的 Multica 公共 API。若 Smoke 证明现有 API 缺失硬性能力，先输出 capability gap 文档，等待独立批准，不在本规格中顺带修改产品。

### 2. 当前可复用 API 能力

Harness 优先组合以下已存在能力：

- workspace Graph profile 的读取与 CAS 更新；
- Graph/Legacy memory type 和 Graph `agent|inject` workspace mode；
- group channel 创建、project 绑定、成员管理、消息和 thread；
- channel Graph mode override、managed Memory Agent active/blocked/inactive 状态和 reset；
- Graph status、manual consolidation、consolidation run 状态、audit、channel lineage 和 citation；
- Agent runtime/model/thinking 配置读取；
- runtime/workspace/agent usage 聚合；
- 任务、频道消息和 Agent 输出轮询。

当前 checkout 的公开 channel message contract 未验证存在 evaluation ID、episode ID、per-message `read_write|read_only|off`、显式 fresh-session 或 Graph evaluation-ready endpoint。现有 `force_fresh_session` 是内部任务能力，但未作为普通 channel message 公共控制暴露。规格因此不假设这些能力存在。

### 3. 现有 API 优先的兼容性策略

Harness 在 Smoke 中建立 capability matrix：

- **Fresh session**：验证是否能通过现有公开 task/issue/channel 生命周期在保留持久 memory 的同时清空 provider session 和短期频道上下文。
- **Graph scope**：验证 project-bound channel 的物理 Graph 路由、GraphView 可见性和新 session 的跨 episode读取；“同一物理 project graph”不能自动等同于跨频道可见。
- **Legacy scope**：验证 project/channel/workspace/agent memory 在所选 session 拓扑中的实际加载范围。
- **Read-only**：验证 test/eval 输出不会写回后续可见状态。
- **Off**：验证 persistence-off 条件不能访问 family 的历史状态。
- **Ready barrier**：验证现有 Graph status/consolidation/channel 状态与可见消息能否构成稳定、可重复的 quiescence 判定。
- **Usage closure**：验证主 Agent、Memory Agent 和后台记忆调用均可通过现有 usage API做前后差分归属。

只有所有硬性能力通过，才标为 official-compatible。若通过新 workspace/channel 等外部编排只能近似实现，则标为 partially-compatible。若 fresh-session、freeze 或 usage closure 失败，仍可运行经用户确认的系统级对比，但不得报告官方 PAST self-evolution gap 或严格 Evo transfer 结论。

### 4. 实验矩阵

主矩阵：

| 轨道 | Legacy | Graph |
| --- | --- | --- |
| 自然学习 | Legacy On | Graph Agent On |
| 等价预置 | Legacy On + canonical state | Graph Agent On + canonical state |

- 每格 3 seeds。
- 全量 Evo 与全量 PAST 只比较两个 On 组。
- PAST 预注册 8-family 子集增加 Legacy Off 和 Graph Off；Off 不扩展到全量。
- 不增加 Graph Inject，不运行 curator-side Anchor Skill 作为可部署 baseline。

### 5. Benchmark 协议

#### EvoAgentBench

- 使用 2,605 source pool 中经 Ability Graph 支持性筛选后的官方 528 train / 267 test 主切分。
- 四领域原样保留：BrowseComp-Plus、LiveCodeBench、SWE-Bench Verified、GDPval knowledge work。
- Train state 只能由 train prompts、轨迹、工具结果和 verifier outcomes构建。
- Test Ability label 只供离线分析，不进入 prompt、memory、route 或检索 query。
- Test 阶段使用冻结 train state；若现有 API 无法 read-only，则每个 test 必须从同一冻结外部快照恢复。若现有 API 既无 read-only 也无可验证快照恢复，严格协议在 Smoke 失败。
- 结果分别报告网页研究 LLM judge binary score、算法 pass@1、SWE resolve rate、GDPval reference-based pairwise score，以及四领域 macro-average。

#### PAST-Bench

- 使用 26 families / 204 episodes：Memory 5/41、Procedural Reuse 8/64、Information Gathering 6/48、Update 7/51。
- Family 内保持 cold、learn、update、evaluation、control 的官方顺序；每个 episode 是新的易失 session。
- persistence-on/off 使用相同 prompt、grader、tool stack、seed、模型和任务顺序。
- 主 task score 保持 `Safety × (0.80 × Completion + 0.20 × Robustness)`；crash/missing 为 0。
- 报告 `Graph On − Legacy On` 的系统差异；8-family 子集另报告各 backend 的 persistence gap。若 Off 不是严格 matched，不使用官方 `Δ` 名称。
- Mech 报告 write precision、recall accuracy、update correctness、retention horizon 与低污染证据；明确它是路径一致性证据，不是因果必要性证明。

### 6. 多轮任务与频道拓扑

- 资源命名必须包含 benchmark、backend、track、seed、domain/family 和 run ID。
- PAST 以 family 为隔离单元；同 family 的持久状态必须保留，family 之间不得共享。
- Evo 以 backend × track × seed 为学习单元；train 可共享 state，test 必须读取同一冻结 state。
- 一个 episode/task 内的多轮对话使用同一 thread；下一 episode/task 必须获得新的易失 session。
- 不假设“新 thread”自动清空 provider session，也不假设“新 channel”自动保留 Graph 可见状态；两者都由 Smoke 以外部行为验证。
- 不在频道中发送 grader 分数、参考答案、test Ability 标签、backend 名称或分析结论；这些只保存在 Harness 侧。
- Graph Agent 的公开消息不直接作为被评分答案；grader 只接收主任务 Agent 的最终交付物。两组采用相同 quiescence 规则，Graph Memory Agent 的 Token/时间仍计入成本。
- channel reset 不能默认用作 episode 边界；必须先验证它是否仅重置内部 cursor/run state、是否保留底层 Graph、以及是否跳过未消费消息。

### 7. Graph 与 Legacy 配置

- Graph workspace 首次启用使用 empty-start/no-fallback 确认；切换和更新遵守 profile CAS。
- Graph effective mode 必须为 `agent`，channel 为 group，managed Memory Agent status 必须为 active，runtime 必须在线且支持所需 steering 能力。
- Graph `ttt_enabled=false`、`recall_ttt_enabled=false`、`consolidation_ttt_enabled=false`，探索并行度为 1；其他 quota 在 Smoke 前冻结。
- Graph profile 中声明的 Memory Agent model/thinking 必须通过实际 managed Agent runtime 配置复核；不能只相信 profile 展示值。
- Legacy workspace 的 `memory_type=legacy`，不得存在 active managed Graph Memory Agent。
- 两组使用不同 workspace 和 project；不在同一 workspace 中切换 memory type后复用旧状态。

### 8. 模型、工具和环境冻结

- 主 Agent 模型两组一致且显式固定，不允许空 model 下沉 provider 默认。
- Graph Memory Agent 使用实际部署模型并显式记录；它不是 Legacy 的隐藏对等调用，而是 Graph treatment 的组成部分。
- Judge 使用独立模型、固定 prompt hash、temperature、seed 和完整 model version。
- 两组工具权限、网页搜索、shell、文件、MCP、上下文窗口、最大轮次、timeout 和 retry一致。
- BrowseComp 优先使用固定网页快照；若只能 live web，则 paired interleaving并记录 URL、时间、HTTP状态和搜索摘要。
- LiveCodeBench/SWE 使用固定仓库 commit、测试镜像、解释器、编译器和依赖版本。
- 代码运行在一次性 sandbox，无生产密钥、用户目录、生产数据库挂载或非必要网络。

### 9. 调度、失败和重试

- 每个实验臂主并发为 1；任务按 paired block 交错 Graph/Legacy 先后顺序。
- Graph 整理/探索并行度为 1；judge 使用固定低并发。
- 不进行高并发 throughput 压测。
- API 5xx、网络中断、队列故障最多基础设施重试 2 次；错误答案、错误工具使用和错误 memory 召回不得重试。
- 所有失败尝试的 Token 和时间计入成本；同时报告 first-attempt 与 after-retry success。
- crash、missing output 和 benchmark timeout 按官方规则给分；usage 缺失标记 unknown。

### 10. 指标与统计

质量指标分三层：

1. **任务质量**：Evo 各领域原生分数、PAST task score。
2. **Memory 质量**：PAST Mech；等价预置轨在有离散可比检索项时报告 Precision@k、Recall@k、Hit@k。Legacy 若只提供不可拆分 snapshot，则标记不适用，不伪造 top-k。
3. **迁移/持久化收益**：Evo 的 evolved system 结果及 PAST 归因子集的 persistence gap；协议不严格时使用自定义名称并标注偏差。

成本指标包括：

- 主 Agent input/output/cache read/cache write Token；
- Graph Memory Agent Token；
- ingest、consolidation、Dive/TTT Token；
- judge Token；
- 总尝试 Token、成功任务 Token、每单位质量 Token；
- 在线端到端时间、memory-ready 等待、离线处理时间和摊销成本。

统计输出包括逐任务 paired difference、family/domain macro-average、95% bootstrap CI、win/tie/loss、失败率和 timeout rate。不生成跨 benchmark 总分，不设置胜负阈值。

### 11. Judge 与人工审计

- Judge 输入移除 backend、workspace、Agent 名称、运行顺序和 memory trace。
- Pairwise judge 交换 A/B 两次；不一致记 tie 或进入人工复核。
- 人工审计随机覆盖 10%，且每 benchmark 至少 50 样本；同时覆盖分歧、临界分、Graph/Legacy 大差异和随机样本。
- 报告 judge-human agreement；自动 grader 是 proxy，不等于人工判断。

### 12. 产物

每次阶段运行产出：

- 原始调用/事件 JSONL；
- task/episode 级 CSV 或 Parquet；
- run manifest、配置 hash、模型版本、上游 commit、容器 digest；
- workspace/project/channel/task/episode 与 execution ID 映射；
- memory snapshot/version/provenance；
- grader 输入输出与人工审计记录；
- Evo 四领域、PAST 四能力报告；
- Token/时间分解、统计图表和 Markdown 总结；
- 协议兼容性清单和可复现实验命令。

### 13. 分阶段执行

1. **Harness 单元验证**：解析上游数据、配置和评分，不调用大规模模型。
2. **Smoke**：每领域/能力少量任务，验证模型冻结、状态隔离、fresh session、Graph/Legacy持久化、read-only/off、ready barrier、usage closure和 grader blind。
3. **Pilot**：全量数据仅 1 seed；执行前提交预计 Token、费用和耗时。
4. **Full**：Pilot 审核通过后补齐其余 2 seeds；不根据 Pilot 结果修改任务选择、指标或 grader。

Smoke 失败时不自动修改 Multica。Harness 输出具体 capability gap、受影响 benchmark 条款和可选降级口径，等待后续决策。

## Testing Decisions

1. **最高层 seam**：测试从 Harness 调用 Multica 公共 API 到最终频道输出、usage 和报告的外部行为，不断言 Multica 私有 helper、SQL 行或文件布局。
2. **Capability Smoke**：分别验证 Graph active/blocked、Legacy inactive、profile CAS、lineage、consolidation、runtime/model pinning、频道状态与错误返回。
3. **Fresh-session 黑盒测试**：learn episode 放入只存在于易失上下文的诱饵，同时将另一事实写入允许持久层；新 episode 应只能使用持久事实，不能复述诱饵。
4. **Cross-family 污染测试**：family A 写入唯一 canary，family B 查询相邻主题；任一 backend 检出 canary 即失败。
5. **Evo freeze 测试**：test 1 产生唯一 canary，test 2 查询它；若可见则 read-only/frozen-state 协议失败。
6. **Off 测试**：On 组能读取 learn 事实，Off 组在相同 eval prompt 下不能读取；两组工具与模型保持一致。
7. **Usage closure 测试**：使用可预测的小任务核对主 Agent、Memory Agent、后台处理与 judge 的 before/after delta；缺失分项不得被总计掩盖。
8. **Barrier 测试**：连续重复同一 learn→wait→eval 流程，验证等待判定不会在后台处理未完成时提前通过，也不会无限等待已完成任务。
9. **Grader 测试**：A/B 对调、同输出自比、已知优劣样本、blind metadata 和人工抽样均通过。
10. **统计测试**：用小型合成结果验证 family/domain macro-average、paired bootstrap、tie、missing和crash处理。
11. **恢复测试**：网络失败、5xx、timeout、Memory Agent blocked和usage unknown按冻结规则处理，不发生选择性重跑。
12. **Prior art**：复用现有 Graph profile/channel mode、lineage、consolidation、runtime usage和benchmark runner的 API 集成测试风格；Harness 自身使用 fixture 和录制响应进行确定性单元测试。
13. **验收依据**：测试以公开结果、错误码、输出、usage和报告字段为断言，不以内部实现步骤为断言。

## Out of Scope

- 修改 Multica Server、Daemon、数据库 schema、UI 或产品级 Memory 行为。
- 新增通用 evaluation control、fresh-session、read-only/off 或 telemetry API；若确有必要，另起规格并单独批准。
- Graph Inject、Graph 与 Legacy 的纯 backend 隔离消融、统一 MemoryBackend 重构。
- 使用 curator-side test Ability 标签进行在线路由，或把 Anchor Skill 当作公平部署 baseline。
- 模型权重、架构或学习算法层面的 recursive self-improvement。
- 多模态/具身 Agent、真实用户长期数据、跨用户隐私状态和生产 workspace。
- 高并发吞吐、压力测试或生产容量规划。
- 将 Evo、PAST、GDPval、SeaEval 合并为统一排行榜分数；GDPval 仅作为 Evo 的知识工作领域参与。
- 自动进入付费 Pilot/Full；两阶段均需费用确认。
- 将 Mech 解释为因果必要性证明，或把 LLM judge 解释为人类真值。

## Further Notes

- EvoAgentBench 与 PAST-Bench 测量对象不同：Evo 是跨任务 Ability transfer，PAST 是跨 fresh-session persistence improvement。报告必须分开呈现。
- PAST 的 `w/o evolve` 是 matched persistence-off，不是 cold-start；无法严格实现 Off 时不能替换概念。
- Graph Agent Mode 是完整多 Agent 产品路径。它可能通过额外公开频道回合修正主 Agent；最终答案选择和 quiescence规则必须预注册，所有额外回合计入成本。
- Graph 的物理 project graph、channel lineage和 GraphView 是不同概念。同一物理 Graph 不保证新频道可见旧频道节点，必须通过 Smoke 验证。
- 本规格以最终回读时当前 checkout 确认存在的公开 API 为准；未在路由和请求合同中验证的 evaluation control/status 能力不得视为可用。
- `memory_agent_model` / `memory_agent_thinking` 的 profile 展示值不能替代实际 managed Agent runtime 配置核验。
- 自动 grader、provider 默认模型、live web 和后台整理都是潜在漂移源；版本和观测时间必须进入 manifest。
- `agent_benchmark_comparison.md` 保存四个 benchmark 的调研背景；本规格是本次 Evo/PAST Multica 评测的执行权威。
- 本规格未发布到 issue tracker：当前会话未提供 tracker、项目或 `ready-for-agent` label 目标。文件落盘是本次请求的交付；如需发布，后续应明确目标仓库和 tracker。
