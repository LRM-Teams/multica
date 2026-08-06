# Research Live Canvas V6 — 相机、层级与性能规格

> 本文固定点击自动居中、默认 2 层/最多 3 层、可见节点预算和 10k 节点策略。

## 1. 点击节点自动居中

点击或键盘选中节点后的顺序：

1. 写入 `selectedNodeId`；
2. 打开/更新 Inspector，先得到新的 overlay insets；
3. 计算节点 bounds 与 safe viewport centre；
4. 若节点中心已在 safe-centre 半径 72px 内，只更新选中，不移动镜头；
5. 否则从当前 viewport 到目标 viewport 执行 260ms `easeOutCubic`；
6. 动画结束后聚焦 Inspector 标题或保留卡片焦点，取决于触发方式。

复用 `ResearchCameraController`，不得另写第二套 camera tween。

### Safe region

- 基础 desktop insets：top 56、right 184、bottom 84、left 16。
- Inspector 打开：right = Inspector 实际宽度 + 16；Minimap 在其左侧。
- 窄屏：top 56、right 16、bottom 68 + safe area、left 16。
- camera 只移动 x/y，选中普通节点时不自动改变 zoom；只有“查看分组/fit selection”可以改 zoom。

### 中断

- 连续点击：新节点立即取消旧 tween，从当前 viewport 重算。
- 用户 pan/wheel/pinch：立即取消 auto camera，用户优先。
- 用户交互后 3000ms 内，普通后台 Delta 不允许抢镜头；blocking dispute/显式“定位”除外。
- Reduced Motion：duration=0，直接落终态并 aria-live 通知。

## 2. 层级规则

- 初始加载：root + 向外 2 hops；只显示 2 级关系。
- 用户点击“展开”：允许临时显示第 3 层。
- 不在同一 viewport 同时展示 4 层及以上；更深内容折叠为 Display Group/“+n 未加载”。
- Breadcrumb 最多显示当前节点、父级、root 3 项；更深祖先进菜单。
- Insight 的 canonical level 可以大于 3，但显示层仍只展开相邻 2/3 层。

## 3. 可见节点硬预算

| viewport | soft limit | hard limit | edge hard limit |
| --- | ---: | ---: | ---: |
| desktop ≥1200 | 120 cards | 180 cards | 420 |
| 768–1199 | 72 cards | 96 cards | 220 |
| <768 | 32 cards | 48 cards | 96 |

DOM 总预算包含 canonical card、Display Group、gutter/anchor，desktop 不超过 220 个图节点元素。达到 soft limit 后新展开先折叠远端低优先路径；达到 hard limit 后拒绝直接铺开，显示分组卡和“在独立视图打开”。

## 4. 保留优先级

预算不足时按顺序保留：

1. selected + ancestors + directly related nodes；
2. blocking/failed/cancelling/stale path；
3. running task/attempt；
4. 最近 transition 的 affected roots；
5. pinned；
6. importance 高的 fresh nodes；
7. 其他节点折叠为 Display Group。

不允许为了数量预算隐藏 selected 或 blocking path。

## 5. 展开算法

```text
request(root, direction, relations, maxDepth<=3, limit)
→ merge page by stable id
→ estimate visible count
→ reserve protected nodes
→ collapse lowest-priority distant subtrees
→ render canonical nodes + honest Display Groups
```

- 每个端口只允许 1 个 in-flight Slice 请求；重复点击去重。
- root/filters/snapshotId 相同才复用 cache。
- 展开期间只在端口显示 spinner，不锁全图。
- page 返回 snapshotId 变化时停止拼页并重取 root Slice。
- `canExpand=false` 时移除展开入口；`unloadedNeighborCount` 必须准确展示。

## 6. 折叠与自动整理

- Display Group 显示 relation、节点数、running/failed/stale 摘要和 top 3 Agent，不展示伪结论。
- 点击 Display Group：先计算是否超 hard limit；超出则打开局部 drill view，不铺回主画布。
- 自动整理只改变 display positions/hidden set；同一输入产生稳定顺序。
- Delta 只重新布局 `affectedRootIds`，未影响节点位置必须逐像素保持。

## 7. 10k 性能门

- 首屏网络不得请求全部 10k 节点。
- Canvas 首屏 canonical cards ≤soft limit；Trajectory 使用 window+overscan。
- 位置计算放纯函数/worker-ready seam，不在 render 读取布局。
- 只动画可视节点；屏外节点直接落终态。
- motion queue ≤64；同类同根 transition coalesce。
- 性能测试记录 initial slice、20-node delta、scroll window、layout affected region、DOM count。

## 8. Minimap

- 只画已加载 Slice，并在标题写“已加载 x / 发现 y+”。
- viewport 框和主图相同坐标系；filter/fold 同步。
- 点击 minimap 属于显式定位，可越过 3s camera 保护期。
- 未加载区域用密度块/边界提示，不画伪节点和伪边。
