# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

Multica Research 面向需要长期、多方向调研的用户。用户通过自然语言持续修改目标、质疑结论和补充范围，同时需要看到 Agent 正在做什么、哪些方向被吸收、哪些方向被停止，以及最终报告依据了哪些结果。

## Product Purpose

Research 把一个开放问题变成可恢复的长期 Research Run。用户只与自己选择的 Research Director 对话；Director 动态组织 Agent、分配工作、处理融合与争议，并基于持久化研究图交付报告。

成功意味着：进程重启、Agent 中断、上下文轮换和任务改派都不丢失研究事实；Director 不需要读取全部子 Agent 原文仍能掌握各分支当前结论；用户可以核查有效结果和未被吸收的错误路径。

## Positioning

Research 的区别机制是“持久研究图 + 分层语义压缩 + 动态 Director”。S 级工作结果逐步融合为 M、L、XL、XXL；Director 主要读取各分支顶部内容和运行控制摘要，而不是把所有 Agent 上下文拼进一个无限增长的会话。

## Operating Context

- 用户在 Research 聊天框中用自然语言与 Director 对话，不直接操作业务状态命令。
- 星图展示 Goal、分支顶部节点、正在工作和所有未被吸收的 S；节点详情通过检查面板按需读取。
- 调研可能持续跨越多次模型会话、Agent 重启和服务重启。
- Director 判断当前 Goal 已满足后，安排 Agent 生成独立 HTML 报告，并在 Goal 的报告弹窗中交付。
- Web 与 Electron Desktop 共用 Research 页面和领域行为。

## Capabilities and Constraints

- 每个 Run 只有一个由用户选择的 Director。Director 不可用时进入等待状态并通知用户，不自动替换。
- Run 启动时不预建固定成员；Director 可以动态创建、改派、闲置和归档 Agent。
- 单个 Run 少于 20 个 Agent 时优先创建合适的新 Agent；20–49 个时提高创建门槛；50 个为硬上限。
- 产品不设置 Research token 总额度；单次模型调用仍必须受模型上下文窗口约束。
- PostgreSQL 中的规范表和 append-only 事件是事实来源。聊天、画布、模型上下文和 Director Brief 都是可重建表示。
- Agent 产生的报告可以包含 JavaScript，但只能在无同源、无外部网络、无存储和无主应用桥接的沙箱页面中执行。
- V1–V5 保持历史兼容；未生产启用的旧 V6 设计由新的 Director 设计原位替换。

## Brand Commitments

- 用户选择的 Research Director 在当前产品中称为“罗纳尔多 / Ronaldo”。
- Research 页面使用 `Research Constellation` 局部视觉语言；视觉权威仍是 [`docs/DESIGN.md`](docs/DESIGN.md)。
- 术语和状态文案必须描述事实、原因和后果，不使用虚假的完成百分比或无法验证的 Agent 能力声明。

## Evidence on Hand

- Research 领域语言：[`CONTEXT.md`](CONTEXT.md)。
- 研究运行与生产约束：[`docs/engineering-principles.md`](docs/engineering-principles.md)。
- Ronaldo V6 产品与开发规格：[`docs/superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md`](docs/superpowers/specs/2026-08-14-ronaldo-research-director-development-spec.zh-CN.md)。
- 机器、存储、传输和实施合同：[`docs/research-run-v6-contract.md`](docs/research-run-v6-contract.md)、[`docs/research-run-v6-storage-contract.md`](docs/research-run-v6-storage-contract.md)、[`docs/research-run-v6-http-contract.md`](docs/research-run-v6-http-contract.md)、[`docs/superpowers/plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md`](docs/superpowers/plans/2026-08-14-ronaldo-research-director-implementation-plan.zh-CN.md)。
- 现有视觉系统：[`docs/DESIGN.md`](docs/DESIGN.md)。
- 首页视觉基准与开发合同：[`docs/superpowers/specs/2026-08-14-research-home-command-center-spec.zh-CN.md`](docs/superpowers/specs/2026-08-14-research-home-command-center-spec.zh-CN.md)。
- 当前生产代码已经具备 Research Session、Task、Attempt、Evidence、Insight、Integration、Dispute、Report 与投影基础，但新的 Director 行为尚未生产接线。

## Product Principles

1. 研究事实必须在 Agent 上下文之外持久存在并可恢复。
2. Director 读取每个方向的顶部结论，同时读取必要的运行控制事实；低层全文只按需获取。
3. 融合只发生一次，已被吸收的内容通过上层节点复用，不在其他分支重复吸收。
4. 错误、弯路和停止记录不参与后续研究，但保留给用户核查工作量和判断过程。
5. 用户只表达意图；Director 负责把意图转成可审计的研究动作。

## Accessibility & Inclusion

状态不能只依赖颜色或动效。星图同时使用颜色、轮廓、中心图案、选择反馈和可访问名称；`prefers-reduced-motion` 下用静态图案替代呼吸、轨迹和位移动画。微型节点必须具有足够的不可见命中区域和键盘访问路径。
