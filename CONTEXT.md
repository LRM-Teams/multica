# Domain context

This file names the domain concepts that implementations, tests, and design
documents use. It records product meaning, not package layout.

## Research

### Research Session

The user-facing container opened from the Research module. It owns the title,
current user-visible goal, fleet, chat, canvas, sources, reports, and optional
handoff. The HTTP routes continue to use `research session` for compatibility.

### Research Run

The durable execution of one Research Session. A Research Run owns the current
Research Contract version, plan version, task and progress ledgers, evidence,
decisions, checkpoints, budgets, and recovery state. A process restart or Agent
failure must not create a new Research Run or discard accepted work.

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

### Research Task

A durable unit of delegated research work with an objective, kind, required
capability, dependencies, expected result, acceptance criteria, priority, and
budget. Research Tasks form a directed acyclic dependency graph; replanning may
add a new graph version without deleting historical tasks.

### Research Task Attempt

One dispatch of a Research Task to an Agent. It links the task to the canonical
Agent inbox execution and records assignment, retry number, result identity,
failure classification, timing, and terminal outcome.

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

### Research Dispute

A durable disagreement between scoped positions or Claims. It records the
conflict type, severity, evidence, investigation tasks, adjudication, residual
uncertainty, and delivery obligation. Contradicting prose alone does not resolve
a Research Dispute.

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

### Research Projection

A user-facing view derived from Research Run state and events. The existing
canvas, process cards, presence, source list, and report are projections. A
projection may be rebuilt without changing canonical task or evidence state.
