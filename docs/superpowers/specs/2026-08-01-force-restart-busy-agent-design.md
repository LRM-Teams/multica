# Force-restart a busy/stuck canonical-resident agent (task #62)

## Problem

`POST /api/agents/{id}/lifecycle` with `action_kind=restart` is the mechanism
behind the agent restart button Frank has been waiting for. Live-tested today
against a genuinely stuck `cursor` agent (real process, real canonical
session, local isolated environment — not a code read): when the target
agent's canonical turn is busy (`hasActiveTurn` true), `CreateAgentLifecycleOperation`
does not execute the restart. It creates an operation with
`status=scheduled, execution_mode=after_current_run` and returns HTTP 202
("accepted"). Confirmed via direct DB query that this operation sat at
`scheduled` for 5+ minutes with `started_at`/`finished_at` both null, while
the underlying process crashed and was re-created multiple times by the
existing crash-recovery path (task #42②) — the scheduled operation never
noticed any of that and never advanced.

This is a silent no-op for exactly the case Frank cares about: a **stuck**
agent's turn never ends on its own, so `after_current_run` never arrives.
The user sees "submitted" and nothing happens, which is worse than an
outright rejection — it looks like the button worked.

## Decisions already made (not open for renegotiation here)

Both from Frank, via Parker, 2026-08-01:

1. **No additional busy-confirmation UI.** Restart executes directly once the
   user picks a mode in the existing 3-mode reset dialog (task #633:
   Restart / Reset Session & Restart / Full Reset & Restart). That dialog is
   already the "are you sure" step; a second "it's busy, still sure?" prompt
   asks the same question twice for no new information.
2. **An interrupted turn is recorded as an existing failure state** — no new
   status enum. The reason must clearly say the agent was restarted by a
   user, not read as a system error (we spent today fixing several "looks
   broken but isn't" cases — this must not add one). **No automatic retry**:
   the human who restarted it decides whether to re-send the task.

## Scope boundary (explicit, so it isn't assumed away later)

This fix targets **`action_kind=restart` only**, for **canonical-resident
providers only** (`pi`, `grok`, `cursor`, `opencode` — the four gated by
`isCanonicalResidentProvider`). Two things are deliberately **out of scope**
(confirmed with Parker/Frank, not an assumption on my part):

Rationale for the split: Frank's stated need all day has been "the agent is
stuck, I want it to come back" — he has never asked to clear a session or
delete a workspace. `restart` maps to that intent; `reset_session_restart`
and `full_reset_restart` are different user intents (reset, not restart) and
whether *they* should also interrupt a busy turn is a separate product
question, not answered here.

- **`reset_session_restart`** keeps its current scheduled-when-busy behavior.
  It also calls `sessionReset.ResetAgentRuntimeSession` before invalidating
  the runtime, which has its own busy assumptions on the daemon side that
  haven't been audited for force-interrupt safety. Extending force-kill to
  this action is a reasonable follow-up, not required to give Frank the
  button he's asking for (which is plain restart on a stuck agent).
- **`full_reset_restart`** keeps its current behavior: rejected outright
  (409 `agent_active`) when busy, never scheduled. This is a destructive
  action (removes the agent's workspace) and the existing code comment
  frames the upfront rejection as intentional ("shouldn't quietly queue a
  destructive action"). Nothing here changes that boundary.
- Non-canonical-resident providers (`claude`, `codex`, etc.) never enter the
  canonical pool, so `restart` against them remains a safe no-op, as today.
  Confirmed by Nash earlier today, not re-verified in this design.

## Design

### 1. Server: stop scheduling `restart` when busy

`CreateAgentLifecycleOperation` (`server/internal/handler/agent_lifecycle.go`)
currently branches on `agentLifecycleHasActiveRun`:

```go
executionMode := agentLifecycleImmediate
status := agentLifecycleRunning
if active {
    executionMode = agentLifecycleAfterCurrentRun
    status = agentLifecycleScheduled
    startedAt = nil
}
```

Change: for `action_kind == agentLifecycleRestart` specifically, always use
`agentLifecycleImmediate` / `agentLifecycleRunning`, regardless of `active`.
`reset_session_restart` and `full_reset_restart` keep the existing branch
untouched. This is a small, scoped `switch` on `req.ActionKind` right where
`active` is currently checked — no new request field, no new API surface.
The existing dispatch path (`AgentLifecycleDispatchStore` → daemon heartbeat
ack → `handleAgentLifecycleOperation`) is unchanged; it already handles
"immediate" operations correctly (verified via today's test-suite run).

### 2. Daemon executor: skip the busy-check for `restart`

`agentLifecycleExecutor.Execute` (`server/internal/daemon/agent_lifecycle.go:55`)
currently has one blanket guard before the action switch:

```go
if e.turns.hasActiveTurn(request.AgentID, request.RuntimeID) {
    return lifecycleStepError("drain", ErrCanonicalAgentRuntimeBusy)
}
```

Change: move this check inside the `switch`, and skip it for
`agentLifecycleActionRestart`. `reset_session_restart`/`full_reset_restart`
keep hitting it exactly as today (they're out of scope, per above).

### 3. Pool: a force variant of `invalidateSession`

`canonicalAgentRuntimePool.invalidateSession` (`agent_runtime_pool.go:452`)
has its own independent guard:

```go
if slot.running {
    return ErrCanonicalAgentRuntimeBusy
}
slot.closeBackend()
```

This guard is real, not decorative: while `slot.running` is true, the
in-flight `Execute()` goroutine holds a reference to the backend and is
reading/writing its process pipes. Naively calling `closeBackend()`
concurrently is a potential data race on that shared state.

Add `forceInvalidateSession(agentID, runtimeID string) error`: when
`slot.running` is true, instead of returning early, call a new **force-kill**
capability on the backend (see §4) and return. It does **not** call
`closeBackend()` directly — that path stays reserved for the
already-safe idle case. The in-flight goroutine's own `Execute()` will get
an I/O error from the killed process and follow its existing error path,
which already calls `release()`/clears `slot.running` — this is the exact
mechanism task #42②'s crash recovery already relies on, and the one I
exercised manually today (`kill -9` on the OS process, 4 times, while
`slot.running` was true) with clean recovery each time and no panics in the
daemon log.

That said, "I killed the OS process from outside Go entirely" is not the
same guarantee as "the backend's own force-kill method, called from a
second goroutine while the first is mid-`Execute()`, is safe." Before this
lands, add a concurrency test that does exactly that (see §6) — this is
the one piece of today's investigation that's a promising signal, not a
proof.

### 4. Backend: a new optional force-kill interface

Mirror the existing optional-capability pattern
(`ResidentRuntimeLivenessChecker`) rather than widening the core `Backend`
interface for every provider:

```go
// ResidentRuntimeForceKillable is an optional contract for backends that
// keep a long-lived provider child process alive across turns. ForceKill
// terminates the underlying process immediately, even if a turn is
// in-flight against it. It must be safe to call concurrently with a
// running Execute() — the caller does not wait for Execute() to notice;
// Execute()'s own error handling is expected to observe the killed
// process and release the turn, exactly as it does for a genuine crash.
type ResidentRuntimeForceKillable interface {
    ForceKill() error
}
```

Implement it on the four canonical-resident backends
(`cursorACPBackend`, and grok/pi/opencode's persistent equivalents).

**`ForceKill()` must NOT simply call the existing `disposeProcessLocked`.**
Nash (original #1682 author) reviewed this and found a concrete stdlib-level
hazard, not a hypothetical one: `disposeProcessLocked` is
`p.stdin.Close() → p.cmd.Process.Kill() → p.cmd.Wait()`. This backend uses
`cmd.StdoutPipe()`/`cmd.StdinPipe()`, and the Go documentation for those is
explicit: *"it is incorrect to call Wait before all reads from the pipe
have completed."* `Execute()`'s own goroutine is the one reading that pipe.
If `ForceKill()` (a second goroutine) also calls `cmd.Wait()` via
`disposeProcessLocked`, two goroutines can end up calling `Wait()` on the
same process concurrently — undefined behavior per the stdlib contract, not
a bug in our own locking.

This is exactly why "I `kill -9`'d it from outside SSH and it recovered
cleanly" (today's informal test) didn't catch this: an external kill never
introduces a second goroutine into the Go process at all, so `Wait()` was
only ever called once, by `Execute()`'s own cleanup. A same-process
`ForceKill()` call is a different scenario.

**Fix**: `ForceKill()` does only the first two steps —
`p.stdin.Close()` + `p.cmd.Process.Kill()` — and never calls `p.cmd.Wait()`.
`Wait()` stays the sole responsibility of `Execute()`'s own goroutine, which
already needs to reap the process once it observes the pipe read failing
(this is presumably already true, since it's how `Execute()` returns from a
real crash today — confirm this explicitly when implementing, don't assume).
Each of the four backends must be checked individually for whether its own
`Close`/`dispose*` path has a similar "only one caller may do this" step
before assuming `ForceKill()` can reuse it — don't copy this pattern
mechanically across all four without checking each one's actual shutdown
sequence.

`Close()`'s existing `b.mu`-guarded access to `b.process` (consistent across
`Close`/`RuntimeAlive`/`getOrCreateProcess`/`disposeProcessLocked` — checked
today) still means `b.process` field access itself is safe; the hazard is
specifically the double-`Wait()` call, not general lock discipline.

If a backend does **not** implement `ResidentRuntimeForceKillable` (there's
a bug, or a future provider is added without it), `forceInvalidateSession`
falls back to the existing busy-rejection behavior rather than silently
no-op'ing — fail closed, not fail silent.

### 5. Interrupted turn's failure record

Per Frank's decision: existing failure state, no new enum, clear
human-readable reason, no auto-retry.

- The task/turn that was mid-flight when force-killed follows its **existing
  crash/error path** (same as task #42②) — it already lands in a failure
  state today for a genuine crash. No new status needed.
- Add a distinct **reason code**, e.g. `agent_restarted_by_user`, set when
  the failure originates from `forceInvalidateSession` rather than a real
  crash (the executor knows which case it's in — it called force-kill
  deliberately, vs. the crash-watch loop discovering an already-dead
  process). Thread this reason through to wherever task #42②'s crash path
  already records a reason/activity event.
- **Visibility classification**: PR #1708 (merged today, task #48) added
  `activityVisibilityFor`/`diagnostic_only` to hide internal housekeeping
  reasons like `agent_reassigned_elsewhere` from the user-facing activity
  feed. `agent_restarted_by_user` must **not** be classified `diagnostic_only`
  — the user deliberately caused this and needs to see that it happened and
  why (Frank's explicit requirement: must not look like a system error).
  Add it to the `user_facing` side of that classification, with copy that
  says something like "restarted by [user] — task not automatically
  retried" rather than a raw error string.
- **No auto-retry**: confirm the existing retry logic (wherever task
  failures get requeued) treats this reason code as terminal, same as it
  presumably already does for other non-retryable failure reasons. If no
  such distinction exists yet, this reason code needs to be added to
  whatever "don't retry these" list already exists (task #48's PR is the
  most recent precedent for this shape of change).

### 6. Safety verification required before merge

Not a proof by inspection — a real, adversarial test. Parker raised the
right objection to today's informal evidence: I proved the OS process can
be `kill -9`'d **from outside the Go process entirely** (an SSH shell
command) while `slot.running` was true, and the daemon recovered cleanly
4/4 times. That is real evidence the *existing crash-recovery path*
(task #42②) handles an unexpectedly-dead process correctly. It is **not**
the same claim as "a second goroutine, inside the same process, taking
`b.mu` to read `b.process` and calling `Process.Kill()`, concurrently with
the first goroutine's in-flight `Execute()` (which may itself be reading/
writing that process's stdio, possibly while holding or about to take
`b.mu`), is race-free." An external `kill -9` never touches Go's own
memory or locks — it only proves the *downstream* recovery is sound, not
that the *new concurrent code path to get there* is safe. Both need
checking, and only the first has evidence so far.

- Integration/unit test that starts a fake long-running backend `Execute()`
  call (blocked on a channel, simulating a hung process), calls `ForceKill()`
  / `forceInvalidateSession` from a second goroutine while the first is
  in-flight, and asserts: no panic, no data race (run with `-race`), the
  in-flight `Execute()` returns (doesn't hang forever), `slot.running`
  clears, and the pool ends up in the same clean state as the existing
  idle-eviction path. This is the test that actually exercises the new
  concurrent path — the informal `kill -9` result does not substitute for it.
  For `cursorACPBackend` specifically, this test must use a real `exec.Cmd`
  with real `StdoutPipe()`/`StdinPipe()` (not a stubbed/mocked process), so
  it can actually surface the double-`Wait()` hazard Nash found — a fake
  backend that never calls the real `cmd.Wait()` contract would pass without
  proving anything.
- A real end-to-end pass on at least one real canonical-resident provider
  (cursor is the one already exercised informally today) mirroring what I
  did manually, but through the **new formal path**, not a manual
  `kill -9`: trigger a real turn, call the real restart API against the
  busy agent, confirm the operation record reaches `succeeded`, confirm a
  follow-up chat message gets a real response (the self-heal path actually
  still works end to end, not just "the slot got cleared").
- Reverse case: force-kill an agent that is **not** currently busy — must
  behave identically to today's already-tested idle-restart path (no
  regression for the common case).

## Open items for implementation, not for further product debate

- Whether `agentLifecycleHasActiveRun` (the **server-side** pre-check that
  currently also gates the SQL query deciding scheduled vs. running) needs
  a matching per-action-kind branch, or whether it's sufficient to just
  change the branch that reads its result. Needs a read of that function's
  current shape before writing the diff.
- Exact reason-code wiring point for task #48's classification — needs a
  fresh read of `internalHousekeepingFailureReason` and the two read paths
  #1708 touched (`agentActivityRowToItem`, the events-feed equivalent) to
  add the new reason code in the same shape Vera used, not a parallel one.
