# 时段工作介绍 — 实现 Todo

## 给实现 AI 的读法（先读这段）

1. **严格按 Slice 顺序做**：完成 Slice N 的全部 checkbox 并满足「完成标准」后，再开 Slice N+1。
2. **一次只做一个 `- [ ]` 任务**：开干前在任务下写「进行中」说明；做完勾成 `- [x]`。
3. **每个任务块都自包含**：含目标 / 要做 / 不要做 / 依赖 / 完成标准。缺信息时先查「代码锚点」与合同文档，再查 `CLAUDE.md`，不要臆造合同。
4. **已定决策是硬约束**，不可在实现时改口径；若代码做不到，停下来问人，不要静默放宽。
5. **测试跟代码走**：Computer / digest 协议测 `server/internal/computer/` 与 handler；共享 UI 测 `packages/views`；Go 测 `server/`。改前端后按仓库要求跑相关检查（用户要求时再跑全量 `make check`）。
6. **复用现有回顾 Facts 与 Note Worker**，禁止另起周报编排引擎，禁止把 LLM 塞进回顾 API。

术语用 `CONTEXT.md` → Period Work：Period Work Brief、Machine Work Journal、Work Digest、Period Work Synthesis。

---

## 目标（一句话）

Computer Owner 选定时间窗后，系统采集 **平台工作 + 整机工作痕迹**（不论是否由 Multica 下发），交给 Agent **筛选整理**，在 **Notes** 里写出一份能向领导/同事汇报的 Period Work Brief。

---

## 核心模型

```text
人：时间窗 + 汇报意图
  ├─ 平台 Facts（Issue / 笔记 / run / 链接 PR）     ← 确定性，服务端
  └─ Work Digest（整机 git commit + 脏路径）         ← Computer，Owner-only
        ↓ 汇合
  Agent Period Work Synthesis（筛选、分类、写主线）
        ↓
  Notes：Period Work Brief（人可读、可改）
```

| 角色 | 含义 |
|------|------|
| Period Work Brief | 汇报用笔记正文；不是活动列表，不是 PPT 文件 |
| Machine Work Journal | Computer 上的本机工作志；观察边界 = Owner 这台机器 |
| Work Digest | 某一时间窗的有界摘要：仓库、commit 元数据、脏路径；**无文件正文** |
| Period Work Synthesis | 一个 Note Worker job：读 Facts + Digest，写出 Brief |
| 本机未归类工作 | Digest 里对不上当前 Workspace 远程的仓库，单独成节，不混进团队主线 |

---

## 禁止做（永久非目标）

- 键鼠、截屏、窗口标题、剪贴板、浏览器历史
- 上传或让模型阅读文件正文 / git diff 全文 / session thinking
- 把 Agent Daily（`memory/daily`）当采集源或灌进笔记
- 密钥、`.env` / `.ssh` 等敏感路径进入 Digest；runtime 诊断进入模型上下文
- 非 Computer Owner 读取 Digest（Workspace 成员/管理员都不行）
- 在 `POST /api/notes/retrospectives` 里跑模型
- 新周报编排引擎、Research Fleet 硬绑、导出 `.pptx`
- 把采集范围缩回「仅 Agent Workspace」或「仅 Multica 下发的任务」
- 静默 `replace_page` 改正文；合成走 Worker + 人可见的笔记写入（`--note-write` 或新建页）

---

## 已定决策（硬约束）

| ID | 决定 |
|----|------|
| D1 | 观察边界是 **Computer Owner 整机工作痕迹**，不要求 Multica 下发，不限 Agent Workspace |
| D2 | 本机只采 **git commit 元数据 + 脏文件路径**；HOME 发现仓库 + denylist。不是无过滤全盘 |
| D3 | 平台 Facts 与 Work Digest **先汇合再交给 Agent 筛选**；回顾 API 保持确定性 |
| D4 | 产物只在 **Notes** 展示，不写 PPT |
| D5 | Digest **仅 Owner** 可启用/拉取；对不上当前 Workspace 的仓库进「本机未归类工作」 |
| D6 | Journal 默认 **关**；未开启时本机源诚实为空，Brief 仍可用平台 Facts 降级生成 |
| D7 | 权威合同：ADR `docs/adr/0018-machine-work-journal-period-brief.md`；原则 `docs/engineering-principles.md` §4.24 |

