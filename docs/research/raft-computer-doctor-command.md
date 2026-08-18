# Raft Computer 1.0.16 `doctor` 命令研究

日期：2026-08-17
研究对象：Raft 官方 CDN 发布的 `raft-computer` **1.0.16** Linux x64 SEA 制品
目标：确认最新版本的真实命令合同、检查项、副作用和可借鉴设计，并对照 Multica 当前实现。

## 结论

**可以做，但 Multica 其实已经有这个命令：`multica computer doctor [/<workspace>] [--fix] [--output json]`。** 当前问题不是“要不要新增命令”，而是要不要把现有的字段快照深化为 Raft 1.0.16 那种可执行的逐项诊断。

Raft 1.0.16 最值得借鉴的变化是：

1. 检查链已经从旧版的“登录 + attachment + preflight”扩展为 **机器身份 → service → attachment → server preflight → per-Server runner → migration identity**。
2. 每个失败项都直接给恢复动作和日志位置，而不是只打印布尔值。
3. 默认诊断不修复；`--fix` 在采证后才执行白名单 cleanup，并逐项报告 mutation。
4. collector 返回结构化 checks，presenter 统一决定文本和退出码。

建议保留 Multica 现有命令入口和更保守的 `--fix`，优先补齐 **结构化 checks、Workspace Binding/child 逐项健康、稳定退出合同和 actionable remediation**。不建议重新加一个平行命令，也不建议照抄 Raft 的宽泛 cleanup。

## 版本与证据

