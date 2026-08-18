---
status: accepted
---

# Use a Raft-style direct Message delivery lifecycle

The Agent message path is a direct
`Message -> Delivery -> Pending -> Context-covered` lifecycle. `Message` is the
only canonical communication fact and remains on the service. `Delivery` is an
at-least-once machine transfer, `Pending` is a rebuildable local coordinator
projection, and Context-covered means that Message bodies crossed an explicit
Agent context boundary. The protocol does not introduce an Inbox Event, Task,
lease, or Agent Execution identity between these facts.

This is a hard cut. The previous `agent_inbox_event`, task-shaped daemon
envelope, lease, and `agent_execution` coupling are not retained as transition
storage, compatibility APIs, dual writes, or replay fallbacks. `message_id` is
canonical identity, target-scoped `seq` orders context, and `delivery_id`
deduplicates transport attempts. A Delivery acknowledgement means only that the
local coordinator accepted the transfer. The service may replay from canonical
Message history after reconnect or local state loss.

The machine wire names align exactly with Raft: the service sends
`agent:deliver`, and the machine returns `agent:deliver:ack`. The acknowledgement
envelope contains `type`, `agentId`, `seq`, and `deliveryId`, plus optional
`traceparent`; it does not contain a read state, Context Boundary, runtime turn,
or execution identifier. `agent:deliver:ack` is emitted only after the
coordinator accepts the Delivery into its live Pending/start buffer or recognizes
the same Delivery as already accepted. It is a transport receipt, never a
Message-read or context-covered receipt.

## Machine-local responsibilities

One long-running coordinator is scoped by Workspace and Agent. It owns Pending,
target Context Boundaries, Delivery deduplication, and content-free coalesced
Notices. A busy Agent retains new Messages as Pending; a Notice neither advances
a boundary nor produces Activity. Body delivery occurs at runtime startup, a
successful idle or gated runtime input, explicit `message check` or `message
read`, or freshness-hold context.

Target Context Boundaries live only in daemon process memory, matching Raft's
`AgentVisibleDeliveryLedger`. Pending Message bodies are never copied into a
machine-local durable ledger. During one daemon lifetime, the boundary prevents
an accepted retry from entering the Provider twice. After daemon restart the
ledger is empty; the Server's durable unacknowledged Delivery state rebuilds it
through conservative at-least-once replay. A provider session ID may still be
restored independently so the new provider process can resume its prior
session.

A boundary advances only after the corresponding body delivery or explicit
read succeeds, never before it. It is not a public cursor and the Server does
not persist it as `seen_up_to_seq`.

Agent CLI coverage is two-phase. The coordinator prepares an opaque,
machine-local receipt for the exact `message check`, `message read`, or
freshness-hold context represented by one response without changing Pending or
the Context Boundary. The CLI removes that receipt from visible output, writes
the response, and only then commits the receipt through its launch-scoped Agent
Proxy credential. Output failure makes no commit; commit failure after output is
reported explicitly and may replay context. A freshness-hold receipt covers the
complete held Pending range even when the response shows only the newest three
Messages.

While recovery is incomplete or failed, the coordinator may accept and merge
new `agent:deliver` traffic but must not claim that inbox freshness is known.
Agent sends whose correctness depends on freshness fail closed into the normal
hold/error path; they never assume that an empty in-memory Pending set means no
new canonical Messages. An `agent:deliver:ack` sent before a crash therefore
cannot permanently hide a Message: recovery is based on Context Boundary, not
on the Delivery acknowledgement.

The Credential Proxy injects service credentials and the internally computed
`seenUpToSeq`, performs a local freshness preflight, forwards requests, and
consumes server `heldMessages`. `seenUpToSeq` is an internal proxy-to-service
field required for the final commit race check. There is no Agent-facing cursor,
`--seen` flag, or public cursor API.

Domain and control events are not Notices. Membership removal, Binding
revocation, and similar canonical service events apply immediately; when a
change must appear in conversation history, the service creates a system
Message that follows ordinary delivery. Removing the current Agent uses an
immediate purge or revoke path rather than waiting for the Agent to read a
Message.

## Busy Notice contract

When a runtime is busy, the coordinator waits three seconds to coalesce new
Pending Messages and writes one content-free Notice into the current runtime
session. Its header contains the total Pending Message count and the number of
targets changed by that batch. Each changed-target row may contain the target's
Pending count, first and latest short Message IDs, latest sender, mention flags,
and an optional server-derived attention hint. It never contains Message text,
Message Parts, attachment metadata, or attachment bytes.

A runtime-session fingerprint of the represented Pending set suppresses
duplicate Notices when nothing changed. A failed runtime write retains
notification debt and retries after fifteen seconds. Compaction, review,
runtime backoff, and a session that is not ready defer the Notice without
discarding debt. When the runtime is idle, the coordinator delivers Message
bodies instead of a Notice. Successful Notice delivery does not consume
Pending, advance a Context Boundary, or emit `Message received` Activity.

## Context and Activity semantics

`Message received` is the user-facing Activity label for a best-effort
observation, not an internal event-name contract or state transition. Internal
enums, trace keys, and wire events may keep implementation-appropriate names.
The UI label appears once per daemon-to-runtime Message body delivery batch: one
for startup context, one for an idle runtime input batch, or one for a gated
busy flush. It does not appear for a Notice. Runtime tool activity remains a
separate projection: `message check` shows `Checking messages`, `message read`
shows `Reading history`, and `message search` shows `Searching messages`. A
freshness hold shows `Send held by freshness check`. Activity upload failure
never blocks delivery, context coverage, or a reply.

