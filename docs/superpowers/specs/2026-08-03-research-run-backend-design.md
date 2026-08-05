# Production Research Run backend

Status: implementation authorized

Date: 2026-08-03

Owner: Codex

## 1. Decision

The Research module will execute every session as a durable Research Run. The
server owns progress, scheduling, retry, recovery, evidence integrity, quality
gates, and stopping. Agents propose plans, perform delegated work, and return
structured results; Agent chat is an interaction surface and no longer the
execution record.

This is the production contract. There is no reduced demo path or separate
later reliability delivery. Implementation order exists only to make schema,
runtime, and deployment changes reviewable.

## 2. Product distinction

Ordinary multi-Agent chat is message-driven coordination. Its canonical record
is a conversation; assignments, completion, evidence, and recovery are natural
language conventions.

A Research Run is state-driven execution. Its canonical records are the
Research Contract, Task Ledger, Progress Ledger, Evidence Ledger, Decision Log,
and report revisions. Chat remains available for user questions and visible
progress, but messages cannot advance tasks or satisfy quality gates by
themselves.

The distinction is enforced by structure:

| Concern | Multi-Agent chat | Research Run |
| --- | --- | --- |
| Trigger | message or mention | ready task, new evidence, gap, failure, user steering |
| Assignment | prose | persisted task plus attempt |
| Progress | inferred from chat | persisted status and dependency transitions |
| Exploration | participant discretion | ranked Research Frontier and replanning |
| Evidence | links in messages | claims, observations, snapshots, typed evidence links |
| Completion | coordinator assertion | deterministic gates plus independent quality review |
| Recovery | reconstruct from history | lease, checkpoint, idempotent result, reconciliation |
| Output | final message | versioned report with evidence provenance and known gaps |

## 3. Invariants

1. The database is authoritative for Research Run progress.
2. Only a human may change the Research Contract.
3. Every Research Task has one goal version and one plan version.
4. Every dispatch creates a Research Task Attempt linked to one canonical Agent
   inbox task.
5. Result ingestion is idempotent and rejects an Agent that is not assigned to
   the attempt.
6. Agent-proposed tasks are inert until the server validates and persists them.
7. A quoted Observation must exist in its Source Snapshot.
8. A Claim cannot count as supported without an Evidence Link.
9. Exact duplicate sources and the same independence family cannot satisfy an
   independent-source gate twice.
10. A running process may die after any database commit. Reconciliation must
    resume without duplicating a task, result, source, claim, report, or event.
11. Pause and cancel prevent new dispatches. Cancel and delete terminate active
    Agent inbox tasks before removing canonical Research Run state.
12. A deploy does not change the orchestrator or prompt version of an already
    running Research Run.
13. Research Projections never control task or evidence state.

## 4. Deep Module

`ResearchRun` is the deep Module. Its external Interface is limited to these
business operations:

- start a run for an existing Research Session;
- submit a structured task result;
- steer the Research Contract;
- pause, resume, cancel, or reconcile a run;
- obtain a canonical run snapshot;
- project committed run events to existing product surfaces.

The Implementation owns task readiness, dependency transitions, Agent
selection, attempt creation, result validation, evidence ingestion, replanning,
quality evaluation, stopping, retry, and recovery. Callers do not manipulate
individual task statuses.

The main seams are:

- a persistence seam with PostgreSQL and in-memory test Adapters;
- an execution seam whose production Adapter uses the existing `TaskService`
  and Agent inbox runtime;
- a projection seam whose production Adapter writes the existing research
  graph, messages, sources, reports, realtime events, and metrics;
- a clock seam for deterministic lease and recovery tests.

These seams have at least two Adapters where behavior genuinely varies. Internal
helpers do not become public seams merely to make unit tests easier.

## 5. Persistent model

`research_session` remains the user-facing root and gains Research Run control
fields:

- `goal_version`, `plan_version`, `state_version`;
- immutable `orchestrator_version` for the active run;
- validated `run_config` and accumulated `run_stats`;
- `last_progress_at`, `next_reconcile_at`;
- reconciliation lease token and expiry;
- bounded `stop_reason` and `last_error`.

The Research Contract history is append-only. Updating the current goal also
inserts a contract revision in the same transaction.

