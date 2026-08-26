# Adaptive Goal and Work Graph Specification

## 1. Design intent

Multica should support long-running, multi-turn problems without hard-coding
business scenarios into the domain model. A user may ask the system to build,
research, reproduce, analyse, plan, debug, or solve something difficult. These
are examples of tasks, not separate product types.

The system MUST provide one generic execution model:

```text
Goal → understand → plan → execute → inspect → verify → replan → deliver
```

The model is adaptive. The initial plan is a hypothesis, not a permanent
workflow. New information, failures, user feedback, and verification results
may change the plan while preserving the history of what happened.

## 2. Non-goals

- Do not create a hard-coded `PaperGoal`, `ResearchGoal`, `GameGoal`, or
  `CodingGoal` hierarchy.
- Do not encode a fixed sequence for a particular domain.
- Do not make the Goal object contain every execution, tool, RL, or sandbox
  field.
- Do not treat a natural-language completion claim as proof that a goal is
  achieved.

Domain-specific behaviour belongs in user-provided context, capabilities,
tools, acceptance criteria, and optional workflow policies.

## 3. Core concepts

```text
Goal          the desired outcome and current requirements
GoalRevision  an immutable version of the requirements
WorkGraph     the current executable plan
GraphRevision an immutable version of that plan
WorkNode      one unit of work in the plan
AgentRun      one concrete execution attempt for a node
Artifact      a file, result, report, dataset, code change, or other output
Verification  evidence that a node, artifact, or goal satisfies requirements
Replan        a new graph revision created after observation or change
```

The most important separation is:

```text
Goal       = why / what outcome
WorkGraph  = current hypothesis about how to achieve it
AgentRun   = what actually happened this time
Artifact   = what was produced
Verification = whether it is acceptable
```

## 4. Goal model

A Goal is a durable container for a long-running task. It must remain useful
whether the task is completed in one run or many runs.

```ts
type Goal = {
  id: string
  workspaceId: string
  currentRevisionId: string
  status: GoalStatus
  createdBy: Actor
  createdAt: string
  updatedAt: string
}
```

The requirements live in immutable revisions:

```ts
type GoalRevision = {
  id: string
  goalId: string
  revision: number
  objective: string
  context: Reference[]
  constraints: Constraint[]
  acceptanceCriteria: AcceptanceCriterion[]
  deliverables: DeliverableSpec[]
  priority?: string
  changeReason?: string
  createdBy: Actor
  createdAt: string
}
```

`objective`, `context`, and `constraints` are intentionally untyped beyond
common references. A user can attach a paper, repository, spreadsheet, image,
dataset, API contract, or plain text without changing the Goal schema.

## 5. Goal updates

Users MUST be able to update an active Goal. An update creates a new
`GoalRevision`; it never overwrites the previous revision.

```text
Goal revision 1
        ↓ user feedback / new evidence
Goal revision 2
        ↓ another change
Goal revision 3
```

A revision may arrive while work is executing. It MUST be applied at the next
safe scheduling boundary, never by interrupting a run mid-flight:

- in-flight AgentRuns continue to completion (or their own timeout) unless the
  revision explicitly cancels their node;
- their reports are still collected and their artifacts registered, then
  evaluated against the new revision — work that still satisfies the new
  requirements is reused, not discarded;
- nodes not yet dispatched are re-evaluated against the new revision before
  they become ready.

After a revision is created, the planner performs impact analysis:

```text
unaffected nodes  → remain reusable
affected nodes    → become superseded or require re-verification
invalid artifacts → are marked stale
new requirements  → create new nodes
```

The system MUST preserve the mapping from each graph revision and run to the
GoalRevision that caused it. This enables audit, rollback, comparison, and
reproduction.

## 6. Work graph model

The WorkGraph is a versioned, mutable execution plan. It is not a fixed
workflow and not necessarily a simple DAG: implementations may use a DAG,
tree, or another dependency graph, provided dependencies are explicit and
cycles are rejected or handled deliberately.

```ts
type WorkNode = {
  id: string
  graphRevisionId: string
  title: string
  description: string
  dependencies: string[]
  requiredCapabilities: string[]
  inputRefs: Reference[]
  expectedOutputs: OutputSpec[]
  acceptanceCriteria: AcceptanceCriterion[]
  status: WorkNodeStatus
  assignedAgentId?: string
  retryPolicy?: RetryPolicy
  verificationPolicy?: VerificationPolicy
}
```

Node kinds are extensible labels, not scenario branches. A minimal built-in
vocabulary may include `understand`, `research`, `design`, `implement`,
`run`, `inspect`, `diagnose`, `revise`, `compare`, `verify`, and `deliver`,
but users and plugins may add kinds without changing Goal semantics.

