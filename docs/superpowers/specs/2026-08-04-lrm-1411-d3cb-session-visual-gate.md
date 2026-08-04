# LRM-1411 — d3cb 真会话复合视觉门

> 状态：验收矩阵已建立；集成像素证据等待可访问的 d3cb 真会话。  
> 路由：`/lrm-team/research/d3cb52ae-bb85-4731-91d7-30c779063770`。  
> 边界：本单只做运行验收和缺陷路由，不修改生产组件、数据投影、布局或动效。

## 1. 结论规则与证据格式

最终结论只有三种：

- **PASS**：第 3～9 节全部有真实运行证据且无未达项。
- **RETURN**：可运行 revision 存在明确视觉/交互缺陷；每个根因新开一个小 issue。
- **BLOCKED**：真会话、认证或集成 revision 不可用；不得用 mock、组件测试或设计稿代替。

每张截图必须是无标注、未合成的浏览器原始像素，并记录：commit SHA、部署版本、完整 route、session id、主题、viewport、DPR、浏览器 zoom、`prefers-reduced-motion`、选中节点和触发步骤。文件名：

`lrm-1411_<sha>_<case>_<theme>_<viewport>_<state>.png`

Before/after 必须使用相同 route、viewport、主题、zoom、选中对象和滚动位置。用户截图只作 Before；After 必须来自实现 revision 的真会话。

## 2. 页面职责与验收顺序

四个表面分别取证，不用一个表面的结果替代另一个：

1. 顶部 stage：`ResearchSessionStageEnergy` / `ResearchStageTimeline`。
2. fleet/命令台：`ResearchFleetStrip`、执行步骤卡与聊天命令入口。
3. canvas：`ResearchCanvas`、`ResearchGraphNode`、窄屏 `ResearchGitList`。
4. 详情/决策舱：`ResearchNodeDetail`、aux drawer、产品轮/人类确认卡。

页面扫读顺序固定为：**当前决策 → 正在执行 → 阻塞 → 历史**。装饰层、光晕、连线、能量段和浮层都不得盖住状态文字、Agent 名称、按钮、focus ring 或详情正文。

## 3. 数据预检

截图前保存只读快照，确认该会话在同一时刻包含：

- session 为 `running`、当前阶段为 S2；
- fleet 共 5 人；恰好 1 人 running；其余可同时看到 ready/pending；恰好 1 个 failed；
- canvas 初态为 sparse，随后收到足以形成 tree 的真实节点/边更新；
- 至少一个可选节点有详情；至少一个决策/阻塞/历史条目可达。

若真实数据不满足任一条件，记录缺失字段和时间点，结论为 BLOCKED；不得在 DevTools 改 response、注入 fixture 或用静态 Storybook 截图补齐。

## 4. 可重复截图矩阵

| Case | Viewport / theme | 触发与稳定点 | 必须同时可见 | 自动/人工断言 |
|---|---|---|---|---|
| M01 | 1440×900 light | 初次进入，网络稳定后 | stage + 5 人 fleet + canvas + 命令入口 | 1 running、ready/pending、1 failed 均有文字/图标双编码 |
| M02 | 1440×900 dark | 与 M01 同一数据快照 | 同 M01 | 状态名称、顺序和控制可达性与 light 一致 |
| M03 | 390×844 light | 窄屏初次进入 | stage 摘要、fleet、Git list/详情入口 | document 无横滚；触控目标 ≥44px；状态不被截断 |
| M04 | 390×844 dark | 与 M03 同一快照 | 同 M03 | 暗色下文字 AA、边界/焦点 3:1，不靠透明度隐藏 |
| M05 | 1440×900 light | sparse 首帧 → tree 稳定终帧 | 相同选中对象和 zoom label | 期间无白屏；最终 zoom 在自动 fit 合理范围且不突跳 141% |
| M06 | 1440×900 dark + reduce | 重复 M05 | tree 最终态 | 动画直接落终点；无脉冲/自动平移动画；ARIA 保留 |
| M07 | 1440×900 light | 打开命令台/执行流 | 当前决策、正在执行、阻塞、历史 | DOM 与视觉顺序一致；装饰不压字；Esc/焦点返回有效 |
| M08 | 1440×900 dark | 与 M07 同一状态 | 同 M07 | pending/success/error 文案、图标、边界均一致 |
| M09 | 1440×900 light | 选节点并开详情 | selected 节点 + 详情/决策舱 + stage/canvas 上下文 | selected 不抹掉其他节点；详情完整、可滚动、可关闭 |
| M10 | 390×844 dark | 窄屏选节点并开 Sheet | selected 行 + Sheet | 无窄浮卡；关闭按钮不裁切；关闭后焦点回触发项 |
| M11 | 1440×900 light | 键盘遍历命令台、节点、详情 | focus + selected | focus 不等同 selected；ring 不被 overflow 裁切 |
| M12 | 1440×900 dark | 依次制造 pending→success 与独立 error | 三个终态各一张原图 | 不只靠颜色；error 不与 loading/skeleton 混态 |

M05 另录一段从 sparse 首帧到 tree 稳定后 2 秒的无剪辑视频或 Playwright trace。记录每次 zoom 文案变化、`transform`、节点数、边数和页面 `scrollWidth/clientWidth`。若产品明确要求 141% 可作为最终 fit，必须提供稳定布局计算依据；否则任何无用户输入的瞬时/最终 141% 都按 RETURN 记录。

## 5. 交互状态矩阵

同一主题下，选一张普通节点、一张运行节点、一个命令台动作和一个详情动作，分别取：

