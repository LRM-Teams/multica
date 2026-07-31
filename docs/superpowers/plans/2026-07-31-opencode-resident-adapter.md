# OpenCode resident runtime adapter

Task #33 (Frank, 2026-07-31, high priority, full/complete adapter — not
MVP). Design doc:
`docs/superpowers/specs/2026-07-31-opencode-resident-adapter-design.md`.

## Decisions carried in from spec review (Parker, 2026-07-31)

- **Coverage parity**: match pi/grok/cursor's *current* actual coverage,
  not an idealized "cursor is full-profile" reading. Re-checked the code:
  cursor's gate (`cursor_canonical.go`) requires **both**
  `executionProfileFull` **and** `task.ChatSessionID != ""` — so all
  three residents (pi/grok/cursor) are chat-only today; none extend to
  issue tasks. OpenCode resident should be gated the same way: chat-only,
  via `task.ChatSessionID != ""` (plus whatever profile check applies).
- **Idle-TTL**: no new dedicated env var. Cursor's resident backend relies
  solely on the canonical pool's generic `evictIdle` sweep, not a
  provider-specific TTL config (unlike the legacy grok/pi persistent
  pools, which predate the canonical pool and carry their own TTL
  knobs). Follow cursor's precedent — no
  `MULTICA_OPENCODE_PERSISTENT_IDLE_TTL`.
- **Thinking/variant field name + per-session vs per-process MCP**: not
  resolvable from docs alone — confirm against the live OpenAPI spec
  (`GET /doc` on a running `opencode serve` instance) during Step 2
  below, before writing the request-body construction code.

## Implementation steps

### Step 1 — OpenCodeServeBackend skeleton + process lifecycle (no HTTP calls yet)

- [ ] New file `server/pkg/agent/opencode_serve.go`.
- [ ] Define `OpenCodeServeBackend` interface (`Backend` + `Close()`),
  mirroring `CursorACPBackend`.
- [ ] `newOpenCodeServeBackend(cfg Config) *opencodeServeBackend` +
  exported `NewOpenCodeServeBackend(cfg Config) OpenCodeServeBackend`.
- [ ] `ensureServer(ctx, opts)` (mutex-guarded, mirrors
  `cursorACPBackend.ensureProcess`):
  - Resolve `opencode` executable path (reuse `resolveOpenCodeNativeFromShim`
    for Windows).
  - Pick a free localhost port (`net.Listen("tcp", "127.0.0.1:0")`, then
    close the listener and hand the port to the child — standard
    "reserve then release" pattern to avoid a race against another
    process grabbing it, acceptable here since the window is a single
    scheduler tick and this process own the machine).
  - Generate a random `OPENCODE_SERVER_PASSWORD` (crypto/rand, never
    logged).
  - Spawn `opencode serve --port <p> --hostname 127.0.0.1` with
    `cmd.Dir`, `PWD=`, `OPENCODE_CONFIG_CONTENT` env — same env-building
    path as `opencode.go`'s `Execute` (extract the shared pieces into a
    small helper both backends call, rather than duplicating the MCP/PWD
    logic — see Step 5).
  - Bounded retry/backoff polling a lightweight endpoint (`GET /doc` or
    a `HEAD /session`) until the server accepts connections, or fail with
    a clear "opencode serve did not become ready within Ns" error.
- [ ] `Close()`: terminate the child process, same
  kill+Wait pattern as `cursorACPBackend.disposeProcessLocked`.
- [ ] Unit test: `ensureServer` starts a process, a second call reuses
  it (no second spawn) — fake `opencode` binary via a test helper script,
  matching the style of `process_liveness_test.go`'s injected helper
  process.

### Step 2 — SSE client + event model, confirmed against live OpenAPI