---

## 代码锚点（实现前先打开看）

| 区域 | 路径 |
|------|------|
| 合同 / 术语 | `docs/adr/0018-machine-work-journal-period-brief.md`、`CONTEXT.md` § Period Work、§4.24 |
| Computer 进程 | `server/internal/computer/host.go`（机器级，**不要**把 Journal 放进每个 DaemonCore） |
| Owner 鉴权 | `server/internal/handler/computer_generation.go` → `authorizeComputerOwnerRequest` |
| Computer 列表 | `server/internal/handler/computer_projection.go` |
| 既有 WS 命令形态 | `server/pkg/protocol/workspace_runner_activity.go`（升级命令是形态参考，Journal 另开命令，勿复用 upgrade payload） |
| 平台 Facts | `server/internal/handler/note_retrospective.go`、`note_retrospective_create.go` |
| Issue ↔ PR | `server/pkg/db/queries/github.sql`、`attachPullRequestsToIssues` |
| Note Worker | `server/internal/handler/note_worker.go`、`note_worker_prompt.go` |
| Worker 剧本 | `packages/views/notes/note-worker-playbooks.ts`、`note-worker-run-dialog.tsx` |
| 回顾 UI | `packages/views/notes/note-retrospective-dialog.tsx` |
| 笔记写入 | `--note-write` / `appendAgentNoteWritePart`；`docs/notes-editor-worker-contract.md` |

---

## 已完成基线（勿重做）

| 基线 | 状态 | 要点 |
|------|------|------|
| 合同落盘 | ✅ | ADR 0018、CONTEXT Period Work、§4.24 |
| 日/周/月回顾列表 | ✅ | 平台 Facts 列表；**不是** Brief；默认还不含整机 |
| Note Worker + 三剧本 | ✅ | coordinate / hire / writeback；本 todo 只加汇报剧本，不改协作合同 |

---

## Slice J1 — 本机工作志（P0 · 先做）

**本 Slice 完成标准：** Computer Owner 开启 Journal 后，能按时间窗拿到自己的 Work Digest（git + 脏路径）；未开启或非 Owner 拿不到；类型上无法携带文件正文。

- [x] **J1-T1 Work Digest 协议（结构上不能装正文）**
  - **已落地（2026-08-17）**：`server/pkg/protocol/work_digest.go` — `ParseWorkDigest` 用 `DisallowUnknownFields`，类型无 content/diff/patch/body；`Validate` 卡每仓 200 commit / 200 dirty、subject 200 字、path 512 字节。测：`work_digest_test.go`。
  - **目标**：Digest 的类型/JSON 没有「文件内容」字段，做错的事没有对象。
  - **要做**：在 `server/pkg/protocol/`（或 `internal/computer` 导出、handler 共用）定义窗口、`computer_id`、仓库列表。每仓：`root`、可选 `remotes`、`commits[]`（hash / at / author / subject / file_count / insertions / deletions）、`dirty[]`（path / status：added\|modified\|deleted\|untracked）。`Validate()` 拒绝超限（建议：每仓 commit ≤ 200、dirty ≤ 200、subject ≤ 200 字、path ≤ 512）。
  - **不要做**：`patch`、`diff`、`content`、`body` 字段；把 Daily / thinking 放进协议。
  - **依赖**：无
  - **完成标准**：单测：合法 digest 通过；带 content/diff 的 JSON 无法解码或 Validate 失败；超限失败。