New canonical tables:

### `research_contract_revision`

Stores goal version, goal, scope, audience, freshness, language, source policy,
run limits, author, reason, and timestamp.

### `research_question`

Stores parent question, kind, text, required flag, status, priority, impact,
uncertainty, novelty, coverage, goal version, plan version, answer claim, and
terminal explanation.

Question kinds include `dimension`, `hypothesis`, `contradiction`, `gap`, and
`follow_up`. Statuses are `open`, `in_progress`, `answered`, `unresolved`, and
`obsolete`.

### `research_task`

Stores task kind, objective, required capability, expected result kind,
acceptance criteria, priority, status, assigned Agent, goal/plan version,
attempt limit, timeout, readiness time, terminal reason,
and timestamps.

Task kinds are `plan`, `discover`, `deep_read`, `verify`, `counter_search`,
`replan`, `synthesize`, `quality_gate`, and `citation_audit`.

Statuses are `pending`, `ready`, `dispatching`, `running`, `succeeded`,
`failed`, `blocked`, `obsolete`, and `cancelled`.

### `research_task_dependency`

Stores prerequisite edges. A database constraint prevents self-dependency;
the Module rejects cycles before committing a plan.

### `research_task_attempt`

Stores attempt number, immutable assigned Agent UUID, canonical inbox task,
idempotency key, status, result hash, failure class, timing, and bounded
diagnostics. The assigned UUID is an attribution value rather than an Agent
foreign key, so permanent Agent deletion cannot erase or block the audit
ledger. Only one active attempt is permitted per Research Task.

### `research_source_snapshot`

Stores canonical URL, title, publisher, source class, independence key,
retrieved time, bounded snapshot text, content hash, metadata, and verification
state. Exact source snapshots are unique within a session.

### `research_observation`

Stores source snapshot, producing task, quote or datum, locator,
interpretation, content hash, and verification state.

### `research_claim`

Stores atomic claim text, significance, confidence, status, producing task,
goal/plan version, resolution, and timestamps. Statuses are `proposed`,
`supported`, `disputed`, `refuted`, `superseded`, and `unresolved`.

### `research_claim_evidence`

Stores supports/contradicts relation, strength, verifier, verification state,
and rationale. The claim/observation/relation tuple is unique.

### `research_decision`

Stores decision kind, actor, plan version, inputs, outcome, rationale, and
timestamp. It is append-only. A `research_method` Decision stores the accepted
method for one goal/plan version: decision question, rationale, analysis
methods, evidence requirements, inclusion/exclusion criteria, source strategy,
counterevidence strategy, stopping conditions, uncertainties, risks, and
server-assigned task/Agent attribution.

### `research_report`

The existing report revision table gains goal and plan versions. Delivery
gates and independent evaluations select only the newest report for the
current versions; older reports remain readable history after steering or
replanning.

### `research_report_claim`

Links a report revision and section to the Claims it presents. This keeps the
existing report delivery schema readable while giving the server a normalized
quality-check surface.

### `research_run_event`

Stores append-only canonical events with a per-run sequence and idempotency key.
Projection state can be rebuilt from these events.

## 6. Execution flow

### 6.1 Start

Creating a Research Session atomically creates:

1. the visible session row with pinned orchestrator version and run limits;
2. contract revision 1;
3. the root required Research Question;
4. one ready `plan` task assigned to the lead capability;
5. a `run_started` event.

Dispatch and canvas projection happen after commit. Both are recoverable from the
durable task and event when the request or process fails after the transaction.

Cancellation is also durable. Cancelled Attempts remain pending until their inbox
tasks are found by inbox ID or dispatch key, cancelled idempotently, and marked with
`cancellation_completed_at`. The scheduler continues selecting runs with unfinished
cancellations, including paused, failed, and cancelled runs.

Only the lead is dispatched. Other fleet members remain idle until a ready task
requires their capability.

### 6.2 Plan

The planner returns a structured plan containing:

- diverse Research Questions;
- tasks with stable client keys;
- task dependencies by client key;
- capability and expected result for every task;
- a decision question and rationale for the chosen method;
- analysis methods and evidence requirements selected for that question;
- inclusion and exclusion criteria;
- source strategy;
- counterevidence strategy and stopping conditions;
- stated uncertainties and planning risks.

