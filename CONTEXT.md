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

### Machine Service

The single machine-local authority for supervising Multica execution under one
OS user environment. It connects to the Multica service at the canonical
server origin `https://leagent.me` and may manage Workspace Execution Bindings
for multiple Workspaces.
_Avoid_: Profile daemon, Workspace daemon

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

The Machine Service's view of Workspaces that the signed-in user may access.
Discovery follows membership changes but does not authorize local Agent
execution or start a Workspace Runner.
_Avoid_: Workspace synchronization, automatic binding

### Workspace Runner

The machine-local execution owner for one Workspace Execution Binding. At most
one Workspace Runner is active for a binding, while the same Workspace may have
other Workspace Runners on other machines.
_Avoid_: Workspace owner, global Workspace runner

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
