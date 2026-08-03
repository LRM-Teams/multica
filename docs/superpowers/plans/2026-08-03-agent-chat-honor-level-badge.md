# Agent chat honor-level badge fix

Date: 2026-08-03
Status: ready for PR

## Goal

Show the agent's permanent honor-level crest beside its name in channel messages. The compact chat identity must use the same 30-level asset set as the agent Honor page instead of the legacy fleet-class medal.

## Constraints

- Reuse `AgentHonorLevelIcon` and the existing agent honor state.
- Keep fleet class/rank independent; the avatar top-three pennant remains unchanged.
- Avoid per-message and per-agent honor requests.
- Preserve compatibility with older backends that omit the new summary field.
- Cover the main message row and thread-root message row through their shared identity component.

## Progress

- [x] Created a clean worktree from `origin/dev` at `f3813f2a6`.
- [x] Confirmed the screenshot's compact hexagon is rendered by `ActorStyledName` through `FleetRankBadge`.
- [x] Confirmed the Honor page and profile popover use `AgentHonorLevelIcon` from the 30 bundled level assets.
- [x] Added failing regressions for the main message row and thread-root row; both failed because no level crest reached the DOM while the other 90 tests passed.
- [x] Batch-projected `honor_level` through the existing agent list response with one workspace query; no per-message or per-agent requests were added.
- [x] Rendered `AgentHonorLevelIcon` in the shared chat identity and retained `fleet_rank` on avatars for the Top-3 pennant.
- [x] Added a core hook test for the agent-directory projection and a handler test for the API field.
- [x] Targeted result: 93 frontend tests passed (2 intentionally skipped); the handler regression passed against a fresh fully migrated database.
- [x] First full verification pass exposed a missing `getAgentHonorLevel` method in the channel message list's test mock; added it with the fixture-accurate `undefined` result.
- [x] Confirmed the two research matrix failures were stale `it.fails` markers left after product fix `fb730b018`; the local correction was later superseded by the equivalent, more complete `dev` fix in PR #1969 during rebase.
- [x] The second full pass reached Go tests and exposed a date-sensitive provider-block test fixture whose fixed unblock timestamp had passed. Replaced only that fixture timestamp with a deterministic future-relative value; production quota parsing was correct and unchanged.
- [x] Run targeted and full verification; classify unrelated baseline E2E failures.
- [ ] Open a PR to `dev`, merge it, and verify deployment.

## Diagnosis

The chat identity path only receives `AgentFleetRank`; it has no permanent agent honor level. As a result, the name row can only render the older six-class fleet medal even though the Honor page already has the new level assets. This is a missing data projection and shared-surface wiring issue, not a deployment or browser-cache issue.

## Full verification pass 1

- `make check-worktree` reached the full frontend test suite after typecheck passed.
- The new hook contract caused 15 channel message list tests to stop before assertions because their local mock omitted `getAgentHonorLevel`; the production hook already implemented it.
- Two unrelated research matrix cases marked `it.fails` reported that their exercised behavior now passes. Git history confirmed product fix `fb730b018` implemented LRM-1104 after the smoke matrix was added but did not flip the two markers. A local test-only correction was made for verification; after PR #1969 landed on `dev`, rebase retained the newer upstream hard-gate version and dropped the duplicate local edit.

## Full verification pass 2

- TypeScript checks and the complete frontend suite passed after the mock and stale-marker corrections.
- The Go suite's only failing package was `server/internal/handler`.
- Isolated package reproduction identified `TestFailAgentInboxEvent_StickyQuotaWritesProviderBlock`: its literal `2026-08-03 13:52:38` unblock timestamp was already in the past, so the production parser correctly declined to persist a past lock.
- Updated the test input to use a local timestamp two hours in the future. No provider-block production behavior was modified.

## Full verification pass 3

- `git diff --check`: passed.
- `pnpm lint`: passed with 9 pre-existing warnings and no errors; none are in files changed for the honor badge path.
- TypeScript typecheck: passed for all scoped packages.
- Frontend unit suites: passed, including the core agent-directory hook, main channel message, thread-root preview, channel message list, and research smoke matrix.
- Go suite: passed in full; `server/internal/handler` passed in 92.114 seconds, including the list-agent honor-level response regression.
- Playwright E2E reached 28 tests but the existing harness produced 19 failures, 2 passes, and 7 tests not run. Root causes were outside this change:
  - `e2e/chat-attachments.spec.ts` still inserts the removed `agent.visibility` column.
  - parallel fixtures reuse one login identity and trigger send-code `429` / verify-code `400`, causing navigation timeout cascades across auth, issues, comments, settings, onboarding, and research pages.
  - the login-page smoke test asserts the retired `h1` and Email/Name placeholder layout.
- The E2E harness was not modified because these are test-fixture/auth-harness problems and do not exercise the agent honor badge path. No production fallback was added to hide them.
- Reverted the generated `apps/web/next-env.d.ts` path change and an unrelated pre-existing formatting block before final review.

## Latest dev rebase

- Fetched `origin/dev` at `33b41d9e3`, 11 commits ahead of the implementation base.
- Rebased both commits onto the latest `dev`.
- PR #1969 already contained the research smoke hard-gate correction, so the conflict was resolved in favor of the upstream version; the remaining test-maintenance commit contains only the future-relative quota fixture and this record.
- Post-rebase regressions passed: 1 core hook test, 112 channel/thread tests with 3 intentional skips, and both backend handler tests.
- Post-rebase full TypeScript typecheck and `git diff --check` passed; the worktree was clean before publishing.
