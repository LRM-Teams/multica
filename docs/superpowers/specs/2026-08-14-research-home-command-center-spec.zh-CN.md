# 调研首页指挥台开发规格

状态：已确认，实施中

日期：2026-08-14

目标分支：`agent/research-homepage`
视觉基准：`/Users/xxx/Desktop/Multica调研主页-星图配套版/index.html`

## 1. 产品定义

调研首页是管理正在进行的调研的指挥台，发起新调研是第二优先级。首页必须让用户无需进入详情页便能判断：哪个调研需要介入、走到哪个阶段、实际完成了多少工作、最近产生了什么可靠成果、哪些 Agent 正在做什么。

首页不是营销页，不用巨大 Hero、装饰图片或假星图占据首屏；也不是详情页缩略图，不在首页操作单个研究节点。

## 2. 首屏验收合同

在 1440×900 窗口中，无需滚动必须看到：

1. 页面栏；
2. 高度不超过 180px 的紧凑创建入口；
3. 一行四项全局摘要；
4. 完整重点调研；
5. 至少四条活跃任务队列；
6. 全部调研或历史调研的入口/标题。

桌面工作台采用约 60/40 分栏：左侧重点调研，右侧活跃任务队列。两侧高度基本对齐。没有其他活跃任务时重点调研扩展为全宽，不展示空容器。

## 3. 视觉合同

- 沿用 Research Constellation 子页面：深海蓝背景、低密度点阵、青色探索、绿色稳定结果、琥珀色风险、紫色新方向。
- 超宽高清背景只营造空间氛围，视觉重心位于右上；不得包含假节点、文字、人物或不可交互的研究关系。
- 阶段节点、轨迹和关系线全部由真实业务数据绘制。
- Agent 使用真实身份头像；缺失时使用现有 `ActorAvatar` 降级。
- 不为成果配无关图库照片。成果以类型、来源、摘要和验证状态表达。
- 主体最大宽度约 1600px；1920 宽度增加信息密度而非无限拉伸。
- 背景失败不影响使用，文本对比度不低于 4.5:1；减少动态效果时关闭脉冲和路径流动。

## 4. 页面结构与交互

### 4.1 紧凑创建入口

默认仅展示：目标输入、行业调研/竞品分析/技术选型模板、参数按钮、生成研究星图按钮。

常驻启动预览取消。参数面板内展示主理 Agent、预计 Agent 数、调研深度、来源策略、语言和轮次预算。输入框下仅保留一行动态摘要，例如「标准深度 · 5 位 Agent · 权威源优先 · 最多 3 轮」。

### 4.2 全局摘要

只展示四项有决策价值的指标：

- 正在推进：未完成且仍可工作的 session 数；
- 等待处理：需用户确认或存在阻塞的 session 数；
- 工作中 Agent：有 running task 的去重 Agent 数；
- 今日新增证据：按工作区时区统计当天接受的 observation 数。

不展示累计证据和独立的全部调研大数字。

### 4.3 重点调研

自动选择优先级：等待用户确认 → 阻塞 → 可恢复失败 → 正在运行 → 暂停 → 最近进展。

展示内容：

- 标题、状态、当前阶段；
- S1 定义、S2 探索、S3 验证、S4 交付；
- 当前计划 `已完成 task / task 总数`；
- 证据数、开放问题数、阻塞 task 数、最近有效进展；
- 当前正在执行的 task；
- 最多四位 Agent 的当前分工；
- 最多三条最近结构化成果；
- 明确的阻塞/待确认原因。

操作仅包含：进入研究现场；等待确认时改为处理待确认；暂停/继续留在更多菜单。重试、改派、来源和目标编辑留在详情页。

用户点击右侧队列可临时切换重点调研。选择只保存在当前页面会话，不写服务器、不改变业务优先级；选中项消失时自动选择下一项。实时更新不得抢走用户手动选择。

### 4.4 最近成果

成果只来自后端确认的结构化记录，降级顺序：

1. 最新 `supported claim`；
2. 最新 verified observation；
3. 最近 succeeded task 的结果摘要；
4. 明确显示「正在建立研究框架」。

禁止从聊天正文截取或由前端编造成果。

### 4.5 Agent 分工

以当前工作为主：running task 显示 task 简短目标；没有 task 时显示 fleet role；主理 Agent 显示综合与调度。失败、离线或等待调度必须准确呈现，不能统一显示为工作中。

### 4.6 活跃队列与历史调研

活跃队列按等待确认、阻塞、可恢复失败、运行中、暂停排序。每行展示标题、阶段、task 进度、证据、当前 Agent、最重要异常和最近进展。

