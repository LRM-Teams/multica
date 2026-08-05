# Research evaluation-feedback implementation record

Date: 2026-08-05

Branch: `feat/research-evaluation-feedback`

Target: turn independent quality/citation evaluations into bounded, task-consumable feedback instead of reducing them to a pass count and one threshold.

## Diagnosis

- [x] `materializeEvaluation` durably stores all dimension scores, dimension rationales, findings, reviewed Claim keys, reviewed section IDs, reviewer, and report ID.
- [x] `EvaluateGate` reads those Decisions only as aggregate pass/fail counts. Failed Gate findings retain only `minimum_score`.
- [x] `remediationTask` therefore sends a synthesis revision the failure code but not the reviewer findings, failed dimensions, target Claims/sections, or report revision ID.
- [x] This is user-reachable: a revision Agent can repeat the same report because the durable reviewer explanation is absent from its objective and acceptance criteria.

## Design boundary

- Keep the pinned evaluation result envelope; the missing behavior is server-side Decision projection.
- Gate metadata carries a bounded deterministic projection: report/evaluation ID, failed dimensions with score and rationale, explicit findings, reviewed Claims, and reviewed sections.
- Remediation continues to choose the smallest action from deterministic Gate precedence. The selected task receives the structured feedback through its existing finding/objective/acceptance-criteria path.
- Agent metadata is not copied wholesale. Every string/list is bounded before it enters a control-task prompt.

## Checklist

- [x] Project latest quality and citation evaluation feedback into failed Gate findings.
- [x] Preserve deterministic dimension ordering and bound prompt-facing data.
- [x] Prove remediation objectives contain the actionable reviewer evidence.
- [x] Add PostgreSQL regression for the durable Decision → Gate path.
- [x] Update backend contract, built-in skill/source map, and verification log.

## Implementation

- `EvaluateGate` now reads the latest matching quality and citation Decisions,
  including their durable Decision ID, report ID, reviewer ID, and outcome.
- A failed evaluation is projected into a deterministic finding payload. Failed
  dimensions follow the server rubric order. Prompt-facing rationales, findings,
  Claim keys, and section IDs have explicit item and byte limits.
- Existing typed remediation selection remains authoritative. Its synthesis
  task objective and acceptance criteria now contain the reviewer evidence, so
  no parallel control API or generic rewrite path was added.
- V4 evaluator instructions require findings to identify affected Claims and
  sections. V4 reporter instructions require item-by-item repair. V1–V3 prompt
  bytes remain unchanged because those orchestrator versions are replay-pinned.
- The built-in fleet skill, source map, backend specification, and engineering
  invariant now describe the same Decision → Gate → revision behavior.

## Bugs found while implementing

- The first prompt edit changed V2 and V3 text. `TestTaskPromptV2CarriesAndPinsReportQualityContract`
  would have rejected it because historical orchestrator prompts are hashed.
  The edit was removed from V2/V3 and retained only in V4; the server-side
  feedback projection remains available to all structured runs through task
  objective and acceptance criteria.
- A focused handler test was initially invoked without `.env.worktree` during
  diagnosis and reached the developer's default database, whose migration 204
  state was already inconsistent; it did not reach the feature assertion. All
  database-backed validation below sources `.env.worktree` and supplies its URL
  as `TEST_DATABASE_URL`.
- The first end-to-end review found that `gateObjective` carried the projected
  findings but `CreateControlTask` persisted only finding codes in
  `acceptance_criteria`. This violated the design contract and made detailed
  feedback prose-only. `target_findings` is now persisted in the task criteria,
  and the PostgreSQL regression reads it back from the created revision task.

## Verification

- [x] Focused prompt, metadata, remediation, quality/citation PostgreSQL, and
  end-to-end delivery regressions:
  `go test ./internal/researchrun -run 'TestTaskPromptV[234]|TestEvaluationFeedbackMetadata|TestEvaluateGateProjectsActionableEvaluationFeedback|TestPostgresStoreRunsFromPlanThroughConfirmedDelivery|TestRemediation|TestCreateControlTask' -count=1`
- [x] Full research-run package with isolated PostgreSQL:
  `go test ./internal/researchrun -count=1`
- [x] Built-in research skill/service tests:
  `go test ./internal/service -run 'Research|BuiltinSkill' -count=1`
- [x] Adjacent handler and metrics tests:
  `go test ./internal/handler ./internal/metrics -count=1`
- [x] Static analysis:
  `go vet ./internal/researchrun ./internal/handler ./internal/metrics`
- [x] Whitespace validation: `git diff --check`