- [ ] Manually run `opencode serve` locally (dev-only sanity check, not
  part of the committed test suite) and fetch `/doc` to confirm exact
  field names for: the message-send request body's model/thinking
  fields, and whether MCP config can be set per-session vs. only
  per-process. Record findings as a code comment at the top of
  `opencode_serve.go` (so the next person doesn't have to re-derive it).
- [ ] `GET /event` SSE reader: parse `server.connected` (discard),
  then per-event-type dispatch. Only need to handle, per the design
  spec: `session.idle`, `session.error`, `message.part.delta` (for
  incremental text). Everything else (`message.updated`,
  `message.part.updated`, `session.diff`, `session.status`, etc.) is
  either unreliable (per the known upstream bug) or not needed for the
  `Message`/`Result` mapping.
- [ ] Demux by `sessionID` embedded in each event so a single SSE
  connection can (in principle) serve multiple sessions on this backend
  instance — even though in practice the pool serializes to one active
  turn at a time, don't hard-code "there is only ever one session,"
  since `Close`/eviction plus a fresh `POST /session` on config drift
  could create a second session on the same still-running server before
  the old one is GC'd.

### Step 3 — Turn execution (`Execute`)

- [ ] `POST /session` (with `parentID` when `opts.ResumeSessionID` is
  set) if no session exists yet for this backend instance; store the
  returned session ID as the resume handle (parity with
  `p.sessionID` in `cursorACPBackend`).
- [ ] `POST /session/:id/message` with the prompt, model, and
  thinking/variant fields (names confirmed in Step 2).
- [ ] Stream `MessageText` to the caller as `message.part.delta` events
  arrive for this session.
- [ ] On `session.idle` for this session: `GET
  /session/:id/message` (latest), reconcile the final message into
  `MessageToolUse`/`MessageToolResult` pairs (same split logic as
  `handleToolUseEvent`, sourced from the polled response) and build the
  final `Output`/`Usage`, then emit `Result{Status: "completed", ...}`.
- [ ] On `session.error` for this session: emit `MessageError` +
  `Result{Status: "failed", Error: ...}`.
- [ ] Timeout watchdog: if neither `session.idle` nor `session.error`
  fires for this session within `opts.Timeout`, cancel and emit
  `Result{Status: "timeout"}` — same contract as every other backend's
  `runCtx` deadline handling.
- [ ] Concurrency guard: reject overlapping `Execute` calls on the same
  backend instance with a busy error (parity with
  `ErrCursorACPTurnBusy`/`cursorACPBackend.running`), since the resident
  pool already serializes per slot and a second concurrent call would
  indicate a caller bug, not a legitimate use case.

### Step 4 — Pool wiring

- [ ] `server/internal/daemon/opencode_canonical.go` (new, mirrors
  `cursor_canonical.go` exactly):
  ```go
  func newCanonicalOpenCodeResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
      backend := agent.NewOpenCodeServeBackend(cfg)
      return backend, backend.Close, nil
  }
  ```
- [ ] `agent_runtime_pool.go`: add `"opencode"` to the resident switch
  in `canonicalRuntimeModeFor` and the `case` in
  `defaultCanonicalRuntimeFactory`.
- [ ] `usesCanonicalResidentChatRuntime` (or wherever the daemon decides
  whether a given task should even attempt to acquire the canonical
  pool for this provider): add an `"opencode"` case requiring
  `task.ChatSessionID != ""`, matching the confirmed chat-only parity
  decision above. Confirm the exact gating function name/location during
  implementation (verify whether `executionProfileFull` is required here
  too, matching cursor, or just `ChatSessionID`, matching grok/pi — go
  with requiring both, the stricter of the two, unless something in the
  surrounding code indicates otherwise).

### Step 5 — Shared config-translation extraction (avoid duplicating opencode.go logic)

- [ ] Extract `buildOpenCodeMCPConfigContent`, the `PWD=`/`--dir`
  project-discovery env override, and the Windows shim resolution into
  helpers callable from both `opencode.go` (one-shot) and
  `opencode_serve.go` (resident) — do not fork-and-duplicate this logic.
  These are small, mechanical extractions; do them as part of this PR
  rather than leaving two copies to drift.

### Step 6 — Tests

- [ ] Fake-HTTP-server-based unit tests for the SSE client (scripted
  event sequences), including:
  - Normal happy path (`message.part.delta`* → `session.idle` → poll →
    `Result completed`).
  - **Regression test for the upstream drop-out bug**: a scripted SSE
    sequence that never emits `message.updated`/`message.part.updated`
    but does emit `session.idle` — must still complete correctly via
    the poll-after-idle path. This is the single most important test
    in this PR; it's the concrete proof the "flaky" risk Parker flagged
    is actually closed.
  - `session.error` → `Result.Status == "failed"`.
  - No terminal event within timeout → `Result.Status == "timeout"`.
- [ ] Process-reuse test: two sequential `Execute` calls against one
  backend instance spawn exactly one `opencode serve` process.
- [ ] `Close()`/unhealthy-release test: process is actually terminated,
  no orphan.
- [ ] `agent_runtime_pool` wiring test:
  `canonicalRuntimeModeFor("opencode", executionProfileFull)` returns
  resident; factory produces a working lease backed by
  `OpenCodeServeBackend`.
- [ ] Full `go build ./... && go vet ./... && go test ./...` (at least
  `./pkg/agent/...` and `./internal/daemon/...`) clean before opening
  the PR.

## Verification

- [ ] `go build ./...`
- [ ] `go vet ./...`
- [ ] `go test ./pkg/agent/... ./internal/daemon/...`
- [ ] Manual smoke test (if an `opencode` CLI with `serve` support is
  available in this environment): create an agent on the `opencode`
  provider, send a chat message, confirm only one `opencode serve`
  process appears in `ps` across two consecutive turns in the same chat
  session, and that a deliberately-slow/erroring turn surfaces a clean
  failure rather than a hang.