历史调研包含 completed、archived、cancelled 与不可恢复失败，默认折叠或经筛选查看。保留标题搜索、状态筛选、滚动位置和返回焦点。历史数据采用 cursor 分页，每页约 30 条；分页可作为独立后续垂直切片，本期接口未分页时不得阻止首屏指挥台交付。

## 5. 进度语义

首页不用一个虚假的总百分比。进度由三层构成：

1. S1–S4 阶段；
2. 当前 goal/plan 版本的 task 完成数；
3. 证据、开放问题、阻塞和最近进展。

`task_completed / task_total` 只表示当前计划工作量，不代表研究质量、耗时或整体完成百分比。

## 6. 状态合同

- 可恢复失败留在活跃队列，显示可恢复，但首页不直接重试；
- 不可恢复失败、取消、完成和归档进入历史；
- 没有活跃调研但有完成记录时，重点区展示最近完成及阅读报告入口；
- 完全为空时展示「提出问题 → 多 Agent 分工 → 多源验证 → 交付研究星图」四个真实步骤；
- 加载使用结构一致的占位；列表局部失败不阻止创建；离线保留缓存并提示可能不是最新；
- 旧后端缺少新增聚合字段时显示阶段和更新时间，不伪造 `0/0` 或零证据。

## 7. 前端规格

共享实现位于 `packages/views/research/`，Web/Desktop 只负责路由。服务端状态归 TanStack Query；临时重点选择、筛选、输入草稿属于客户端状态。WebSocket 只使 Query cache 失效，事件合并到最多每秒一次刷新。

建议组件：

```text
research-list-page.tsx
research-home-header.tsx
research-compact-composer.tsx
research-home-summary.tsx
research-focus-workbench.tsx
research-focus-session.tsx
research-active-queue.tsx
research-stage-path.tsx
research-session-history.tsx
```

响应式：≥1280px 使用 60/40；768–1279px 上下排列；<768px 单列并将关系图退化为四段阶段轨迹。`apps/mobile` 不在本期范围。

必须覆盖键盘、focus、ARIA、长文本、减少动态效果、loading/empty/error/offline/disabled/unknown enum 状态。卡片只能有一个主链接，更多菜单为独立焦点。

## 8. 后端列表投影

复用 `GET /api/research/sessions`，不新建 homepage 平行接口。每个 session 可选返回：

```json
{
  "list_progress": {
    "task_total": 12,
    "task_completed": 5,
    "task_running": 3,
    "task_blocked": 1,
    "evidence_count": 18,
    "today_evidence_count": 4,
    "node_count": 6,
    "open_question_count": 2,
    "awaiting_user_action": false,
    "attention_kind": "blocked_tasks",
    "recoverable": true,
    "last_progress_at": "2026-08-14T08:41:00Z"
  },
  "active_assignments": [
    {
      "agent_id": "...",
      "role": "validator",
      "task_id": "...",
      "task_title": "验证竞品定价数据",
      "state": "running"
    }
  ],
  "latest_outcomes": [
    {
      "id": "...",
      "kind": "claim",
      "title": "...",
      "summary": "...",
      "verification_state": "supported",
      "created_at": "..."
    }
  ]
}
```

聚合必须按 workspace 一次批量取得，不得逐 session 查询。task/question 只统计 session 当前 goal/plan 版本；证据排除 rejected observation；成果每 session 最多三条；分工每 session 最多四条。列表不得包含完整 nodes、messages、sources 或 report 正文。

`attention_kind` 使用稳定枚举：`user_confirmation`、`blocked_tasks`、`recoverable_failure`、`stalled` 或空。前端不解析错误文案。未知枚举降级为中性状态。

## 9. 实时与兼容

task、阶段、证据、问题、用户动作和 fleet 变化触发现有 Research session 更新事件。前端批量失效 workspace session list query。WebSocket 断开保留缓存并显示状态，恢复后完整同步。

所有新增字段可选。旧 Desktop 客户端忽略新字段；新客户端面对旧后端安全降级。API schema 必须覆盖缺字段、错误类型、null 和未知枚举。

## 10. 完成标准

- 1440×900 首屏容量满足第 2 节；
- 重点调研、队列、摘要中的数据都有稳定后端语义；
- 用户可以切换重点调研且实时更新不抢选择；
- 多任务时能快速识别需要处理、阻塞和停滞项；
- 背景与子页面同属一套视觉系统，但不冒充业务图；
- Web/Desktop 共享，窄屏可操作；
- 50 个 session 下无 N+1；
- 旧后端缺字段时不白屏、不展示假数据；
- 代码、契约测试、截图、PR 描述与本规格一致后才算开发交付完成。
