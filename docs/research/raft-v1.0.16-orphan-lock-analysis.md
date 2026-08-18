# Raft daemon v1.0.16：orphan child 与 stale lock 源码核验

> 研究日期：2026-08-17  
> 外部对象严格锁定：`@botiverse/raft-daemon@1.0.16`  
> 范围：只核验 npm 发布物，并与当前 Multica 实现对照；不以 Multica 注释或旧分析倒推 Raft 行为。
>
> 后续决定（2026-08-17）：Multica 已删除 per-Binding OS lease、启动 registration 与周期性 Host attest，采用同进程 Binding reconcile。本文的 Raft 源码核验不变；Multica 对照与建议已按该决定更新。

## 结论摘要

以下标签贯穿全文：**Observed fact（观察事实）**、**Inference（推断）**、**Recommendation（建议）**。

1. **“Raft v1.0.16 会在启动时通过 `ps` 扫描、PID/命令匹配并向 orphan runner 发 `SIGTERM`”不准确。** 在锁定的 npm tarball 全部 11 个文件中，没有这条机制。发布物中仅有两处 `ps` 调用，作用是沿当前 daemon 的父进程链识别 PM2 等 legacy supervisor，不会枚举 sibling/child，不会按 runner 命令清理，也不发送 `SIGTERM`。发布物确有名为 `reapOrphanProcesses` 的函数，但它只处理当前 `AgentProcessManager.stopAll` 预先收集的、内存中已知 runtime PID，先 signal-0 探活，随后直接 `SIGKILL`；这不是 Host 崩溃后的 orphan child discovery/recovery。
2. **“Raft v1.0.16 使用带 TTL 的 lock directory 清 stale lock”方向正确，但表述需要收窄。** `daemon.lock/` 由原子 `mkdir` 竞争创建，包含 `owner.json`。完整 owner 的 stale 判据是 owner PID 不存活；只有 owner 缺失/不可解析的“不完整锁”才使用目录 `mtime` 的 30 秒 TTL。确认 stale 后递归强制删除目录并重试一次。它不是续租型 TTL、没有 heartbeat，也不是所有锁统一按 TTL 过期。
3. Multica 生产 Binding 与 Host 在同一 OS 进程中运行。它保留 machine-wide OS advisory lock（Unix `flock`、Windows `LockFileEx`），但不再为每个 Binding 加锁，也不再周期性 attest Host。Computer 进程消失时 Binding goroutine 一起消失，因此 production 不存在本文最初假设的 orphan OS child；`computer __runner` 只是 executable fallback。

## 1. 外部证据的版本与完整性

### Observed fact

- npm metadata 的精确版本 `_id` 是 `@botiverse/raft-daemon@1.0.16`，`gitHead` 为 `f5cf04705910f725a7fe6a114cee8851e9360623`，发布物 `shasum` 为 `288d5e4eead7aacc7c83ccccb3ce9b2d8630c741`，integrity 为 `sha512-Nd+N2ztwg1azqGL9+c/3TxITGUvTW7Yn4wHJhnjeMnbZZfDzkXWEo9Z2JP807IoaLiVZsorpdknWY4NFLYVeNg==`。
- 官方 tarball：<https://registry.npmjs.org/@botiverse/raft-daemon/-/raft-daemon-1.0.16.tgz>。metadata 记录 `fileCount: 11`；解包后也是 11 个文件。与本题有关的实现均在 `package/dist/chunk-J5Y72PN7.js`。包没有发布 `src/*.ts` 或 source map，因此下文给出 bundle 行范围、bundle 内保留的原始源码路径注释、函数/常量和摘录。
- metadata 来源：<https://registry.npmjs.org/@botiverse%2fraft-daemon/1.0.16>。

### 证据限制

**Observed fact：** npm 包是编译 bundle，行号可稳定对应这个 tarball，但不是上游 TypeScript 仓库行号。bundle 保留了 `// src/machineLock.ts`、`// src/daemonOrphanReaper.ts` 等模块边界，足以定位函数与行为。本文不使用 GitHub 当前分支补齐未随 1.0.16 发布的源码，也不使用 `docs/raft-v1.0.16-multica-architecture-comparison.md` 的 `strings.txt` 作为外部事实来源。

## 2. orphan child recovery 核验

### 2.1 发布物中的 `ps` 到底做什么

**Observed fact：** 对 tarball 中所有 `.js` 搜索 `ps` 的执行点，只有：

