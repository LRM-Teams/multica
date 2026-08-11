# Raft-style Direct Agent Message Lifecycle

Status: ready-for-agent

## Problem Statement

An Agent can successfully send or receive a canonical chat Message while the
machine-side inbox, runtime context, freshness gate, and frontend Activity
projection disagree about what happened. The current path couples durable inbox
rows, leases, task execution state, server-side seen cursors, runtime wakeup, and
frontend display. That creates several user-visible failure modes:

- a Message exists on the Server but is not shown promptly in chat;
- a machine acknowledges a delivery before the Message is safely available to
  the Agent coordinator, then loses it across a restart;
- an Agent is treated as having seen context merely because a transport event or
  content-free notification arrived;
- a freshness hold returns newer Messages but a later send still carries an old
  sequence boundary and is held again;
- local and server cursors become competing sources of truth;
- command compatibility paths obscure which Agent messaging contract is
  supported;
- Activity labels imply a stronger state transition than the underlying event
  proves.

The result is a chat system whose correctness depends on incidental execution
state rather than the canonical Message record. Fixing only the current read
index or hold retry would preserve the same failure-prone ownership model.

## Solution

Replace the Agent chat delivery path with a direct, Raft-style lifecycle built
around one communication truth: the Server canonical Message. A long-running
coordinator for each Workspace and Agent accepts at-least-once Deliveries,
maintains an in-memory Pending projection, gives the runtime either concrete
Message bodies or content-free Notices, and records a machine-local,
target-scoped Context Boundary only after context is actually covered.

The Server never stores an Agent-facing seen cursor. On coordinator startup and
machine reconnect, the machine sends its local Context Boundary map to an
internal recovery read. The Server pages canonical Messages beyond those
boundaries, and the coordinator merges recovery results with live Deliveries by
Message identity and target sequence. Until recovery completes, freshness is
unknown and sends that depend on it fail closed.

The Credential Proxy owns freshness checks, held-context consumption, local
Drafts, and internal idempotency. Agent commands expose the Raft-style messaging
surface without legacy aliases, cursor flags, or public idempotency controls.
Attachments are uploaded independently and referenced by ID; the Server alone
normalizes them into canonical Message Parts.

Frontend chat remains a projection of canonical Messages through the existing
realtime invalidation path. Activity is a separate best-effort narrative:
concrete runtime handoff shows `Message received`, while Agent tools show their
own stages such as `Checking messages`, `Reading history`, and `Searching
messages`.

## User Stories

1. As a human sender, I want a successfully created Message to appear from the
   canonical chat source immediately, so that machine delivery state cannot hide
   a Message I already sent.
2. As a human recipient, I want chat to refresh from canonical Message events,
   so that Agent runtime state does not control whether conversation history is
   visible.
3. As an Agent, I want concrete incoming Messages delivered with their original
   target and sequence, so that I can reply in the correct channel, DM, or
   thread.
4. As an Agent, I want delivery acknowledgements to mean only that my local
   coordinator accepted a Delivery, so that transport acceptance is not confused
   with reading or understanding a Message.
5. As an Agent, I want a busy runtime to receive a content-free Notice rather
   than injected Message bodies, so that new chat does not disrupt an unsafe
   point in my current work.
6. As an Agent, I want a Notice to tell me how many Messages and targets changed,
   so that I can decide whether to check at a natural breakpoint.
7. As an Agent, I want Notice metadata to omit bodies and attachments, so that a
   notification does not silently become context coverage.
8. As an Agent, I want repeated unchanged Notices suppressed within a runtime
   session, so that I am not distracted by duplicate notifications.
9. As an Agent, I want a failed Notice write retried without losing notification
   debt, so that a temporary runtime condition does not hide Pending work.
10. As an Agent, I want an idle runtime to receive concrete Message bodies rather
    than only a Notice, so that available work can proceed without an unnecessary
    extra check.
11. As an Agent, I want `message check` to drain a bounded window of Pending
    Messages and tell me when more remain, so that large inboxes are safe and
    explicitly repeatable.
