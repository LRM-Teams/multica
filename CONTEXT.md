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

### Research Projection

A user-facing view derived from Research Run state and events. The existing
canvas, process cards, presence, source list, and report are projections. A
projection may be rebuilt without changing canonical task or evidence state.
