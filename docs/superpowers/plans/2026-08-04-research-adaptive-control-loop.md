# Research adaptive control loop implementation record

Date: 2026-08-04

Branch: `feat/research-adaptive-control-loop`

Target: turn delivery-gate observations into the smallest typed remediation task without discarding valid plan or evidence state.

## Design boundary

- The delivery gate is the observation/evaluation boundary. It reports durable finding codes and target metadata; it does not mutate the graph.
- The runtime router maps findings to one of five actions: explore, verify, counter-search, synthesize/audit, or replan.
- Replan is reserved for an invalid research method or task graph. Evidence and report defects must not increment `plan_version`.
- A question-scoped remediation must carry the durable `research_question.id`; prose that happens to mention a question is not a binding.
- Every system routing decision must be written to `research_decision`, including the observed findings, selected action, target, task id, and rationale.
- Existing active runs remain on their pinned result/prompt contract. This change only corrects runtime control routing and task targeting.

## Completed steps

- [x] Inspected the current Observe → Evaluate → Remediate path, gate finding vocabulary, task schema, decision ledger, and integration fixtures.
- [x] Identified the root cause of repeated/invalid nodes: most evidence and report findings are routed to `replan`, which increments the plan version and obsoletes otherwise valid current-plan work.
- [x] Identified a second correctness defect: `CreateControlTask` cannot bind a remediation task to a question and deduplicates only by task kind.
- [x] Identified an observability defect: `control_task_created` emits an event but does not preserve the routing decision in `research_decision`.

## Implementation checklist

- [x] Add a typed control-task input carrying findings and optional question target.
- [x] Add the highest-priority unanswered required question to gate metadata.
- [x] Implement deterministic finding precedence and typed action routing.
- [x] Persist question binding, acceptance criteria, event payload, and remediation routing decision atomically.
- [x] Make active-task reuse target-aware.
- [x] Add unit and PostgreSQL regression tests, including invalid/stale/cross-session target rejection.
- [x] Update the research runtime contract documentation and built-in skill source map where behavior is user/Agent visible.
- [x] Run focused Go tests with the worktree database, then broader package checks.

## Bugs encountered

- The first typed router classified `tasks_incomplete` as a structural defect. A real Gate snapshot can contain that finding while planned tasks are merely ready/running; treating it as structural would recreate the old replan churn. Removed it from replan precedence. Runtime reconciliation already waits for active work before evaluating remediation.
- The first test command sourced the worktree environment but did not map `DATABASE_URL` to the integration suite's `TEST_DATABASE_URL`; tests therefore skipped instead of proving PostgreSQL behavior. Re-ran every persistence test with `TEST_DATABASE_URL="$DATABASE_URL"` and recorded only those results below.
- The old task-budget check ran before active control-task reuse. An idempotent retry at the task limit could fail with budget exhaustion even though no new task was needed. Active exact-target reuse now occurs before the budget check.
- Gate aggregate and targeted-question reads can observe a concurrent accepted result at different instants. If the fresh targeted read finds no unanswered question, the stale aggregate finding is suppressed; if the target changes before task creation, the Store returns `ErrControlTargetChanged` and the Engine schedules reconciliation instead of failing the run.
- The first typed task objective embedded every simultaneous Gate finding. A question or Claim task could therefore be instructed about unrelated report/audit defects it cannot legally return. The Decision now retains the full Gate observation while task objective/acceptance criteria contain only the selected defect; Claim verification and counter-search assign one deterministic Claim defect per task.

## Verification log

- `go test ./internal/researchrun -run 'TestRemediation|TestTerminal' -count=1` — passed.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/researchrun -run 'TestControlTaskBindsGateQuestionAndRecordsRoutingDecision|TestCreateControlTaskDoesNotReuseSucceededDeliveryTask|TestPostgresStorePersistsCurrentResearchMethodAcrossReplan|TestPostgresStoreRunsFromPlanThroughConfirmedDelivery' -count=1` — passed against the isolated worktree PostgreSQL database.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/researchrun -count=1` — passed against the isolated worktree PostgreSQL database.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/handler ./internal/metrics -count=1` — passed.
- `TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/service -run 'Research|BuiltinSkill' -count=1` — passed.
- `go vet ./internal/researchrun ./internal/handler ./internal/metrics` — passed.
- `git diff --check` — passed.