The server validates limits, keys, dependencies, allowed task kinds, required
questions, method completeness, and acyclicity before materializing a new plan
version. Acceptance writes the Method, Questions, Tasks, and result in one
transaction. Every later task reads the current Method from the canonical
snapshot. A task may request replanning when evidence invalidates the Method;
it cannot silently replace scope, analysis, evidence, or stopping rules.

### 6.3 Dispatch

The scheduler selects ready tasks by planner-assigned information-gain priority,
then readiness age and stable identity. A capability is the normalized role of
an active fleet member:

| Capability | Default role |
| --- | --- |
| `lead` | lead |
| `scout` | scout |
| `reader` | reader |
| `validator` | validator |
| `reporter` | reporter |
| a normalized specialty such as `patent_scout` | the active member with that exact role |

The server rejects a proposed task graph whose capabilities do not exist in the
active fleet. The lead may hire, configure, and activate a specialist for a
documented capability gap, then retry the same structured result. Per-run and
per-Agent concurrency limits are enforced before dispatch.

Dispatch follows an outbox-shaped transition:

1. lock the run and task;
2. create a `dispatching` attempt with deterministic idempotency key;
3. commit;
4. call the execution Adapter;
5. attach the canonical inbox task and mark the attempt `running`;
6. on uncertainty, reconciliation resolves by idempotency key before retrying.

### 6.4 Structured result

Agents submit one result envelope for the assigned attempt:

```json
{
  "schema_version": 1,
  "client_request_id": "stable-id",
  "summary": "bounded summary",
  "questions": [
    {
      "client_key": "q-market-risk",
      "kind": "gap",
      "text": "...",
      "required": true,
      "impact": 0.9,
      "uncertainty": 0.7,
      "novelty": 0.8
    }
  ],
  "sources": [
    {
      "client_key": "src-1",
      "url": "https://example.com/original",
      "title": "...",
      "publisher": "...",
      "source_class": "primary",
      "independence_key": "publisher:document",
      "retrieved_at": "RFC3339",
      "snapshot_text": "bounded retrieved text",
      "metadata": {}
    }
  ],
  "observations": [
    {
      "client_key": "obs-1",
      "source_key": "src-1",
      "quote": "exact text present in snapshot_text",
      "locator": "section or page",
      "interpretation": "..."
    }
  ],
  "claims": [
    {
      "client_key": "claim-1",
      "text": "one atomic proposition",
      "significance": "high",
      "confidence": 0.75,
      "evidence": [
        {
          "observation_key": "obs-1",
          "relation": "supports",
          "strength": 0.8,
          "rationale": "..."
        }
      ]
    }
  ],
  "proposed_tasks": [],
  "report": null,
  "evaluation": null,
  "coverage_delta": 0.2,
  "confidence": 0.75,
  "incomplete_reason": null
}
```

Limits apply to every string, collection, snapshot, and envelope. The server
canonicalizes URLs, hashes source and result content, validates references, and
inserts the complete result in one transaction. A repeated
`client_request_id` with the same hash returns the original result; a different
hash returns conflict.

`research-run-v2` keeps the existing report reader's
`structured.schema_version = 1`, but upgrades the task-result envelope to
`schema_version = 2`. Question-scoped evidence that increases coverage must
identify `answer_claim_key`. Synthesis must submit the full outline, sections,
citations, source snapshot, conclusion, and
`report.claims[{claim_key, section_id, anchor_quote}]`; section prose,
conclusion, and each anchor must occur exactly in `content_md`. A report Claim
is accepted only when the section cites a `research_source.id` that resolves
through verified Evidence, Observation, and Source Snapshot support for that
Claim.

The v2 delivery graph fixes `synthesize` to `reporter` and both
`quality_gate` and `citation_audit` to `validator`; both audit tasks depend on
synthesis. Evaluations include a substantive rationale for all seven rubric
dimensions plus `reviewed_claim_keys` and `reviewed_section_ids`. Persistence
rejects an evaluation from the report author's Agent ID or a review set that
does not exactly cover the latest report.