- [x] **J1-T2 denylist（发现与脏路径共用）**
  - **已落地（2026-08-17）**：`server/internal/computer/work_journal_denylist.go` — `WorkJournalDeniedRepoRoot` / `WorkJournalDeniedDirtyPath` / `FilterWorkJournalDirtyPaths`。目录段含 `node_modules`、`.next`、`dist`、`build`、`target`、`vendor`、`__pycache__`、`.cache`、`.ssh`、`.gnupg`、`.git` 则跳过仓或丢 dirty 条；basename `.env*` / `id_rsa*` / `*.pem` / `credentials.json` 丢 dirty。测：`work_journal_denylist_test.go`。
  - **目标**：噪声和密钥目录进不了 Digest。
  - **要做**：单一 denylist（代码常量 + 测）：至少 `node_modules`、`.git` 对象以外的依赖/缓存（`.next`、`dist`、`build`、`target`、`vendor`、`__pycache__`、`.cache`）、`.ssh`、`.gnupg`、basename 匹配 `.env*` / `id_rsa` / `*.pem` / `credentials.json`。发现 git 根时跳过 denylist 路径下的仓库；dirty path 命中则丢弃该条，不丢整个仓。
  - **不要做**：把 denylist 做成每用户随便改的远程配置（一期代码内置即可）；因一个脏路径丢弃整个仓的 commits。
  - **依赖**：J1-T1
  - **完成标准**：测：HOME 下 `~/code/app/node_modules/pkg` 不算仓；`.env` 不出现在 dirty；普通 `~/code/app` 仍被发现。

- [x] **J1-T3 Computer 侧索引 + 窗口收割**
  - **已落地（2026-08-17）**：`HarvestWorkJournal` 在 `server/internal/computer/work_journal.go`（Host 同级，不进 BindingSupervisor）。临时 HOME 测：窗口内 commit + dirty 入 digest，`node_modules` 仓省略；`Enabled=false` → `disabled: true` 且 repos 空。`git log --no-patch --numstat`，无 `--patch`。
  - **目标**：机器级 Computer 进程在 Owner HOME 发现 git 根，按窗口收割，不进 DaemonCore。
  - **要做**：模块放 `server/internal/computer/`（Host 同级，不挂 BindingSupervisor）。开启后：索引 `$HOME` 下 `.git` 目录的父路径；对窗口 `git log`（不要 `--patch`）+ `git status --porcelain`。Journal 关闭时收割返回空 repos 且带 `disabled: true`。索引可落本机文件，**不要**把仓库清单默认同步进服务端 DB。
  - **不要做**：inotify 全盘长驻；扫 `/` 或他人 HOME；在每个 Workspace DaemonCore 里各扫一遍；调用 Agent 进程来 git。
  - **依赖**：J1-T1、J1-T2
  - **完成标准**：Go 测用临时目录当 HOME：造两个仓、一个在 denylist 下；窗口内 commit + dirty 出现在 digest，denylist 仓不出现。关闭开关 → `disabled: true` 且 repos 空。

- [x] **J1-T4 Owner-only 拉取**
  - **已落地（2026-08-17）**：`GET /api/computers/{daemonId}/work-digest?start=&end=`（RFC3339 半开区间）。`authorizeComputerOwnerRequest`；`computer:work-digest` / `computer:work-digest:done` 新 payload，不复用 upgrade。离线 `503 computer_offline`。未开启 200 + `disabled` 空 repos。Digest 不入库。
  - **目标**：只有 Computer Owner 能对某台 Computer 要某一窗口的 Digest。
  - **要做**：`GET`（或等价 RPC）`/api/computers/{id}/work-digest?start=&end=`（RFC3339，半开区间）。鉴权走 `authorizeComputerOwnerRequest`（或同等「必须是该 Computer 的 Owner」）。服务端向 **该 Computer 的 resident** 要收割结果（新 Computer 控制命令，参考 upgrade 的 request/response 形态，**新 payload 类型**）。Computer 离线 → 明确错误（不要用平台 Facts 假装本机源成功）。
  - **不要做**：Workspace 成员/管理员代拉；把 digest 写入可被他人 query 的表；digest 进 Activity feed。
  - **依赖**：J1-T3
  - **完成标准**：Handler 测：Owner 200 + 形状符合 J1-T1；同 Workspace 非 Owner 403；未开启返回 `disabled` 空 repos 而非 500。