本次没有再使用 npm 的 `@botiverse/raft-computer@0.0.70` 代替最新版。Raft 官方安装器从 CDN manifest 解析版本并校验制品 SHA-256；[官方安装脚本](https://cdn.raft.build/computer/install.sh)和[当前 manifest](https://cdn.raft.build/computer/manifest.json)在研究时给出：

- name：`raft-computer`
- version：`1.0.16`
- target：`linux-x64`
- gzip SHA-256：`d2bf030cfa77d2529e62435155387fdc9613275552056aa3993b5ecf319d7bf8`
- 解压后 SHA-256：`d46c2f1a76bded81faf1b887ed26604ac0094b75eed10c634027e562bbfbd4f7`

研究过程：下载[官方 1.0.16 Linux x64 gzip 制品](https://cdn.raft.build/computer/1.0.16/raft-computer-linux-x64.gz)，先按 manifest 校验 gzip，再校验解压后的二进制；实际运行 `--version`、`doctor --help`，并在隔离的临时 `HOME`/`SLOCK_HOME` 中运行普通 doctor、`--fix` 和 `--migration-details`。SEA 制品保留了编译后的模块标签和 JavaScript，因此还能核验 `packages/computer/src/doctor.ts`、`doctorCli.ts`、`cleanup.ts` 等发行实现。

限制：`botiverse/slock` 源码仓库不公开，官方 CDN 二进制是可审计的第一方发行制品，但不是公开源码；本文不声称看过未发布测试。Raft 的公开文档只承诺用户场景和 `--migration-details`，完整检查项来自 1.0.16 发行制品。[官方 Computers 文档](https://docs.raft.build/features/server/computers.md)、[公开文档源码](https://github.com/botiverse/raft-docs/blob/main/content/features/server/computers/index.md)

## 命令合同

1.0.16 的实际帮助为：

```text
Usage: raft-computer doctor [options] [serverSlug]

Check Raft Computer setup and connection health. Secrets are never printed.

Arguments:
  serverSlug           optional: scope to one attached server (canonical
                       `/myserver`; bare accepted; default: all attached)
                       (scopes recent-crash detail to that server)

Options:
  --fix                after diagnosis, clean up stale local state when it is
                       safe
  --migration-details  show local legacy migration evidence and server-relative
                       exclusion reasons
  -h, --help           display help for command
```

官方文档定义了两个主要场景：Computer 显示在线但 Agent 不推进时先 doctor、再按结果 restart；legacy daemon 自动匹配失败时运行 `doctor --migration-details /<server>`。[官方 Computers 文档](https://docs.raft.build/features/server/computers.md)

普通 doctor 的 selector 在 1.0.16 中会把 attachment 和后续昂贵检查真正限制到指定 Server；不传 selector 时检查全部 attachment。近期 crash 详情也只在指定 Server 时读取。

## 普通 doctor 检查项

发行实现先调用共享 `buildStatusReport`，再生成 `{kind?, name, ok, detail}` checks。Server heading 也是 `kind: section` 的 check，因此 collector 与文本 presenter 没有混在一起。

| 检查 | 通过条件 | 失败后的引导 |
| --- | --- | --- |
| `SLOCK_HOME` | 始终通过，仅报告实际 state root | 无；它是 evidence，不是真正健康检查。|
| `user session` | session 可解析且已登录 | 缺失或无效时运行 `raft-computer login`。|
| `service` | running 和 stopped 都通过 | stopped 被视为允许的静止状态；需要后台运行时执行 `start`。|
| `attachments` | selector 命中一个 attachment，或默认至少有一个 | 执行 `attach /<serverSlug>`。|
| `attach /server` | 本地 attachment 可读取 | 展示 server-issued machine ID 和 server URL。|
| `preflight /server` | 保存的 Computer credential 被服务端识别 | 网络失败检查 URL/网络；鉴权拒绝明确提示 credential/identity 已失效，避免靠复用 display name 创建冲突身份；其他拒绝提示升级 Server 或检查兼容性。|
| `runner /server` | per-Server child 运行且连接 | 区分 unlinked、crash-budget degraded、stopped、process alive but disconnected；每种状态给 `setup`/`start`/`restart` 和具体 runner log 路径。|
| `identity /server` | 没有阻断当前恢复的 legacy identity/migration 矛盾 | unmatched legacy evidence 会要求先看 `--migration-details`；若新 attachment 暂无 Agent、Agent 仍在匹配的 legacy Computer，则提示等待重试且明确“不要仅凭本次诊断删除 Computer”。|

### Runner 与 identity 是 1.0.16 的关键增量

1.0.16 不再只证明“credential 可以 preflight”，还证明真正承载 Agent 的 per-Server runner 是否活着并已连接。若 WebSocket 日志显示 `computer_machine_unlinked`，它把这类鉴权/身份错误与普通网络断开分开，并指向 `setup /server` 恢复或 rebind。

Identity 检查也不是泛化的“有 legacy 文件就失败”：只有当前 runner 不健康、migration 判定为 zero-match，或 server-side Computer/Agent 分布看起来发生错误切换时才失败。对可能仍在收敛的状态，文案要求等待、重跑、不要删除数据。这种 **evidence + conservative remediation** 比自动修复更值得借鉴。

### Crash 取证

指定 Server 时读取 60 秒 crash window，输出时间、exit code、signal，并建议修复根因后运行：

```text
raft-computer restart /<server>
```

Crash 列表本身不直接参与 `allOk`；对应 runner 若已进入 degraded 会单独作为失败 check。

## `--migration-details`

该 flag 不是给普通 report “多打印几行”，而是提前进入独立诊断流：

1. 收集本机 legacy machine traces、owner 文件状态、fingerprint evidence 和 owner server URL。
2. 已登录且能确定 Server 时，读取该 Server 的 legacy roster 与 server-side Computers。
3. 按本地路径解释 `matched`、`not_in_roster`、`no_fingerprint_evidence`、`server_url_mismatch`、owner unreadable/malformed、roster unavailable。
4. 若当前 Server 已 attachment，明确这些 traces 只是历史 setup evidence，不能据此执行 legacy connect、`setup --machine` 或 `--fresh`。
5. 未 attachment 时才引导用户到 Computers 页面辨认 legacy row，再显式 `setup /server --machine <machineId>`。

在空隔离目录中实际运行时，它输出 0 条本地 trace、提示带 Server 重跑，退出 0，且没有创建 state 文件。它会在已登录场景进行只读远端查询，因此“默认无本地 mutation”不等于“纯离线”。

Multica 已经通过 `identity_state` 和 `legacy_identity_candidates` 暴露迁移歧义，而且 adoption/fresh 是显式命令；短期不需要机械复制 `--migration-details`。更合理的是先让现有 doctor 对每个候选解释来源、匹配/排除理由和下一步，只有信息量确实过大时再增加独立 flag。

## `--fix` 的真实边界

普通 checks 先完成并确定 `allOk`，之后才调用 cleanup。1.0.16 的 cleanup 顺序和护栏如下：

| mutation | 护栏 |
| --- | --- |
| 删除 stale service/runner PID files | PID 不存在或不可读不动；PID 仍存活不动。|
| SIGTERM orphan child | 非 Windows；必须存在 supervisor PID；只处理其直接 child；command 必须以 `slock-` 开头；已记录的 managed PID 不动。|
| 隔离 power-loss partial Server state | 目录名必须是合法 Server ID且 attachment 无法读取；rename 到 `.quarantine/<timestamp>-<serverId>`，不直接删除。|
| 清 upgrade temp | 只清超过 24 小时的 upgrade staging 与 snapshot。|
| 释放 stale lock | mtime 超过 60 秒后仍通过 `proper-lockfile` 尝试获取；仍 locked 时不动。|

输出逐项列出 `stalePidfiles`、`orphanProcesses`、`powerLossRecovered`、`tmpFilesCleared`、`staleLocks`；没有动作时显示 `No residue found — clean baseline.`。

值得注意：cleanup 完成后不会重新计算 checks。因此 `--fix` 可能成功采取动作，但本次仍按修复前的 `allOk` 退出；用户或自动化需要重跑 doctor 验证。这是合理的“采证 → mutation → 重新验证”模型。

Multica 采用 Raft 的直接 cleanup 模型，不为 doctor 另加 owner protocol 或锁层。每类 residue 用已有事实和 24 小时阈值独立判断；不满足条件或操作失败就保留原状，只把成功 mutation 写入 `fix_applied`。

### 在 Multica 对齐架构中的可复用性

| Raft cleanup 语义 | Multica 等价机制 | 结论 |
| --- | --- | --- |
| stale service/runner PID | 已有 resident PID；Binding child 当前只有 in-memory PID + generation | 已实现 resident 部分：仅在 resident 确认 stopped 时删除；不扫描或清理 child PID。|
| orphan child termination | Binding child 每秒向 Host control attest；Host 消失后 child 自行退出；per-Binding advisory lease 阻止 successor 双开 | 已由现有运行时语义处理，不加入 `--fix`。|
| power-loss partial Server state quarantine | `binding-children/<environment>/<workspace_id>` 保存 per-Binding outbox/reminder 等 coordinator state；`bindings.json` 是独立的原子 Binding store | 已实现：目录超过 24 小时且没有任何对应 persisted Binding 时原子移动到 `.quarantine`；`bindings.json` 无法读取时跳过全部 quarantine。|
| stale upgrade staging/snapshot | `upgrade-staging/<version>/multica` 明确定义为 ephemeral；Host journal `machine-upgrade-host.json` 已做 successor version attestation 后删除 | 已实现：删除超过 24 小时的 staging version 目录；不处理 Machine Upgrade journal。|
| stale mutation lock release | Multica 使用 OS advisory `flock`；进程退出自动释放，遗留 lock file 不阻塞新 owner | **无需 cleanup，且不能 unlink。** Raft 的 lock-directory stale TTL 是其实现细节；在 Multica 删除仍被锁住的 pathname 可能让两个 inode 各自持锁，反而破坏 singleton。|

这里复用的是 Raft 的 **recovery semantics**，不是它的文件名和 `ps` heuristic：只处理可由本地事实识别的旧 residue、优先 quarantine、逐项报告成功动作。Multica 现有 child self-termination 和 advisory lock 不再叠加第二套 cleanup。

## 输出、脱敏和退出码

在空隔离 state root 中实际运行普通 doctor，stdout 为：

```text
Using state at /tmp/.../slock
✓ SLOCK_HOME                                       /tmp/.../slock
✗ user session                                     not logged in — run `raft-computer login`
✓ service                                          stopped (run `raft-computer start` when you want background)
✗ attachments                                      no attachments — run `raft-computer attach /<serverSlug>` (e.g. `/myserver`)

Some checks failed — see the actionable hints above.
```

实际退出 1，stderr 为空，且没有创建 state 文件。普通 doctor 的退出规则是：

- `allOk = checks.every(check => check.ok)`；全通过为 0，任一失败为 1。
- `section` checks 自身是通过项，不影响结果。
- crash detail 和 cleanup action 不改变已计算的 `allOk`。
- `--migration-details` 走另一条 presenter 流，不使用普通 doctor 的 check 聚合；执行错误仍由统一 CLI error wrapper 处理。

文本 presenter 会对 `sk_*`、JWT-like 和 40 位以上 hex 串做兜底 redaction。更重要的第一道防线仍是 collector 只产出 allowlisted evidence。Raft 1.0.16 没有 doctor JSON flag；Multica 已有 `--output json`，应保留并把它升级为稳定机器合同，而不是退回纯文本。

## 实现结构

```text
Commander: doctor [serverSlug] [--fix] [--migration-details]
├── migration-details → runDoctorMigrationDetails
└── normal → resolveTargetServerId → runDoctor presenter
    └── createComputerApi(slockHome).doctor(...)
        ├── runDoctorChecks
        │   ├── buildStatusReport
        │   ├── scoped attachments
        │   ├── remote preflight
        │   ├── per-Server runner detail + log evidence
        │   └── migration/identity adjudication
        ├── runFullCleanup (only with --fix)
        └── readCrashHistory (only with selected Server)
```

这个设计的核心不是命令框架，而是：root 显式绑定、collector/presenter 分离、统一 check 结构、逐协作边界检查、修复后置。

## 与 Multica 当前实现的差距

| 能力 | Multica 当前状态 | 判断 |
| --- | --- | --- |
| 命令入口 | 已有 `multica computer doctor [/<workspace>]` | **无需新增命令。** |
| 默认只读 / 显式 `--fix` | 已有；fix 清 stale resident PID、超过 24 小时的 abandoned Binding state 和 upgrade staging | 已采用 Raft 的直接 cleanup 语义。|
| JSON | 已有 `--output json` | 优于 Raft，但 schema 还是字段快照。|
| machine identity | 有 `identity_state`、Computer ID、legacy candidates | 基础正确，缺逐项判定和 remediation。|
| resident / server connection | 有 running/starting/stopped 与全局 connected | 有 evidence，但只用“disconnected 且非 starting”决定失败。|
| 配置漂移 | environment/service origin/package source 对比 | 是 Multica 特有价值，但只输出 `config drift: true/false`。|
| Workspace selector | 能解析 Binding，并把 ID/slug/active 写进 JSON | 当前没有真正 scope per-Binding child/preflight 检查，文本甚至不展示 selected Binding。|
| per-Binding runner | doctor 未逐 child 判断 ready/degraded/disconnected | **最大功能缺口。** |
| actionable hints | 大多是字段值；断连只返回通用 error | 明显弱于 Raft 1.0.16。|
| crash evidence | Host 已有 diagnostic aggregation，doctor 未投影 crash budget/log hint | 有可复用数据源，尚未接入。|

对应代码：`server/internal/computer/diagnose.go`、`server/cmd/multica/cmd_computer.go`、`server/internal/computer/diagnose_test.go`、`server/cmd/multica/cmd_computer_test.go`。

## 推荐方案

### P0：不增加新命令，深化现有 seam

把 `Diagnosis` 从一组平铺字段提升为：

```text
Diagnosis
├── Checks[]
│   ├── code          稳定机器标识
│   ├── scope         machine | workspace
│   ├── status        pass | warn | fail
│   ├── detail        安全的人类 evidence
│   └── remediation   可执行下一步
├── Evidence          当前 typed machine/config/identity 字段
└── AllRequiredOK     唯一退出判定
```

Cobra 层只负责 text/JSON presenter 和把 `AllRequiredOK=false` 映射到既有 CLI error/exit 机制。不要让命令层重新解释每个字段。

### P1：补齐最有价值的 checks

按以下顺序实现：

1. machine identity 是否 usable/ambiguous；
2. resident 状态与 machine server connection；
3. environment/service/package drift，分别给 remediation；
4. selector 指定时只检查该 Workspace Binding；否则检查全部 active Bindings；
5. 每个 Binding 的 child running/ready/connected/degraded、generation/PID fence 与日志位置；
6. bounded、只读的服务端 credential/capability preflight；每个 Workspace 独立超时并保留部分结果；
7. 最近 crash evidence 和明确的 restart/log action。

停止状态是否失败应按产品语义定义，而不是照抄 Raft：Raft 的 machine service stopped 是 pass，但有 attachment 时 runner stopped 又会 fail。Multica 应直接把“允许静止”和“期望在线”建模成不同 status/code，避免靠文案暗示。

### P2：已实现的 Raft recovery semantics

1. stale resident PID：resident stopped 且复查不存活后删除。
2. upgrade staging：version 目录超过 24 小时后删除。
3. abandoned per-Binding coordinator state：没有对应 persisted Binding 且目录超过 24 小时后 quarantine。
4. orphan child 继续使用现有 Host-attest 自退出；不增加进程扫描。
5. advisory lock 文件不清理；文件残留不是 lock 残留。

每个成功 mutation 都进入 `fix_applied`。Agent Workspace、Binding、Binding credential 和 Machine Upgrade journal 不进入 age-only cleanup。

### 不建议做

- 不新增平行的 `multica doctor` 或第二套 daemon doctor。
- 不把 root path 这种定位信息算作 required pass。
- 不复制 Raft 只有自由文本 `detail` 的机器合同；Multica 已有 JSON，应提供稳定 code/status。
- 不默认上传日志或诊断；若未来需要支持工单，单独设计显式 `diagnostics export/push` 和脱敏合同。
- 不让 Workspace selector 只改展示；它必须真正 scope Workspace 级远端/child 检查，同时保留 machine-global checks。

## 最终判断

**建议做“Raft 1.0.16 风格的 doctor”，但形式是增强已有 `multica computer doctor`，不是再加命令。** 最小高价值版本只需三件事：

1. `Diagnosis.Checks + AllRequiredOK`；
2. per-Workspace Binding child/connection 检查；
3. 每个失败项附稳定 code、日志位置和下一条安全命令。

这会让现有 doctor 从“dump 当前状态”变成真正的“一条命令定位 Computer 为什么不工作”，同时不扩大 mutation 风险。

## 第一方来源

- [Raft Computer 官方 latest manifest（研究时为 1.0.16）](https://cdn.raft.build/computer/manifest.json)
- [Raft Computer 1.0.16 固定版本 manifest](https://cdn.raft.build/computer/1.0.16/manifest.json)
- [Raft Computer 官方安装器](https://cdn.raft.build/computer/install.sh)
- [Raft Computer 1.0.16 Linux x64 制品](https://cdn.raft.build/computer/1.0.16/raft-computer-linux-x64.gz)
- [Raft Computers 官方文档](https://docs.raft.build/features/server/computers.md)
- [Raft Computers 公开文档源码](https://github.com/botiverse/raft-docs/blob/main/content/features/server/computers/index.md)

本仓库相关入口：

- `server/internal/computer/diagnose.go`
- `server/cmd/multica/cmd_computer.go`
- `server/internal/computer/diagnose_test.go`
- `server/cmd/multica/cmd_computer_test.go`
