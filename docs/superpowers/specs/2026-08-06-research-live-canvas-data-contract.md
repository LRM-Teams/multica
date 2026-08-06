# Research Live Canvas V6 — 数据合同与展示来源

> 本文决定“UI 上每一条信息从哪里来”。缺字段就显示未知/不可用，不允许编码 Agent 用 summary、动画或相邻节点猜。

## 1. 数据进入顺序

```text
API schema parse
  → V5/V6 adapter
  → CanvasSnapshot / CanvasDelta / CanvasSlice
  → React Query cache
  → selectors / view model
  → renderer
```

生产组件不得跳过 adapter 直接消费 `ResearchGraphNode` 或 `ResearchV6ProjectionNode`。

## 2. 会话能力探测

| 情况 | 行为 |
| --- | --- |
| V6 snapshot route 成功且 schema 合法 | 使用 V6；记录 `source=v6` |
| V6 route 明确 404/501 | 使用 V5 adapter；显示“经典投影视图”，不报错 |
| V6 200 但 schema 错 | 显示接口错误，不静默退回 V5 |
| V6 Snapshot 分页的 snapshot/sequence 不一致 | 丢弃整次加载并 resync |
| Delta 缺口 8s 未补齐 | 冻结写操作，触发一次 Snapshot resync |

## 3. CanvasNode → 卡片字段

| UI 槽位 | 唯一来源 | 缺失时 |
| --- | --- | --- |
| 标题 | `CanvasNode.title` | “未命名节点” |
| 类型 | `kind/subtype` | Generic + 原始 kind 诊断 |
| 状态 | `status` | `unknown`，不得归为 idle/done |
| 负责人 | `actor`；执行节点可用 detail.assigned_agent_id | “未分配” |
| 目标 | task objective / question text / canonical detail 字段 | “目标未提供” |
| 当前动作 | Attempt detail + phase/status | “暂无执行动作” |
| 已解决问题 | accepted result/claim/decision 的稳定引用与状态 | 0 项，不从 summary 数句子 |
| 最新进展 | 最近 `updatedAtSequence` 对应的 canonical result/claim/turn/revision | “暂无新进展” |
| 重要度 | `importance` | adapter 的中性默认，仅用于排序 |
| Freshness | `freshness` | “未知”，不自行按时间判 stale |
| 详情入口 | `detailRef` | 禁用并说明接口缺口 |
| 更新时间 | `updatedAt` | 隐藏相对时间，不显示假“刚刚” |

`summary` 只是一段有界显示文案。不能从 summary 推断完成比例、证据数、负责人、冲突、父子关系或 Agent 状态。

## 4. Edge → 关系显示

| relation | 视觉语义 | 允许改变布局 |
| --- | --- | --- |
| decomposes / depends_on | 任务结构/依赖 | 是，决定相邻层级 |
| tests | 验证目标 | 是，进入验证分支 |
| produced / consumed | 工件流 | 是，证据 lens 强调 |
| derived_from / integrates | 真实派生/整合 | 是，Insight 组合 |
| supports / contradicts / refines | 证据语义 | 否，不当树父子 |
| supersedes / invalidates | 版本/失效 | 否，保留历史节点 |
| discussed_by / challenged_by | 争议关系 | 是，争议 lens 局部布局 |
| escalated_to / resolved_by | 升级/裁决 | 是，形成 Director/Decision 路径 |
| staffed_by / created_for / retired_after | 团队与任务 | 执行 lens 使用 |

未知 edge 使用中性虚线和 relation 文本，只在详情/选中上下文出现。

## 5. Delta → 局部更新

| Delta 字段 | 前端行为 |
| --- | --- |
| `fromSequenceExclusive/throughSequence` | 连续性、重复、乱序和 resync 判断 |
| `upsertNodes/upsertEdges` | 稳定 id 覆盖更新，不追加重复项 |
| tombstones | 移除可见项和 dangling edge，保留必要的历史诊断 |
| `affectedRootIds` | 只重排对应子图，其他节点位置不动 |
| `transitionKind` | 只选择动画 directive，不改变终态 |

## 6. Presence 与 Attempt 的职责

- Presence 决定 Fleet 全员是否出现在 Agent Deck，以及 `idle/queued/running/done/failed/stale` 派生 phase。
- Attempt ledger/Projection detail 决定实际执行状态、开始时间、最近观测、lease、取消、失败和产出。
- Presence 与 Attempt 冲突时，卡片显示“状态待同步”，执行详情以 Attempt ledger 为依据，并触发 Presence query invalidation。
- 群消息、Agent prose、头像在线点、动画和前端计时器不能把 queued 改成 running。

## 7. NodeCommand

仅显示后端已支持的 4 个动作：

| action | 入口条件 | UI 文案 |
| --- | --- | --- |
| continue | 可继续的 question/task anchor | “继续此路径” |
| fork | 可分叉的 question/task anchor | “从这里分叉” |
| retry | failed/lost 且可重试 | “重试此次任务” |
| reassign | task 可改派且旧执行已允许释放 | “改派负责人” |

请求必须带 `client_request_id`。409/403 显示服务端 message_key 对应文案并刷新节点；不得先在画布上乐观创建 canonical 节点。

## 8. 允许的前端派生

只允许以下显示派生：

- Lens filter、selection、pinned path、viewport、layout positions；
- Display Group 与折叠计数；
- 卡片密度、edge emphasis、minimap window；
- 从明确时间戳计算的持续时长；
- 从明确数组/引用计算的可见数量。

不允许派生：research completion、Claim 真伪、Insight level、stale、Agent running、争议裁决、真正父子或合并关系。
