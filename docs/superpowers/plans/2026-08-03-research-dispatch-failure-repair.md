# Research dispatch failure repair record

This record is updated after each completed repair step. It distinguishes
production evidence, reproduced defects, implementation decisions, and
verification results.

## Scope

- Repair the production Research Run dispatch failure that leaves sessions in
  `s1_plan` while generating repeated failed attempts.
- Prevent a non-retryable dispatch defect from consuming the Research Run task
  budget through repeated replan tasks.
- Record the duplicated built-in template instructions as required frontend
  follow-up without changing the frontend package in this backend repair.
- Preserve the canonical Research Run / Agent inbox architecture and existing
  APIs.

## Completed steps

### 1. Isolated the repair from user work

- Fetched `origin/dev` and created branch
  `fix/research-dispatch-failure-loop` in an isolated worktree.
- Base commit: `8cacfef67` (`Merge pull request #1965`).
- Confirmed the isolated worktree was clean before editing.
- The user's existing dirty worktree and branch were not modified.

### 2. Captured the production failure before changing code

- Session: `b9a60bbc-815e-48ca-adb8-0d06092b8fca`.
- Orchestrator: `research-run-v2`; stage: `s1_plan`.
- At the last observation, 13 of 13 attempts had failed before Agent execution.
- Every attempt had failure class `dispatch_failed` and diagnostic
  `could not determine data type of parameter $2 (SQLSTATE 42P18)`.
- Canonical output remained empty: zero accepted results, sources,
  observations, claims, and reports.
- The scheduler had already blocked four plan/replan tasks and created a fifth,
  demonstrating that this infrastructure error is user-visible and repeatedly
  consumes task budget.
- A later production snapshot, after the user paused the run, showed the full
  impact: 25 tasks (24 blocked, one ready), 74 failed dispatch attempts, and
  174 graph nodes. The graph consisted of 74 `agent_activity`, 74 `dead_end`,
  and 24 `probe` nodes, but still had zero edges, sources, evaluations,
  messages, results, observations, claims, and reports. These are repeated
  failure artifacts, not research work.

### 3. Confirmed both code-level causes

- `researchRunDispatcher.Dispatch` passes untyped placeholders `$2` through
  `$6` to PostgreSQL `jsonb_build_object`; PostgreSQL has no argument type
  context and rejects `$2` while binding the statement.
- `researchrun.Engine.dispatchReady` marks every Adapter dispatch error as
  retryable. Exhaustion blocks the plan task, after which normal planning repair
  creates another replan task. A deterministic dispatch defect therefore loops
  until the global task/time budget is exhausted.
- The Chinese built-in prompts in
  `packages/views/research/lib/research-template-prompts.ts` contain the same
  sentence 11-13 times in source. This is exercised by the normal template
  creation path and the repeated text is present in the production session
  goal, so it is a user-facing defect rather than test setup noise.

### 4. Added regression feedback loops and observed all three fail

- A handler integration test calls the production Dispatcher against a fresh
  PostgreSQL database and reads the resulting canonical inbox context. Before
  the fix it fails with the same production error: `SQLSTATE 42P18` at `$2`.
- A Research Run integration test uses the real PostgreSQL Store and a
  Dispatcher error explicitly classified as non-retryable. Before the fix,
  `Engine.Start` incorrectly succeeds and leaves the run eligible for further
  remediation.
- A template unit test detects repeated substantive sentences in every built-in
  prompt. Before the fix, the industry prompt has 22 substantive sentence
  occurrences but only 12 unique sentences.
- The shared local database could not migrate from its historical state. This
  was isolated to test setup, not a product path; the regressions run against a
  dedicated empty database migrated from 001 through 277 instead of changing
  production code to accommodate the drift.

### 5. Implemented root fixes

- Added explicit PostgreSQL casts at the production `jsonb_build_object` call:
  dispatch/session/task/attempt identifiers are `text`, timeout is `integer`,
  and acceptance criteria remains `jsonb`.
- Added an Adapter-to-Engine retryability contract. Unknown and transient
  errors keep the existing bounded retry behavior; deterministic Adapter
  contract/configuration errors are explicitly non-retryable.
- PostgreSQL class 42 programming/contract errors and invalid fleet-Agent
  configuration are classified as non-retryable. Transaction serialization
  error `40001` remains retryable.
- A non-retryable dispatch failure now persists one failed attempt, marks the
  Research Run failed, cancels any already-dispatched siblings, and does not
  enter the evidence-remediation planner.
- Replaced duplicated Chinese prompt padding with distinct research
  instructions covering scope, methods, source policy, falsifiable checks,
  risks, delivery structure, and uncertainty. Final prompt lengths are 1390
  (industry), 1377 (competitor), and 1513 (tech selection) characters.

### 6. Re-ran focused regressions after the fix

- Real Dispatcher/PostgreSQL context test: pass.
- Real Store non-retryable dispatch lifecycle test: pass; one plan task and one
  failed attempt, with the run in `failed` state.
- SQL error classification test: class 42 is permanent while `40001` remains
  transient.
- Template suite: 6/6 pass; every Chinese and English prompt has unique
  substantive sentences and remains above the existing 800-character floor.

### 7. Resolved validation defects without changing product behavior

- The new handler test initially reused the handler package's shared workspace,
  which collided with the database invariant of one Research Fleet per
  workspace during the full suite. It now creates and deletes its own user,
  workspace, runtime, Agent, fleet, and session. The full handler suite passes.