12. As an Agent, I want `message read` to retrieve a bounded history window
    around explicit anchors, so that I can recover context omitted from a hold or
    notification.
13. As an Agent, I want `message search` to find relevant canonical Messages
    without marking them context-covered, so that discovery is not mistaken for
    reading a target.
14. As an Agent, I want `message resolve` to locate one exact canonical Message,
    so that identity lookup does not require loading nearby history.
15. As an Agent, I want `message react` to add or remove my reaction using a
    Message ID and emoji, so that reaction semantics are independent of target
    cursors.
16. As an Agent, I want my Context Boundary to advance only after concrete
    context handoff, explicit reading, or accepted held context, so that freshness
    checks never skip unseen Messages.
17. As an Agent, I want a malformed or unwritable local boundary file treated as
    unknown coverage, so that corruption can cause safe replay but never silent
    omission.
18. As an Agent, I want a coordinator restart to reconstruct Pending from
    canonical Messages, so that Message bodies do not need a second durable local
    ledger.
19. As an Agent, I want live Deliveries merged with recovery pages without
    duplicates or gaps, so that reconnect races do not lose Messages.
20. As an Agent, I want freshness-dependent sends held while recovery is
    incomplete or failed, so that an empty in-memory Pending set is not mistaken
    for current context.
21. As an Agent, I want a held send to show at most the newest three Message
    bodies while reporting the full newer count, so that the response is bounded
    without concealing that more context exists.
22. As an Agent, I want accepting held context to advance through the entire
    covered Pending range, so that omitted older bodies do not cause the same
    draft to be held repeatedly.
23. As an Agent, I want omitted held bodies to remain available through explicit
    history reading, so that bounded hold output does not destroy information.
24. As an Agent, I want a held send saved locally as a Draft instead of retried
    automatically, so that I retain control after seeing new context.
25. As an Agent, I want one Draft per target to expire after ten minutes, so that
    stale unsent intent is not retained indefinitely.
26. As an Agent, I want a normal send to replace the target Draft and an explicit
    draft send to reuse it, so that revising and replaying intent are
    unambiguous.
27. As an Agent, I want an internal idempotency identity saved before the first
    network send and reused with the Draft, so that an unknown response cannot
    create duplicate canonical Messages.
28. As an Agent, I want `--anyway` accepted only with an explicit saved-Draft
    send, so that the freshness escape hatch cannot bypass review accidentally.
29. As an Agent, I want to send a non-empty body through stdin, so that shell
    quoting and multiline content follow one predictable contract.
30. As an Agent, I want repeatable attachment IDs on a normal send, so that files
    and text become one atomic canonical Message.
31. As an Agent, I want attachment-only sends rejected, so that an Agent Message
    always contains an intentional textual statement.
32. As an Agent, I want uploads verified before their IDs can be attached, so that
    incomplete or expired objects cannot enter canonical Message Parts.
33. As an Agent, I want the Server to normalize attachment IDs into Message
    Parts, so that the CLI and Credential Proxy do not become alternate owners of
    structured Message semantics.
34. As a machine operator, I want one long-running coordinator per Workspace and
    Agent, so that inbox ownership is stable across runtime turns.
35. As a machine operator, I want Agent roots isolated by Workspace and Agent, so
    that one user can synchronize several workspaces from the same computer
    without state collision.
36. As a machine operator, I want a Workspace explicitly bound with setup before
    its Agents run locally, so that membership discovery does not silently create
    execution authority.
37. As a machine operator, I want the canonical service origin to remain
    `https://leagent.me`, so that local state does not model unsupported multiple
    servers.
38. As a human watching Activity, I want `Message received` only when concrete
    Message bodies are handed to the runtime, so that the label does not claim a
    read receipt for a Notice.
39. As a human watching Activity, I want `Checking messages`, `Reading history`,
    and `Searching messages` during the corresponding Agent tools, so that I can
    understand what the Agent is doing.
