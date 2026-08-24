# Agent Message Delivery Contract

Status: implemented

## Evidence boundary

The no-process / ACK decision table below was verified from the published
`@botiverse/raft-daemon@1.0.16` `deliverMessage` implementation. Older 1.0.15
Computer notes still apply to reconnect redelivery and ACK envelope shape. The
private Raft Server storage implementation was not available, so this document
does not claim its table shape or retry scheduler as observed fact.

Verified Raft Computer behavior:

- the wire path is `agent:deliver` followed by `agent:deliver:ack`;
- a delivery received while core Agent start is in progress is buffered locally
  without an acknowledgement;
- after start succeeds, one buffered non-transient delivery may become the wake
  Message and is acknowledged only after that start succeeds; remaining
  deliveries re-enter the ordinary delivery path;
- the Computer acknowledges only when `deliverMessage` resolves `accepted=true`;
- `accepted=true` is local responsibility, not process liveness. Already
  consumed, starting/queued, terminal, idle-snapshot restore, and spawn-cooldown
  deliveries accept without a live provider process;
- a rejected or failed provider input on a *running* launch is not acknowledged;
  the server retains delivery responsibility and may retry the same `deliveryId`
  and sequence;
- APM acceptance includes durable local responsibility such as a starting,
  idle, busy, or already-consumed inbox state. It is not provider-turn
  completion, a read receipt, or Context Boundary advancement;
- the acknowledgement echoes the delivery's `agentId`, `deliveryId`, and
  positive outer `seq` (with an old-message fallback to the nested Message
  sequence).

### Computer accept table (Raft 1.0.16)

| State | ACK | Pending | Side effect |
|---|---|---|---|
| already consumed | yes | no | drop |
| starting or queued | yes | yes | no provider delivery |
| terminal failure | yes | yes | publish error |
| idle snapshot | yes | yes | restore original launch |
| spawn cooldown | yes | yes | do not restart yet |
| no process, no snapshot | no | no | inactive + `runtime_unavailable` |
| live + busy | yes | yes | Notice, no body delivery |
| live + idle | yes | no after delivery | body delivery |
| live + provider reject | no | yes | Server retries |

### Forbidden

- Do not NACK solely because APM `Snapshot` is missing.
- Do not ACK unless the coordinator still owns the body (Pending), the target
  is already context-covered, or an idle restore of the original launch is in
  flight.
- Do not invent a `startDispatchId` for idle restore. Restore is Computer-local
  and reuses the last server launch identity.
- Do not collapse those outcomes into one `has not been accepted by APM` error.

## Multica contract

`agent_message_delivery` is the Server's durable at-least-once responsibility
ledger. One row identifies a canonical Message selected for one Agent and
retains its original target sequence. `acked_at IS NULL` means the Server is
still responsible for delivery.

When a Workspace Runner becomes ready, the Server reconstructs and sends every
unacknowledged delivery for that Computer and Workspace, ordered by the original
Message sequence. This is independent of `agent:start:ack`: deliveries may
arrive before or during Agent start, and the Computer/APM owns the corresponding
local buffer.

The Computer sends `agent:deliver:ack` only after the accept table above says
the Computer still owns the Delivery. Missing APM launch is not by itself a
NACK. The Server accepts an ACK only when
`workspaceId`, `agentId`, canonical `deliveryId`, and `seq` match the persisted
row. A matching ACK durably sets `acked_at`; subsequent Runner reconnects do not
redeliver it.

Identity and ordering have separate jobs:

- `message_id` identifies the canonical Message;
- `delivery_id` identifies delivery to one Agent and correlates retries/ACKs;
- `target + seq` orders context and powers the Computer's process-local
  Context Boundary.

The process-local Context Boundary remains necessary. Server ACK state prevents
loss; the target sequence boundary prevents a retry from entering the Provider
twice within one daemon process and records actual context coverage. ACK never
advances that boundary. A daemon restart clears the boundary and may
conservatively replay context.

Provider failure and lifecycle presentation follow the verified Raft phases:

- failure of the outer `agent:start` path publishes `agent:status=inactive`
  and `agent:activity=offline` with a safe detail;
- failure after a provider process exists (including its startup request or
  first-input authentication) publishes `agent:activity=error` with
  `detailKind=runtime_error`, terminates the unusable process, and publishes
  `agent:status=inactive`;
- neither failure consumes Pending Message bodies. A later successful start can
  accept the original delivery exactly once.

Ordinary Message recovery has no snapshot side channel. The retired
`agent:recovery:request` and `agent:recovery:page` events must not be reintroduced.
Reminder snapshots remain an independent persistent wakeup protocol.

`computer_id` is the domain name for placement. Existing `daemon_id` fields are
legacy storage/auth adapters and must not be copied into new protocol names,
module interfaces, or logs.

## Executable enforcement

- protocol types contain `agent:deliver` and `agent:deliver:ack` but no ordinary
  Message recovery events;
- migration `340_agent_message_delivery_ack` adds durable ACK state and the
  unacknowledged-delivery index;
- `TestWorkspaceDaemonReadyRedeliversUnacknowledgedMessagesInSequenceOrder`
  proves reconnect redelivery preserves sequence;
- `TestAgentDeliveryAcknowledgementRequiresExactSequenceAndStopsRedelivery`
  proves wrong-sequence rejection, durable ACK, and the no-redelivery control.
- `TestWorkspaceDaemonDeliveryDoesNotAcknowledgeProviderRejection` proves a
  provider rejection is retained without ACK and succeeds exactly once after a
  later retry;
- `TestWorkspaceDaemonProviderSpawnFailureReportsInactiveAndOffline` and
  `TestIdleMessageAcceptanceFailurePublishesVisibleErrorActivity` enforce the
  two Raft failure phases;
- `requiredDeliveryRouteTests` plus
  `TestAcceptMessageDeliveryForbidsUnmanagedEarlyNack` freeze the 1.0.16 accept
  table. Changing `acceptMessageDelivery` without those tests is a contract
  break. Run `make test-agent-delivery-route`.