Freshness-hold context is bounded to the newest three Messages. Counts describe
all newer Messages while `heldMessages` includes at most three; the local
Context Boundary advances through the maximum sequence of the full Pending set.
Omitted bodies remain canonical on the service and are recovered explicitly
with `message read`.

`message check`, `message read`, runtime body delivery, and non-withheld held
context advance the Context Boundary. `message search`, `message resolve`,
`message react`, Notices, and Delivery acknowledgements do not.

## Draft and send semantics

Before a normal send reaches the network, the CLI saves one local Draft keyed by
Workspace, Agent, and target in:

`~/.multica/workspaces/<workspace_id>/agents/<agent_id>/continue-state.json`

The Draft contains `content`, ordered attachment IDs, save time, hold count, a
stable internal `idempotencyKey`, and the internal context boundary needed by
the send protocol. It expires ten minutes after its latest save or hold. A
normal send replaces the target's prior Draft and creates a new idempotency key;
a confirmed successful send clears it; a local or server freshness hold keeps
and updates it without changing the key. No failure or hold automatically
resends it.

`--send-draft` explicitly sends the saved Draft and accepts neither stdin nor
new `--attachment-id` values. `--anyway` is valid only with `--send-draft` and is
the explicit escape hatch after repeated freshness holds. The Credential Proxy
generates `idempotencyKey` before the first upstream attempt and reuses it for
explicit Draft sends. The service maps it to the canonical Message insertion
deduplication key: replay with the same target, content, and attachments returns
the original Message, while reuse with different send intent is a conflict.
Consequently, an upstream response lost after commit cannot create a duplicate
Message when the Draft is explicitly replayed.

The initial implementation does not add a per-target send lock, persistent send
queue, or `SEND_IN_PROGRESS` behavior beyond Raft. The Credential Proxy keeps
an observation-only in-memory count of active sends. When requests overlap for
the same Agent or target, it emits a structured `agent_message_send_overlap`
warning with canonical Workspace, Agent, target, request correlation, overlap
count, and duration fields, but never Message content, attachment names, or
credentials. Overlap observation does not change request ordering or outcome;
serialization is reconsidered only from measured evidence.

The overlap log uses `phase=start|finish` and records `workspace_id`,
`agent_id`, canonical target identifiers, `request_id`, overlapping request
IDs, Agent- and target-scoped active counts, `duration_ms`, terminal outcome,
and Draft action (`kept`, `cleared`, `replaced`, or `unchanged`). IDs remain
structured-log fields and never become metric labels. These fields must be
sufficient to correlate an observed overlap with Draft loss or replacement and
place a later per-target serialization seam without reconstructing Message
content.

## Agent command contract

The canonical Message commands are `send`, `check`, `read`, `search`, `resolve`,
and `react`. There are no compatibility aliases, `ask-choice`, `a2a-control`, or
top-level `react` command.

- `message send` requires `--target` and a non-empty stdin body. It supports
  repeatable `--attachment-id`, `--send-draft`, `--anyway`, and `--json`. It has
  no positional body, `--message`, `--message-stdin`, `--message-file`, `--seen`,
  or `--output` selector. The existing public `--client-message-id` flag is
  deleted, and no public `--idempotency-key` flag is introduced. Attachments
  cannot be sent without a text body.
- `message check` has no target and non-blockingly drains Pending Messages.
- `message read` requires `--target` and supports `--before`, `--after`,
  `--around`, and `--limit` against canonical history.
- `message search` supports query and target/sender/time/sort/pagination filters
  but does not consume inbox context.
- `message resolve <id>` performs an exact canonical Message get by full or
  uniquely resolved short ID; ambiguity is an error.
- `message react` requires `--message-id` and `--emoji`, supports `--remove`, and
  needs no target because Message identity locates the conversation.

Human-readable text is the default command output. Only commands whose Raft
contract defines `--json` expose that boolean flag; there is no generic
`--output json|text`. `attachment view <id> --output <path>` retains `--output`
because it names a filesystem destination rather than an output format.

## Attachment boundary

The upload pipeline aligns with Raft's capability negotiation, server-authority
size limit, Upload Session, direct presigned PUT above the configured threshold,
idempotent object creation, completion verification, retry, expiry, and cancel
semantics. `attachment upload` requires `--path` and `--target`, resolves
channel/DM/thread targets uniformly, and returns an Attachment ID.

The Agent send DTO contains `content` and ordered `attachmentIds`. The
Credential Proxy treats them as opaque Draft data. The service alone validates
Attachment ownership and completed state, constructs canonical text and
attachment Message Parts, persists the Message, and binds its Attachment
references atomically. The Agent endpoint does not also accept caller-supplied
Parts, so request DTO and canonical Message representation cannot conflict.

## Considered options

- Retaining `agent_inbox_event` as an internal bridge was rejected because it
  preserves two identities and makes acknowledgement, retry, and visibility
  depend on the obsolete task lifecycle.
- Binding Delivery to `agent_execution` was rejected because Messages can be
  batched into one runtime turn, one Message can contribute to later turns, and
  a freshness hold can provide context without creating an execution aggregate.
- Persisting Pending as a second local Message ledger was rejected because it
  creates two canonical truths; the service already provides recovery history.
- Constructing Message Parts in the Credential Proxy was rejected because the
  proxy owns credentials and freshness, while the service owns Message and
  Attachment domain validation.

## Consequences

- Transport deduplication uses `delivery_id`; context coverage uses the local
  Context Boundary. They are separate facts.
- The Agent-facing CLI and environment expose no inbox sequence, seen cursor,
  lease token, Inbox Event ID, Task ID, or Agent Execution ID for this path.
- Product Tasks and Issues remain separate collaboration concepts and are not
  renamed or removed by this decision.
- Provider usage remains observability data outside this Message lifecycle and
  is intentionally deferred to a separate decision.
