# Agent Message Delivery Contract

Status: implemented

## Evidence boundary

The Raft Computer behavior in this document was verified from the locally
installed `raft-computer` 1.0.15 artifact. The private Raft Server storage
implementation was not available, so this document does not claim its table
shape or retry scheduler as observed fact.

Verified Raft Computer behavior:

- the wire path is `agent:deliver` followed by `agent:deliver:ack`;
- a delivery received while core Agent start is in progress is buffered locally
  without an acknowledgement;
- after start succeeds, one buffered non-transient delivery may become the wake
  Message and is acknowledged only after that start succeeds; remaining
  deliveries re-enter the ordinary delivery path;
- the Computer acknowledges only when `AgentProcessManager.deliverMessage`
  resolves `accepted=true`;
- APM acceptance includes durable local responsibility such as a starting,
  idle, busy, or already-consumed inbox state. It is not provider-turn
  completion, a read receipt, or Context Boundary advancement;
- the acknowledgement echoes the delivery's `agentId`, `deliveryId`, and
  positive outer `seq` (with an old-message fallback to the nested Message
  sequence).

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

The Computer sends `agent:deliver:ack` only after its APM accepts responsibility
or proves the delivery was already consumed. The Server accepts an ACK only when
`workspaceId`, `agentId`, canonical `deliveryId`, and `seq` match the persisted
row. A matching ACK durably sets `acked_at`; subsequent Runner reconnects do not
redeliver it.

Identity and ordering have separate jobs:

- `message_id` identifies the canonical Message;
- `delivery_id` identifies delivery to one Agent and correlates retries/ACKs;
- `target + seq` orders context and powers the Computer's durable
  `consumed-seqs.json` boundary.

The local Context Boundary remains necessary. Server ACK state prevents loss;
the local target sequence boundary prevents an accepted retry from entering the
Provider twice and records actual context coverage. ACK never advances that
boundary.

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
- `TestWorkspaceRunnerReadyRedeliversUnacknowledgedMessagesInSequenceOrder`
  proves reconnect redelivery preserves sequence;
- `TestAgentDeliveryAcknowledgementRequiresExactSequenceAndStopsRedelivery`
  proves wrong-sequence rejection, durable ACK, and the no-redelivery control.
