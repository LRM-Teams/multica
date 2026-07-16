# Engineering Principles — 隐性知识入基建（living doc）

> 来源：Frank 2026-07-16 指令「把隐性知识写进基建里……但是尽量避免隐性知识」。
> 本文是团队规矩/契约的**索引与"为什么"**；能进代码的必须进代码，文档只放代码装不下的判断。
> 维护规则：**每次拍板/定契约，当天写进本文或其指向的文档——聊天记录不算存放地。**

---

> ## 「外观一致不靠两边小心写得一样，靠结构上没法不一样。」
> — Felix, 2026-07-16
>
> 每条规矩先问：**"能不能让它在结构上没法违反？"** 能 → 它该活在代码里，不该活在文档里。

## 0. 标签制（本文的使用规则）

每条规矩**强制标注**其中之一：

- **`可执行`** — 已经或必须变成 lint / test / 门禁 / 类型 / API 形状 / 共享组件。文档只留"为什么"+ 指针。**三要件缺一 = `仅文档`：**
  1. **指认到具体的物**（哪条 lint、哪个 test、哪个类型、哪个 API 形状）；
  2. **owner 签字**：由**要实现它的工程师**认领；没 owner 签 = `仅文档`。
     （2026-07-16 实证：设计侧三条提案两条边界画错——`IssueChip` 全局禁会误伤编辑器操作态；"客户端不能排序"会把合法的本地数组扫进去。边界只有 owner 知道。）
  3. **owner 见过它红**：**每个验证装置必须先被证伪一次**——"没人见过它失败的 lint，等于'我希望它存在'"（Felix）。
     **验证装置本身会静默空转**，四个实例同一个毛病（全都"看起来在工作"——绿的、跑了、有断言，但什么都没测）：
     fixture 先改后写→快照天然等于真值（#622）；`data-ref-source` 若 `AppLink` 不透传 props 就是静默 no-op（#638 差点）；测试 mock 吞掉属性→断言变成测 mock（#638 差点）；量到 wrapper 元素→"token 不是蓝的"假结论。
     **样板流程（hover 验收三步法）**：先把实体改成"错的"值→hover 必须显示错值（见红=证明真现查）→再改回→这时的 PASS 才是真 PASS。只做最后一步=空转。
- **`仅文档`** — 只有真判断题（权衡、口径、依据分级）才够格。
  - **每条 `仅文档` 都是欠债，不是终点。哪天能变 `可执行`，就该搬走。**

**同类问题修到第二次，就该变成规则**（Frank）：不再让 agent/人一次次修实例，写成 lint/CI 一劳永逸。

**两个标杆案例**（新规矩照这个标准落地）：
1. **"把规矩变成一个不存在的字段"**：「hover 卡不许拿过期快照兜底」的可执行形态 = #507/#625 把 `part.ref_status`/`ref_title` 整个删掉——**没人能读到过期快照，因为快照不存在了**。配套 write-then-mutate 回归（#622）。
2. **"把具体 bug 锁进回归让它回不来"**：#521 的漏锚（`LRM-126。LRM-126` 相邻重复被 RE2 吞标点）——parser 结构修复 + N>2/相邻/UTF-16 span 回归，而不是在文档里写"要全部锚定"。

---

## 1. 消息写入管道（BE）

### 1.1 destination-first 统一 finalizer — `可执行`（已落地）
- **契约**：一切 agent 输出（普通 ChatDone、targeted send、thread、DM、transport send/send_draft、Radar/Wendy 内部写手）**必须经 `finalizeAgentChannelMessage` / `finalizedAgentTransportInsertInput` 再落库**；禁止直接 `insertChannelMessageWithParts*`。
- **顺序**：先解析唯一最终 destination → 按**目标频道**成员/可见性解析 mention/issue-ref → 落库/通知/wake。来源频道只用于权限/上下文。
- **caller 传来的 reference span 一律不可信**——finalizer 只保留按目标重验证的锚。
- **物**：#613（chat-done）/ #624（Radar/Wendy 三路）+ 结构约束测试（防新写手绕管道）+ destination 矩阵回归（默认群/同群 thread/跨群/跨群 thread/DM/无效 target/成员只在 B/同名歧义）。
- **已知例外（欠债）**：`service/quick_create_return.go` 仍直插且写 legacy `mention://`（task #510，#463 前置）。
- **身份边界**：handoff 内部 actor identity（`member.id`）≠ 频道 human mention identity（`user.id`）；回归必须显式构造两者不等（#624 教训）。