- [x] **J1-T5 Owner 启用开关**
  - **已落地（2026-08-17）**：本机 `{resident}/work-journal.json` 为权威（缺文件=关）；Owner `PATCH /api/computers/{id}/work-journal`；列表投影 `work_journal_enabled`；Notes 回顾入口未开启时提示「本机工作未采集」，不阻断。测：Host 关→开→有仓→再关；handler 非 Owner 403、离线 503。
  - **目标**：Journal 默关；Owner 能打开/关闭自己的 Computer。
  - **要做**：Computer 本机设置 + 服务端能读到 enabled 位（本机权威即可，列表 API 可投影 `work_journal_enabled`）。Notes 入口在未开启时提示「本机工作未采集」，仍允许只用平台 Facts 继续。
  - **不要做**：Workspace 级总开关替 Owner 打开别人的机器；默认扫描未同意的机器。
  - **依赖**：J1-T4
  - **完成标准**：关→开→拉 digest 从空变为有仓（fixture）；开→关再拉 `disabled`。

---

## Slice J2 — 平台 Facts 打包（P0）

**本 Slice 完成标准：** 同一时间窗能取出平台 Facts，Issue 带链接 PR，并能把 Digest 仓库标成「当前 Workspace」或「本机未归类」。仍不跑模型。

- [ ] **J2-T1 窗口 Facts 包（不写回顾笔记）**
  - **进行中（2026-08-17）**：抽出 `loadNoteRetrospective*Facts` 为可复用函数，不写笔记、不跑模型。
  - **目标**：合成入口能拿到与回顾同源的 Facts，而不必先插入一篇「回顾 YYYY-Www」列表页。
  - **要做**：抽出 `loadNoteRetrospective*Facts` 为可复用函数（同包或小 module），输入 `workspace, user, start, end, sources`。本入口默认源：`issue_activity`、`touched_notes`、`agent_runs`。保留亲手/委派/仅相关归因。
  - **不要做**：改回顾 markdown 模板当 Brief；在 loader 里调模型；删除现有「生成回顾」按钮（可并存）。
  - **依赖**：现有 retrospective loader；建议 J1 已可返回 digest（合成要两端，但本任务可先单测平台侧）
  - **完成标准**：Go 测：窗口内 Issue/笔记/run 出现；窗口外不出现；归因与现回顾测试一致。

- [ ] **J2-T2 Issue 挂链接 PR**
  - **目标**：Facts 里的 Issue 带窗口内（或当前仍链接的）PR 标识与状态，而不是另开一节 PR 流水。
  - **要做**：复用 `issue_pull_request` / `ListPullRequestsByIssue`（或 `attachPullRequestsToIssues`）。每条 Issue fact 附 `pull_requests[]`：number、url、state、title。无 PR 则空数组。
  - **不要做**：把 GitHub API 同步放进这条任务；把 PR patch 塞进 Facts。
  - **依赖**：J2-T1
  - **完成标准**：测：链了 PR 的 Issue 在包里能看到 url+state；未链的为空数组。

- [ ] **J2-T3 Digest 归仓：Workspace vs 未归类**
  - **目标**：Agent 合成时能区分「这是本 Workspace 的活」和「这台机器上的其他仓」。
  - **要做**：用 Workspace 已绑 Git 项目的 remote URL 与 digest `remotes` 做规范化匹配（去 `.git`、大小写、ssh/https 等价）。匹配上 → `scope=workspace`；否则 `scope=unscoped`。匹配失败不得丢弃该仓。
  - **不要做**：把未归类仓静默删掉；用本地文件夹名猜 Workspace。
  - **依赖**：J1-T1 形状；J2-T1 可并行，但本任务需要 digest + 项目 remote
  - **完成标准**：测：remote 等于项目 URL → workspace；只存在于 HOME 的无关仓 → unscoped。

---

## Slice J3 — Agent 筛选 + 笔记入口（P0）

**本 Slice 完成标准：** Owner 在 Notes 一键生成 Period Work Brief：汇合 Facts+Digest → Worker 筛选 → 笔记里是汇报主线，不是采集清单。

- [ ] **J3-T1 Worker 剧本 `period_brief`**
  - **目标**：instruction 锁成「给领导/同事的汇报稿」，不是协作收工，也不是 PPT。
  - **要做**：`note-worker-playbooks.ts` 增加 `period_brief`；`prefersChannel: false`（默认 Agent DM）。中英文 locale。instruction 稳定要点：一句话主张；3–7 条主线（每条≤3 要点且尽量带 Issue/PR/仓路径证据）；委派杠杆；未完成；「本机未归类」单独一节；禁止罗列原始 commit；禁止编造无证据主张。
  - **不要做**：默认发到群频道；文案再写「适合放进 PPT」。
  - **依赖**：现有 playbook 机制
  - **完成标准**：组件测：选剧本 → instruction 含「主线 / 未归类 / 不要罗列」等稳定关键词；destination 不是强制频道。