- An existing provider-block acceptance test embedded an absolute reset time of
  `2026-08-03 13:52:38`. After that time the production parser correctly
  rejected it as a future block, making the test fail by wall clock. The fixture
  now formats a reset time two hours in the future; production parsing and
  blocking logic were not changed.
- Two LRM-1104 smoke cases were still marked `it.fails` after the production
  duplicate-goal-chip fix had already landed. Vitest therefore failed because
  the assertions passed. They are now ordinary passing tests; no component code
  changed.

### 8. Classified the full-check E2E failures without changing product code

- `make check` passed the complete TypeScript typecheck, all TypeScript unit
  tests, and all Go package tests before reaching Playwright.
- Playwright then exposed pre-existing test-suite drift: concurrent workers
  race the same verification-code email, browser setup omits the marker cookie
  required by the current route guard, an attachment fixture writes the
  migration-253-retired `agent.visibility` field and omits the current required
  Agent model, and multiple selectors still describe retired login,
  onboarding, navigation, issue, comment, settings, and research-list UI.
- The backend and frontend stayed healthy while these tests ran. Focused
  experiments confirmed the product authentication redirect, email rate limit,
  Agent model validation, and current UI were behaving as implemented; the
  failures belong to the old E2E setup rather than a user-path regression from
  this repair.
- Per the task boundary, exploratory edits to the shared E2E suite were
  reverted instead of expanding this backend repair into a repository-wide E2E
  rewrite. The generated Next.js `next-env.d.ts` edit was also reverted.

### 9. Corrected the create-response semantics for terminal dispatch failures

- A session whose initial dispatch is classified as non-retryable is already
  persisted in `failed` state. The create handler previously logged that the
  start was deferred and returned a warning claiming the reconciler would
  retry, which contradicted the canonical run state.
- The handler now reports whether retry is actually scheduled. Unknown
  transient errors retain the existing reconciler warning; non-retryable
  dispatch and missing-capability failures tell the client that no automatic
  retry will occur and direct it to the persisted run diagnostics.
- Added a focused classification test covering nil, deterministic dispatch,
  missing capability, and unknown transient errors.

### 10. Re-ran focused verification after the response correction

- Handler classification and real PostgreSQL dispatch tests pass, including
  the new create-response retry semantics.
- The real PostgreSQL Research Run lifecycle test passes with exactly one
  failed attempt and no remediation/replan loop.
- The built-in template suite and LRM-1104 parallel regression matrix pass:
  20 passing assertions plus nine intentionally documented expected failures.
- `go vet` passes for the changed `handler` and `researchrun` packages.

### 11. Completed pre-merge repository checks

- Monorepo TypeScript typecheck passes for `core`, `ui`, `views`, and `web`.
- React Doctor scanned the changed Research View source and reported zero
  issues.
- `git diff --check` passes and the worktree contains only the intended
  Research Run repair, regression-test corrections, and this repair record.

### 12. Merged the current `dev` baseline

- Fetched and merged `origin/dev` at `60b1f3bac` before opening the PR.
- Upstream had independently landed the same two stale-test corrections for
  LRM-1104 and the provider quota reset-time fixture. Both conflicts were
  resolved with the upstream versions because they test the same behavior and
  include the current comments/assertions.
- The Research Run production code, Dispatcher integration test, Engine
  lifecycle test, and prompt corrections had no merge conflicts.

### 13. Fixed and recorded a merge-only compile defect

- The upstream provider-quota fixture uses string concatenation instead of
  `fmt.Sprintf`; resolving the conflict left the old `fmt` import behind.
- The first post-merge handler build rejected the unused import. It was removed
  without changing runtime or test behavior, and the handler suite was queued
  for a clean rerun.

### 14. Repaired a user-reachable defect found in the merged `dev`

- With PostgreSQL restored, the full handler suite reached three new
  shared-sandbox provisioning tests and all failed with SQLSTATE `42703`.
- The hand-written production clone query in
  `env_dispatch_clone_adapter.go` still inserted and selected
  `agent.visibility`, although migration 253 intentionally removed that field.
  A real user's first mention of another Agent in a shared-sandbox rollout
  would hit the same query and fail provisioning.
- Removed the retired column from both sides of the `INSERT ... SELECT` and
  added a unit assertion that the derived-Agent query cannot reintroduce a
  `visibility` reference. No compatibility branch or schema rollback was
  added.

### 15. Completed post-merge database and repository verification

- The three shared-sandbox provisioning paths, the derived-Agent SQL contract
  assertion, and all Research Dispatcher focused tests pass together against
  the migrated PostgreSQL database.
- The complete handler package passes after the retired-field fix in 51.7
  seconds; the complete `researchrun` package also passes.
- The complete Views suite passes: 402 test files, 3,569 passing tests, seven
  documented expected failures, and five skipped tests.
- Monorepo TypeScript typecheck passes on the merged `dev` baseline.

### 16. Enforced the backend-only change boundary before publication

- The template prompt duplication is real and appeared verbatim in the
  production session goal, but its source and tests live in `packages/views`.
- Reverted the prompt rewrite and uniqueness test to the exact current `dev`
  versions before publishing. They did not cause SQLSTATE `42P18`, the 74
  dispatch failures, or the repeated failure nodes.
- Frontend follow-up: replace repeated Chinese template padding with distinct
  instructions and add the sentence-uniqueness regression that was validated
  during diagnosis. The backend PR contains no `packages/views` delta.

## Pending steps

- [x] Add regression tests and observe them fail on the unmodified code.
- [x] Implement the root fixes.
- [x] Run focused and repository verification.
- [ ] Review, commit, publish, and merge a non-draft PR.
- [ ] Confirm deployment and verify the production recovery path.