### 1.2 所有可见 occurrence 都要锚 — `可执行`（已落地）
- 同一 actor/issue 在正文出现 N 次 → N 个独立 span 锚；解析器不许吞边界字符（#521：regex 只匹配 identifier、边界独立校验）。
- **物**：#624 mention 契约回归；#637 issue-ref 相邻/N>2 回归；FE `data-ref-source` 断言（§2.4）。

### 1.3 写读不拆部署 / reader-first 删除 — `可执行`（流程门禁）
- 格式迁移三端（写边界/读渲染/输入端）同批上；删字段先停读后删写（#622→#507 顺序），**永不反向**。
- 迁移 forward-only、不留兼容层（precedent：migration 178/179）；**兼容的是路径，不是外观**（§2.3）。
- **物**：PR 模板"生效层：server / daemon / both"检查项（task #320，待落）。

## 2. 引用与渲染（FE）

（详细规范与验收清单见 Iris 设计稿；此处为契约要点。）

### 2.1 服务端给 entity，前端不搜文本 — `可执行`（地基）
- 渲染按 `parts[]` 的 span 投影（`projectInlineReferences`），never 正则、never 读 URI；无 part → 纯可读文本降级，**绝不露 `mention://` 等内部 URI**。
- **物**：#600/#601 投影器 + 测试；#520 兜底并线。

### 2.2 正文不可变 / 实体状态一律现查 — `可执行`（已落地）
- `parts[]` = 锚 + 身份（ref_id/label/span），仅此而已；status/assignee/priority/project 一律 `useResolvedIssue(ref_id)` 现查；快照不是渲染源也不是 fallback。
- **物**：#507/#625 字段已删（类型里没有=读不到）；#622 write-then-mutate 回归。

### 2.3 一处概念一个长相；兼容的是路径不是外观 — `可执行`
- 正文（阅读面）：引用=零装饰纯链接+hover 卡；编辑器（操作面）：chip 正确（原子 token 的功能信号）。两语境是两个概念，不许互相"统一"。
- 老数据兜底路径可留，**渲染必须与主路径共用同一组件**（`IssueRefLink`）——结构上没法长出第二副面孔。
- **物**：#520 共享组件 + restricted-import lint（`IssueChip` 只准 editor 目录 import，阅读态渲染链出现=构建失败）。拆迁日：#521 落地后兜底与 `data-ref-source` 按 #463/#510 整体删。

### 2.4 「猜」与「认」必须可分辨（对用户不撒谎；对自己必须说实话）— `可执行`
- 外观并线后漏锚会隐形 → token 带 `data-ref-source="anchor"|"fallback"`（用户不可见、测试可见）。
- jsdom 断言：N 次出现全 `anchor`（fallback 出现=漏锚 FAIL）；代码块/行内 code/URL 内不升级；兜底解析失败→纯文本（fallback+失败=正则误报，同一属性抓两类错）。
- **物**：#520 测试套件。

### 2.5 「别让界面撒谎」家族 — 主体 `可执行`
**宁可不显示，也不显示看起来对的假东西。** 变体：不假渲 / 不露 URI / 不弹空卡 / 不拿旧快照兜底 / 不显示"看着完整其实残缺"的列表（server-paginated 响应禁止客户端重排/重分组——顺序归 API 契约、跨页 fixture 证明，#635）/ 不按别处开关瞒本处事实（§3.2）/ 未知枚举值（status/priority）白名单降级隐藏、不崩不猜（#632 `toIssueStatus`/`toIssuePriority`）。

## 3. 属性显示（跨面）

### 3.1 本家属性语法 — `可执行`（task #518）
- 一个属性=一种语法=`[标记]+[文字]`（`ActorAvatar`/`ProjectIcon`+title/`PriorityIcon`，出处=已运行数月的 `list-row.tsx`）；assignee 必带头像；属性值不套 chip/pill/底色。
- **物**：#518 共享组件（组件不提供"裸文字 assignee"选项）；mobile 已有先例（`attribute-chip/attribute-row`）。

### 3.2 共享语法 ≠ 共享显示策略 — `可执行`（API 形状）
- 共享组件只接受"一项属性长什么样"，**不接受 `storeProperties` 等策略参数**；"哪些属性显示"归调用方。hover 卡永远"设了就显、没设不占位"。

## 4. Provider / 环境（daemon）

### 4.1 子进程环境合同 — `可执行`（task #512，进行中）
- 全局宿主私有变量禁继承（Raft SEA 标记等）+ per-provider 声明允许/禁止清单 + 显式 `custom_env` 最后叠加最优先 + 全 provider 矩阵回归。首刀已落：中央 provider-aware sanitizer（#627，Pi 声明 `PI_PACKAGE_DIR` 宿主私有）。
- provider 可执行路径/版本/模型目录须可审计解析（不依赖 PATH 顺序碰巧正确）——#512 范围，含 restart/PATH-drift 测试。

