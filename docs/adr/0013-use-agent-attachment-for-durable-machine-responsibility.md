---
status: accepted
---

# Use Agent Attachment for durable machine responsibility

An Agent Attachment is the durable fact that one Machine Service is responsible
for one Workspace Agent through one Runtime. It is not a process, launch,
session, Activity, Message coordinator, or Reminder identity. Attaching an
Agent therefore does not start a provider process, while detaching it does not
delete the Agent Root, Inbox state, Message Drafts, or other durable Agent data.

## Decision

The machine-wide `AgentAttachmentRegistry` is the only implementation that may
compare Attachment generations, retain detach tombstones, advance lifecycle
replay cursors, reconcile Runtime sets, or persist those facts. Callers receive
semantic `attached`, `moved`, `detached`, or `unchanged` results and must not
inspect registry maps or reproduce generation comparisons.

Every public operation has a fixed authenticated Workspace scope. Attachment
events deliberately omit `WorkspaceID`; a payload cannot select or change its
own authority boundary. Recovery reads and Runtime reconciliation accept an
explicit set of Runtime IDs for that Workspace. Runtime disappearance may
detach an Agent from this machine, but reconciliation never guesses a
replacement Runtime or silently moves the Agent.

`AttachmentGeneration` is a per-Agent fencing token. A lower generation never
changes current state. Reapplying the same generation to the same Attachment or
detach is idempotent, and the same generation cannot replace a current
Attachment with conflicting Workspace or Runtime identity. When a move is
delivered as `detach A(gen=N)` followed by `attach B(gen=N)`, the attach is
accepted because there is no conflicting current Attachment; its per-Runtime
lifecycle cursor prevents an already-consumed same-Runtime attach from replaying
after the detach. A higher attach may create or move the Attachment. A detach
removes only the matching current Runtime and records the generation, so lower
generation attach or detach frames cannot resurrect or remove newer A -> B -> A
placement.

`AttachmentLifecycleSequence` is a separate, per-Runtime replay cursor. A
formal `Apply` durably commits its semantic result or tombstone together with
the monotonic cursor. Processing a newer lifecycle event whose Attachment
generation is stale may still advance the cursor: the event was consumed even
though the generation fence correctly left Attachment state unchanged. A
duplicate sequence never re-applies state. Recovery returns the stored cursor,
or zero when that Runtime has no durable cursor, so the server sends only the
missing suffix.

## Migration compatibility

The first extraction continues reading and writing the existing
`.daemon/reminder_agents.json` path and JSON field names. There is one durable
state, not a new Attachment file plus a Reminder file. The obsolete
`reminderAgentManager` facade is removed. Legacy `daemon:agent_start`,
`daemon:agent_stop`, and replay-end frames remain supported by a protocol
translation adapter routed directly to the registry. The adapter retains the
existing acceptance results and cursor behavior but does not maintain parallel
generation or tombstone maps.

A legacy live frame may omit `lifecycle_seq`; sequence zero is accepted only by
the legacy translation adapter and applies no invented cursor. A legacy replay
frame with a positive sequence uses the registry Apply core and commits that
Runtime cursor atomically with its Attachment result. The existing replay-end
ACK remains on the wire and monotonically advances any cursor not already
committed, preserving mixed-version Server behavior while making a partially
applied replay resumable from its durable prefix.

Local credential bootstrap remains available for an upgraded daemon. A
bootstrapped or task-observed generation-zero record is provisional and cannot
be returned by Workspace-scoped `Resolve` or authorize Inbox restart recovery.
A generation-bearing lifecycle event establishes the formal Attachment.
Tombstones always win over stale credential directories.

Restart-time Message Delivery carries the authenticated Workspace Runner scope
separately from its Message envelope. Recreating the local Inbox coordinator
requires `Resolve(workspaceID, agentID)` and a Runtime owned by that same fixed
Workspace. Runtime and Agent Root are derived only after those checks; an Agent
attached in another Workspace or detached from this machine is rejected before
ACK.

## Observability

Structured logs classify Attachment transitions and fences with bounded fields:
Workspace, Agent, Runtime, event kind, generation, lifecycle sequence, semantic
outcome, and a stable reason code. Logs never include credentials, Agent file
contents, Message or Draft bodies, unrestricted command arguments, or provider
output. Persistence failure is an error outcome and rolls back both Attachment
state and its cursor before returning.

## Consequences

- Reminder projection and Message recovery may consume Attachment state but do
  not define placement identity.
- Process start/stop remains an explicit, independent Manager operation.
- Workspace-scoped resolution cannot return an Agent attached in another
  Workspace.
- Mixed old/new lifecycle frames converge through one registry core during the
  migration; removing the protocol adapter is a later wire cutover.
