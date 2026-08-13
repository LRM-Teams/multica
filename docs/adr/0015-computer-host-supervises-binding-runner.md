---
status: accepted
---

# Computer host supervises one Binding Runner child

The Computer host owns the desired Binding set. Each wanted Binding has at
most one supervised OS child (`computer __runner --workspace-id`). Host
reconcile, not the cloud, decides spawn, stop, backoff, and degrade.

This matches Raft Computer's host loop:

- `RECONCILE_INTERVAL_MS = 5s`
- `CHILD_RESTART_BACKOFF_MS = 2s`
- `CRASH_WINDOW_MS = 60s`, `DEGRADED_THRESHOLD = 3`
- `canSpawn` is false while a child occupies the slot, or while lifecycle is
  not `crashed` / `stopped`, or while backoff has not elapsed
- `unlinked-terminal` is a child that the server deleted; it is not "Binding
  removed from the local desired set"

Desired-set removal is a graceful stop. The Binding can be re-added and
spawned again. Using unlinked here would degrade the slot and refuse a later
re-add.

## Generation fence

Raft Computer rejects stale process work as `inactive_process_generation`
when `current !== process`. The Binding slot uses the same fence: each
`ObserveSpawn` increments a generation, and `observe` is a no-op unless that
generation still owns the slot. A previous supervise — including
`observe(nil)` when there is no child handle — must not crash, degrade, or
delete a later spawn.

The live child handle is a second identity check when an OS child exists.

## What this cut is not

The OS child is the lifetime and crash boundary. Workspace Runner delivery,
Inbox, and Agent Process Manager still run in the host process. Moving that
coordinator into the child is a later cut. This ADR does not claim Raft-complete
process isolation of delivery.

Machine Upgrade (local swap completes locally; cloud is post-hoc attestation)
is a different cut.
