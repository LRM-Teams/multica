---
name: multica-runtimes
description: "Use when inspecting or debugging Multica runtimes, daemon task claiming, or an agent that did not run. Covers runtime online state, heartbeat, claim, lease, and safe diagnostic commands."
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

A runtime is the execution target behind an Agent. A daemon owns local runtime
processes and leases pending `agent_inbox_event` rows from the server.

The chain is:

1. a user action creates or updates an `agent_inbox_event`;
2. the event targets an Agent and runtime;
3. the server wakes the runtime over the daemon websocket when possible;
4. the daemon drains and leases the canonical inbox event;
5. the daemon starts the provider CLI in the Agent's durable workspace;
6. the daemon reports completion.

Machine Upgrade is daemon-scoped: use the canonical daemon upgrade API for new
work. Installed clients may still call the legacy runtime-scoped HTTP update
paths; those are compatibility adapters over the same daemon operation and do
not create runtime-owned update state.

## CLI

```bash
multica runtime list --output json
multica runtime usage <runtime-id> --output json
multica runtime activity <runtime-id> --output json
```


## Debugging an Agent that did not run

1. Confirm a task was supposed to be created.
2. Confirm the assignee is an active Agent or a supported routing target.
3. Check the runtime with `multica runtime list --output json`.
4. Check the daemon heartbeat (`last_seen_at`).
5. Determine whether the inbox event is pending, leased, running, or terminal.

More source-backed details: `references/runtimes-source-map.md`.