`research-run-v3` retains all v2 evidence, report, author-attribution, and
independent-review rules and upgrades the task-result envelope to
`schema_version = 3`. Plan and replan results additionally require
`plan.method` with a decision question, method rationale, analysis methods,
evidence requirements, counterevidence strategy, and stopping conditions. The
existing inclusion/exclusion criteria, source strategy, uncertainties, and
planning risks are non-empty parts of the same accepted Research Method.

The Method is domain-neutral. The planner selects comparison, measurement,
mechanism analysis, time series, case analysis, fact checking, risk analysis,
or a justified combination according to the Research Contract. Academic
protocols such as FINER, IRB, PRISMA, CONSORT, STROBE, journal formats, and
publication-specific identifiers apply only when the goal requires them.

### 6.5 Adaptive loop

After each accepted result, the Module:

1. updates Question coverage and Claim states;
2. recalculates the Research Frontier;
3. activates dependency-ready tasks;
4. materializes valid proposed follow-up tasks;
5. routes the highest-priority unmet Gate finding to the smallest typed task:
   question-bound `discover`, Claim-targeted `verify` or `counter_search`, report
   `synthesize`, or independent quality/citation audit;
6. creates a versioned `replan` only when the question, scope, accepted Method,
   evidence standards, or executable task graph is invalidated;
7. records every routing choice, finding set, target, task, and rationale in the
   Decision Log atomically with control-task creation;
8. evaluates stopping against the accepted stopping conditions and deterministic
   evidence gates only when no higher-priority work is runnable.

Planner-assigned frontier priority may use impact, uncertainty, novelty,
missing coverage, contradiction severity, expected information gain, and
estimated cost. Task priority is persisted; gate, replan, exhaustion, and
delivery decisions are persisted in Research Decisions.

The server ranks unresolved required Questions deterministically from priority,
impact, uncertainty, novelty, missing coverage, and a contradiction/gap boost.
The selected Question ID, score, answer Claim, verified-support state, and
incomplete reasons are part of the Gate finding. An existing answer without
verified support routes to verification; a missing or incomplete answer routes
to discovery.

### 6.6 Wide, deep, adversarial, synthesis

The planner begins with independent perspectives and broad discovery. Strong
sources produce `deep_read` tasks. High-significance Claims produce independent
`verify` or `counter_search` tasks. Non-delivery synthesis may run incrementally
after useful evidence batches; the delivery synthesis is transitively downstream
of every planned evidence task. Unsupported report sections generate new frontier
items. V4 required follow-up Questions must include question-bound verification.
Evidence-result follow-up tasks depend on their producer; dynamic evidence and
replan work also become dependencies of pending delivery synthesis.

## 7. Quality gates

Deterministic gates reject delivery when:

- a required Research Question is still open or in progress;
- a required Research Question's answer Claim lacks verified support;
- a current V4 required/report Claim fails its referenced EvidenceStandard's
  source-trait, independence, strength, directness, method-fit, or counter-search
  requirement (legacy versions retain their pinned independent-source rubric);
- a disputed Claim is presented without an explicit unresolved marker;
- an Observation quote is absent from its Source Snapshot;
- a report Claim link does not resolve through verified Evidence and an
  Observation to a stored Source Snapshot;
- a required Research Question's answer Claim is absent from the latest
  report;
- the latest report is structurally incomplete, contains placeholder prose,
  or lacks durable author attribution;
- the latest report predates a later Information Gain Decision whose canonical
  graph state actually changed;
- a report presents a high-significance Claim without a report-claim link;
- a running, ready, dispatching, or retryable task remains;
- result, task, or source limits were bypassed;
- the citation audit did not pass.

For v2, the report stores its producing task, attempt, and author. Quality and
citation decisions store their reviewer actor. A passing score from the report
author is rejected, and a passing evaluation must enumerate every Claim and
section in the latest revision. A failed quality review schedules a new
`synthesize` task and report revision; a succeeded delivery task is never
reused as the remediation task.

A `quality_gate` task is assigned to a verifier that did not author the report.
It scores factual grounding, coverage, analytical depth, source quality,
contradiction handling, instruction adherence, and readability against a rubric
derived from the Research Contract. Every score must meet the depth-tier floor
(`0.65` shallow, `0.75` standard, `0.80` deep). The model evaluation cannot
override a deterministic failure.

