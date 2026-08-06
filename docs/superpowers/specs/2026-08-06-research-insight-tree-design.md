# Research Insight 组合树 Design（LRM-1470 · 2026-08-06）

面向 front-end 的可直接实现设计。配套可运行交互原型/纯函数与 contract fixture 见
`packages/views/research/insight-tree/`（本仓库 PR
`feat/lrm1470-insight-tree` · 设计规格 + 交互原型 + 合约适配 + 测试）。

后端依据：2026-08-05-autonomous-research-system.md §9.1 Recursive Integration、
§7.2 无限画布投影协议、aggregate-tree-contract-v0.md（LRM-1278）。

---

## 1. 问题诊断

现有画布（LRM-1278/1295）把 `leads_to` 显示树投影成 goal→subquestion→finding
三列；它不表达「结果整合」。V6 引入整合语义后，后端会给出 **Insight Derivation
DAG**：多次验结果递归归纳，Claim 为 level 0，Insight 层级由
`1 + max(input level)` 计算，任一输入失效（refuted/superseded/范围改变/权限撤销）
会让所有祖先 Insight 进入 `stale`。

直接平铺全部 Claim + Insight 节点会：
- 把「同一归纳的输入群」拆成几十个孤立叶子，丢失组合语义；
- 顶层结论与底层证据距离太远，无法一眼看到「哪些证据支撑了哪个结论」；
- stale 路径淹没在大图中，用户看不到「哪条链被输入失效波及、要不要重整合」。

本设计把 insight 组合树做成 **「顶层归纳结论（摘要）↔ 逐层钻取（展开）」可切换**
的画布视图：小节点（leaf Claim）形成 Insight，大量 Insight 再形成更高层 Insight，
组合卡片显示输入数量、结论、证据覆盖、矛盾和层级；点击展开到直接输入，再逐层钻取。

### 核心边界（必须遵守）
- 真实 merge 只展示 **后端 Insight Derivation**；前端显示分组必须明确标记为
  「显示分组」且**不冒充 Insight**，**不能写回 canonical Graph**（§7.2 + 本 issue 边界）。
- level、父子关系、贡献 Agent、失效传播**只来自后端事实**，前端不推断、不从
  摘要/聊天/动画状态推导 canonical research state（并行约束）。
- 失效传播是展示层「受影响路径」的可辨认化，不是 freshness 判定。
- 接口未落地部分使用严格遵循文档的 **contract fixture**，生产路径不得保留伪数据。

---

## 2. 信息架构与层级

把 Insight 组合树分为三层显示单位：

| 层级 | 视觉单位 | 内容 | 交互 |
| --- | --- | --- | --- |
| L0 叶子 | **Claim 卡** | 证据结论、覆盖数量、贡献 Agent | 点击看源/详情 |
| L1+ 归纳 | **Insight 卡**（越大层级越大） | 输入数量、结论、证据覆盖、矛盾、层级徽标 | 点击**展开到直接输入**，再逐层钻取 |
| 折叠 | **显示分组卡**（非真实 Insight） | 「n 个节点折叠」，诚实标注 | 点击展开该组 |

节点大小按层级递进（复用 LRM-1295 的三档规格思想）：

| 级别 | 尺寸（参考） |
| --- | --- |
| Level 0（Claim） | 184×76（= AGGREGATE_CHILD_CARD） |
| Level 1 | 218×142（= AGGREGATE_SIBLING_CARD） |
| Level 2 | 282×242（= AGGREGATE_PARENT_CARD） |
| Level 3+ | 282×242 封顶，用层级徽标区分 |

层级是「输入 DAG 计算」的事实，卡片只读 `node.level` 渲染，**不自我计算**。

---

## 3. 摘要/展开两种视图模式（AC1）

### 模式 A · 摘要（默认）
- 锚点 = **DAG 汇点（顶层 Insight）**，即对外无消费者的最高层结论。
- 默认只展示顶层结论卡；其下整棵归纳子树**折叠成显示分组卡**（标明节点数与
  「显示分组」标签），显著降低画布节点数。
- **例外保持上下文**：`pinnedIds`（选中节点及其输入祖先 + stale 受影响路径）
  始终展开，保证失效传播路径可辨（AC3）。

### 模式 B · 展开
- 全部 Claim + Insight 节点可见，无任何显示分组。
- 逐层钻取：点 Insight 卡 → 展开其直接输入 → 再点下一层。

### 切换约束（AC2）
- 展开/合并**只改变可见节点集合与布局**；`viewportCenter`、`zoom`、
  `selectedId`、`pinnedIds`（选择、相机、上下文）原样保留。
- 布局是稳定纯函数：同一 (节点集, pinned) 输入必产出同序可见集合，动画后
  无重叠、无跳位（配合 LRM-1335 canvas-reorg-motion 时间预算：单元素 ≤320ms、
  总 ≤900ms、stagger 上限 6）。

---

## 4. 组件与 token

复用 research view 既有 token 纪律（LRM-793/972 锁：禁用硬编码 hex /
tailwind palette-500）。

