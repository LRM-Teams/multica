# Reachable human honor curve

Date: 2026-08-04

## Delivery record

- [x] Confirmed the exponential tail requires 4,159,366 XP and roughly 18 years at every daily cap.
- [x] Chose a non-demoting curve: levels 1-20 retain their existing thresholds; every later threshold is less than or equal to the previous rule.
- [x] Added failing exact-threshold, boundary, non-demotion, and maximum-rate duration tests.
- [x] Implement piecewise level increments and bump the public rules version.
- [x] Recalculate stored human levels during migration so identity surfaces update after deployment.
- [x] Update the user honor product contract and public changelog.
- [x] Run migration verification and focused Go tests.

## Implementation target

Production XP percentiles are not available in the repository. This revision therefore uses an explicit provisional target:

- level 20: 874 XP, unchanged;
- level 40: 7,474 XP;
- level 60: 31,774 XP;
- level 70: 68,024 XP;
- level 80: 140,524 XP;
- level 80 requires 223 days at the absolute 631 XP/day cap, and roughly 1.3-2.6 years at a sustained 300-150 XP/day.

The public threshold table remains the client contract. A future data-based recalibration can change the table without duplicating the formula in clients.

## Verification

- Exact threshold, boundary, non-demotion, and duration regressions: passed.
- Full Honor service test package: passed.
- Migration 283 up: applied successfully on the isolated worktree database.
- Migration fixture at 140,523 XP: recalculated from level 55 to level 79, without the level-80 badge.
- Migration fixture at 140,524 XP: recalculated from level 55 to level 80, with the level-80 badge and level-60 name style.
- Migration 283 down: restored both fixtures to the legacy level 55. The repository's down command then continued into older migrations until migration 272 intentionally refused further rollback; migration 283 itself completed before that guard.
- Migration up after rollback: restored all migrations through 283 successfully.