Passing gates moves the session to `awaiting_user_confirm`. Human confirmation
moves it to `completed`; rejection creates a new contract-preserving replan and
returns it to `running`.

## 8. Stop conditions and budgets

The Module may propose delivery when required coverage, evidence, validation,
and citation gates pass and marginal information gain has remained below the
configured threshold for consecutive completed work batches.

Marginal gain is calculated from the current goal/plan's canonical evidence
graph before and after each accepted evidence result. Verified answer coverage,
answer transitions, independent verified evidence, verified Evidence Links,
counterevidence, Claim resolution, and verified Claim adjudication carry most of
the score. New raw graph entities contribute diminishing novelty as the graph
grows. Duplicate content, Agent confidence, and unverified self-reported coverage
do not create measured gain. The complete calculation and canonical-change flag
are stored as an `information_gain` Decision.

Hard limits exist to prevent runaway autonomous execution: total tasks,
attempts per task, parallel tasks, task and run wall time, result payload bytes,
and source snapshot bytes. Reaching a limit creates an explicit `budget_exhausted`
decision. The run may deliver a visibly incomplete report only after hard
evidence checks pass and all missing scope is listed; it may not silently claim
completion.

Depth tier selects default limits. Operators may change defaults for new runs;
the resolved run config is immutable for an active run unless a human performs
an audited steering action.

## 9. Human steering

Material steering uses a typed operation, not a magic chat phrase. The request
contains the new goal or constraints, reason, and whether active work should be
allowed to finish.

The transaction:

1. locks the Research Run;
2. inserts the next Research Contract revision;
3. increments goal and plan versions;
4. marks pending/ready work from the old goal obsolete;
5. records whether running attempts remain admissible or become obsolete;
6. preserves accepted evidence with its original version;
7. creates a replan task;
8. emits a goal-steered event and projection.

Ordinary chat remains ordinary chat. An Agent may propose a goal change, but
cannot execute it.

## 10. Retry and recovery

Reconciliation is both event-driven and periodic. The periodic job claims due
Research Runs with a lease so multiple server replicas may scan safely.

For every active attempt it compares Research Task state with canonical Agent
inbox state:

- queued/running inbox task: renew progress view;
- completed with accepted result: finalize success;
- completed without accepted result: fail as `missing_structured_result`;
- retryable failure: create the next attempt after backoff;
- non-retryable or exhausted failure: block the task and schedule replanning;
- missing inbox task after uncertain dispatch: resolve by idempotency key or
  create a new attempt;
- obsolete goal version: retain the result for audit but exclude it from
  current coverage until revalidated.

Transitions serialize on the session row, increment `state_version`, and use
unique idempotency keys for side effects. Reconciliation leases are claimed
with a conditional update, so reconciliation is safe to repeat after a crash.

## 11. Projections and existing product surfaces

Canonical Research Run events project to existing tables and realtime events:

- questions and tasks create/update canvas nodes;
- accepted sources update the existing source panel;
- Claims create finding, conflict, refuted, pivot, or dead-end nodes;
- attempts update presence and process cards;
- synthesis creates ordinary report revisions;
- gate outcomes create stage and round cards;
- status transitions use the existing session status event.

Direct graph/source/report commands remain available as human-visible research
notes, but they do not satisfy task completion or quality gates. Fleet task
instructions use structured result submission as the canonical write path.

## 12. Authorization and data safety

- Human session routes require current workspace membership.
- Agent task-result routes require an Agent principal, active fleet membership,
  attempt assignment, matching workspace, and non-terminal run.
- Lead privileges do not permit changing the Research Contract.
- URLs accept only `http` and `https` schemes.
- Result bodies, snapshot text, metadata, diagnostics, and report content have
  explicit byte and item limits.
- Stored snapshots are bounded excerpts or licensed internal content; the
  system does not copy unrestricted full web pages by default.
- Logs and metrics record identifiers, states, durations, and counts, not raw
  source text or private report content.

## 13. Observability

The bounded-cardinality Prometheus sampler exposes:

- active runs by state and orchestrator version;
- tasks by kind/status and attempts by status/normalized failure class;
- source snapshots, observations, and Claim Evidence Links by verification
  status;
