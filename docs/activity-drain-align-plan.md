# Activity drain alignment — implementation plan (Step 2, full)

## Goal
Align multica Agent Activity to raft 1.0.15 real semantics, no new semantics:
- **B chain (state)**: snapshot `activity_kind ∈ {online,thinking,working,error,offline}`, daemon
  push + 60s heartbeat. Thinking is a state, NOT a timeline event.
- **A chain (events)**: timeline entries = hookEventName-driven tool/lifecycle events only.
- **Transport**: daemon buffers A-chain entries; server pulls them via WS reverse
  request (reuse the existing NotifyWorkspaceRunner request/response channel), not
  daemon real-time pushing each entry.

## Critical clarification: no "bridge↔plugin local endpoint" layer in multica
- raft: computer/bridge pulls an **external agent's channel plugin** via local HTTP
  `GET /activity/drain?max=N`, then POSTs the drained events to its server.
- multica: there is NO such layer. Agent messages flow into the daemon over a Go
  channel (`messages <-chan agent.Message`, see `agent_runtime_pool.go:drainResidentActivity`).
  Agents expose no local HTTP drain endpoint, and `agentBridge` is voice-call-specific only.
- Therefore multica's only natural drain boundary is **server ↔ daemon over the WS**:
  daemon buffers A-chain entries; server issues a reverse request (same framing as the
  existing probe request) and daemon replies with buffered entries. This is the correct
  alignment location, not a fabricated semantic.

## Changes

### 1. Daemon: thinking is B-chain-only (DONE)
- `daemon.go:4528`: thinking published with empty narrative → snapshot becomes
  `ActivityKindThinking`, no entry. ✅
- `message_runtime.go` `emitResidentMessageRuntimeActivity`: thinking keeps empty narrative;
  entry construction conditional on non-empty narrative. ✅

### 2. Daemon: buffer A-chain entries for drain
- Producer keeps bounded in-memory entry buffer per managed launch (cap ~50, raft
  `activityDrainLimit`). State snapshot (B chain) is still pushed real-time; only A-chain
  entries are buffered instead of immediately written as `EventAgentActivity` frames.

### 3. Protocol: drain request/response (server→daemon)
- `EventRunnerActivityDrain` request `{ max int, drain_id string }`.
- daemon replies with `{ drain_id, entries[], has_more bool, seq int64 }` using the same
  request/response framing `NotifyWorkspaceRunner` already provides (probe uses this).

### 4. Server: drain scheduler + ingest
- Reuse/extend `ReapStaleRunnerActivity` to send `EventRunnerActivityDrain` for active
  working/thinking launches via `NotifyWorkspaceRunner`.
- Response enters `recordRunnerActivity` (existing fence + UPSERT + UNIQUE).
- One-time historical cleanup: delete the 1704 stale thinking entries for a-tai.

### 5. Tests
- Producer: thinking→no entry; buffer cap; drain returns ≤max + has_more.
- message_runtime: thinking→no entry; tool/compaction→entry.
- Server: drain scheduler requests, ingests response, fence monotonic.
- Existing `runner_activity_*` tests stay green.

## Not changing (already raft-aligned)
- `agent_activity_snapshot`/`entry`/`launch`/`probe` tables (migrations 299/300/301).
- `recordRunnerActivity` fence/UPSERT/UNIQUE.
- `Tick()` 60s heartbeat, `Probe()` reverse request.

## Status & 待办演进方向

**已落地（本分支，thinking 止血，v0.4.21 已在本机激活验证通过）**
- thinking 不再写 A 链 timeline entry，只作 B 链 snapshot 状态（raft 对齐）。
- 文件：`server/internal/daemon/daemon.go`（任务循环）、`message_runtime.go`（resident runtime）。
- 已验证：编译通过、13 个 activity/thinking 测试全绿、本机 daemon 切 v0.4.21 后不再产生 thinking 风暴。

**待办 / 后续演进（不在此分支实现，单独规划）**
1. A 链 drain 全量对齐：daemon 缓冲 entry → server 经 WS 反向 request drain（复用 NotifyWorkspaceRunner
   request/response 通道），把「实时逐条 push」改为「批量拉取」。
2. Agent 黑盒化（B 协议 + 优先 Pi，再 cursor/claude/codex）：把 pi/cursor 等从「daemon spawn 子进程 + 管道」
   演进为「本地服务 + 标准 activity/wake 端点」，daemon 作为 bridge 拉取（对等 raft computer↔plugin）。
3. 服务端 memory-center hydrate/sync 401 "daemon workspace required"：本机 daemon 请求缺 workspace 标识，需排查。
4. 历史存量 thinking entry 清理（a-tai 等 1704 条）——一次性 DELETE。