40. As a human watching Activity, I want a freshness hold shown as `Send held by
    freshness check`, so that a missing reply is explained without calling the
    Agent failed.
41. As a system operator, I want Activity upload failure isolated from Message
    delivery and reply correctness, so that observability cannot block chat.
42. As a system operator, I want duplicate Deliveries, failed acknowledgements,
    recovery state, Notice retries, boundary failures, held sends, and overlapping
    sends logged without Message content or credentials, so that failures are
    diagnosable safely.
43. As a system operator, I want overlapping sends observed before adding
    serialization, so that concurrency control is driven by actual evidence.
44. As a developer, I want the old Agent inbox lease, task-execution identity,
    Agent-facing cursor, and compatibility command paths removed from chat in one
    cut, so that the new lifecycle has one contract rather than two partial ones.
45. As a developer, I want product Tasks and usage accounting kept separate from
    Message delivery, so that unrelated product and observability concepts do not
    regain ownership of chat state.

## Implementation Decisions

### Canonical ownership and identity

- The Server canonical Message is the only durable communication truth. Chat
  reads, search, resolve, reactions, frontend projection, and recovery all refer
  to that record.
- `message_id` identifies the canonical Message globally.
- A monotonic `seq` orders Messages within a target. Context coverage is always
  target-scoped; sequences from different targets are never compared.
- `delivery_id` identifies one transport attempt lineage and is used only for
  delivery idempotency and acknowledgement correlation. It is not a read state or
  Message identity.
- Delivery, Pending, Notice, and Context Boundary are transport or local context
  concepts. They do not become new server-side Message types.
- Product Tasks remain a separate product model. `agent_execution`, Task IDs,
  inbox leases, and server-side seen cursors do not participate in the long-term
  Agent Message lifecycle.

### Machine and workspace isolation

- The system has one canonical service origin, `https://leagent.me`; it models
  multiple Workspaces, not multiple Servers.
- A Computer may bind many Workspaces and a Workspace may bind many Computers.
- Workspace membership may be discovered automatically, but local execution
  authority is created only by an explicit setup command for that Workspace.
- Each Workspace and Agent pair has exactly one long-running coordinator on a
  Computer.
- Each Agent's local root is isolated under the machine data root by Workspace
  ID and Agent ID. The boundary file and Draft file live under that Agent root.

### Delivery protocol

- Server-to-machine delivery uses the wire event name `agent:deliver`.
- Machine-to-Server acknowledgement uses `agent:deliver:ack`.
- A Delivery carries `agentId`, target sequence, `deliveryId`, the canonical
  Message projection required by the runtime, and optional tracing context.
- An acknowledgement carries `agentId`, sequence, `deliveryId`, and optional
  tracing context.
- The coordinator sends the acknowledgement only after it has accepted the
  Delivery into its owned Pending projection or recognized it as a duplicate.
- An acknowledgement proves neither read nor runtime handoff nor Context
  Boundary advancement.
- The Server retries unacknowledged live Deliveries with the same delivery
  identity for that attempt lineage. The coordinator deduplicates across retries
  and recovery using canonical Message identity plus target sequence.

### Pending and Context Boundary

- Pending is an in-memory, rebuildable projection of canonical Messages newer
  than the local Context Boundary.
- Message bodies are never persisted in a second machine-local inbox ledger.
- The only durable local receive state is a target-to-maximum-sequence map named
  `consumed-seqs.json` under the Agent root.
- Boundary writes use atomic replacement and must be durably completed before
  the coordinator forgets the corresponding Pending coverage.
- A boundary advances only after a successful concrete runtime handoff, a
  successful explicit `message check` or `message read`, or freshness-held
  context returned to the Agent.
- Search, resolve, reactions, Notices, and delivery acknowledgements never
  advance a boundary.
- A missing, malformed, regressed, or unwritable boundary file means unknown
  coverage. The system may replay context but may not skip it or authorize a
  freshness-dependent send based on it.
- Held output may include only the newest three bodies, but the accepted coverage
  boundary advances through the maximum sequence of the complete Pending set for
  that target. Omitted Messages remain retrievable through explicit read.