- current unresolved Research Frontier size and oldest-item age;
- projection backlog size and oldest-event age;
- pending inbox-cancellation count and oldest pending age;
- sampler query duration and error counters.

The Decision Log and ordered run-event sequence are the per-run trace for
steering, dispatch, retry, result acceptance, Gate observations, typed
remediation routing, budget exhaustion, and terminal transitions. Task and
Attempt timestamps retain dispatch/start/result
latency for SQL diagnostics without adding per-run Prometheus labels. Projection
errors and retry counters remain on each event; projection failure never rolls
back canonical accepted evidence.

## 14. Deployment and compatibility

The schema change is additive. Existing Research Sessions receive a contract
revision 1 backfill but retain a null `run_initialized_at`, so their existing
legacy execution path continues unchanged. The metrics Adapter reports those
sessions as `legacy`. The server does not silently convert an in-progress legacy
session into a Research Run. New sessions initialize the durable task/evidence
ledgers and use `research-run-v4`.

Old desktop clients continue consuming the existing session snapshot fields.
New response fields are additive and schema-parsed with defaults. The existing
report `content_md` remains readable even when new provenance fields are
present.

Running Research Runs retain their orchestrator, result schema, prompt, and
gate rubric versions across deploys. New server code must continue processing
those versions until no active run references them.

Existing `research-run-v1` through `research-run-v3` runs remain on their pinned
prompts, result contracts, and versioned Gate rules. They are not silently
rewritten or re-evaluated under v4. Existing
HTTP and WebSocket report response shapes are unchanged; author, task,
attempt, and report-claim anchors are internal provenance fields.

## 15. Verification contract

Tests cross the ResearchRun Interface and its production persistence Adapter.

The repository test suite covers:

- plan validation, dependency ordering, and cycle rejection;
- v3/v4 method and EvidenceStandard validation, persistence, task-context
  inheritance, replan history, and v1/v2 compatibility;
- typed remediation precedence, question-target binding, routing Decision
  persistence, target-aware idempotency, and stale/cross-session target rejection;
- capability routing and concurrency limits;
- duplicate dispatch and duplicate result replay;
- quote/snapshot, claim/evidence, independence, and citation validation;
- goal steering with stale in-flight results;
- retryable, non-retryable, missing-result, timeout, and exhausted attempts;
- terminal-run reconciliation leases and projection idempotency;
- initialized-run guards on legacy mutation routes;
- malformed network responses for added frontend fields;
- a PostgreSQL-backed end-to-end run from initialization through user
  confirmation;
- bounded metric labels, sampler caching, and database-hang isolation.

Release qualification must additionally run representative Multica research
requests across technical, academic, product, market, policy, and mixed
internal/external research. Every orchestrator or prompt change is compared
against ordinary multi-Agent chat using the same models, tools, and run limits.
The evaluation record includes coverage, supported-claim ratio, citation
correctness, source independence, contradictions found/resolved, useful novel
findings, cost, latency, and recovery success. This is an operational evaluation
requirement; unit and persistence tests cannot substitute for model-output
quality measurement.

## 16. Rejected designs

### Prompt-only orchestration

Rejected because assignments, completion, evidence, and retry remain
unobservable server state. It cannot satisfy crash recovery or idempotency.

### Fixed wake of every fleet member

Rejected because it creates duplicate work, consumes budget without a task,
and makes Agent count rather than information need control execution.

### Source-count and report-length gates

Rejected because self-assigned credibility and text length do not establish
claim support, citation accuracy, or coverage.

V2 applies depth-tier section and conclusion character floors only as an
anti-placeholder precondition. Delivery still depends on answer-Claim
coverage, exact Claim-to-prose anchors, verified cited support, contradiction
handling, and independent full-surface review; passing the length floor alone
cannot satisfy any gate.

### A second Agent runtime

Rejected because the canonical Agent inbox already provides identity,
execution, retries, runtime selection, cancellation, and observability. Research
adds an execution Adapter and domain state, not another wake protocol.

### Canvas as canonical execution state

Rejected because canvas nodes optimize human comprehension, allow lossy
summaries, and lack task attempt and evidence invariants. The canvas remains a
Research Projection.