- default：状态文字常显；
- hover：仅轻量背景/描边反馈，不增删信息、不 scale；
- active/pressed：有形状或位置反馈，且不冒充 selected；
- focus-visible：项目统一 ring，和相邻色至少 3:1；
- selected：至少使用稳定轮廓/位置/图标之一，未选内容仍可读；
- pending：文字 + 进度语义，控制防重复提交但正文不整体降 opacity；
- success：文字/图标 + 语义 token，不只绿色；
- error：安全错误摘要 + 恢复入口，不暴露 raw error/task code。

每种状态至少一张原始截图；hover、focus、selected 必须分别取证，不能用一张组合图代替。

## 6. 默认、加载、空、错误、禁用、权限

四个表面逐项核对：

| 状态 | 要求 |
|---|---|
| 默认 | 页面信息层级和四表面入口完整；无假状态文案 |
| 加载 | 保留稳定几何；不得和 error 同屏；skeleton 不伪造 Agent/节点内容 |
| 空 | 说明尚无数据与下一步；不渲染假 fleet/tree |
| 错误 | 安全摘要、重试入口、焦点可达；保留已知上下文时不得伪造新数据 |
| 禁用 | 可见原因；`aria-disabled`/原生 disabled 与视觉一致；不靠祖先 opacity |
| 权限 | 不泄露 Agent、节点、结论、证据或拓扑；仅保留获准导航 |

## 7. 布局、token 与主题

- 桌面 1440：决策/执行/阻塞/历史主次清楚；详情 drawer 不把 canvas 压到不可读。
- 窄屏 390：正文单列，使用 Git list 与 Sheet；document 无横滚；stage/fleet 不挤成不可辨色条。
- 另验 768×900 和浏览器 200% zoom：文本可重排，功能与信息不消失，浮层不双重遮挡。
- 只接受项目 token：`background/card/popover/muted/border` 与 `brand/success/warning/destructive/info`；禁止裸 hex 和固定 Tailwind 色。
- 同屏文字最多 foreground、muted-foreground 和一个当前语义色；只用项目允许字号/字重。
- lane/Agent 身份色与 status 语义分离；running/ready/pending/failed 必须有文字或图标/形状双编码。

## 8. 可访问性与动效

- Tab/方向键能抵达命令入口、fleet 状态、节点、详情关闭和决策动作；Esc 关闭顶层浮层并还焦。
- accessible name 至少包含对象标题、Agent/路径身份和状态；failed/pending/selected 不只靠颜色。
- DOM/读屏顺序遵循当前决策→正在执行→阻塞→历史；装饰 SVG/图标 `aria-hidden`。
- 普通文字对比度 ≥4.5:1；大文字、边界和 focus indicator ≥3:1；light/dark 测最内层实际绘制元素。
- 正常 motion 只表达一次状态变化，不循环抢注意力、不以 scale 改布局。
- `prefers-reduced-motion: reduce` 下取消重组、路径绘制、粒子、脉冲和自动平移；元素与 ARIA 语义保留。

## 9. 自动记录与性能门

Playwright 运行必须保存 screenshot、trace 和 JSON 摘要。最小断言：

- `document.documentElement.scrollWidth <= clientWidth`；
- 页面在 sparse→tree 期间持续有可见 session shell，不出现空白整页；
- 节点/边数量只来自真会话 API；
- 未进行缩放手势时，zoom 不出现无依据的 `141%`；
- stage、fleet、canvas、detail 在主题切换后状态名称/`aria-*` 不变；
- focus、selected、pending、success、error 的非颜色线索各自存在；
- reduced-motion 下相关元素 computed `animation-name: none`，自动 fit duration 为 0。

性能证据记录 sparse 更新起点、tree 首次稳定、最长主线程任务和是否发生页面白屏。验收门：无 ≥1s 单次主线程长任务、无连续 2s 不可交互；若实际设备基线另有产品裁定，保留原始 trace 并升级确认，不能靠扩大 timeout 放过。

## 10. 缺陷路由与文件边界

每个未达项记录：case、SHA、部署、route、theme、viewport、zoom/motion、expected、actual、原始截图/trace。相同根因只开一个 issue；新 issue 指向唯一 owner，本验收单不旁路修复。

| 根因 | 建议实现面 |
|---|---|
| stage 状态/能量段/主题 | `research-session-stage-energy.tsx`、`research-stage-timeline.tsx` |
| fleet 身份与 running/ready/pending/failed | `research-fleet-strip.tsx`、fleet step/card 投影 |
| sparse→tree、fit/zoom、白屏、reduced motion | `research-canvas.tsx`、canvas layout/motion lib |
| 节点 hover/focus/selected/status | `research-graph-node.tsx`、`research-git-list.tsx` |
| 详情/决策舱层级与状态 | `research-node-detail.tsx`、aux drawer、product-round/confirm card |
| 跨表面状态投影不一致 | core typed research projection / session query owner |

## 11. 2026-08-04 基线

- QA checkout：`origin/dev@a8bee408b`。
- 指定 HTTPS route 在本执行环境建立代理连接后被对端 reset，未取得认证页面或运行像素。
- checkout 无 `node_modules`，本机无可用 Chromium binary；因此本轮不能生成可信截图、trace 或 computed-style 证据。
- 源码中可定位 stage、fleet、canvas、Git list、detail 与决策卡组件，但源码存在不等同于 d3cb 集成状态通过。
- 当前结论：**BLOCKED（矩阵已就绪，等待可访问的真会话/实现 revision 后复核）**。