### Startup and reconnect recovery

- Coordinator startup and every machine reconnect begin an internal recovery
  sync using the complete local target boundary map.
- The Server does not persist that map. It statelessly pages canonical Messages
  newer than each supplied boundary and eligible Messages for targets absent
  from the map.
- Recovery uses an explicit snapshot high-watermark or an equivalent ordering
  fence so that Messages created during pagination are delivered through the
  live stream or included in the snapshot, never lost between them.
- Live Deliveries are accepted while recovery is running and merged with
  recovery pages by Message ID and target sequence.
- Recovery becomes complete only after every page has been validated and merged.
  A partial page set cannot establish freshness.
- While recovery is incomplete or failed, reads may report their bounded data
  and Deliveries may continue to be accepted, but sends requiring freshness fail
  closed as held or unavailable.
- Recovery has bounded page sizes and an explicit `hasMore` continuation. It
  does not silently truncate eligible history.

### Runtime delivery and Notices

- When a runtime can safely accept input, the coordinator hands it concrete
  Message bodies in target sequence order and advances boundaries only after the
  runtime input boundary accepts the batch.
- When the Agent is busy, the coordinator retains concrete Messages as Pending
  and sends a content-free Notice at a safe notification point.
- Notice coalescing uses a three-second window.
- A Notice contains total Pending count, changed targets, and per-target Pending
  count. It may include first and latest short Message IDs, latest sender,
  mention flags, and an optional server-derived attention hint.
- A Notice never includes Message text, Message Parts, attachment metadata, or
  attachment bytes.
- An unchanged Pending fingerprint suppresses duplicate Notices within the same
  runtime session.
- A failed Notice write retains debt and retries after fifteen seconds.
  Compaction, review, runtime backoff, or a session that cannot safely accept a
  Notice defers rather than clears the debt.
- Successful Notice delivery does not consume Pending, advance a Context
  Boundary, or emit the `Message received` Activity.

### Activity projection

- Activity is best-effort user-facing narrative and is not a Message state
  machine, transport acknowledgement, or read receipt.
- A successful daemon-to-runtime concrete body handoff emits one `Message
  received` Activity per batch, including startup context, idle input, and gated
  busy flushes.
- Starting `message check` emits `Checking messages…` with the
  `checking_messages` detail kind.
- Starting `message read` emits `Reading history…`.
- Starting `message search` emits `Searching messages…`.
- A freshness hold emits `Send held by freshness check`.
- A content-free Notice does not itself emit `Message received`; if the Agent
  later checks because of that Notice, the check tool emits its own Activity.
- Internal trace keys, event names, and enums need not use UI copy as their
  names. Activity failure never blocks Message delivery, boundary advancement,
  or an Agent reply.

### Credential Proxy, freshness, and Drafts

- The local Credential Proxy owns credentials and all freshness-sensitive send
  behavior. The Agent runtime never receives service credentials.
- Each concrete Agent process launch receives a random machine-local proxy
  credential through an owner-only token file. A generated `multica` wrapper
  pins `MULTICA_WORKSPACE_ID`, `MULTICA_AGENT_ID`, the proxy URL, and the token
  file path; the Machine Service maps the token to the fixed Workspace, Agent,
  and runtime scope. Environment values and request fields never grant that
  authority.
- Agent Proxy rollout is additive to the existing Agent CLI authorization
  contract. It must not reduce the commands that an Agent can already invoke.
  `MULTICA_AGENT_ACTIVE_CAPABILITIES` is therefore not injected or enforced
  until a complete command manifest and its derivation from authoritative
  server policy are designed and migration-tested. An incomplete allowlist
  such as only `message.read` and `message.send` is invalid. Agent Command
  Policy uses the additive rollout and single-state decision contract in
  [ADR-0013](../../adr/0013-roll-out-agent-command-policy-additively.md):
  existing unclassified commands retain legacy passthrough, explicit denial
  requires authoritative policy, and newly added Agent commands must declare
  their classification.
