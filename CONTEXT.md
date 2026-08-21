# Domain context

This file names the domain concepts that implementations, tests, and design
documents use. It records product meaning, not package layout.

## Local Agent Execution

### Workspace

The collaboration boundary in which human members and Agents work together.
Its immutable `workspace_id` scopes memberships, Agents, and local execution
bindings. This is the Raft 1.0.16 Server analogue: Computer attaches one
DaemonCore per Workspace, the way Raft Computer attaches one DaemonCore per
Server. A Workspace is not an individual Agent's filesystem working directory.
_Avoid_: Agent workdir, machine directory, Raft Server as a second product
entity, host, Computer

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
execution or start a DaemonCore.
_Avoid_: Workspace synchronization, automatic binding

### DaemonCore

The Computer-supervised execution child for one Workspace Execution Binding.
It is the Raft 1.0.16 `DaemonCore` analogue: one OS child per Binding
(`computer __runner`), not a second Computer and not a provider runtime.
At most one DaemonCore is active for a binding; the same Workspace may have
other DaemonCores on other machines. The Host reconciles the child every 5s,
backs off 2s after a crash, and degrades after 3 crashes in 60s. A previous
child generation cannot mutate the live slot.
_Avoid_: Binding child as a product name, Workspace daemon, profile daemon,
second Computer, runtime

### Workspace Runner

The control-plane owner inside one DaemonCore: the live connection and command
surface for that Binding. Presence is derived from this connection. It is not
the OS child and not a RuntimeSession.
_Avoid_: Workspace owner, global Workspace runner, DaemonCore, runtime

### DaemonConnection

The Raft 1.0.16 analogue for one live `/api/daemon/connect` socket inside a
DaemonCore. Socket open is Computer liveness for that Workspace. Workspace
Runner owns commands on top of it; `GET /api/computers` only reads the
server-side Hub registration of this socket.
_Avoid_: heartbeat liveness, `/api/computers` as a daemon RPC

### RuntimeSession

One Agent's live provider execution session inside a DaemonCore: one Agent,
one provider runtime, one session. Restarting or replacing it does not create
a new Computer or a new DaemonCore.
_Avoid_: Computer, DaemonCore, Workspace Runner, Agent Root

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

The server operation is the durable product orchestrator. It advances only
through Raft 1.0.16's discrete Runner boundaries: `agent:stop`, inactive
status, optional session clear, optional `agent:reset-workspace`, then
`agent:start(config.sessionId)`. A replacement is complete only after the new
launch reports active. The Runner never receives a composite restart action;
heartbeat lifecycle queues and parallel stop/start paths remain retired.
_Avoid_: Restart boolean, session reset as workspace reset, full reset as Agent deletion

## Standalone Agent Chat

### Standalone Agent Chat

A 1:1 conversation with one Agent that is not a Workspace Message thread. The
FAB and the isolated bubble are the same surface. It carries that Agent's
memory and skills. A user line is a `chat_message`; the Computer is woken by
Raft `agent:deliver` of that line, not by an inbox Task. A missing resident
process is not an ACK: the service starts the desired launch and redelivers
the unacked line, same as a channel Message. A visible reply is leftover
assistant prose written into the conversation that woke this turn.
_Avoid_: isolated bubble as a second product, DM bubble, FAB chat, inbox task

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

### Resident Agent Process

The currently running provider OS process behind one RuntimeSession. While it
is alive, later configuration changes do not replace it. A new process starts
only after an explicit Agent Restart Mode or after the process has died.
_Avoid_: fingerprint restart, implicit stop+start, hash-based recycle, one-shot slot, RuntimeSession as the OS process

## Research

### Research Session

The user-facing container opened from the Research module. It owns the title,
current user-visible goal, Director, run-scoped team, chat, canvas, sources,
reports, and optional handoff. The HTTP routes continue to use
`research session` for compatibility.

### Research Run