- [ ] **J3-T2 合成派发（Facts+Digest 进 Worker，回顾不跑模型）**
  - **目标**：一个产品动作完成拉取、汇合、派发 Synthesis。
  - **要做**：新 endpoint（例如 `POST /api/notes/period-briefs`）或等价：鉴权 Owner；解析窗口；拉平台 Facts（J2）；对 Owner 的在线 Computer 拉 Digest（J1，失败/未开启记入 `sources_empty`，不整单失败）；写一份 **私有底稿笔记**（或 Worker brief 分区）包含 Facts + Digest JSON/markdown；`createNoteWorkerJob` + `period_brief` instruction。Prompt 分区：`<system_contract>` / untrusted `<facts>` / untrusted `<digest>` / `<instruction>`，转义规则同 Worker。
  - **不要做**：在 retrospective handler 里调模型；Digest 明文进群频道；新 job 表以外的编排状态机。
  - **依赖**：J1-T4、J1-T5、J2-T1、J2-T3、J3-T1
  - **完成标准**：Handler 测：Owner 派发成功且 job 带 brief；非 Owner 403；Journal 关闭时仍派发且 digest 分区标明 disabled；prompt 转义防 `</digest>` 截断。

- [ ] **J3-T3 Notes UI「本期工作介绍」**
  - **目标**：人不必先生成回顾再手改 instruction。
  - **要做**：Notes 页入口（与「生成回顾」并存、文案分开）。选窗口（先复用日/周/月）→ 选执行 Agent → 调用 J3-T2 → 打开 Worker 目的地（DM）。未开 Journal 时显示降级说明，不阻断。
  - **不要做**：和「生成回顾」做成一个按钮两套合同；在 UI 里渲染原始 digest 当成品。
  - **依赖**：J3-T2
  - **完成标准**：组件测：点击走 period-brief API 而非仅 `createNoteRetrospective`；locale 中英齐全。

- [ ] **J3-T4 产物落入 Notes**
  - **目标**：Agent 写出 Brief 后，人在笔记里看到汇报稿并可改。
  - **要做**：instruction 要求 `--note-write`（新建 Brief 页，或写入指定 page）。标题建议 `工作介绍 {窗口标签}`，放在 Owner 私有树（可与 `回顾/` 并列，例如 `工作介绍/`，**不要**覆盖回顾列表页）。人点确认才落正文（现有 note_write 按钮即可）。
  - **不要做**：等 N3 待审 writeback 才交货；Agent 直写 `replace_page`；把底稿 Facts 页当成 Brief 展示给领导。
  - **依赖**：J3-T2、现有 `--note-write`
  - **完成标准**：手测或组件测：合成回复带 note_write；确认后存在 Brief 笔记，正文含主线结构而非纯 commit 列表（可用 fixture Agent 输出断言结构标题）。

---

## Slice J4 — 可选增强（勿阻塞 J1–J3）

- [ ] **J4-T1 自定义半开区间**（不止日/周/月）
  - **不要做**：先做任意日历产品化而 J3 还不通

- [ ] **J4-T2 每周节奏**（Routine 打同一 `period-briefs` 入口）
  - **不要做**：另做 standup 管道或挂在 `agent_reminder` 上凑合

---

## 进度建议

| 顺序 | 下一步 |
|------|--------|
| 1 | ~~J1-T1~~ Digest 协议（已完成） |
| 2 | ~~J1-T2~~ denylist（已完成）→ ~~J1-T3~~ 收割（已完成）→ ~~J1-T4~~ Owner 拉取（已完成）→ ~~J1-T5~~ 开关（已完成） |
| 3 | J2（平台包 + PR + 归仓） |
| 4 | J3（剧本 → 派发 → UI → 落笔记） |
| 5 | 仅当产品需要时再开 J4 |

**当前焦点：** Slice J1。下一个 checkbox：**J1-T5**。
