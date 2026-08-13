# Domain context

This file names the domain concepts that implementations, tests, and design
documents use. It records product meaning, not package layout.

## Local Agent Execution

### Workspace

The collaboration boundary in which human members and Agents work together.
Its immutable `workspace_id` scopes memberships, Agents, and local execution
bindings. A Workspace is not an individual Agent's filesystem working
directory.
_Avoid_: Agent workdir, machine directory

### Members Directory

The workspace product surface that catalogs active human members and
non-archived Agents for browse and profile management. Agents are still
Agents for execution and identity; the directory is how the product presents
both kinds of participants in one roster. It is the sole product entry for
inviting humans and changing or removing human membership.
_Avoid_: Agents page (retired name for this surface), Settings members tab
(retired human-admin entry), team page, people list

### Computer

The single machine-local authority for supervising Multica execution under one
OS user environment. It connects to the Multica service at the canonical
server origin `https://api.leagent.me` and may manage Workspace Execution Bindings
for multiple Workspaces.
_Avoid_: Machine Service, daemon, Profile daemon, Workspace daemon

### Computer Owner

The human whose OS-user-scoped Computer identity may be restarted, upgraded,
or otherwise mutated. Workspace ownership and administration do not grant
control over another person's Computer.
_Avoid_: Workspace owner, Workspace admin, runtime owner

### Credential Proxy

The machine-local HTTP boundary through which an Agent CLI reaches the Multica
service without receiving long-lived service credentials. It owns credential
injection, Draft interception, freshness coordination, and response
consumption; it does not assemble canonical Message Parts.
_Avoid_: API gateway, Message store, runtime proxy

### Agent Command Capability

One named category of Agent CLI action whose effective state is resolved for a
concrete local Agent launch. It describes what the launch may do, not what the
Agent knows how to do; absence from an incomplete capability catalog never
means denial.
_Avoid_: Agent skill, Agent role, CLI feature flag, environment permission

### Agent Command Policy

The launch-scoped decision map that resolves each Agent Command Capability to
one state: legacy passthrough, allowed, denied, or unavailable. Service-side
role and Workspace authorization remains authoritative; the policy does not
turn a machine-local credential or environment variable into new authority.
_Avoid_: Command allowlist, capability environment variable, Agent skill list

### Workspace Execution Binding

A durable authorization relationship that permits one machine to execute
Agents for one Workspace. A Workspace may have bindings to multiple machines;
the binding is not Workspace membership or exclusive ownership. Discovering a
Workspace membership does not create a binding: the user creates it explicitly
with `multica setup /<workspace>`. If that user's membership is revoked, the
service revokes the corresponding Binding and its execution credential without
deleting the local Agent Roots.
_Avoid_: Workspace attachment, local Workspace

### Workspace Discovery

The Computer's view of Workspaces that the signed-in user may access.
Discovery follows membership changes but does not authorize local Agent
execution or start a Workspace Runner.
_Avoid_: Workspace synchronization, automatic binding

### Workspace Runner

The machine-local execution owner for one Workspace Execution Binding. At most
one Workspace Runner is active for a binding, while the same Workspace may have
other Workspace Runners on other machines.
_Avoid_: Workspace owner, global Workspace runner

### Agent Attachment

The durable fact that one Computer handles one Agent in one Workspace
through one Runtime. Attachment is fenced by its own generation and survives
process exit or Computer restart. It does not mean that a provider
process is running, and detaching it does not delete the Agent Root, Inbox,
Message Draft, or other durable Agent data.
_Avoid_: Agent process, launch, session, Reminder owner, Workspace attachment

### Agent Workspace (Agent Root)

The canonical persistent local working directory for one Agent in one
Workspace on one machine:
`~/.multica/workspaces/<workspace_id>/agents/<agent_id>`. It is keyed only by
immutable domain IDs and remains stable across runtime or harness changes.
Production uses this canonical root; a WorkspacesRoot override exists only for
development and tests. Binding, Runner, login, and membership lifecycle changes
do not delete it.
_Avoid_: Runtime root, profile Agent directory, slug-based Agent path