| 元素 | token / 规则 |
| --- | --- |
| Claim 卡（fresh） | `ring-1 ring-success/50` · accent `bg-success` · tone `success` |
| Insight 卡（fresh） | `ring-1 ring-primary/40` · accent `bg-primary/80` · tone `default` |
| Insight 层级徽标 | `badge` + `L{n}`；level 越高用 `primary` 实心 |
| 矛盾徽标 | `ring-warning/55` · accent `bg-warning` · tone `warning`（读 `contradictionCount>0`） |
| stale 卡 | `ring-destructive/50` · accent `bg-destructive/60` · title `line-through decoration-destructive` · shell `bg-destructive/10`（沿用 refuted 语义但标注 stale badge） |
| 显示分组卡 | `ring-dashed ring-border/70` · `bg-muted/60` · 标签「显示分组 · n 节点」；**不能**用 insight 实心样式，避免冒充真实归纳 |
| 重新整合入口 | destructive outline button「重新整合」· 仅受影响排序位置出现 |

卡片内容（Insight 卡）自左到右：
`层级徽标` · `输入数量 (n)` · `证据覆盖` · `矛盾徽标(若>0)` · `结论文案` · `贡献 Agent 头像堆`。

---

## 5. 状态矩阵

| 状态 | 处理 |
| --- | --- |
| 默认 | 摘要模式：顶层结论 + 折叠分组；fresh 绿色系 / insight 主色系 |
| 加载 | 骨架卡（输入数量/结论为 shimmer），不渲染假数据 |
| 空 | 无 Derivation 时显示「尚无整合结果」，绝不显示占位凑数节点 |
| 错误 / 合约缺口 | `selectInsightDerivationNodes` 返回 `ok:false` → 显示「投影数据缺口」错误态，不伪造树 |
| 禁用 | 层级徽标置灰；无可展开输入时不显示展开箭头（不提供空展开） |
| 权限 | 越权输入按后端 access_revoked 传播为 stale 并在卡上标注「访问已撤销」，不显示内容 |
| stale | 受影响路径沿自底向上描红边 + stale badge；提供最小范围「重新整合」入口 |

---

## 6. 桌面与窄屏规则

- **桌面（≥1200px）**：完整画布；摘要/展开切换在顶部 chrome（同 research-canvas）。
- **窄屏（<900px）**：摘要模式为默认；折叠分组默认收进，仅保留顶层结论 + stale
  路径；展开一次只深入一层，避免同时铺开整棵 DAG。
- 层级较高的卡在窄屏下可横滑查看详情，不缩放丢失。

---

## 7. 可访问性

- AI（aria-label）描述每张卡：层级、输入数、结论、freshness、是否受影响路径。
- 摘要/展开切换是可聚焦的 toggle，支持折叠时 Enter 展开。
- stale 受影响路径不只靠颜色：红色描边之外叠加 text/label「已失效」与图标。
- 动画遵循 `prefers-reduced-motion`（复用 reduced-motion-fallback）。

---

## 8. 目标文件 / 组件接入提示

本 PR（设计切片）交付 `packages/views/research/insight-tree/`：

| 文件 | 职责 |
| --- | --- |
| `insight-derivation-contract.ts` | canonical 后端字段适配 + 校验（拒缺字段，不编造） |
| `insight-derivation-fixture.ts` | ≥3 层 DAG contract fixture（含 stale 演示），生产不引用 |
| `insight-tree-layout.ts` | 摘要/展开纯函数 + 视图上下文保持（AC1/AC2） |
| `insight-tree-stale.ts` | stale 受影响路径 + 最小重新整合入口（AC3） |
| `insight-tree.test.ts` | AC1/AC2/AC3 单元测试（12 例） |
| `docs/superpowers/specs/2026-08-06-research-insight-tree-design.md` | 本设计 |

**前端接入点（本 PR 之外，由前端实现）**：
- `research-canvas.tsx` / `research-graph-node.tsx`：把 insight-tree 的 viewNodes
  喂给 canvas，把 `planSummary`/`planExpanded` 的切换接到顶部 chrome。
- 新卡片组件 `research-insight-card.tsx` + `research-display-group-card.tsx`：
  消费上述纯函数产出的 `InsightViewNode` / `CollapsedGroup`。
- `canvas-reorg-motion.ts`（LRM-1335）做展开/合并动画的位移预算。

---

## 9. 验收截图要求

前端提交实现截图，逐项对照：
1. **AC1**：≥3 层 DAG 的摘要态 vs 展开态对比截图；标注折叠前后画布节点数
   （折叠后明显下降，如 12 → 1~4）。
2. **AC2**：展开与合并前后，选中卡、相机中心、缩放不变的对比；动画结束后无
   节点/边重叠或跳位（可录屏）。
3. **AC3**：stale 输入（如 c2）到 m1 的受影响路径用红色描边 + stale badge 可辨；
   「重新整合」入口只出现在受影响分量上。

---

## 10. 待升级确认的产品取舍（请 @bei-ke-han-mu-15 记录）

- 摘要模式**默认是否始终只显示顶层结论**，还是保留「最近选中分支」的记忆：
  推荐默认只显示顶层结论（最干净），记忆展开态为可选增强。
- 顶层有多个并列汇点时，摘要首屏最多同时展示几个结论卡（推荐 ≤6）与排序规则。
- 「重新整合」按钮点击后的行为：a) 触发后端 catch-up Integration Round（推荐，
  最小范围），还是 b) 仅前端展开待重整合路径。
