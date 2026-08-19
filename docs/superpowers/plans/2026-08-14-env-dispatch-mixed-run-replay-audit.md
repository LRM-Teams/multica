# Env-dispatch mixed-run replay audit

**Source commit:** `082b25b409cd0ff5631cda2cb4ed5b2b09fb6456`
**Source parent:** `865e01a9c877e38f027292159599fbe6fcc64b9a`
**Audited dev base:** `604be03d4e2eaff0391d6d13f19b822876da7bd0`
**Source subject:** `feat(env-dispatch): add mixed-run RL launch, capture, and activity accounting`

## Recorded source change

The source commit changes 65 server files (13,641 added and 643 removed lines), grouped as follows:

1. **Env-dispatch request and response contract** — adds `online_trainable_agents`,
   `offline_trainable_agents`, `quiet_window_ms`, `total_timeout_seconds`, and
   `shared_sandbox` handling in the handler and service layers.
2. **Mixed-run preflight and persistence** — validates the roster, resolves per-agent
   execution semantics, records mixed dispatch runs, and adds migrations 313–315 for
   run state, provider call ledger, and frozen interaction-DAG snapshots.
3. **Message delivery and runtime lifecycle accounting** — records run-scoped delivery
   obligations and daemon activity transitions for turns, queued messages, tools, and
   capture batches; persists/replays unacknowledged transitions and acknowledges them
   only after server commit.
4. **Sandbox and derived-agent provisioning** — carries the resolved shared-sandbox
   decision through channel provisioning and agent creation.
5. **Protocol/database/client integration** — extends Pi RPC, event/message protocol,
   generated database accessors, and environment queries.
6. **Regression coverage** — adds or updates handler, service, daemon, WebSocket,
   protocol, migration, and fixture tests for the above behaviours.

## Latest-dev replay result

The latest dev base already contains the source functionality. A file-by-file comparison
of the source commit against this base found:

- all 65 source-change paths exist in latest dev;
- 29 target files are byte-for-byte unchanged since the source commit, including the
  shared-sandbox provisioning, mixed-run migrations, and the primary preflight/roster
  regression suites;
- the other 36 paths have subsequent changes, while the current code retains the target
  request fields, dispatch lifecycle, activity transition protocol, and persistence
  interfaces.

Representative current locations:

- `server/internal/handler/env_dispatch.go` — HTTP request fields and defaults.
- `server/internal/service/env_dispatch.go` — preflight, mixed-run creation, timeout,
  quiet-window, and shared-sandbox orchestration.
- `server/internal/daemon/mixed_run_activity*.go` — durable lifecycle outbox/replay.
- `server/internal/handler/runner_activity.go` and
  `server/internal/daemonws/hub.go` — server commit and acknowledgement path.
- `server/migrations/313_env_dispatch_mixed_rl_run.*.sql`,
  `314_pi_provider_call_ledger.*.sql`, and
  `315_interaction_dag_frozen_snapshot.*.sql` — persisted state.

## Verification

The retained source-derived regression coverage was exercised on the audited dev base:

```bash
cd server
go test ./internal/handler ./internal/service ./internal/daemon ./internal/daemonws ./pkg/protocol ./pkg/agent
```

The command passed.

## Replay decision

Do not cherry-pick or duplicate the source code: it would reintroduce already-absorbed
implementation and create merge conflicts. This branch records the provenance and
verification that latest dev retains the required env-dispatch contract. Deployment
readiness was not assessed by this code-retention audit; no deployment or environment
mutation is performed by this audit.
