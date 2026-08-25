---
name: multica-runtimes
description: "Use when inspecting or debugging Multica Computer runtimes, task claiming, or an Agent that did not run. Covers Workspace connections, runtime online state, heartbeat, claim, lease, and safe diagnostic commands."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Multica Runtimes

## Quick start

For an Agent that did not run, inspect the execution chain before changing state:

```bash
multica workspace info --agents --output json
multica runtime list --output json
```

Do not restart daemons or update runtimes merely to test a hypothesis.

## Core model

A runtime is one `(Workspace connection x provider tool)` execution target
behind an Agent. The machine-wide Computer resident owns those local runtime
processes and leases pending `agent_inbox_event` rows from the server.

The chain is:

1. a user action creates or updates an `agent_inbox_event`;
2. the event targets an Agent and runtime;
3. the server wakes the runtime over the Computer's websocket when possible;
4. the Computer resident drains and leases the canonical inbox event;
5. the resident starts the provider CLI in the Agent's durable workspace;
6. the resident reports completion.

Machine Upgrade is Computer-scoped. Internally, historical route and database
fields still use `daemon` names; installed clients may also call legacy runtime-scoped HTTP update
paths. Those are compatibility adapters over the same Computer operation and do
not create runtime-owned update state. Startup keeps incomplete local upgrade
journals fail-closed; only a proven later Active generation may supersede a
retained `candidate_ready` marker, and the marker remains available for diagnosis.
For standalone upgrades, the target first proves its exact local binary,
PID, Computer generation, and accepted Workspace binding set. That local
proof completes the handoff. The successor then heartbeats, registers
runtimes, connects its WebSocket, and notifies the server that the upgrade
completed. Heartbeat and register claim the new Computer generation. There
is no predecessor-to-candidate cloud CAS. Runtime registration is recovery
and convergence evidence; Runtime cardinality is not Computer takeover
identity. A candidate rejected before the local proof cannot fence the
incumbent and requires no remote rollback.

## CLI

```bash
multica runtime list --output json
multica runtime usage <runtime-id> --output json
multica runtime activity <runtime-id> --output json
multica computer upgrade --target-version <version>
```

`computer upgrade` is the only local upgrade command. It first checks the
machine-wide resident. With a live resident, the CLI uses the saved human
session to create the canonical server Machine Upgrade operation, and the
server dispatches that operation over the current DaemonCore WebSocket.
Computer Host remains the sole owner of download, verification, handoff,
rollback, and convergence. By default the command waits, renders those real
Host phases, and finishes only after the successor is running and its prior
Workspace connections have recovered. `--no-wait` returns after submission for
callers that will monitor separately with `multica computer status`. Host never
uses a Workspace execution credential to create a human operation. If no
resident owns the machine, the command may swap the on-PATH Computer
(`$HOME/.local/bin/multica`) under the machine lock; that offline result is not
proof of a running successor. Held resident ownership with unavailable control
returns `upgrade_service_unreachable` and never falls back to offline
activation. Omit `--target-version` to use the package selected by the active
production or test environment. There is no top-level `multica update` command.

Computer owners can perform this action. A Workspace owner/admin does not gain
lifecycle control over another person's Computer; the initiating Workspace is
only an entry point, and every active Workspace connection observes the same
Computer upgrade. The Computer's active Workspace Binding is the ownership and
dispatch authority; Agent Runtime cardinality, pinning, and provider launch
metadata do not gate the machine-wide Computer lifecycle.
Upgrade changes are projected to those Workspaces as `computer:updated`; the
event carries only `computer_id`, and clients refetch their Workspace-scoped
Computer projection. It is not a Runtime update event.

The resident Computer is machine-wide: it runs as one detached process and is
controlled by the Computer lifecycle, not an OS supervisor:

```bash
multica computer start      # run the machine-wide resident detached
multica computer stop       # stop it and all WorkspaceDaemon children
multica computer restart    # stop + start
multica computer status     # read-only status (identity, resident, Workspace connections)
multica computer logs       # tail the resident service log
multica computer doctor     # read-only diagnostics (--fix only clears a confirmed-stopped stale PID)
```

Stop first requests graceful resident shutdown. If local control fails, its
forced fallback terminates the resident and every persisted WorkspaceDaemon
child; it reports an error rather than claiming success while a child remains.
Interactive lifecycle commands render colored spinners and concise completion
summaries; redirected output and CI automatically use stable plain-text lines.

Computer identity metadata (device name, OS, and CLI version) belongs to the
Computer connection and remains visible even when no provider Runtime is
installed.

The service environment determines the package source: production uses stable
packages, while test uses preview packages. Production uses `https://www.leagent.me`
for the app and `https://api.leagent.me` for API/auth/WebSocket. Test requires
explicit Web and API HTTP(S) origins. They may currently use the same IP or
`test.leagent.me`, but they are stored separately so a later split needs no CLI
contract change:

```bash
multica setup /my-workspace
multica setup --environment test --server-url https://test.leagent.me --app-url https://test.leagent.me /my-workspace
multica config use test
multica config use production
```

On `multica setup`, `--server-url` and `--app-url` are the supported Test
environment origins. They are not the retired lifecycle URL override:
`multica computer start|restart` still reject a command-local `--server-url`,
while first-time Test setup must accept both explicit origins together. A later
repair setup may omit them and reuse the saved Test origins. If setup targets a
different environment from the active one, it asks before changing config or
restarting; `--yes` is reserved for automation that already accepted the switch.
A successful setup activates the target environment, establishes the selected
Workspace connection, and ensures the resident is running, so `config use` is
not an extra setup step. Repeating setup for the same Computer and Workspace
repairs the existing connection and completes without restarting a healthy,
matching resident or creating a duplicate.

Workspace connections are keyed locally by `(environment, workspace_id)`, so
the same Computer can retain connections from both databases. One resident
generation serves only the currently selected environment. `config use`
stages the matching package, then prompts before immediately restarting a
running resident because current work may be interrupted. Use `--yes` only
when that interruption has already been explicitly accepted by automation.

`multica daemon ...` is a hidden, deprecated compatibility alias that delegates
to the same machine-wide Computer; use `multica computer ...` instead.


## Debugging an Agent that did not run

1. Confirm a task was supposed to be created.
2. Confirm the assignee is an active Agent or a supported routing target.
3. Check the runtime with `multica runtime list --output json`.
4. Check the Computer heartbeat (`last_seen_at`).
5. Determine whether the inbox event is pending, leased, running, or terminal.

More source-backed details: `references/runtimes-source-map.md`.
