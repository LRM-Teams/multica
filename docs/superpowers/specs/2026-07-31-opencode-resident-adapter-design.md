# OpenCode resident runtime adapter — design

Task: #33 (Frank, 2026-07-31, high priority). Add `opencode` to Multica's
resident agent-runtime pool alongside `pi`, `grok`, `cursor`, so an
OpenCode-backed agent keeps one long-lived process per agent×runtime slot
instead of spawning a fresh `opencode run` subprocess every turn.

Scope note from Frank/Parker: full/complete adapter, not an MVP — auth,
error handling, and event-shape parity should all land together, at the
same completion level as the pi/grok/cursor adapters.

## Sources used

Per the IP boundary discussed in `#prj-daemon` (thread `6cb04a5c`): this
design is built entirely from public, legitimately-available sources —
no reverse engineering of any closed-source third-party binary was done
or is planned.

1. **OpenCode's own public API docs** (opencode.ai/docs/server) — the
   `opencode serve` HTTP+SSE contract.
2. **Public GitHub issues on the opencode repo** (anomalyco/opencode
   #26697, #27966) — real-world SSE delivery bugs in recent OpenCode
   versions that any client must design around.
3. **Multica's own existing code** — `agent_runtime_pool.go`,
   `cursor_acp.go`, `grok_acp.go`, `grok_persistent.go`,
   `cursor_canonical.go`, `opencode.go` (the current one-shot backend).
4. **Raft's public agent-facing manual** (`raft manual get runtime`) —
   confirms OpenCode is one of Raft's ten supported runtime families, but
   the manual is user-facing and does not document Raft's internal
   process-persistence implementation, so it does not materially inform
   this design beyond "OpenCode residency is a solved problem elsewhere,
   worth doing well here too."

## Current state (one-shot `opencode.go`)

`opencodeBackend.Execute` (`server/pkg/agent/opencode.go:32`) spawns
`opencode run --format json --dangerously-skip-permissions ...` fresh
per turn, parses NDJSON events off stdout, and exits. `--session <id>`
only resumes OpenCode's own on-disk conversation state in a **new**
process — it is not residency. `agent_runtime_pool.go:167` only grants
`canonicalRuntimeResident` mode to `pi`, `grok`, `cursor`; everything
else (including `opencode`) falls through to `canonicalRuntimeOneShot`.

## Target architecture

Mirror the **cursor** integration exactly — it's the newest of the three
resident adapters and the simplest (single canonical pool path, no
legacy per-provider pool):

```
server/pkg/agent/opencode_serve.go       (NEW) — OpenCodeServeBackend
server/internal/daemon/opencode_canonical.go (NEW) — newCanonicalOpenCodeResidentBackend
server/internal/daemon/agent_runtime_pool.go (EDIT) — add "opencode" to the resident switch + factory
```

`OpenCodeServeBackend` implements `agent.Backend` (`Execute(ctx, prompt,
opts) (*Session, error)`) plus a `Close()` method, same shape as
`CursorACPBackend`/`GrokACPBackend`. One backend instance owns exactly
one `opencode serve` child process, started lazily on first `Execute`
and reused for every subsequent turn until `Close()` (config drift,
unhealthy release, or idle eviction — all handled by the existing
`canonicalAgentRuntimePool`, unchanged).

### Why HTTP+SSE instead of stdio

Cursor/Grok/Pi are resident over **stdio** (ACP JSON-RPC or Pi's own RPC
protocol) because that's the transport their CLIs expose for a
long-running session. OpenCode's long-running mode is different: `opencode
serve` is a **headless HTTP server** — `POST /session`, `POST
/session/:id/message`, `GET /event` (SSE). The resident *pattern*
(pool/slot/lease, lazy process start, health-triggered disposal) is
identical; only the wire protocol inside the adapter differs. This is a
protocol-appropriate variation of the established Multica pattern, not a
deviation from it.

### Process lifecycle

1. `ensureServer(ctx, opts)` (guarded by a mutex, mirroring
   `cursorACPBackend.ensureProcess`):
   - If a live server process exists, reuse it.
   - Otherwise: pick a free localhost port, generate a random
     `OPENCODE_SERVER_PASSWORD` (see Auth below), spawn `opencode serve
     --port <p> --hostname 127.0.0.1` with `cmd.Dir = opts.Cwd`, the same
     env-building path as today's `opencode.go` (`buildEnv`,
     `OPENCODE_CONFIG_CONTENT` for MCP, `PWD` override for project
     discovery — all of that logic is reusable as-is, see "Config parity"
     below).
   - Poll `GET /doc` (or attempt a `POST /session` and treat connection
     refused as "not ready yet") with a bounded retry/backoff until the
     server accepts connections or a startup timeout elapses.
   - Open one persistent `GET /event` SSE connection for the life of the
     process; a background goroutine reads it and fans events out by
     `sessionID` to whichever turn is currently active (there is at most
     one, since the resident pool serializes turns per slot — same
     invariant `cursorACPBackend.running` enforces).
2. `Execute` per turn:
   - If no OpenCode session exists yet for this backend instance, `POST
     /session` (optionally `{parentID}` when `opts.ResumeSessionID` names
     a prior session — OpenCode's session IDs **are** the resume
     handle, same role `p.sessionID` plays for Cursor ACP).
   - `POST /session/:id/message` with the prompt as a `parts: [{type:
     "text", text: prompt}]` message body, `model`/`agent` fields set
     from `opts.Model`/thinking-level equivalent.
   - Stream `Message` values to the caller's channel as SSE events for
     that `sessionID` arrive (see Event mapping below), and return the
     final `Result` when the turn's terminal signal (see Turn completion
     below) fires.
3. `Close()`: kill the `opencode serve` process, close the SSE
   connection, same as `cursorACPBackend.Close()`/`disposeProcessLocked`.

### Auth

Per opencode.ai/docs/server: setting `OPENCODE_SERVER_PASSWORD` enables
HTTP basic auth on the server; `OPENCODE_SERVER_USERNAME` defaults to
`"opencode"`. Since this server only ever listens on `127.0.0.1` and is
spawned+owned by our own daemon process for its own exclusive use (no
other process is meant to reach it), the adapter should still **always**
set a random per-process password (generated at process start, kept
in-memory only, never logged) rather than relying on loopback-only
binding as the sole protection — defense in depth against any other
local process/user on a shared host. This mirrors treating `cursor-agent
acp`'s stdio transport as inherently private (it's a pipe): the HTTP
transport needs its own equivalent isolation since a TCP port is
reachable by more than just our own process tree.

### Turn completion — the critical correctness issue

**OpenCode has a live, currently-unresolved upstream bug** (GitHub
anomalyco/opencode#27966, confirmed present through OpenCode 1.15.1):
`message.updated` and `message.part.updated` SSE events — the ones a
naive client would use to detect "the assistant's message is done" —
silently stop being delivered over `/event` on affected versions, even
though the underlying DB writes succeed. The **unaffected** event types
are `session.status`, `session.diff`, `session.idle`, and
`message.part.delta`.

Design consequence: **the adapter must not depend on `message.updated`/
`message.part.updated` as the turn-completion signal.** Instead:

- Use `session.idle` (confirmed reliably delivered across versions) as
  the authoritative "this turn is finished" signal.
- Use `message.part.delta` for incremental text streaming (`MessageText`
  messages) as it arrives — this is unaffected and gives the same
  responsive token-by-token UX the one-shot backend's `"text"` event
  handling gives today.
- On `session.idle`, **do not trust accumulated SSE state alone for the
  final message content** — follow up with `GET
  /session/:id/message/:messageID` (or `GET /session/:id/message` for
  the latest) to fetch the authoritative final message/parts, and derive
  `Result.Output`, tool-use messages, and token usage from that response
  rather than solely from whatever streamed through SSE. This is exactly
  the "poll as a safety net" mitigation the bug reporter itself
  recommends, and it also naturally protects us if a **future** OpenCode
  version regresses a *different* event type the same way — the
  reconciling poll after idle is the source of truth, SSE is only the
  live-typing UX layer.
- Watch for `session.error` (present in OpenCode's event type list) as
  the error channel — same spirit as the one-shot backend's existing
  comment that "OpenCode can exit with RC=0 even on errors ... error
  events are the reliable signal," except here there is no process exit
  code at all to lean on (the server keeps running across turns), so
  `session.error` and a request-level timeout watchdog are the *only*
  failure signals. A turn that produces neither a `session.idle` nor a
  `session.error` within a bounded timeout must be treated as failed
  (status `"timeout"`), same semantics as `opts.Timeout` today.

### Event → Message mapping

| OpenCode SSE event / API response         | Multica `Message`/`Result` field                                          |
|---|---|
| `message.part.delta` (text part)          | `Message{Type: MessageText, Content: ...}` (incremental)                  |
| Tool part in the reconciled final message  | `Message{Type: MessageToolUse, ...}` then `MessageToolResult` — same split the one-shot `handleToolUseEvent` already does, just sourced from the polled final message instead of a stream event |
| `session.error` / reconciled error state  | `Message{Type: MessageError, ...}`, `Result.Status = "failed"`            |
| `session.idle`                            | triggers the reconcile-then-emit-`Result` path                           |
| final message's token usage (if present)  | `Result.Usage` — same accumulation shape as today's `eventResult.usage`   |

### Config / feature parity with the one-shot backend

Everything in `opencode.go` that isn't about *how the process is
invoked* carries over unchanged, since it's config translation, not
transport:

- `buildOpenCodeMCPConfigContent(opts.McpConfig)` → `OPENCODE_CONFIG_CONTENT` env var, set once at server spawn (MCP config is process-wide for `opencode serve`, not per-session — needs verifying against the OpenAPI spec at `/doc` during implementation; if OpenCode's session API accepts a per-session MCP override that would be a better fit for the canonical pool's per-turn `opts.McpConfig`, and the plan phase should check for that field explicitly rather than assuming process-wide is correct).
- `--dir`/`PWD` project-discovery anchoring → same env var override at server spawn (`cmd.Dir`, `PWD=`), since the resident server's cwd, like the one-shot process's, drives `AGENTS.md`/`.opencode/skills` discovery.
- Model/thinking-level selection → per-turn fields on `POST
  /session/:id/message` (`model`, and whatever OpenCode's server API
  calls thinking/variant — confirm exact field name against `/doc`
  during implementation).
- Windows shim resolution (`resolveOpenCodeNativeFromShim`) → reused
  as-is for locating the `opencode` binary to spawn as `opencode serve`.
- `opencodeBlockedArgs` → equivalent guard for whichever `serve` flags
  must not be user-overridable (`--port`, `--hostname` at minimum).

### Pool wiring

```go
// agent_runtime_pool.go
func canonicalRuntimeModeFor(...) {
    switch strings.TrimSpace(provider) {
    case "pi", "grok", "cursor", "opencode": // add opencode
        return canonicalRuntimeResident, nil
    ...
}

func defaultCanonicalRuntimeFactory(...) {
    if mode == canonicalRuntimeResident {
        switch provider {
        ...
        case "opencode":
            return newCanonicalOpenCodeResidentBackend(config)
        ...
```

```go
// opencode_canonical.go (new, mirrors cursor_canonical.go exactly)
func newCanonicalOpenCodeResidentBackend(cfg agent.Config) (agent.Backend, func(), error) {
    backend := agent.NewOpenCodeServeBackend(cfg)
    return backend, backend.Close, nil
}
```

No changes needed to `usesCanonicalResidentChatRuntime` beyond adding an
`"opencode"` case if OpenCode should also gate on `ChatSessionID` the way
Grok/Pi do (needs a product decision in the plan phase: should OpenCode
resident apply to chat only, or also to issue tasks, mirroring
cursor's `executionProfileFull` gate rather than grok/pi's chat-only
gate — cursor is the closer precedent since both are wired purely
through the canonical pool with no legacy per-provider pool).

## Comparison with Raft (public-information basis only)

Raft's public manual (`raft manual get runtime`) confirms OpenCode is
one of Raft's ten supported runtime families and documents user-facing
behavior (model dropdown via `opencode models` detection, custom
provider config via `~/.config/opencode/opencode.json`), but is
explicitly user-facing documentation — it does not describe whether
Raft's OpenCode integration is resident or one-shot, nor its internal
adapter architecture. No claim about "Raft does X internally" should be
taken from this design doc beyond what's quoted above; anything more
would require Raft's own engineering documentation or explicit
authorization from Raft (not from a Raft customer) to inspect
non-public implementation details, neither of which is available here.

Net: Multica's own `agent_runtime_pool` design (fingerprint-based
slot/lease pool, canonical across providers, provider-specific
transport adapters underneath) is already a solid, general pattern —
this design extends it rather than replacing it, consistent with "this
is good enough, no need to import an unseen architecture."

## Open questions for the plan phase

1. Exact OpenCode server API field names for thinking/variant selection
   and per-session vs per-process MCP config — resolve against the live
   `GET /doc` OpenAPI spec (or the `@opencode-ai/sdk` TypeScript types)
   during implementation, not assumed here.
2. Whether OpenCode resident should be chat-only (grok/pi precedent) or
   full-profile (cursor precedent) — needs Parker/Frank's product call,
   default to cursor's precedent (full profile) since it's the newer
   pattern and OpenCode's server mode has no inherent reason to be
   chat-restricted.
3. Idle-TTL config: add `MULTICA_OPENCODE_PERSISTENT_IDLE_TTL` /
   `DefaultOpenCodePersistentIdleTTL`? Cursor's resident backend is
   evicted purely through the canonical pool's generic `evictIdle`, not
   a dedicated env var — follow that precedent (no new env var needed;
   the canonical pool's existing idle eviction sweep applies uniformly).

## Test plan (for the implementation phase)

- Unit tests for the OpenCode-serve HTTP/SSE client in isolation
  (mirroring `grok_persistent_test.go`'s style): fake HTTP server
  emitting scripted SSE sequences, including a scripted repro of the
  `message.updated` drop-out bug, proving the adapter still completes
  correctly via `session.idle` + reconcile-poll.
- A test that a `session.error` event surfaces as `Result.Status ==
  "failed"` with the error message populated (parity with the one-shot
  backend's `handleErrorEvent` test coverage).
- A test that two sequential `Execute` calls against the same backend
  instance reuse the same underlying process (no second `opencode serve`
  spawn) — the actual residency proof, analogous to whatever test
  currently proves Cursor ACP process reuse.
- A test that `Close()`/unhealthy release actually terminates the
  spawned process (no orphaned `opencode serve`).
- Full `agent_runtime_pool` wiring test: `canonicalRuntimeModeFor("opencode", ...)`
  returns resident; factory produces a working lease.
