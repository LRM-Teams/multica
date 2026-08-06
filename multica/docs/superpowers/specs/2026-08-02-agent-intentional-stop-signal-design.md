# Agent intentional-stop signal (task ① of "align with Raft" status work)

## Problem

Yesterday's incident (机器掉线 15 小时, UI still showed "running") burned a full
day of investigation because the team couldn't tell, from the agent's display
status, whether a silent runtime was **network-dropped** (transient, will
likely come back on its own) or **deliberately stopped** (`multica daemon
stop`, needs a human to start it again). Both currently look identical:
5 minutes without a heartbeat.

Parker's ask (task ①): give the daemon's own intentional shutdown a signal
that survives to the read-time display status, so `disconnected`/`offline`
can eventually split into "will probably reconnect" vs "was stopped on
purpose."

## What already exists (don't rebuild)

Contrary to the initial assumption in chat, the daemon **already tells the
server** when it shuts down gracefully:

- `Daemon.Run` defers `d.deregisterRuntimes()` (`server/internal/daemon/daemon.go:817`),
  which calls `d.client.Deregister(...)` on a fresh 5s context — this fires
  for `Ctrl-C` / `multica daemon stop` / `daemon restart`'s shutdown step,
  anything that lets Go's `defer` run.
- Server-side, `DaemonDeregister` (`server/internal/handler/daemon.go:738`)
  calls `Queries.SetAgentRuntimeOffline` and writes an `agent_activity_event`
  row with `reason_code: "daemon_deregistered"`.
- This is a **different SQL path** than the sweeper's silent-timeout flip:
  the sweeper uses the batch query `MarkRuntimesOfflineByIDs`
  (`server/cmd/server/runtime_sweeper.go`), never `SetAgentRuntimeOffline`.
  `SetAgentRuntimeOffline` is also reused by ephemeral-sandbox teardown
  (`ephemeral_sandbox_manager.go`) and `service/task.go` — all three of its
  callers represent "the server/daemon *knows* this runtime is done," as
  opposed to the sweeper's "haven't heard from it, presuming dead."

**The gap is narrower than assumed**: the fact is recorded, but it never
reaches the read-time status computation. `agentRuntimeDisplayStatus` /
`service.RuntimeConnectivity` (`server/internal/service/runtime_connectivity.go`)
is purely time-based — it reads `rt.Status`, `rt.LastSeenAt`, `rt.UpdatedAt`
and nothing else. A gracefully-deregistered runtime and a network-dropped one
both flip `status='offline'`, then ride the exact same `Stale` (<5min) →
`Dead` (>=5min) ramp — i.e. `disconnected` → `offline` — with no way to tell
them apart on the badge. The `daemon_deregistered` event exists but is
admin/owner-gated in the Activity Health tab (`hasInternalsAccess`) and nobody
thought to look there mid-incident.

Caveat already covered in chat: `kill -9` / power loss skips Go's `defer`
entirely, so no Deregister call happens — that case is indistinguishable from
a network drop by construction, and correctly should be: we have no fact to
report, so it must fall back to the existing silence-based path. Not a bug,
not something this design tries to fix.

## Design

Add a `reason` to "offline" that's optionally set at write time by whichever
of the three call sites *knows* it, and consulted at read time.

### 1. Schema (migration `263_agent_runtime_offline_reason`)

```sql
ALTER TABLE agent_runtime ADD COLUMN offline_reason TEXT;
```

Nullable, no default. `NULL` = "we don't know why it's offline" (sweeper
path, or the row was never explicitly deregistered) — same behavior as
today. Non-null = a known reason code, mirroring the `reason_code` string
vocabulary `agent_activity_event` already uses (no new enum type).

`MarkRuntimesOfflineByIDs` (sweeper) does **not** set this column — leaving
it untouched (or explicitly clearing it back to `NULL`? see Open Question 1
below) preserves "silence-based, don't know why" as the honest default.

### 2. Write side

`SetAgentRuntimeOffline` gains an optional reason parameter:

```sql
-- name: SetAgentRuntimeOffline :exec
UPDATE agent_runtime
SET status = 'offline', offline_reason = @offline_reason, updated_at = now()
WHERE id = @id;
```

sqlc will generate a `pgtype.Text` param — callers that don't have a specific
reason pass an empty/null value, so behavior is unchanged unless a caller
opts in. Three call sites to update:

- `DaemonDeregister` → `"daemon_deregistered"` (the one this task is actually
  about).