- The Proxy derives its internal `seenUpToSeq` value from the local Context
  Boundary for the target. This field is an internal Server request detail, not
  a public CLI flag or persisted server cursor.
- Before a send, the Proxy completes local freshness preflight and consumes any
  Server-held response. The Server returns newer counts and at most the newest
  three held bodies.
- A held response prepares one machine-local coverage receipt for the complete
  represented range without changing Pending or the Context Boundary, saves or
  refreshes the Draft, and returns at most the newest three held bodies. The CLI
  commits that receipt only after visible output succeeds; failed output keeps
  the same Draft identity and Pending context replayable. A hold never
  automatically resends.
- Drafts are local, target-scoped, and stored in `continue-state.json` under the
  Agent root. Each contains body, attachment IDs, stable internal idempotency
  identity, save time, and re-hold state needed by the explicit replay flow.
- A Draft expires ten minutes after its most recent normal save or hold update.
- A normal send replaces the target Draft before network submission. An explicit
  draft send reuses the saved content, attachment IDs, and idempotency identity.
- The Proxy generates the idempotency identity before the first network attempt
  and persists it with the Draft. Replaying the same identity and same payload
  returns the original canonical Message; reusing it with a different payload is
  rejected.
- `--anyway` is valid only with an explicit saved-Draft send. A successful send
  clears the matching Draft; unknown delivery outcome retains it for explicit
  verification and replay.
- The first version does not add a per-target send lock or durable local send
  queue. Actual overlapping sends emit a content-free structured warning for
  later evidence-based decisions.

### Agent messaging command contract

- The supported Agent commands are `message send`, `message check`, `message
  read`, `message search`, `message resolve`, and `message react`.
- Removed commands and top-level compatibility aliases are deleted rather than
  deprecated. `message ask-choice` and `message a2a-control` are not part of the
  new surface.
- There is no public `--seen`, `--client-message-id`, `--idempotency-key`,
  `--message`, `--message-stdin`, `--message-file`, or generic `--output
  json|text` option.
- `message send` requires `--target`. A normal send requires a non-empty body on
  stdin and accepts repeatable `--attachment-id`. Positional content and
  attachment-only messages are rejected.
- `message send --send-draft --target <target>` sends the current saved Draft and
  does not accept replacement stdin or new attachment flags. `--anyway` is valid
  only on this explicit Draft path.
- `message check` is non-blocking and bounded. It returns a clear completion
  marker or tells the Agent to run it again when `hasMore` is true.
- `message read` requires the canonical `--target` spelling and supports bounded
  `before`, `after`, `around`, and `limit` windows. The legacy `--channel` alias
  is not retained.
- Read anchors come in two grammars with separate fields at every layer:
  message identity (`--before-id` / `before_id`, full or 8+ character unique ID
  prefix) and target sequence (`--before-seq` / `before_seq`, positive integer).
  The same split applies to `after` and `around`, the Credential Proxy request
  contract rejects unknown fields, and combining both grammars of one direction
  is a 400. Identity anchors never fall back to sequence interpretation, so a
  digits-only ID prefix cannot be misread as a sequence. Agents are taught the
  id flags only; sequence anchors are machine bookkeeping for Proxy recovery
  and freshness, mirroring the response-side rule that cursor-like read state
  (context target, seen-up-to sequence) never reaches the Agent process.
- `message search` accepts query and canonical target/filter pagination options
  without retaining the legacy `--channel` spelling.
- `message resolve` accepts one full or short Message ID as its positional
  identity and returns the exact canonical Message or a precise ambiguity/not
  found error.
- `message react` accepts `--message-id`, `--emoji`, and optional `--remove`; it
  does not require a target.
- Command-specific structured output may remain only where the Raft canonical
  command defines it. Multica does not add a global output-format abstraction.

### Attachments and canonical Message Parts

- Attachment upload is a separate capability-negotiated flow: create an upload
  session, upload directly to the presigned object destination, complete and
  verify it with the Server, and retry, cancel, or expire through explicit
  states.