Examples such as “paper reproduction” or “game development” are represented by
different graphs and capabilities, not different Goal classes.

## 7. Dynamic planning and replanning

The initial plan is generated from the current GoalRevision, available context,
capabilities, tools, and policies. The coordinator MUST be able to revise the
plan after every meaningful observation.

```text
observe state
  → select ready nodes
  → dispatch bounded work
  → collect reports and artifacts
  → inspect results
  → verify
  → create a new GraphRevision when needed
```

Replanning may be triggered by:

- a failed run or timeout;
- a verification failure;
- contradictory evidence;
- a missing capability or input;
- newly discovered dependencies;
- user feedback or GoalRevision;
- a successful result that unlocks new work.

The old GraphRevision remains immutable. The new revision records its parent,
reason, affected nodes, and reused artifacts.

### Budgets and bounded parallelism

Dispatch MUST be bounded by an explicit budget policy, not left implicit:

```ts
type ExecutionBudget = {
  maxConcurrentRuns: number
  maxRunsPerNode: number        // retry ceiling
  maxDelegationDepth: number    // a run that spawns sub-work counts against this
  maxTotalRuns?: number
  maxWallClock?: string
  maxCost?: number
}
```

Delegation multiplies: if a coordinator node spawns sub-agents that may spawn
their own work, effective concurrency approaches
`maxConcurrentRuns × spawn limit per level`. The scheduler enforces the budget
at dispatch time and records budget exhaustion as a `blocked` state with a
structured reason, so a stalled goal is diagnosable instead of silently idle.

## 8. Agent execution

An AgentRun is an immutable attempt to execute one WorkNode under a specific
context and runtime.

```ts
type AgentRun = {
  id: string
  nodeId: string
  parentRunId?: string
  agentId: string
  runtimeId?: string
  graphRevisionId: string
  goalRevisionId: string
  status: RunStatus
  inputSnapshot: Reference[]
  startedAt?: string
  finishedAt?: string
  failure?: FailureReport
}
```

Retrying a node creates another AgentRun. Runs MUST NOT overwrite one another.
This supports debugging, replay, cost analysis, and training-data export.

## 9. Reports, artifacts, and evidence

Every run should return a structured report rather than only free-form text.

```ts
type WorkReport = {
  runId: string
  status: "completed" | "blocked" | "failed"
  summary: string
  claims: Claim[]
  evidence: Reference[]
  artifacts: Reference[]
  uncertainties: string[]
  proposedNextActions: ActionProposal[]
  failure?: FailureReport
}
```

Claims, evidence, and artifacts must be traceable:

```text
GoalRevision → GraphRevision → WorkNode → AgentRun → Artifact / Evidence
```

This model works equally for source code, a research report, a trained model,
a dataset, an experiment result, or a design document.

## 10. Verification

Verification is a first-class operation and may be performed by code, tests,
rules, a separate Agent, a human, or a combination.

Verification operates at three scopes:

```text
node verification      does this unit satisfy its contract?
artifact verification  is this output valid and traceable?
goal verification      are all acceptance criteria satisfied?
```

An implementation Agent MUST NOT be the sole authority for its own completion
when independent verification is available.

Verification failures produce structured diagnostics and may create
`diagnose` or `revise` nodes. “Rethinking” is therefore an observable change in
the graph, not merely another paragraph of explanation.

## 11. Generic execution loop

```python
while not goal_is_terminal(goal):
    state = observe(goal)

    if user_changed_requirements(state):
        create_goal_revision(state)
        create_impacted_graph_revision(state)

    ready = scheduler.ready_nodes(state.graph)
    runs = dispatch(ready, bounded_by=state.policy)
    results = collect(runs)

    register_reports(results)
    register_artifacts(results)
    verdicts = verify(results, state.requirements)

    if verdicts.have_failures:
        create_diagnosis_and_revision_nodes(verdicts)

    if state.requires_replanning or verdicts.unlock_new_work:
        create_graph_revision(state, verdicts)

    if goal_acceptance_is_satisfied(state):
        finalize_goal(goal)
```

The loop is generic. Domain behaviour comes from the current GoalRevision,
graph nodes, tools, capabilities, and verification policies.

### Durability and resume

The loop itself must be restartable. Every iteration reads state from durable
storage (Goal, GraphRevision, WorkNode, AgentRun, reports, verdicts) and never
relies on coordinator process memory. If the coordinator crashes or is
redeployed:

- completed runs, reports, artifacts, and verdicts are already persisted;
- in-flight runs are detected by lease/heartbeat expiry and re-dispatched or
  failed according to the node's retry policy;