### Local Agent Workspace Deletion

A user-confirmed, machine-scoped destructive operation initiated from the
frontend. The user first selects one machine, then requests deletion of one
Agent Workspace at
`~/.multica/workspaces/<workspace_id>/agents/<agent_id>`. It does not delete the
Multica Workspace, sibling Agent Workspaces, or data on any other machine.
The whole Agent Workspace is the deletion unit, including its local memory,
sessions, skills, runtime state, and working files.
_Avoid_: Multica Workspace deletion, Binding revocation cleanup, global machine cleanup

### Agent Deletion

The server-side removal of an Agent identity. It stops and prevents further
execution for that Agent but does not itself delete any Agent Workspace from a
machine. Local files remain available until a separate Local Agent Workspace
Deletion is explicitly requested for a selected machine.
_Avoid_: Agent Workspace deletion, full reset

### Agent Restart Mode

One of three explicit ways to restart an Agent runtime:

- `restart` stops and starts the runtime while preserving its model session and
  complete Agent Workspace.
- `reset_session_restart` discards the model session and context, preserves the
  complete Agent Workspace, and starts a fresh model session.
- `full_reset_restart` discards the model session, removes and reprovisions the
  complete Agent Workspace, then starts the Agent fresh.

All three preserve the server-side Agent identity, configuration, chat history,
and Issues.
_Avoid_: Restart boolean, session reset as workspace reset, full reset as Agent deletion

## Agent Status Semantics

### Agent Presence

The binary user-facing reachability of a Workspace Agent: `Online` or
`Offline`. It is derived from the current Workspace Runner connection together
with the Manager's current managed/wakeable Agent state. Loading or missing
evidence is not a third Presence state and must not be presented as Online.
_Avoid_: Workload, runtime health, Disconnected, Stopped, Blocked, Crashed

### Agent Activity

A separate, best-effort projection of what an Agent is doing now and what it
recently did. Activity may be Idle, Thinking, Working, or Error while Presence
remains Online. Idle is Activity and never means Offline.
_Avoid_: Presence, liveness heartbeat, management ownership

### Context Compaction

A provider-runtime maintenance operation that rewrites an Agent session's
context while preserving the logical launch, provider process, Message turn,
and Agent identity. Compaction is user-visible Activity (`started`, `finished`,
one stale observation, or interruption), but it is not Message acceptance,
turn completion, process restart, or Idle. Pending runtime input waits at the
compaction boundary and resumes after completion or interruption.
_Avoid_: Session restart, turn restart, Message processing, silent maintenance

### Agent Management State

The internal active/inactive Manager ownership fact for a concrete Agent
launch. It contributes to Agent Presence but is never compact user-interface
copy.
_Avoid_: Online label, Offline label, Stopped badge

### Agent Diagnostic Reason

A contextual explanation for lifecycle, transport, recovery, provider, or
health evidence. Reasons belong in diagnostics, timelines, and recovery
surfaces; they are never values of the Agent Presence enum.
_Avoid_: Presence enum, compact Agent badge, avatar color

## Agent Message Delivery

### Message

The canonical Workspace-scoped communication fact addressed to a channel, DM,
or thread. A Message has a stable identity, target sequence, and structured
Parts independent of whether any Agent is online or has processed it; it owns
the author's stable identity and may retain a fallback label, while the avatar
is a current Identity Profile fact. It exists only on the service as the
communication source of truth.
_Avoid_: Inbox task, execution request, wake job, author appearance snapshot

### Identity Profile

The current display identity of one human or Agent, including its display name
and avatar. Historical Messages resolve its current avatar rather than
preserving their own copy.
_Avoid_: Message author snapshot, message avatar

### Message Part

One typed element of a Message's canonical content, such as text, attachment,
voice, reference, or system event. Agent CLI attachment flags are send intent;
the service validates them and assembles the resulting Message Parts.
_Avoid_: Attachment sidecar, markdown-only payload