- Uploads validate file existence, non-empty size, configured size limits, MIME
  type, target authorization, expiry, and final object metadata.
- Agent send requests contain only textual `content` and parallel
  `attachmentIds`. The Credential Proxy treats attachment IDs as opaque.
- The Server validates every attachment ID against actor, Workspace, target,
  completion state, and expiry, then atomically constructs the canonical Message
  Parts and Message record.
- The Agent send endpoint does not accept both arbitrary Parts and attachment IDs
  in one request.
- Existing UI and native structured-message entry points may continue to submit
  supported Parts, but all entry points pass through one Server normalizer and
  produce the same canonical Message representation.

### Frontend projection and hard cut

- Chat lists and message detail views read canonical Messages through React
  Query. Canonical Message realtime events invalidate the appropriate
  Workspace-scoped queries; websocket handlers do not write server data into
  Zustand.
- Frontend visibility of a Message is independent of Agent Delivery, Pending,
  boundary, runtime, and Activity state.
- Activity remains a separate user-facing projection with the stage-specific
  labels defined above.
- The new path replaces the current Agent chat inbox/lease/task-execution and
  server seen-cursor path in one cut. There is no dual write, compatibility
  adapter, or fallback ownership model.
- Database changes remove obsolete chat-path constraints and columns only after
  all callers are cut over. Product Task data and unrelated Activity or usage
  data are not deleted.

### Observability

- Structured logs cover Delivery accepted, duplicate, acknowledged, retried,
  and rejected outcomes; recovery start/page/complete/failure; live/recovery
  merge; Notice coalesce/retry/defer; boundary load/write/corruption; freshness
  hold; Draft save/replay/expiry; attachment verification; and overlapping
  sends.
- Logs may include Workspace, Agent, target, Message ID, sequence, Delivery ID,
  counts, state, and reason codes. They must not include Message bodies,
  attachment filenames, raw attachment metadata, Draft content, or credentials.
- Metrics use bounded dimensions and do not put Workspace, Agent, target,
  Message, or Delivery IDs in labels.
- Machine-local rotating traces may support diagnosis, but they are not a
  durable Message, usage, or Activity ledger and are not required for
  correctness.

## Testing Decisions

- The primary acceptance seam is one integration harness that runs a real Server
  HTTP and WebSocket surface together with the Machine Service, Credential Proxy,
  a temporary Agent root, and a fake runtime input boundary. Tests assert
  externally observable protocol, Message, file, runtime-input, and Activity
  behavior rather than internal helper calls or direct table state.
- Existing Agent transport integration tests, daemon websocket hub tests,
  canonical inbox tests, and message command tests are prior art. The new suite
  should deepen those seams instead of creating parallel test-only architecture.
- The primary harness covers an idle delivery from canonical Message creation to
  chat projection, `agent:deliver`, acknowledgement, runtime body handoff,
  boundary persistence, and exactly one `Message received` Activity.
- The harness covers busy delivery: the Message remains Pending, acknowledgement
  is transport-only, a coalesced content-free Notice reaches the runtime, no
  boundary advances, and no `Message received` Activity appears.
- The harness then runs `message check` after a busy Notice and proves bounded
  Message return, boundary advancement, Pending removal, `Checking messages…`
  Activity, and no false duplicate `Message received` label from the tool itself.
- Separate read and search scenarios prove `Reading history…` and `Searching
  messages…` projection. Read advances only the returned target's boundary;
  search and resolve do not.
- Crash tests stop the coordinator after server delivery acknowledgement but
  before runtime handoff, restart with the same Agent root, complete recovery,
  and prove the canonical Message is handed off rather than lost.
- Boundary failure tests corrupt, delete, regress, and make the boundary file
  unwritable. They prove conservative replay or held behavior and prohibit
  sequence skipping.
- Recovery tests interleave paginated snapshot reads with live Deliveries on
  several targets, including a target absent from the boundary map. They prove
  no gaps, deterministic ordering, deduplication, and freshness remaining
  unknown until all pages merge.