- `package/dist/chunk-J5Y72PN7.js:33324-33462`（bundle 模块为 legacy daemon supervisor detection）
  - `parentPid(probe, pid)` 执行 `ps -o ppid= -p <pid>`；
  - `processCommand(probe, pid)` 执行 `ps -o command= -p <pid>` 并转小写；
  - `relatedProcessIds` 从 `probe.pid`（当前进程）开始最多沿父 PID 链向上；
  - `detectPm2` 检查祖先 command 是否包含 `pm2`，再结合 `pm2 jlist`；
  - `detectLegacyDaemonSupervisorWithProbe` 的结果来自 `detectPm2 ?? detectSystemd ?? detectLaunchd`。

摘录（锁定 tarball、上述行范围）：

```js
function parentPid(probe, pid) {
  const stdout = probe.execFile("ps", ["-o", "ppid=", "-p", String(pid)]);
  ...
}
function processCommand(probe, pid) {
  return probe.execFile("ps", ["-o", "command=", "-p", String(pid)])?.trim().toLowerCase() ?? "";
}
```

**Observed fact：** 这里没有进程表枚举（如 `ps -A`/`-ax`）、没有 runner argv pattern、没有对匹配 PID 调用 `process.kill`。tarball 全量搜索也没有其他 `ps` 执行点。

**Inference：** 因此不能把这段 supervisor detection 描述成 orphan child recovery。它只识别“谁在监管当前 daemon”，不是“当前 daemon 应监管哪些遗留 child”。

### 2.2 名为 orphan reaper 的真实机制

**Observed fact：** `package/dist/chunk-J5Y72PN7.js:20061-20113` 保留模块注释 `// src/daemonOrphanReaper.ts`，函数 `reapOrphanProcesses(pids, logger, recordTrace)`：

1. 对调用方传入的 PID 数组逐个 `process.kill(pid, 0)` 探活；`ESRCH` 判死，其余异常保守视作存活；
2. 对 survivors 逐个 `process.kill(pid, "SIGKILL")`；
3. 以 50ms 间隔继续 signal-0 探测，最多等待 2 秒；
4. 记录 `daemon.agent.stop_all.survivor_reaped` trace 及仍存活 PID。

调用点 `package/dist/chunk-J5Y72PN7.js:27674-27696` 位于 `AgentProcessManager.stopAll`：它先遍历当前内存中的 agent processes，把 `ap.runtime.pid` 放进 `pids`，再执行正常 stop，最后将这些 **预先已知 PID** 交给 `reapOrphanProcesses`。

**Inference：** 这是“本进程 shutdown 时清理 stopAll survivor”，不是 daemon/Host crash 后由 successor 扫描 OS 恢复 orphan。它没有命令匹配，也没有 `SIGTERM` 阶段（reaper 自身直接 `SIGKILL`）。

### 2.3 其他 SIGTERM 不构成所声称机制

**Observed fact：** bundle 确有多处 runtime `stop({signal: "SIGTERM"})`，例如显式 stop（`26783`）和 stalled recovery（`29432-29450`）；stalled recovery 超时常量是 10 秒（`23652-23655`, `23715`），随后 `SIGKILL`（`28604-28669`）。这些操作针对内存中已有 runtime object，不含 `ps` discovery 或命令匹配。

### 判定

**Observed fact + Inference：** 若用户结论是“v1.0.16 的 orphan child recovery 由 ps 扫描 → PID/命令匹配 → SIGTERM 实现”，判定为 **不准确**。这实际上混合了三段互不相同的代码：父链 `ps` supervisor detection、已知 PID 的 stopAll survivor reaper、runtime 的 SIGTERM/SIGKILL stop。

## 3. stale lock cleanup 核验

### 3.1 创建、owner 与 release

**Observed fact：** `package/dist/chunk-J5Y72PN7.js:32720-32842` 保留 `// src/machineLock.ts`：

- `INCOMPLETE_LOCK_STALE_MS = 3e4`（30 秒）；
- lock ID 是 API key SHA-256 fingerprint 前 16 hex，路径为 `<root>/machine-<fingerprint>/daemon.lock/`；
- `acquireDaemonMachineLock` 以不带 `recursive` 的 `mkdirSync(lockDir)` 作为互斥原语；成功后写 `owner.json`（mode `0600`），字段含 `pid`、随机 `token`、`hostname`、`startedAt`、`serverUrl`、fingerprint；owner 写失败会 `rmSync(lockDir, {recursive:true, force:true})`；
- `release` 先确认当前 owner 的 PID 与 token 都匹配，随后把 `pid` 写为 0；仅当该写入失败时才删除 lock directory。也就是说正常 release 会保留一个明确可回收的目录。

### 3.2 stale 判定与删除

**Observed fact：** 首次 `mkdir` 遇到 `EEXIST` 后：

