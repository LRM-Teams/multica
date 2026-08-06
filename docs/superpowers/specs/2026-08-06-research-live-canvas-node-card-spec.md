# Research Live Canvas V6 — 节点卡信息与组件规格

> 卡片回答“谁负责、目标是什么、正在做什么、解决了什么、进展到哪”。卡面负责扫读，Inspector 负责详细审计。

## 1. 标准卡结构（100% zoom）

使用现有 `RESEARCH_NODE_WIDTH=240` 作为普通卡宽。高度由内容族决定，但必须落在 112–168px；Insight parent 可使用现有 282×242，leaf 可使用 184×76。

```text
┌──────────────────────────────────────┐
│ [glyph] 类型              [状态] [⋯] │ 24px
│ 标题：一个具体任务/结论              │ 36px，2 行
│ 👤 负责人 · Agent 名                 │ 20px
│ ◎ 目标 · 12–32 字                    │ 20px，1 行
│ ↻ 当前 · 正在核验 3 个来源           │ 20px，1 行
│ ✓ 已解决 2  ·  新进展 1  ·  风险 1   │ 22px
└──── typed ports / +18 未加载 ────────┘
```

### 卡面必填顺序

1. 类型 + 状态；
2. 标题；
3. 负责人；
4. 目标；
5. 当前动作；
6. 已解决/新进展/风险计数；
7. 未加载邻居。

目标和当前动作必须分行。禁止写成“张三正在做竞品分析已经找了三个网站可能还有风险”这类混合句。

## 2. 具体文案规则

### 负责人

- 有 `actor`：头像 18px + 截断姓名 + role/attempt tooltip。
- 无负责人：显示“未分配”，不用空头像。
- 多贡献者 Insight：主行显示“3 位贡献者”，头像堆最多 3 个，完整名单在 Inspector。

### 目标

- task：objective；question：问题正文；hypothesis：待验证命题；Insight：该层结论要解释的关系。
- 最多 32 个中文字符或 64 个拉丁字符；超出 clamp，Inspector 完整显示。
- 缺失显示“目标未提供”，不能拿 title 重复填充。

### 当前动作

- Attempt running：方法/阶段 + 已运行时长，例如“核验来源 · 03:18”。
- queued/dispatching：“等待运行时接收”或“已分派，尚未启动”。
- cancelling：“停止中 · 等待取消确认”。
- terminal：当前动作行改为“最近完成/失败”。

### 已解决问题

- 只统计有稳定引用的 accepted result、answered question、resolved dispute 或 accepted decision。
- 卡面显示计数；Inspector 列出最多 5 项标题和跳转，更多分页。
- 不用 task status=succeeded 自动宣称研究问题已解决。

### 最新进展

- 显示最近一条 canonical result/claim/insight/turn/revision 的短标题 + 相对时间。
- 进展来自 sequence/timestamp 排序；不能按当前数组顺序。
- 过长内容只显示 1 行，完整内容在 Inspector。

## 3. 缩放密度

| zoom | 卡片显示 | 禁止 |
| --- | --- | --- |
| ≤55% | glyph、状态形状、标题 1 行、负责人头像 | 细正文、操作按钮 |
| 56–119% | 标准卡全部 7 行中的必要项 | 完整证据列表 |
| ≥120% | 增加最多 2 条进展/风险、端口标签 | 把 Inspector 全塞进卡片 |

密度切换使用 CSS/container state，不通过缩放字体到不可读尺寸。

## 4. 状态矩阵

| 状态 | 结构变化 | 非颜色标识 |
| --- | --- | --- |
| selected | 双层 ring + 四角定位锚 | `已选中` sr 文案 |
| queued/dispatching | 细进度轨静止/缓慢扫过 | queue glyph + 文案 |
| running | 左侧 activity rail + 时长 | play/working glyph |
| cancelling | rail 反向收束 | stop-pending glyph |
| done/succeeded | 终态印章，不保留脉冲 | check + “已完成” |
| failed/lost | 原因行占用当前动作位置 | error glyph + 下一步 |
| stale | freshness 条带 + 受影响关系 | stale glyph + “已失效” |
| unknown | 中性边框 | `?` + raw status |

Running 不使用无限呼吸 scale；最多使用低幅度 opacity/activity rail，Reduced Motion 下静止。

## 5. 6 个卡片族差异

- Execution：负责人、Attempt、时长最突出；目标 1 行。
- Inquiry：目标、验证方法、不确定性最突出；负责人次之。
- Corpus：来源类型、筛选决定、证据强度、Claim 状态最突出。
- Integration：Insight level、输入数、证据覆盖、stale、贡献 Agent 最突出。
- Dispute：立场数、未决问题、升级/Decision 最突出；关系不能只靠颜色。
- System：版本、周期、团队变更、缺陷 severity 最突出。

## 6. 交互区

- 整卡是选择按钮语义，Enter 打开 Inspector。
- 右上 `⋯` 是独立 button，必须 `aria-label="节点操作"`，点击不重复触发选中。
- 底部端口可点击展开 Slice；accessible name 包含方向、relation 和未加载数。
- 双击不承担唯一操作，避免触控和可访问性缺口。

## 7. 长内容与空内容

- flex 子项 `min-w-0`；标题/目标/动作 clamp；ID/URL `break-all` 只在 Inspector。
- 0 个进展不渲染“进展 0”噪声；改为中性“暂无新进展”。
- 负责人、目标、动作同时缺失时使用 Generic detail error，不展示空白卡。
- 时间/数字使用 `Intl.*` 和 tabular numerals。