- the next loop iteration resumes from persisted state without replaying
  finished work.

This is a Phase 1 requirement, not an optimisation: the most common failure of
a long-running goal is interruption, and a system that cannot resume loses all
in-progress work exactly when the task is most expensive. Full run traces and
sandbox snapshot/restore (Phase 3) build on this but are not required for it.

## 12. User interaction

The UI should show the evolving result, not force users to understand every
internal Agent:

```text
Goal overview
├── current objective and revision
├── progress and current plan
├── completed artifacts
├── risks, blockers, and uncertainties
├── requests for user decisions
├── verification status
└── execution trace (expandable)
```

The system may pause in:

```text
waiting_user
needs_approval
blocked
waiting_external_resource
```

User feedback becomes a GoalRevision or an explicit decision record, not an
untracked chat-side mutation.

## 13. Extensibility

Scenario-specific behaviour MUST be implemented through extension points:

- capability registries;
- tool registries;
- node-kind plugins;
- prompt/policy profiles;
- verifier implementations;
- artifact adapters;
- runtime providers;
- optional domain templates.

For example, a paper-reproduction template can provide defaults for evidence,
experiments, and comparison, while a game template can provide build, runtime,
playtest, and asset capabilities. Both still use the same Goal, WorkGraph,
AgentRun, Artifact, and Verification primitives.

## 14. Learning and RL boundary

Training-specific concepts such as episode, branch, reward, advantage, and
trajectory belong to the execution/learning layer. They reference Goal,
GraphRevision, WorkNode, and AgentRun IDs but must not become required fields on
the user-facing Goal model.

```text
Product domain: Goal / WorkGraph / Artifact / Verification
Execution domain: Run / ToolCall / Runtime / Sandbox / Checkpoint
Learning domain: Episode / Branch / Reward / Trajectory
```

This separation keeps the product model generic while allowing Multica to
train and evaluate agent teams later.

## 15. Initial implementation plan

### Phase 1 — generic execution

- Goal and immutable GoalRevision
- WorkGraph and WorkNode
- AgentRun
- structured WorkReport
- Artifact references
- node and goal verification
- one dynamic replan loop
- durable loop state and coordinator resume (see “Durability and resume”)
- explicit execution budget enforced at dispatch

### Phase 2 — adaptive correction

- diagnose/revise node kinds
- retry policies
- blocked and waiting-user states
- GraphRevision and impact analysis
- artifact reuse and staleness
- safe-boundary application of mid-flight GoalRevisions

### Phase 3 — runtime and reproducibility

- run trace
- checkpoints
- replay
- sandbox snapshot/restore
- side-effect journaling so a superseded run's mutations can be reverted
- cost accounting and budget tuning

### Phase 4 — optional domain templates and learning

- installable workflow/capability templates
- benchmark adapters
- trajectory export
- branch/reward/learning integration

## 16. Current implementation mapping

Migration `455_channel_goal_revision_history` is the first persisted slice of
this spec. It is narrower than the ideal `GoalRevision` above, deliberately:

- `channel_goal_revision` is a trigger-maintained immutable snapshot of the
  existing `channel_goal` row, keyed by `(goal_id, version)`. It reuses the
  live table's `version` counter (bumped by every contract-changing UPDATE)
  rather than introducing a separate revision id.
- Snapshot semantics are first-write-wins per version (`ON CONFLICT DO
  NOTHING`): an UPDATE that does not bump `version` — for example a runtime
  counter such as `execution_graph_revision` — does not produce or alter a
  revision. Revisions capture the user-facing contract only.
- Fields from the ideal model that have no `channel_goal` counterpart yet
  (`changeReason`, `constraints`, `deliverables`, typed `context` references)
  are not stored. They are added when the write path can populate them —
  a trigger cannot invent a change reason.

The gap between this snapshot table and the full `GoalRevision` model is
tracked Phase 1 work, not an accident of the migration.

## 17. Acceptance criteria

The implementation is successful when a user can provide an arbitrary complex
request and the system can:

1. create a Goal without selecting a hard-coded domain type;
2. generate an initial WorkGraph from the request and available context;
3. execute independent nodes in parallel when safe;
4. pass structured reports and artifacts between nodes;
5. detect failures and create diagnosis/revision work;
6. accept user changes by creating a new GoalRevision;
7. reuse unaffected work while invalidating affected work;
8. verify outputs independently of the implementation Agent where possible;
9. resume from a checkpoint or retry a specific node;
10. produce a final deliverable with complete provenance.

The core promise is therefore:

```text
generic Goal
  + adaptive WorkGraph
  + persistent AgentRuns
  + verifiable Artifacts
  + user-driven revisions
  = long-running problem-solving system
```