1. `readOwner` 读取/解析 `owner.json`；任何读取或 JSON 错误都返回 `null`；
2. 若 `owner.pid` 非零且 `isProcessAlive(owner.pid)` 为真，抛 `DaemonMachineLockConflictError`；探活使用 signal 0，仅 `ESRCH` 判死，`EPERM` 等保守判活；
3. 若 owner 存在但 PID 为 0 或 signal-0 得到 `ESRCH`，立即视作 stale，不检查 age；
4. 若 owner 不存在/不可解析，则用 `Date.now() - stat(lockDir).mtimeMs`；age 取不到或 `< 30s` 时按 conflict 处理，只有 `>= 30s` 才视为 stale；
5. stale 后 `rmSync(lockDir, {recursive:true, force:true})`，for-loop 最多再尝试创建一次；两次后仍失败则 conflict。

关键摘录（同一 tarball、`package/dist/chunk-J5Y72PN7.js:32828-32841`）：

```js
const owner = readOwner(lockDir);
if (owner?.pid && isProcessAlive(owner.pid)) throw ...;
if (!owner) {
  const ageMs = lockAgeMs(lockDir);
  if (ageMs === null || ageMs < INCOMPLETE_LOCK_STALE_MS) throw ...;
}
rmSync(lockDir, { recursive: true, force: true });
```

### 判定

**Observed fact + Inference：** “使用 TTL lock directory、判 stale 后删除”是 **部分准确**：directory 与删除属实；但 TTL 只保护 owner 尚未完成写入/已损坏的窗口。完整 owner 的主判据是 PID liveness，目录不会按时间自动过期，也没有 TTL refresh/heartbeat。更准确的表述是“`mkdir` directory lock + PID liveness；30 秒 incomplete-lock grace period；stale recursive cleanup”。

### 已知边界

**Inference：** 仅以 PID 探活无法抵御 PID reuse，也未校验 hostname 是否为本机或进程启动时间/命令是否仍匹配 owner。删除与重建之间也没有 compare-and-delete token；当前代码靠两次有限重试降低而非消除竞争。以上是源码可见的设计边界，不代表已观察到生产故障。

## 4. 与当前 Multica 对照

### 4.1 锁模型不同：无需移植 TTL cleanup

**Observed fact：**

- `server/internal/computer/lease.go` 定义 machine-wide `resident.lock` 与 startup `start.lock`；创建普通文件并在打开的 fd 上获取 OS advisory lock。
- Unix `server/internal/computer/file_lock_unix.go:13-31` 使用 nonblocking exclusive `flock`，每 10ms 重试直到 context 结束；Windows `server/internal/computer/file_lock_windows.go:14-41` 使用 `LockFileEx`/`UnlockFileEx`。
- `FileLease.Close` 解锁并关闭 fd，但不删文件（`lease.go:56-65`）。测试明确先放置 stale 普通文件并成功获取锁，同时验证同一 live lock 排他（`server/internal/computer/lease_test.go:11-33`）。
- resident 还受 loopback listener 二次约束（`server/internal/computer/host_process.go:82-100`）。

**Inference：** Multica 的 stale path 是“文件残留但内核锁随进程/fd 释放”，不是 Raft 的“目录存在即锁定”。因此不需要 owner JSON、mtime TTL 或递归删除来恢复 correctness。单纯删除一个已经确认无锁的残留文件未必立即出错；危险在于 cleanup 的检查与删除会和 live acquire 竞争：若删除仍被某进程打开并持锁的路径，旧 owner 继续锁住旧 inode，后续 opener 却可创建并锁住新 inode，singleton 就被拆成两套。移植 directory cleanup 会增加第二种锁语义及这类竞争，收益为零。

### 4.2 Multica 简化后的 in-process supervision

**Observed fact：**

- production resident 注入 `BindingRunnerLauncher.Run`，`Spawn` 因而调用 `StartInProcessBinding`；每个 Binding 的 PID 都是 Computer 自身 PID。
- Host 先登记 generation-fenced execution identity，再 activate 同进程 Binding goroutine；不需要 Binding 反向 registration 或重试。
- Ready 之后不再周期性 attest Host，也不获取 per-Workspace `runner.lock`。正常 reconcile 通过 context cancel 停止 in-process Binding。
- `computer __runner` OS process 仍作为 executable fallback 存在，但不是 production resident 的正常 supervision path。

**Inference：** production Computer 退出时所有 Binding goroutine 随进程一起结束，不需要额外 orphan containment。per-Binding lease 与周期 attest 是从旧 OS-child 模型遗留到同进程模型的冗余机制。

### 4.3 文档准确性

**Observed fact：** `docs/raft-v1.0.16-multica-architecture-comparison.md` 已修正为 production Binding 与 Host 同进程；其 Raft evidence index 没有 machine lock 或 orphan startup scan 的源码证据。