### Delivery

An at-least-once transfer attempt of one Message to the Computer
currently responsible for an Agent. Replaying the same `delivery_id` is the
same Delivery; acceptance means the local coordinator accepted it, not that a
runtime saw it or that a second canonical Message copy was persisted locally.

### Delivery Acknowledgement

The Computer's receipt that it still owns one exact Delivery: the coordinator
holds it as Pending, the target is already context-covered, or an Idle Snapshot
restore of the original launch is in flight. It correlates the Agent, Delivery
identity, and target sequence. It ends Server redelivery but does not mean the
Agent read the Message, a provider process is alive, or the Context Boundary
advanced.
_Avoid_: Process liveness, read receipt, Message completion, Context Boundary, recovery cursor
_Avoid_: Inbox lease, task claim, execution

### Idle Snapshot

A machine-local record that one Agent recently had a runnable launch, enough to
restore that same launch after the process is gone. It is not a Message, a
Pending body, or a Context Boundary.
_Avoid_: APM launch, consumed-seqs, Message Draft, second message ledger

### Pending Message

A Message in a machine-local coordinator projection that has not yet crossed
into an Agent runtime's context. Pending is rebuildable from service-side
canonical Messages and is not a second durable message ledger.
_Avoid_: Received message, seen message, held response

### Context-covered Message

A Message whose body crossed an explicit Agent context boundary through an
initial prompt, successful runtime input, `message check`, `message read`, or
freshness-hold context. A transport Notice or Delivery acknowledgement never
makes a Message context-covered.
_Avoid_: Seen Message, WebSocket receipt, wake attempt

### Context Boundary

The monotonic, target-scoped sequence through which the local coordinator will
not block a send for older context. It is computed locally from explicit
context handoffs, may advance across bounded held-context omissions recoverable
with `message read`, and is never supplied or controlled by the Agent process.
_Avoid_: Visible Boundary, Seen cursor, Agent cursor, delivery acknowledgement sequence

### Notice

A content-free, target-scoped, coalescible signal that Pending Messages are
available. A Notice does not advance the Context Boundary or produce a
`Message received` Activity.
_Avoid_: Message event, Delivery, read receipt

### Message Received Activity

A user-facing Activity label shown once for a daemon-to-runtime body handoff
batch. The label does not define the internal event name, enum, trace key, or
wire protocol. The underlying observation is best effort and never participates
in Message state transitions. Notice delivery does not emit this label. Agent
message tools have their own Activity projections: `message check` shows
`Checking messages`, `message read` shows `Reading history`, and `message
search` shows `Searching messages`.
_Avoid_: Per-message read receipt, Delivery acknowledgement

### Message Draft

One locally saved, unsent Agent send intent scoped by Workspace, Agent, and
target. It contains the text, attachment IDs, and stable internal idempotency
identity; it expires ten minutes after its latest save or freshness hold and is
never resent automatically.
_Avoid_: Pending Message, server Message, retry queue

### Freshness Hold

A send outcome stating that newer target context must be handed to the Agent
before the saved Message Draft may be sent explicitly. It does not commit a
Message and never triggers an automatic retry.
_Avoid_: Send failure, Pending Message, automatic continuation

### Attachment

A Workspace-scoped uploaded file resource that a Message references through an
attachment Message Part. Uploading an Attachment does not itself create or
deliver a Message.
_Avoid_: Message file, inline Message bytes

### Attachment Upload Session

A service-authorized, expiring attempt to upload one Attachment directly to
object storage. The Attachment becomes sendable only after the service verifies
the uploaded object and completes the session.
_Avoid_: Message send, presigned URL

## Workspace Onboarding

### Onboarding Agent

The single Workspace-scoped Agent bound by `workspace.onboarding_agent_id` to
help humans form the Agent team. Its display name may change; a name such as
Wendy never establishes this role.
_Avoid_: Wendy role, HR by name, recruiting Agent by convention

