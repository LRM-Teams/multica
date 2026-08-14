# Agent Start Dispatch Contract

Status: accepted design, implementation in progress (2026-08-12)

This document defines the server-to-Computer protocol for starting a managed
Agent. It is the source of truth for setup, reconnect, Computer restart, Agent
restart, and Runtime replacement. Those entry points must converge through the
same lifecycle module rather than implement their own start behavior.

## 1. Ownership and terminology

- **Computer** is the machine-scoped product entity. `computer_id` identifies
  it across process and WebSocket restarts.
- **Computer process** is one running local process and has its own process or
  connection identity. It is not the Computer identity.
- **Runtime** is the selected provider configuration for an Agent. A Runtime is
  assigned to a Computer by `computer_id`.
- **Launch** is one desired Agent residency epoch on one Runtime.
- **Start dispatch** is one idempotent `agent:start` command acceptance attempt.
- **APM** is the Computer-local Agent Process Manager. It owns acceptance,
  queuing, process admission, and managed residency.

The protocol requires that an APM-queued start is accepted and acknowledged.
The scheduling policy behind that queue is Computer-local. Raft Computer
1.0.15 directly demonstrates concurrent-start scheduling; Multica additionally
has a `MaxAgentProcesses` resident-process policy. That Multica resource policy
is not part of the wire contract and is not claimed to be a Raft equivalent.

New protocol, domain, log, and schema names must use `computer_id`. Existing
storage named `daemon_id` is a legacy adapter detail and must not be exposed as
the lifecycle module's terminology or copied into new schema.

## 2. Two independent identities

Every `agent:start` command carries both identities:

```text
agent:start {
  agentId,
  runtimeId,
  launchId,
  startDispatchId
}
```

`launchId` identifies the Agent lifecycle. Status, session, Activity, Message
acceptance, and stop reports are fenced by the current Launch.

`startDispatchId` identifies the start command. It pairs one command with its
`agent:start:ack` receipt and makes reconnect delivery idempotent.

They are deliberately not aliases:

- retrying the same command preserves both IDs;
- a new start command receives a new `startDispatchId`;
- Runtime replacement receives a new `launchId` and a new `startDispatchId`;
- missing IDs are invalid at the wire and APM interfaces;
- neither ID may be synthesized from the other as a fallback.

The server generates and durably stores both IDs before dispatch. The Computer
must never invent either server identity.

## 3. Command acceptance and acknowledgement

The required ordering is:

```text
Server desired state        Computer / APM              Provider
        |                         |                         |
        | agent:start (L1, D1)    |                         |
        |------------------------>|                         |
        |                         | accept or queue          |
        | agent:start:ack (L1,D1) |                         |
        |<------------------------|                         |
        |                         | start process ---------->|
        |                         |                         |
        | agent:status/session/activity                     |
        |<--------------------------------------------------|
```

`agent:start:ack` means only that the APM accepted or queued the exact command.
It does not prove that a process spawned, the provider became ready, a session
exists, or a Message was delivered.

The acknowledgement echoes:

```text
agent:start:ack {
  agentId,
  launchId,
  startDispatchId,
  queueState,
  queueDepth,
  queueAgeMs
}
```

The server accepts an acknowledgement only when Agent, Runtime, Computer,
Launch, and start dispatch still match current desired state. A stale receipt
must fail closed and must not make the Agent appear online.

## 4. Idempotent retry

The Computer keeps two bounded start-dispatch projections:

- accepting dispatches: commands currently crossing the APM acceptance seam;
- accepted dispatches: completed acceptance receipts available for replay.

For an identical `startDispatchId`:

- a concurrent duplicate waits for the first acceptance and returns the same
  receipt;
- a later duplicate returns the cached receipt;
- neither duplicate invokes APM start again;
- a duplicate with conflicting Agent, Runtime, or Launch facts is rejected and
  logged as a protocol identity conflict.

Receipt retention is bounded. Eviction may permit an old command to cross the
APM seam again, so APM start must also be idempotent for the current Launch.
Eviction must never authorize a stale Launch or Runtime replacement.

WebSocket reconnect retransmits the current unacknowledged command with the
same IDs. Reconnect is not a reason to generate a new dispatch or reset Agent
freshness.

## 5. Desired-state reconciliation

The server owns desired placement. A Workspace Runner connection reports the
Computer and workspace; it does not provide an allow-list of Runtime IDs for
Agent lifecycle dispatch.

Desired launches are selected by:

```text
desired workspace
  + Runtime assigned to the connected computer_id
  + current Agent placement
```