**Inference：** 不能从 Multica 的 fallback executable path 或高层 supervision 描述推出 Raft v1.0.16 存在 `ps` orphan recovery。

## 5. 建议

### 最小方案（Recommendation）

**保留 machine-wide resident advisory lock 与 in-process Binding reconcile；Host 先登记 identity 再 activate goroutine；删除 per-Binding lease、启动 registration 和周期性 Host attest。** 不增加 startup `ps` reaper、TTL directory lock、PID sidecar 或 orphan recovery state machine。

### 触发升级复杂方案的条件（Recommendation）

只有 executable fallback 再次成为受支持的生产 topology，且出现以下任一可测事实，才考虑主动 orphan discovery/termination：

- 已观测到 Host 异常退出后 orphan child 与 successor 重叠执行；
- 重叠执行造成重复 provider、重复消息、重复任务或资源泄漏；
- 产品明确要求 Host `kill -9` 后仍提供 cross-Host at-most-one execution owner。

届时复杂方案不能只按 PID 或模糊命令 substring kill。至少需要持久化且不可伪造/误配的 identity（workspace、computer/runner generation、PID、process start time 或 OS-native process handle），先验证当前 Host generation 与 process identity，再 TERM、bounded wait、最后 KILL；Windows 需独立设计，不能假设 `ps`/POSIX signals。

### 明确不做（Recommendation）

- 不把 Raft 的 `daemon.lock/owner.json` 复制到 Multica；两者锁原语不同。
- 不为 Binding 新增或清理 `runner.lock`。
- 不以 `ps | grep __runner`、可复用 PID 或命令 substring 作为 kill authority。
- 不把 API `RecoverOrphans`（`binding_child_runtime.go`，是 server-side task/runtime recovery）混同为 OS orphan child recovery。
- 不根据现有 Multica 注释或旧 `strings.txt` 反推 npm 发布物行为。

## 6. 最终判断表

| 命题 | 判断 | 依据 |
| --- | --- | --- |
| Raft 1.0.16 用 `ps` 扫描 orphan child | 否 | tarball 唯一 `ps` 用途是当前进程父链 supervisor detection |
| 用 PID + command 匹配 orphan runner | 否 | 没有 runner process-table enumeration/matcher |
| orphan recovery 发 SIGTERM | 否（对该命题） | stopAll reaper 对已知 PID 直接 SIGKILL；其他 runtime stop 的 SIGTERM 不含 discovery |
| 有 stale lock directory cleanup | 是 | `daemon.lock`, `rmSync(...recursive, force)` |
| 所有 stale lock 都由 TTL 判定 | 否 | 完整 owner 用 PID liveness；仅 incomplete owner 使用 30s mtime grace |
| Multica 缺少同等 stale cleanup | 不构成缺口 | advisory lock 随 fd/process 释放；stale 文件不持锁 |
| Multica production 会遗留 orphan Binding OS child | 否 | Binding execution 与 Computer Host 同进程 |

## 来源索引

### 外部主来源

- npm metadata（精确版本）：<https://registry.npmjs.org/@botiverse%2fraft-daemon/1.0.16>
- npm 官方 tarball：<https://registry.npmjs.org/@botiverse/raft-daemon/-/raft-daemon-1.0.16.tgz>
- tarball 内 `package/dist/chunk-J5Y72PN7.js`：
  - `20061-20113`，`src/daemonOrphanReaper.ts` / `reapOrphanProcesses`
  - `27674-27696`，`AgentProcessManager.stopAll` 的 PID 收集与调用
  - `32720-32842`，`src/machineLock.ts` / `INCOMPLETE_LOCK_STALE_MS` / `acquireDaemonMachineLock`
  - `33324-33462`，legacy supervisor detection / `parentPid` / `processCommand` / `detectPm2`
  - `28604-28669`, `29386-29450`，stalled runtime recovery（用于排除混淆）

### 本地主来源

- `server/internal/computer/lease.go`
- `server/internal/computer/file_lock_unix.go:13-31`
- `server/internal/computer/file_lock_windows.go:14-41`
- `server/internal/computer/lease_test.go`
- `server/internal/computer/binding_supervisor.go:124-250,297-306`
- `server/internal/computer/binding_child.go:50-100,118-185`
- `server/internal/computer/binding_child_unix.go:10-14`
- `server/internal/computer/host_process.go:69-100`
- `server/internal/computer/host_control.go:30-42,104-143`
- `server/internal/daemon/binding_child_runtime.go`
- `server/internal/daemon/binding_child_runtime_test.go`
- `docs/raft-v1.0.16-multica-architecture-comparison.md:208-210,238-244`
