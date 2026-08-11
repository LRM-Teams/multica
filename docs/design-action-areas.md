# 全站「操作区」统一规范（Action Area Spec）v1.1 · LRM-1054

> 状态：**已冻**（Frank 锁 LRM-1054 / Computer 首刀落地 LRM-1071）。
> 参考：Raft Computer 详情（Select/Create 贴区块头；Restart 贴 Computer 说明；Copy 贴命令行尾；Delete 贴危险说明）。
> Computer 详情 Gate：对照 `lrm-1054` draft v5（含 LRM-1048 Restart 显眼度合流）。

## 0. 铁律

1. **就近（Proximity）**：操作出现在它作用对象的最近槽位——区块动作贴区块头，行动作贴行尾，字段编辑贴字段，命令复制贴命令行。禁止把本该贴对象的动作抽到远距离全局墙。
2. **简化（Simplify）**：一屏可见实心主按钮 ≤1；同区块可见动作 ≤2（1 主 + 1 次/⋯）；能就地改就不另开按钮；状态展示（pill/标签）不是操作。

## 1. 骨架：4 槽位 + 就近优先级

```
S1 页面头右（header trailing）      → 仅页面级主操作（≤1）+ ⋯（低频/危险入口）
S2 区块头右（section header right） → 该区块新增/管理（Create / Scan / Invite）
S3 行/对象尾（row / object trailing）→ 对该行/该字段/该命令的动作（ghost / 铅笔 / Copy / ⋯）
S4 危险区（页底 danger zone）       → 删除等不可逆；按钮贴本区说明，不散落
```

### 就近优先级（冲突时按此选槽）

1. 动作作用在**某一个字段/命令/行** → 必须 S3（贴对象）
2. 动作作用在**某一个区块集合**（新增 agent、扫 workspace） → S2（贴区块头）
3. 动作作用在**整页资源**且需显眼 → S1；否则可进该资源说明旁的 S3 块
4. 不可逆删除 → 仅 S4

- 禁止第 5 槽位：不新增浮动全局 ACTIONS 墙。
- 同一操作禁止双入口（S1 与 S2/S3 不同时出现同一动词）。

## 2. 分层 → 控件形态

| 层级 | 定义 | 形态 |
|---|---|---|
| 主操作 | 每页或每区块至多 1 | 实心 `default` |
| 次操作 | 常用非主线 | `outline` |
| 三级 | 编辑单项 / 复制 / 查看 | `ghost` / 铅笔 / Copy |
| 溢出 | ≥3 或低频 | `⋯` |
| 危险 | 不可逆 | S4 danger 或 ⋯ 内 destructive |

铁律：

- 实心主按钮一屏 ≤1；区块内可见动作 ≤2。
- pill/标签是状态，不是按钮。
- 名称/描述：就地编辑或铅笔（S3），禁止远处 RENAME 实心钮。
- 无权限：默认不渲染；需解释时禁用 + 一句短原因。

## 3. 文案

- 按钮 ≤2 词（Create / Scan / Restart / Delete）。
- 说明 ≤1 行贴在动作上方。
- 禁用态：短标签（如 Unavailable），不另开长说明条。

## 4. 桌面 / 窄屏

| | 桌面 | 窄屏 |
|---|---|---|
| S2 | 文字+图标贴区块头右 | 可 icon-only，触击 ≥40 |
| S3 | hover 显；focus 显 | 常驻 ⋯ / Copy |
| S4 | 页底，按钮贴说明右或说明下 | 全宽 |

## 5. Computer 详情首刀（LRM-1071 / draft v5）

| 落点 | 行为 |
|---|---|
| S1 头右 | Restart `outline` + ghost `⋯`；**无** Upgrade / Delete |
| Basics · Daemon 行（S3） | 版本 mono；有更新时 `→` 目标版 + `available` + Upgrade `outline` xs |
| Runtimes 卡脚（S3） | Make public / Make private（LRM-1421 曾误退，已恢复）；**删除**独立 Sharing 列表 |
| Workspaces 头右（S2） | Scan |
| S4 Danger Zone | Delete computer 仅此 |

验收：一屏无实心主按钮墙；Upgrade / Make public·private 均为 outline；每卡可见动作 ≤1。

## 6. 承接顺序

1. Computer 详情（本参考直接对标）—— LRM-1071 / 与 LRM-1048 合流。
2. 频道详情侧栏（LRM-1052）—— 字段就地改。
3. 列表/卡片行 —— 卡角 ⋯，禁卡面实心墙。

## 7. Review 红旗

- 一屏 >1 个实心主按钮。
- 远距离全局墙收「本该贴对象」的动作。
- 同一动词双入口。
- 不新增视觉语言；复用现有 button variants / tokens。