An attachment or historical Runtime list on the WebSocket identity must not
silently filter `agent:start`. Attachment reconciliation and Agent lifecycle
reconciliation are separate paths.

Setup, reconnect, Computer process restart, Agent creation, lifecycle start,
and Runtime update call the same desired-versus-observed reconciliation module.
The server lifecycle orchestrator owns stop/reset/start ordering; HTTP callers
and the Runner never construct a composite sequence.

## 6. Runtime replacement is two phase

The server must not send stop-old and start-new as an unordered batch.

```text
agent:stop(oldLaunch)
        |
        | agent:status inactive(oldLaunch)
        v
agent:start(newLaunch, newStartDispatch, newRuntime)
```

The old Launch remains authoritative until the current Computer reports it
inactive. Only the next reconciliation may dispatch the new Runtime. Stale
inactive reports cannot unlock a newer transition.

Changing provider configuration without changing Runtime identity must still
have an explicit version/generation fence before it can trigger a new command;
mutable configuration must never alter the meaning of an already accepted
start dispatch.

## 7. Message acceptance

Messages received before APM acceptance remain buffered for that Agent and are
not acknowledged as accepted by the Agent.

After APM acceptance, the corresponding Agent queue may accept Messages even
while local start scheduling, process admission, or provider startup is pending.
A Message delivery ACK
means the Agent's APM-owned buffer accepted it, not that the provider consumed
it. Provider-visible consumption remains a separate cursor/freshness fact.

No connection, attachment, or Runtime update may reset consumption state for
other Agents.

## 8. Observability

Every lifecycle log or trace at this seam includes, when applicable:

- `computer_id`
- `workspace_id`
- `agent_id`
- `runtime_id`
- `launch_id`
- `start_dispatch_id`
- `queue_state`
- outcome: `accepted`, `duplicate`, `rejected`, or `failed`

Provider startup and process failure logs retain the same Launch and start
dispatch identities so an incident can distinguish command delivery, APM
acceptance, process spawn, provider readiness, and Message consumption.

Logs must not use `daemon_id` for a Computer identity. A legacy column name may
appear only in a storage-adapter diagnostic that also emits `computer_id` as the
domain field.

## 9. Explicit non-goals and prohibited designs

- Do not restore an `agent_start_intent` polling protocol. The wire command is
  `agent:start`; durable desired launch/dispatch projection is its server state.
- Do not use Attachment `correlation_id` as start command identity.
- Do not use `launchId` as an implicit fallback for `startDispatchId`.
- Do not ACK at WebSocket receipt, before APM acceptance.
- Do not claim Runtime/provider readiness from `agent:start:ack`.
- Do not generate a fresh dispatch merely because a WebSocket reconnects.
- Do not start a new Runtime before the old Launch is inactive.
- Do not let a legacy `daemon_id` name define the domain model.

## 10. Verification required before executable status

The contract becomes executable only after tests have been observed failing for
each entry point and then passing with the shared implementation:

1. first setup dispatches without a Runtime allow-list on the connection;
2. reconnect retransmits the same unacknowledged dispatch;
3. concurrent and completed duplicate dispatches return the same ACK;
4. ACK precedes provider readiness but follows APM acceptance;
5. conflicting reuse of a dispatch ID fails closed;
6. Runtime replacement sends stop, waits for matching inactive, then starts;
7. stale inactive and stale ACK reports cannot advance desired state;
8. queued Agent Messages are buffered and ACKed only after APM acceptance;
9. lifecycle logs correlate Computer, Agent, Runtime, Launch, and dispatch;
10. migration removes the `agent_start_intent` protocol without mixed-version
    deployment reading a dropped table.

## 11. Raft alignment evidence

The accepted behavior was checked against `@botiverse/raft-daemon@1.0.16` on
2026-08-14. The artifact directly verifies:

- separate `launchId` and `startDispatchId` fields;
- in-flight and accepted dispatch deduplication;
- bounded accepted-receipt caching (1024 entries);
- `agent:start:ack` after APM acceptance and before provider startup completes;
- correlation of both IDs in lifecycle traces;
- buffering of deliveries received while start acceptance is in progress.
- `agent:stop`, `agent:reset-workspace`, and `agent:start` are separate input
  messages, with no composite `agent:lifecycle` message;
- `agent:start.config.sessionId` is the provider resume/fresh-session boundary.

The Raft server source was not available in that artifact. Exact server-side ID
generation and retention policy is therefore a Multica contract above, not a
claim that the Raft server implementation was directly inspected.