### Hiring Proposal

A human-confirmable request prepared by the Onboarding Agent to create one
Workspace Agent. Preparing a proposal does not create the Agent; an authorized
human must commit it exactly once.
_Avoid_: Agent creation draft, autonomous hire, executable suggestion

## Research

### Research Session

The user-facing container opened from the Research module. It owns the title,
current user-visible goal, fleet, chat, canvas, sources, reports, and optional
handoff. The HTTP routes continue to use `research session` for compatibility.

### Research Run

The durable execution of one Research Session. A Research Run owns the current
Research Contract version, plan version, task and progress ledgers, evidence,
decisions, checkpoints, budgets, and recovery state. A process restart or Agent
failure must not create a new Research Run or discard accepted work. At start it
pins the Research Fleet lead as `research_director_agent_id`; changing the Fleet
lead later does not silently change an active Run.

### Research Director

The lead Agent responsible for interpreting the current Contract and Method,
proposing the next research portfolio, forming the run-scoped team, and
adjudicating escalated disputes. A Research Director proposes actions but cannot
override server evidence, permission, budget, or delivery invariants. In the
sealed Research Fleet this identity is Ronaldo. Only the pinned Director may
submit team-formation or lead-adjudication decisions. A human may explicitly
replace an unavailable Director; the replacement is versioned and audited.

### Research Contract

The versioned statement of what the user asked for: goal, scope, audience,
freshness, delivery language, source policy, and execution limits. Only a human
user may change the Research Contract. Every question, task, claim, decision,
and report revision records the contract version under which it was produced.

### Research Question

A durable item on the Research Frontier. It represents an unresolved dimension,
hypothesis, contradiction, or follow-up question. Required questions must reach
an answered or explicitly unresolved terminal state before delivery.

### Research Frontier

The ordered set of open Research Questions and proposed follow-up work. The
Research Run ranks frontier items by impact, uncertainty, novelty, coverage,
and estimated cost. It is the source of proactive exploration.

### Research Hypothesis

A falsifiable proposition attached to a Research Question. It records scope,
expected observations, weakening conditions, lifecycle state, and the accepted
evidence that changed that state. A confidence value alone cannot advance it.

### Research Branch

A durable exploration direction with an objective, entry and exit conditions,
budget allocation, parent branch, and terminal reason. It groups inquiry work;
it does not duplicate Research Task execution state.

### Research Insight

A versioned integration result that changes how the Research Run understands
its questions, hypotheses, disputes, or next work. Every Research Insight cites
accepted input entities. Integrator prose without valid references is not an
Insight.

### Insight Derivation

The directed acyclic provenance of one Research Insight. It records input
Claims and lower-level Insights, server-computed level, integration round,
semantic value, and freshness. Invalidating an input makes every dependent
Insight stale until reintegration.

### Research Task

A durable unit of delegated research work with an objective, kind, required
capability, dependencies, expected result, acceptance criteria, priority, and
budget. Research Tasks form a directed acyclic dependency graph; replanning may
add a new graph version without deleting historical tasks.

### Research Task Attempt

One dispatch of a Research Task to an Agent. It links the task to the canonical
Agent inbox execution and records assignment, retry number, result identity,
failure classification, timing, and terminal outcome.

### Team Formation Decision

A durable Research Director decision to add an Agent because of a capability
gap, capacity need, independence requirement, novel perspective, or new Branch.
It records the target, role, permissions, budget, expected artifact, lifecycle,
and why the current team is insufficient.

### Research Team Membership

The run-scoped relationship between an Agent and a Research Run. It records the
formation decision, authorized role and capabilities, join and leave times, and
terminal reason without deleting historical Agent attribution.

### Source Snapshot

The immutable source material seen by an Agent at a particular time. It stores
canonical provenance, a bounded text snapshot or excerpt, locator data, a
content hash, and an independence key used to avoid counting mirrors or exact
duplicates as separate support.

### Search Plan

