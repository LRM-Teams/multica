# Plan: agent intentional-stop signal

Design: `docs/superpowers/specs/2026-08-02-agent-intentional-stop-signal-design.md`.
Approved by Parker, all three open questions resolved: bug-fix is in scope,
sandbox teardown reason is in scope, frontend is explicitly out of scope
(routed separately).

Backend-only, Go. TDD: failing test before each implementation step.

## 1. Migration

New file pair `server/migrations/263_agent_runtime_offline_reason.{up,down}.sql`
(263 is next after 262_daemon_runtime_update_intent).

```sql
-- up
ALTER TABLE agent_runtime ADD COLUMN offline_reason TEXT;
-- down
ALTER TABLE agent_runtime DROP COLUMN offline_reason;
```

Nullable, no default, no backfill needed (existing rows: unknown reason = NULL,
which is the correct honest default).

## 2. Query changes (`server/pkg/db/queries/runtime.sql`)

- `SetAgentRuntimeOffline`: add `offline_reason` param, write it in the same
  `UPDATE`.
- `UpsertAgentRuntime`: add `offline_reason = NULL` to `DO UPDATE SET`. This
  is the bug-fix half — required, not optional cleanup.

Run `make sqlc` after editing to regenerate `server/pkg/db/generated`.

## 3. Call sites

- `server/internal/handler/daemon.go` `DaemonDeregister` (~line 773):
  pass `"daemon_deregistered"`.
- `server/internal/handler/ephemeral_sandbox_manager.go` (~lines 137, 144):
  pass `"sandbox_teardown"`.
- `server/internal/service/task.go` (~line 2446): leave unset/NULL — do not
  guess a reason for this path.

## 4. Read-side tier (`server/internal/service/runtime_connectivity.go`)

Add `RuntimeConnectivityStopped` tier. In `RuntimeConnectivity()`, check
`rt.Status == "offline" && rt.OfflineReason.Valid && rt.OfflineReason.String != ""`
**before** the existing time-based Stale/Dead branches — a confirmed reason
short-circuits the ramp, no 5-minute "disconnected" phase for a runtime we
know was deliberately stopped.

## 5. Display status (`server/internal/handler/agent_health.go`)

Add `agentDisplayStatusStopped = "stopped"`. Add a `case
runtimeConnectivityStopped:` to `agentRuntimeDisplayStatus()`'s switch,
returning it before the `Dead`/`Stale` cases (mirrors tier ordering).

## 6. Tests (write failing, then make pass, in this order)

1. `runtime_connectivity_test.go` (new or existing, check for one first):
   `TestRuntimeConnectivity_OfflineReasonBypassesTimeRamp` — a runtime with
   `status='offline'`, `offline_reason='daemon_deregistered'`, and
   `UpdatedAt` only 1 second ago must return `RuntimeConnectivityStopped`,
   not `RuntimeConnectivityOnline`/`Stale` — proves the reason check runs
   before, not after, the time gate.
2. `agent_health_test.go`: `TestAgentRuntimeDisplayStatus_Stopped` — same
   fixture, asserts `agentRuntimeDisplayStatus(...) == agentDisplayStatusStopped`.
3. **Critical regression test** — `TestUpsertAgentRuntime_ClearsStaleOfflineReason`
   (sqlc-generated query test, likely alongside existing `UpsertAgentRuntime`
   tests in `server/pkg/db/generated` test files or an integration test under
   `server/internal/handler`): seed a runtime row with a stale
   `offline_reason` from a prior deregister, call `UpsertAgentRuntime` as a
   reconnect would, assert `offline_reason` is NULL afterward. This test
   must fail against the current (unfixed) query and pass after step 2's
   query edit — write it before editing the query to prove it actually
   catches the bug.
4. `handler_test.go` or `daemon_test.go`: `TestDaemonDeregister_SetsOfflineReason`
   — call the handler, assert the persisted row has
   `offline_reason = "daemon_deregistered"`.
5. Equivalent assertion for the two `ephemeral_sandbox_manager.go` call
   sites if existing tests there provide a natural hook; otherwise a small
   dedicated test.

## 7. Verification

`make check` before calling this done (per project CLAUDE.md pre-push gate).
No frontend changes, so no React Doctor gate applies to this PR.

## 8. PR

Single PR, `/commit-push-pr` per user's global convention. Post to Parker's
thread for review before merge (matches team norm this session — small
focused PRs, PM/lead sign-off, exact-head CI green before self-merge).
