# Honor correctness fixes

Date: 2026-08-04

## Scope

- Publish permanent Agent honor level changes and refresh every cached Agent identity surface.
- Include the Agent display name in achievement and level notifications.
- Render fleet warm-up progress from the configured minimum sample size.
- Replace the unreachable human 80-level exponential curve with a tested piecewise curve.

## Delivery record

- [x] Read the repository architecture, engineering principles, Chinese copy conventions, and Agent honor v2 contract.
- [x] Confirmed the four defects on current `origin/dev` with code-level repro seams.
- [x] PR 1: Agent level realtime event and named notifications.
- [ ] PR 2: Configured fleet warm-up threshold.
- [ ] PR 3: Reachable human 80-level curve.
- [ ] Final focused tests, typecheck, lint, and React Doctor results recorded.

### PR 1 verification

- Go service tests: passed.
- Go handler event payload tests: passed against the worktree-isolated database.
- Agent honor listener Vitest: 6 passed.
- TypeScript typecheck: passed.
- React Doctor: 0 issues in changed Core and Views files.
- Core lint: passed; Views lint reported only 12 pre-existing warnings outside the changed files.

## Progression target

Production XP percentiles are not available in the repository. The curve change therefore uses explicit provisional targets instead of pretending to be data-derived:

- levels 1-20 establish the system during the first weeks of active use;
- levels 21-50 represent sustained use over months;
- levels 51-70 represent long-term contribution;
- levels 71-80 are prestige levels;
- level 80 must require at least roughly six months at the absolute 631 XP/day cap and remain achievable in roughly one to three years for consistently active users.

The thresholds and monotonicity are enforced by tests. The target should be recalibrated later from observed daily XP percentiles without changing the client contract.