The durable execution of one Research Session. A Research Run owns the current
Research Contract version, task and progress ledgers, evidence, decisions,
checkpoints, team, and recovery state. A process restart, context rotation, or
Agent failure must not create a new Research Run or discard accepted work. The
user-selected Research Director remains pinned until the user replaces it.

### Research Director

The user-selected lead Agent responsible for interpreting the current Contract,
creating and retiring the run-scoped team, assigning work, integrating results,
adjudicating escalations, and accepting delivery. In Research this identity is
Ronaldo. The Director may propose any Research action, while the server enforces
identity, workspace, durability, graph, concurrency, and security invariants.

### Research Contract

The versioned statement of the user's current intent: goal, scope, audience,
freshness, delivery language, source policy, and execution limits. The user is
the source of intent; the Research Director records whether each user message
changes the Contract and materializes the resulting version. Every Research
artifact records the Contract version under which it was produced.

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
parent branch, terminal reason, and a Branch Frontier. It groups inquiry work;
it does not duplicate Research Task execution state. A Branch has at most one
current XXL Insight, which may also be current for other Branches.

### Branch Frontier

The set of fresh, unabsorbed Research Results and Insights currently carrying
one Research Branch. It may contain several incomparable nodes; successful
Absorption removes its inputs and adds their canonical successor.

### Research Insight

An immutable M, L, XL, or XXL integration result that changes how the Research
Run understands a Branch. Every Research Insight cites accepted input entities
and stores persisted catalog, brief, and full representations. Revised content
creates a successor Insight instead of mutating the accepted version.

### Atomic Research Result

An immutable S-level result accepted from one Research Task Attempt. It is
distinct from the running Work and remains S until globally absorbed into one
canonical successor or retained as an unabsorbed terminal result.

### Research Node Tier

The semantic compression level of Research content: S, M, L, XL, or XXL.
Promotion requires at least two fresh same-tier inputs; assimilation may update
a higher-tier Insight without increasing its tier.

### Research Absorption

The irreversible canonical succession of at least two input versions into one
accepted Insight successor. Promotion uses at least two fresh same-tier inputs;
assimilation uses an existing higher node plus at least one fresh supplement.
Each input has at most one direct successor; other Branches reuse that successor
instead of absorbing the input again.

### Node Steward

The Agent currently responsible for one fresh Research content node. Stewardship
is transferable and audited; the Agent whose accepted work completes an
integration becomes the successor's preferred Steward.

### Insight Derivation

The directed acyclic provenance of one Research Insight. It records input
Claims and lower-level Insights, server-computed level, integration round,
semantic value, and freshness. Refuting an absorbed input challenges every
dependent Insight and requires an explicit repair decision; it does not revive
or silently delete historical nodes.

### Research Task

A durable unit of delegated research work with a Director-authored type,
objective, optional capability requirements, dependencies, expected result,
acceptance criteria, and priority. Research Tasks form a directed acyclic
dependency graph; changed work creates new tasks without deleting history.

### Research Task Attempt

One dispatch of a Research Task to an Agent. It links the task to the canonical
Agent inbox execution and records assignment, retry number, result identity,
failure classification, timing, and terminal outcome.

### Research Work Item

A persistent, leaseable execution envelope for Research, matching, Discussion,
integration, Director, report, or review work. It references the typed domain
object it advances and is recoverable without an Agent's previous conversation
context.

### Team Formation Decision

A durable Research Director decision to add an Agent because of a capability
gap, capacity need, independence requirement, novel perspective, or new Branch.
It records the target, role, permissions, budget, expected artifact, lifecycle,
and why the current team is insufficient.

### Research Team Membership

The run-scoped relationship between an Agent and a Research Run. It records the
formation decision, Director-authored mission, model and tool configuration,
join and leave times, current availability, and terminal reason without deleting
historical Agent attribution. A new Run begins with only its Director.

### Director Brief