- Delivery tests lose acknowledgements and resend the same delivery identity.
  They prove idempotent coordinator acceptance and one concrete runtime handoff.
- Notice tests prove three-second coalescing, same-session fingerprint
  suppression, fifteen-second retry debt, deferred unsafe runtime states, and
  absence of bodies and attachment data.
- Freshness tests create more than three newer Messages, attempt a send, and
  prove the newest three are shown, full counts are reported, no boundary
  advances before output, the complete covered boundary advances after receipt
  commit, failed output preserves Pending and the same unsent Draft identity,
  omitted context remains readable, and no automatic retry occurs.
- Draft tests prove save-before-network ordering, ten-minute expiry, normal-send
  replacement, explicit replay, `--anyway` validation, success cleanup, unknown
  outcome retention, same-payload idempotent replay, and different-payload
  rejection.
- Attachment tests cover upload capability negotiation, presigned upload,
  completion verification, expiry/cancel/retry, target authorization, atomic
  attachment-ID normalization, and rejection of attachment-only or mixed Parts
  plus attachment-ID Agent requests.
- A thin CLI contract suite snapshots or otherwise asserts the supported command
  tree, canonical flags, stdin rules, Draft rules, attachment flags, bounded
  outputs, and precise rejection of removed commands, aliases, and cursor or
  idempotency flags.
- A thin frontend projection suite feeds canonical Message websocket events and
  Activity facts through the public client boundary. It proves React Query
  invalidation makes Messages visible and that stage-specific Activity labels
  do not depend on Notice or boundary internals.
- Schema and migration tests may directly validate constraints, rollback shape,
  and obsolete object removal. Direct database assertions are not the acceptance
  proof for delivery lifecycle behavior.
- Tests use fake time for coalescing, retries, and Draft expiry. They do not sleep
  for real timing windows.
- Every failure-path test distinguishes Message truth, transport acceptance,
  runtime handoff, Context Boundary, and Activity projection so that one green
  layer cannot conceal another layer's failure.

## Out of Scope

- Provider token usage, cost normalization, billing, pricing versions, and usage
  upload are deferred to a separate design.
- Agent restart modes, forced busy-turn interruption, full reset, Agent Workspace
  deletion, and Server-initiated cleanup are not part of this Message repair.
- Deleting a Server Agent does not automatically delete local Agent workspaces as
  part of this work.
- Multi-server configuration or an API-specific service domain is not supported.
- Product Task, issue, research execution, reminder, and general Activity
  lifecycle redesign are outside this spec.
- A new `Execution` domain entity or `agent_execution`-based Message ownership is
  explicitly excluded.
- Per-target send locking, a durable send queue, and automatic serialization are
  deferred until overlap observations demonstrate a real correctness problem.
- Automatic resend after a freshness hold is excluded.
- Automatic continuation of a completed runtime turn solely because Pending
  exists is not introduced here; runtime scheduling remains an independent
  concern.
- Blind-review or reviewer-isolation product behavior is not introduced by this
  Message repair.
- A broad visual redesign of chat or Activity is not required beyond correct
  canonical Message visibility and stage-specific copy.
- Compatibility for removed Agent commands, flags, aliases, server seen cursors,
  or lease-based inbox APIs is not provided.

## Further Notes

- This specification supersedes the July 18 Agent Message Freshness draft for
  Agent delivery, read state, freshness, Draft, and command behavior.
- The direct Message Delivery lifecycle ADR remains the architectural rationale
  and domain vocabulary source for this spec.
- Raft is the behavioral reference for coordinator, Notice, Draft, command, and
  Activity semantics. Multica deliberately strengthens crash recovery and
  boundary persistence where a purely in-memory implementation would permit
  loss.
- `Message received` and the other Activity labels are frontend copy, not wire
  event names or canonical state transitions.
- The implementation must begin only after this spec is split into vertical,
  independently verifiable tickets. Existing uncommitted implementation work is
  not evidence that any acceptance criterion is already satisfied.
