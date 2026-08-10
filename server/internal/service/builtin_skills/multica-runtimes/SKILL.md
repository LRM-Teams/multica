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

Machine Upgrade is Computer-scoped. Internally it uses the canonical daemon
upgrade API; installed clients may still call legacy runtime-scoped HTTP update
paths. Those are compatibility adapters over the same Computer operation and do
not create runtime-owned update state.

## CLI

```bash
multica runtime list --output json
multica runtime usage <runtime-id> --output json
multica runtime activity <runtime-id> --output json
multica computer upgrade --target-version <version> --output json
```

`computer upgrade` uses the one machine-wide Computer identity and creates or
polls the canonical machine-upgrade operation. Omit `--target-version` to use
the package selected by the active production or test environment. Computer owners
and workspace owners/admins can perform this action.

The resident Computer is machine-wide: it runs as one detached process and is
controlled by the Computer lifecycle, not an OS supervisor:

```bash
multica computer start      # run the machine-wide resident detached
multica computer stop       # stop it gracefully
multica computer restart    # stop + start
multica computer status     # read-only status (identity, resident, Workspace connections)
multica computer logs       # tail the resident service log
multica computer doctor     # read-only diagnostics (--fix only clears a confirmed-stopped stale PID)
```

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
