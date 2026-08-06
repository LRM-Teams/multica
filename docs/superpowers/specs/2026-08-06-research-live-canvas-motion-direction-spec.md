# Research Live Canvas V6 — 动效导演与视觉时序

> 文本编码 Agent 按本表实现，不自行增加粒子、循环光效或修改语义。动画只解释已提交变化。

## 1. 通用参数

| token | 值 | 用途 |
| --- | --- | --- |
| camera-focus | 260ms easeOutCubic | 点击节点居中 |
| node-enter | 180ms | 新卡淡入/轻位移 |
| edge-draw | 220ms | 新关系显现 |
| reorg-element | ≤320ms | 单节点重排 |
| choreography | ≤900ms | 一次语义变化总预算 |
| stagger-count | ≤6 | 超过 6 个分批/直接终态 |
| queue-peak | ≤64 | motion backpressure |

只动画 `transform` 和 `opacity`。Ring/glow 用伪元素 opacity，不动画 box-shadow blur 半径。禁止 `transition: all`。

## 2. 10 类 transition 分镜

### branch_spawned · Branch Bloom

1. 父节点输出端口 80ms 提亮；
2. 新 edge 160ms 从父向外显现；
3. 新 branch/task 卡 180ms 淡入并移动 8px；
4. 若用户刚触发 fork，camera 260ms 聚焦新分支；后台自动分支不抢镜头。

终态：父子关系、分支 label 和负责人常驻，光效消失。

### task_dispatched · Handoff

1. task 卡 assigned slot 显示 Agent avatar；
2. 一条 160ms 的细光带从 task 移到 Agent Deck；
3. Deck 单元从 idle/queued 排到目标位置；
4. 只有 runtime start 后才切 running rail。

终态：queued 与 running 明确不同。

### result_accepted · Result Crystallization

1. attempt 的 activity rail 收束；
2. result/claim 卡在下游 180ms 显现；
3. produced edge 220ms 建立；
4. 卡面“新进展 +1”短暂高亮 600ms 后静止。

### integration_formed · Insight Crystallization

1. 相关输入边提升到 70% opacity；
2. 输入卡向各自目标位置移动，不能真的缩进 Insight；
3. integrates/derived_from 边汇向新 Insight；
4. Insight 卡 220ms 从 0.96 scale/opacity 进入；
5. level、输入数和贡献 Agent 依次显现，总时长 ≤900ms。

终态必须保留全部输入与关系；不能把输入视觉删除成“已合并”。

### insight_staled · Stale Propagation

1. 从失效输入沿 canonical ancestor path 分 6 段以内传播；
2. 每个祖先卡加 stale glyph/label，边改为 invalidated 语义；
3. 最小重新整合入口出现。

不用抖动、红色全屏闪或循环 pulse。

### dispute_opened · Conflict Fracture

1. contradicts/challenged_by 关系从普通边中提升；
2. dispute 卡在两侧立场之间出现；
3. position 扇面依次展开，最多 5 个；
4. 其他无关节点降到 40–45%，仍可读。

### deliberation_progressed · Turn Advance

新增 turn 在 Inspector timeline 180ms 插入；Canvas deliberation 卡只更新计数和结论摘要，不为每个 turn 新建全卡。

### lead_escalated · Director Escalation

1. deadlock 状态静态锁定；
2. 唯一粗阶梯 `escalated_to` 线 240ms 指向 Director/lead task；
3. Director Deck 单元提升并显示“接手裁决”；
4. camera 只在用户当前聚焦 dispute 时调整到同时容纳两端。

### team_membership_changed · Roster Shift

Deck 单元 FLIP 重排；加入 180ms 淡入，退出先显示 retired reason 再 180ms 淡出。Canvas team relation 局部更新，不重排全图。

### report_revised · Revision Replace

旧 revision 保留历史并降为 superseded；新 revision 卡从右侧 8px 淡入，版本号和 defect 修复计数高亮一次。

## 3. 交互微动效目录

这些效果不代表 canonical state，只反馈用户操作或解释视图变化。一次只允许 1 个空间重排效果和 1 个局部反馈效果。

### 3.1 Selection Magnet

- 触发：点击/键盘选择卡片。
- 分镜：四角 6px 外侧定位括号在 140ms 内收拢到卡边；selected ring 由 0→1 opacity；随后 camera 聚焦。
- 终态：括号与 ring 静止，不持续呼吸。

### 3.2 Port Invitation

- 触发：hover/focus 一个可展开端口。
- 分镜：端口扩大 2px、未加载数淡入、相关 edge 的前 24px 只描一次。
- 终态：保持可点击端口；pointer 离开 100ms 恢复。
- 禁止：端口自己闪烁吸引用户。

### 3.3 Constellation Expand

- 触发：展开第 2/3 层或 Display Group。
- 分镜：锚点保持；子卡从锚点方向偏移 8–16px 淡入；edge 随后建立；同批最多 6 个 stagger。
- 超预算：先展示前 6 个和“+n”分组，其余直接终态或不挂 DOM。

### 3.4 Constellation Fold

- 触发：折叠远端路径。
- 分镜：子卡 120ms 向锚点方向收束并淡出；边同步淡出；Display Group 在锚点位置 160ms 出现；布局 260ms 重排。
- 终态：分组卡显示节点/异常/Agent 数，不形成伪 Insight。