The persisted, bounded context compiled for a Research Director cycle. Its
Research Brief contains the current Contract and each Branch Frontier summary;
its Control Brief contains steering, team, Work Item, Discussion, dispute,
failure, and change-watermark facts.

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

### Research Discussion

A persistent, version-pinned conversation among the Stewards evaluating one
match, fusion, assimilation, or dispute. Turns are user-visible, but structured
votes and the accepted Decision—not hidden model reasoning—control state.

### Research Report

An immutable, rendered HTML delivery revision attached to the Goal. It records
the exact Contract and Branch Frontier inputs used, does not absorb Research
nodes, and becomes published only after the Research Director accepts it.

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

## Period Work

### Period Work Brief

A note that states what someone accomplished in a time window, written for
colleagues or a manager. It is a filtered narrative in Notes, not an activity
list and not a slide file.
_Avoid_: retrospective as the deliverable, PPT, standup dump, weekly report file

### Period Work Collector

An Agent on a provisioned per-Computer runtime (local or cloud) that gathers
recent work on the OS where that runtime runs. Scan roots are `SCAN_ROOTS`
(`$HOME` plus `/workspace` when present and other visible project dirs — not
HOME-only on container sandboxes), including non-git in-window source files.
Evidence (short diffs, file summaries, key snippets) plus preliminary **Work
groups** (same project together; related cross-repo work in one group with
why) and optional Mermaid diagrams that need full local context. Delivers via
`submit-pack` onto the Period Brief run (not a Notes「采集包」page).
Completeness first: groups and diagrams are additive, never a substitute for
Highlights. Not Computer Host Digest harvest.
_Avoid_: Host Journal as the Brief machine source, keymouse, full-repo dump,
HOME-only scans that miss `/workspace`, groups-only packs that drop evidence,
--note-write packs into Notes

### Period Brief Collect Plan

The Notes Assistant restatement of an optional human 写汇报 focus (paths,
topics, aspects) used to assign scoped collector tasks. Humans pick time
range and owned computers as chips for one send in the Notes bubble; typed
composer text wins if it conflicts. Saying 写汇报 in the bubble (no chips)
makes the Notes Assistant ask for time and computers first, then start.
Empty focus means full-scope default collection on every selected collector.
After send, chips go away, the composer locks until the run finishes, and
progress is spoken in this page's right-side assistant sidebar. Collector packs and the
finished brief show as collapsed cards of **this run** (never the latest
`工作介绍/` write from a previous run); the brief has Insert below note and
Insert as child note.
_Avoid_: treating the plan as the Brief; letting collectors invent extra machines; a dedicated 写汇报 dialog; auto-jumping to Messages; treating chips as a standing form

### Period Brief Agent

The Notes Assistant (笔记助手) in its 写汇报 wakes: collect-plan commander
when the human gave a focus, then synthesizer from platform Facts plus
collector packs, force_fresh_session, --note-write. Not a second Workspace
Agent. Leftover 周报 / weekly-report rows are archived on Ensure.
Progress is narrated in the issuing page's bubble session.
_Avoid_: a dedicated weekly-report Agent; treating Worker --note-write as the human-facing transcript

### Period Work Synthesis

The job that reads platform facts plus collector packs and writes one Period
Work Brief. When the human gave a focus, a collect-plan wake assigns scoped
collector tasks first; selection and grouping of the Brief still happen here.
After the human confirms in the bubble, the Brief is inserted as a child of
the note page that issued the task.
_Avoid_: LLM inside the retrospective API, Host Digest as required input,
silent note overwrite, inserting under a global 工作介绍/ folder when the
task started from a page bubble

### Machine Work Journal / Work Digest (legacy)

ADR 0018 Host-path terms. Superseded for Period Work by ADR 0019 Collectors.
May remain in code temporarily; do not teach them as the Brief contract.
_Avoid_: new Brief features depending on Host Digest