A versioned description of how one Question or Branch will search a bounded
information space. It records retrieval Adapters, query strategy, language,
time and source scope, screening rules, and stopping conditions.

### Query Execution

One immutable execution of a search query through a named retrieval Adapter.
Query refinements create child executions so cost, failures, cursors, and
source discovery remain reproducible.

### Source Candidate

A discovered source that has not yet entered the evidence graph. It records
where it was found, duplicate and independence candidates, and safety signals.
A Screening Decision must accept it before a Source Snapshot is created.

### Screening Decision

An append-only acceptance, exclusion, or deferral decision for a Source
Candidate under a specific Research Method version. It records the applied
rule and reason so omitted sources are auditable.

### Observation

A bounded quote, datum, or directly observed fact extracted from one Source
Snapshot. A quoted Observation must be verifiably present in its snapshot.

### Claim

An atomic proposition produced during research. Claims have significance,
confidence, lifecycle state, contract version, and explicit supporting or
contradicting Evidence Links.

### Evidence Link

A typed relation from an Observation to a Claim. The relation is `supports` or
`contradicts`; strength and verification state are recorded separately.

### Research Decision

An append-only record of a material planning, routing, quality, pivot, or stop
decision and its reasons. Decisions make non-deterministic Agent behavior
auditable without requiring chat history to be the execution record.

### Integration Round

A durable integration of a fixed set of accepted artifacts. It may propose
Insights, inquiry changes, disputes, branch changes, and follow-up work. The
Research Run validates every reference and transition before applying it.

### Integration Contribution

A producing Agent's structured comparison of its accepted result with related
artifacts in one Integration Round. It records agreements, unique findings,
conflicts, scope, omissions, and proposed higher-level Insights. It is an input
to integration and cannot mutate canonical Claims or Insights by itself.

### Research Dispute

A durable disagreement between scoped positions or Claims. It records the
conflict type, severity, evidence, investigation tasks, adjudication, residual
uncertainty, and delivery obligation. Contradicting prose alone does not resolve
a Research Dispute.

### Research Deliberation

A bounded, structured discussion among Agents holding positions in a Research
Dispute. Turns cite evidence and record challenges, concessions, scope changes,
and server-observed progress. Deadlock escalates to the Research Director;
messages alone cannot resolve the dispute.

### Capability Observation

One Task Attempt's observed relationship between a required capability and the
Agent, model, provider, tool, and retrieval Adapter used. It includes outcome,
quality, cost, latency, and failure class; it is evidence for routing rather
than a permanent capability declaration.

### Research Episode

A read-only record produced after a delivery cycle or terminal Run. It contains
the Contract, Method, material state changes, decisions, failures, cost,
quality, and user steering. It supports offline evaluation and cannot mutate
production strategy by itself.

### Strategy Version

An immutable version of exploration, integration, routing, failure, prompt,
tool, and stopping policies. A Research Run pins one Strategy Version at start.
Promotion requires recorded offline evaluation and supports rollback.

### Research Monitor

A user-approved schedule and material-change policy attached to a delivered
Research Run. Monitoring Cycles reuse versioned Search Plans. A no-change cycle
records a Decision; a material change creates incremental inquiry, integration,
and report work without overwriting the baseline Report.

### Divergence Pass

A bounded exploration performed with an intentionally isolated context to find
missing perspectives, stakeholders, source ecosystems, methods, anomalies,
counterevidence, and cross-domain analogies. It can propose Questions,
Hypotheses, Branches, and probe Tasks but cannot create verified Claims.

### Research Projection

A user-facing view derived from Research Run state and events. The existing
canvas, process cards, presence, source list, and report are projections. A
projection may be rebuilt without changing canonical task or evidence state.
Every canonical research entity has a stable typed node, typed edges, complete
detail data, and event-sequenced deltas so a client can render hierarchy,
discussion, conflict, provenance, and change without parsing Agent prose. A
snapshot is pinned to one event sequence; reconnecting clients resume after that
sequence or reload a snapshot when the retained delta range has expired.