### 3.5 Lens Shift

- 触发：执行/探索/证据/Insight/争议 Lens 切换。
- 分镜：保留节点位置；非目标卡 140ms 降权，目标卡 180ms 提升，typed edges 180ms 交叉淡化；Inspector 内容 160ms 交叉淡入。
- 禁止：每次切 Lens 全图重新飞位。只有 filter 真正改变 visible set 时才重排。

### 3.6 Provenance Trace

- 触发：用户点击“查看证据路径”或聚焦 Claim/Insight。
- 分镜：从 Source Snapshot → Observation → Claim → Insight 沿已存在 edge 播放一次 360–600ms 的细光迹；每到一个节点，事实 badge 提亮 120ms。
- 终态：关系维持高对比，光迹消失；不循环。

### 3.7 Progress Tick

- 触发：节点收到新的 accepted result/claim/turn，但未形成新节点视图。
- 分镜：“新进展”计数数字旧值向上 4px 淡出、新值从下 4px 淡入；进度 rail 增量段 180ms 填入。
- 终态：只保留新数字和时间，不持续发光。

### 3.8 Failure Impact & Recovery

- failure：卡片左侧 rail 在 120ms 内收为断点，failure glyph 显现；不抖动卡片。
- retry：断点到新 Attempt 画一条 220ms retry edge，新 Attempt 淡入；旧失败节点保留。
- reassign：Agent avatar 旧→新交叉淡入，Deck 使用 Handoff，不把旧 Attempt 改名。

### 3.9 Inspector Origin Reveal

- 触发：打开 Node Inspector。
- 分镜：Inspector 从右侧 12px/opacity 0 在 180ms 内进入；Header 先出现，Sections 每批 2 个、stagger 30ms。
- 关闭：120ms 退出，焦点回卡片；camera insets 更新后才计算下一次定位。
- 禁止：共享元素跨整个屏幕放大正文，避免文字模糊和布局读写。

### 3.10 Minimap Glide

- 触发：主图 camera 移动或用户点击 minimap。
- 分镜：viewport frame 用与 camera 同一 authority token 更新；显式 minimap 定位使用 220ms，普通 pan 同帧跟随不再套第二个 easing。
- filter/fold 后 density blocks 160ms 交叉淡化，不画不存在的节点。

### 3.11 Sync Scan

- 触发：Snapshot resync 成功。
- 分镜：画布顶部一条 1px 语义扫描线在 280ms 内向下移动并淡出；节点只淡入最终状态，不重放旧 transition。
- 同步失败：扫描线不播放，Chrome 显示静态错误和重试。

### 3.12 Stage Advance

- 触发：后端确认 S1→S2、S2→S3、S3→S4。
- 分镜：StageRail 当前段 180ms 填充，下一段 label 提升；Canvas 当前 active roots 只做一次 120ms outline acknowledgment。
- 终态：完成阶段保持 check，不能用动画自行推进阶段。

### 3.13 Research Completion

- 触发：Run/交付 Gate canonical terminal success。
- 分镜：活动 edge 流动全部停止；顶层 fresh Insight 与 report revision 在 600ms 内依重要度依次提亮；Chrome 显示交付入口。
- 不做：彩带、全屏爆炸、遮挡报告按钮。失败/条件交付不能播放成功分镜。

## 4. 点击、展开与折叠

- 点击：先 selection ring，后 camera；Inspector 内容可与 camera 并行淡入，但不能先抢焦点。
- 展开：锚点屏幕位置保持；新层从端口附近进入，超过 6 个不 stagger。
- 折叠：子节点先淡出 120ms，再重排 260ms，Display Group 180ms 出现。
- 快速连续展开：取消旧动画，以最新 visible set 为终态。

## 5. 动效优先级与光效预算

发生冲突时按优先级执行：用户直接操作 > blocking/error > canonical transition > progress tick > ambient。低优先动效被高优先动效取消，不排队补播。

同一时刻最多：

- 1 个 active transition glow；
- 1 个 selected ring；
- 1 个 blocking/error attention mark。

静止画面不允许：全图流动粒子、所有 running 节点呼吸、所有边持续流光、背景高频动画。Atmosphere 只能是低频低对比且低性能/Reduced Motion 关闭。

## 6. 降级

| 条件 | 行为 |
| --- | --- |
| prefers-reduced-motion | 位移=0、duration=0；保留 badge/edge/终态 |
| low performance | 关闭粒子/glow，stagger=0，camera 保留 180ms 或即时 |
| document hidden | 丢弃动画 intent，只应用最新终态 |
| resync | 不补播历史，Snapshot 120ms 淡入 |
| >64 intents | coalesce 同 root/kind，旧 intent 丢弃 |
| 屏外节点 | 不动画，直接更新终态 |

## 7. 多模态验收镜头

每个视觉 PR 生成以下 artifact，编码 Agent 不读取内容：

1. before/mid/after 三帧；
2. 1440 dark + light；
3. 768 与 360 终态；
4. Reduced Motion 终态；
5. 连续点击中断录屏；
6. 100 delta 后终态截图与无动画 replay hash。

多模态验收检查：视觉焦点是否唯一、边是否穿卡、状态是否只靠颜色、文字是否被 glow 吃掉、camera 是否把节点放到 safe centre、动画结束后是否仍能理解事实。