### 4.2 并发/投递 — `可执行`（已落地）
- 同 agent 聊天 lane 全局一条活 run，服务端 lease 排他（agent 行锁+active-delivery exclusion），daemon per-agent 单槽兜底；lease 永久拒绝→取消旧 executor 丢弃结果。聊天 lane 不读 `max_concurrent_tasks`（那是 issue/task 调度的公开配置，别混）。
- **物**：#611 + 双 wake/异 agent/失租回归。

## 5. 验证方法论 — `仅文档`（诚实标注：拦不住人，只能让"猜"显式化）

- **渲染活实体的功能，验收必须含"写入后变更"测例**（fixture 先改后写=永远假绿）。→ 有测试模板后升 `可执行`。
- **验证确切的那个面，不是那一族**：同名概念跨面（Activity 时间线 vs chat transcript vs 模型配置的"思考"）删改前写端+渲染端都要 grep 到；"页面上有个 token"不证明整条链路健康——先问 token 是谁写进去的。
- **先拉 parts 再归因**；两种渲染并存≠写手的锅。
- **"合了≠部署了"**：验收前先核 deploy 终态（readyz），拿旧版验新功能=假 FAIL。
- **数值必须注来源**（`getComputedStyle`/file:line/设计决定）；目测（尤其 2x 截图）不准进稿。量色/量身份取真正绘制的最内层元素。
- **验收分道**：数据/DB/投递→automation；hover/弹卡→自起 `--headless=new` Chrome（有合成器，rAF 正常）；真人只留观感与环境不可用两种情况。点击前先滚进视口。
- **依据分级**（设计稿必备节）：`抄`（注出处）/`定`（注理由）/`实测`（注 file:line）/`目测`（禁止）。别把"我们的选择"说成"Linear 就是这么做的"。

## 6. 元规矩：别拿没验证的环节当地基 — `仅文档`（本文立身之本）

> 「当"这条假说解释不了不对称"时，先问"是不是我假设的那个环节本身有 bug"，别拿它当反证。」— Felix

2026-07-16 一天内三人各违反一条**自己写过**的规矩：Felix 拿"服务端不会漏"否掉自己正确的假说；Parker 拿版本假说当结论（差点催错升级）；Iris 目测字号当事实。**三条都写下来过，照样犯。** 当天真正拦住错误的三样全是可执行物：`data-ref-source`、#521 回归 fixture、react:doctor 门禁。

> **"写下来 ≠ 拦得住。" 把"靠人小心"换成"结构上做不到"，才是消灭隐性知识的工程形态。文档是退而求其次。**
> **本文每一条 `仅文档` 都是欠债；哪天能变 `可执行`，就该搬走。**

## 7. 本周教训 → 归层对照表（2026-07-14 ~ 07-16）

| 教训/规矩 | 层 | 物 | 状态 |
|---|---|---|---|
| 快照陈旧 | 类型（删字段）+测试 | #507/#625/#622 | ✅ |
| 写手绕管道 | 结构约束测试 | #613/#624 | ✅（#510 欠） |
| 漏锚/吞边界 | parser+回归 | #637 | review 中 |
| 漏锚可观测 | `data-ref-source` 断言 | #520 | 进行中 |
| chip 第二面孔 | 共享组件+restricted-import lint | #520 | 进行中 |
| 属性语法孤儿 | 共享组件 API | #518/#636 | #636 review 中 |
| 客户端假全局序 | 服务端 sort/group+跨页 fixture | #635 | review 完 |
| 未知枚举崩卡 | 白名单降级 | #632 | ✅ |
| env 泄漏/PATH 漂移 | 中央 sanitizer+合同 | #627/#512 | 首刀✅/合同进行中 |
| 同 agent 并发分身 | lease 排他+单槽 | #611 | ✅ |
| 本地 test-DB drift | 迁移 bootstrap+guard | #634 | review 中 |
| 每文件一组件（工具盲区） | 拆件+真 0 验证 | #515 | 进行中 |
| legacy `mention://` 写入回流 | CI grep（候选） | 待立 | ⛔ 待 owner 签 |
| 写后变更测例模板 | 测试模板（候选） | 待立 | ⛔ 待 owner 签 |

---
维护人：Parker（产品）。规矩变更走 PR；`可执行` 升降档需 owner 签字。