- `ephemeral_sandbox_manager.go` → `"sandbox_teardown"` (free correctness
  improvement, same "we know, not guessing" family — low risk to include
  since it's the same call already being touched, but flagged as optional
  scope in the plan; can be dropped to keep the PR minimal if Parker wants
  task ① narrow).
- `service/task.go` → leave as `NULL`/unset for now unless research shows
  it's also a "confirmed" path — not spending time on this in task ①, not
  guessing.

### 3. Read side

`service.RuntimeConnectivity` needs the reason to bypass the Stale→Dead time
ramp for a confirmed-offline runtime — an intentional stop should read as
"stopped" immediately, not spend 5 minutes looking like "disconnected" first.

```go
const RuntimeConnectivityStopped RuntimeConnectivityTier = iota + 3 // after Dead

func RuntimeConnectivity(rt db.AgentRuntime, now time.Time) RuntimeConnectivityTier {
	if rt.Status == "offline" && rt.OfflineReason.Valid && rt.OfflineReason.String != "" {
		return RuntimeConnectivityStopped
	}
	... existing time-based logic unchanged ...
}
```

`agentRuntimeDisplayStatus` gets a new case:

```go
const agentDisplayStatusStopped = "stopped"
...
switch runtimeConnectivity(rt, now) {
case runtimeConnectivityStopped:
	return agentDisplayStatusStopped
case runtimeConnectivityDead:
	return agentDisplayStatusOffline
...
```

This is additive to the existing `idle/working/disconnected/offline`
4-word vocabulary from task #42③ — not a rename, so it doesn't disturb
existing callers switching on those strings (frontend enum-drift rule in
CLAUDE.md: any `switch` on this string needs a `default`, which the existing
code already needs regardless).

A runtime that reconnects after a `stopped` state (daemon restarted) goes
through the normal register/heartbeat path, which presumably clears `status`
back to `online` — confirm `offline_reason` gets cleared too on
re-registration (`UpsertAgentRuntime` — check `DO UPDATE` clause) so a stale
reason from three restarts ago doesn't linger. This is a required check
during implementation, not optional polish — a stuck stale reason would be
worse than no reason (invented-looking, violates the #815/#42③ truth-model
rule).

## Open questions for Parker before implementing

1. ~~Does `UpsertAgentRuntime` clear a stale `offline_reason` on
   reconnect?~~ **Checked, it doesn't — found a real bug while writing this
   doc.** `UpsertAgentRuntime`'s `DO UPDATE SET` (`server/pkg/db/queries/runtime.sql`)
   refreshes `name`/`status`/`device_info`/`metadata`/`owner_id`/timestamps
   but never touches `offline_reason`. Without an explicit
   `offline_reason = NULL` added to that `DO UPDATE`, a runtime that was
   gracefully stopped and later reconnects would carry its stale
   `"daemon_deregistered"` reason forever — so a *future* sweeper-timeout
   offline (a real disconnect) would incorrectly still read as "stopped."
   This is now a required line in the migration-adjacent query change, not
   optional polish.
2. Ephemeral-sandbox teardown (`sandbox_teardown` reason) — include in this
   PR (same callsite family, ~2 line diff) or keep task ① strictly to the
   daemon-shutdown case Parker asked about? Leaning include, since it's the
   same mechanism and free, but flagging since it widens the diff slightly
   beyond the literal ask.
3. Frontend: this design only gets the new `"stopped"` value as far as the
   API response (`RuntimeDisplayStatus` field, already returned by
   `GetAgentHealth`/list endpoints per the existing 4-word vocab). Whether/how
   the UI renders it (icon, copy, "Stopped" vs current default-branch
   fallback) is Felix's territory per the Package Boundary rules — this
   design does not include a frontend change, just makes the honest value
   available. Confirm that's the right scope boundary for task ①.

## Non-goals (explicitly out of scope for task ①)

- `crashed` and `starting` signals — Alice's ② and Barry's ③, separate work.
- Changing the Activity Health tab's event vocabulary
  (`agentHealthStateSuspectedDisconnect` is arguably the wrong state label
  for a *confirmed* deregister — it reads as "suspected," not certain — but
  that's the 5-state Activity Health vocab, a different surface from the
  4-word display-status badge this design touches. Worth a follow-up note,
  not fixing here to keep this PR's diff focused.)
- Any change to `kill -9` / crash handling — no fact exists to report there;
  see caveat above.
