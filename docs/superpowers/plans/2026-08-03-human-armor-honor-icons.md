# Human Armor Honor Icons Implementation Record

Date: 2026-08-03

## Goal

- Replace the human honor visuals with the approved 80-level cosmic battleship armor series.
- Keep the two approved 5 x 8 source sheets in order: levels 1-40, then levels 41-80.
- Use one shared level-icon component across honor pages, chat, member/profile surfaces, and other human-name renderers.
- Submit a non-draft PR to `dev`, merge it, and verify deployment.

## Source assets

- Levels 1-40: `exec-4bfa6450-117a-4d8c-99e0-8b0c4137b63d.png`
- Levels 41-80: `exec-375202db-7a27-4ee9-8d9b-739da5157e04.png`
- Ordering: left-to-right, then top-to-bottom within each sheet.
- Rejected combined sheets must not be used because the final rows overlapped.

## Implementation record

- [x] Read root `CLAUDE.md`, engineering principles, Chinese conventions, image asset instructions, and React performance guidance.
- [x] Detected unrelated uncommitted changes in the main checkout and created isolated branch `feat/human-armor-honor-icons` from `origin/dev` at `3d9f8fa3f`.
- [x] Audit the existing human honor level contract and every visible human honor renderer.
- [x] Extract and validate 80 project-local icon assets.
- [x] Implement the shared human level icon and wire every renderer to the same source.
- [x] Add regression tests for level mapping and all affected surfaces.
- [x] Run React Doctor and the full verification pipeline; classify the pre-existing E2E failures instead of altering product code to satisfy stale tests.
- [ ] Open a non-draft PR to `dev`, merge it, and verify deployment/provenance/health.

## Findings and decisions

- The main checkout is dirty on `feat/agent-armor-honor-icons`; it is not modified by this work.
- Human honor already exists independently from agent honor (`user_honor` vs `agent_honor_state`). The implementation will reuse that system instead of creating a parallel store or endpoint.
- Human honor currently caps at level 60. The approved asset count requires raising the authoritative server cap and public threshold table to 80; client-side clamping alone would make levels 61-80 unreachable.
- Message bubbles and thread roots already receive the human `HonorSnapshot` through `useActorName`. `ActorStyledName` is the single shared identity renderer for those surfaces, so it will own the new inline level crest.
- Additional independent old-badge renderers exist in the member self-editor header, member profile popover summary, account overview, and honor dashboard. They must use the same level component.
- Achievement badges remain a separate collection/showcase concept. Identity surfaces will use the level crest; achievement catalog/showcase surfaces keep achievement icons.
- Initial equal-grid extraction was rejected during visual validation because the generated sheets use irregular row gaps. Final extraction uses measured inter-row/inter-column blank bands per source sheet, removes the background, trims each alpha bounding box, and pads it to 256 x 256.
- Final asset validation: exactly 80 WebP files; every file is 256 x 256 with `yuva420p` alpha; the reconstructed 5 x 16 inspection sheet has no cross-row contamination.
- `UserHonorLevelIcon` is the only human level-crest renderer. Identity surfaces no longer use the equipped achievement as the rank marker.
- Covered identity surfaces: group messages, thread roots, DM header and DM messages, shared actor identity rows, member profile popover, member side panel (including the self-edit path), account overview, and the honor dashboard hero.
- Preserved the existing dense-list contract from `27a38e92d`: DM search, DM conversation rows, and channel member rows retain earned name styling but intentionally omit space-consuming badges.
- Raised the server progression cap and published threshold table from 60 to 80. Migration `278_user_honor_80_levels` adds the database maximum and moves the Infinity Engine completion rule to level 80 without revoking durable existing unlocks.
- Migration verification used the isolated `multica_worktree_593` database. The new migration's down SQL executed successfully; the repository-wide `make migrate-down` then stopped at the pre-existing intentionally irreversible migration 272. A subsequent `make migrate-up` restored every migration through 278. Direct database inspection confirmed `CHECK (level <= 80)` and the level-80 Infinity Engine description/rule.
- Red/green evidence: the server test failed at the old cap of 60; the frontend tests failed while the user level component and shared identity rendering were absent. After implementation, 8 focused frontend files passed 119 tests (2 existing skips), focused Go rules tests passed, and the web/shared TypeScript typecheck passed.
- React Doctor scanned the 11 changed React files and reported 0 issues. Lint completed with no errors and only 8 warnings already present on `dev` in unrelated files.
- `make check` passed TypeScript typecheck, all TypeScript unit tests (529 files, 4675 passing tests, 7 expected failures, 5 existing skips), database migration application, and all Go tests. Its E2E phase reproduced 19 failures already present on the current `dev` baseline: tests still write the removed `agent.visibility` column; authentication setup receives 429/400; old English selectors no longer match the current UI; remaining failures cascade from authentication/navigation timeouts. None of the failed files are changed by this branch.
- Production builds passed for both `@multica/web` and `@multica/desktop`. The desktop manifest emitted all 80 `user-honor-level-*` WebP assets, proving the shared-view imports resolve in the Electron/Vite bundle as well as Next/Webpack.
- Real-page Playwright QA used a level-42 user and a real group message against the isolated backend. The honor page rendered 3 level crests, the account page rendered 2, and the group message rendered 1 beside the human name. Every surface resolved the same `user-honor-level-42.webp`; no network response returned 4xx/5xx. Visual inspection found no overlap or layout regression.
- Removed the temporary extraction/QA scripts, screenshots, generated E2E artifact, and dropped the isolated `multica_worktree_593` database after verification. The source worktree and the user's main checkout remain separate.
- Before publication, fetched and rebased the implementation onto `origin/dev` at `10431c24b` (96 upstream commits beyond the original base) with no conflicts. On that exact base, all 422 Views test files passed (3702 passing tests, 2 expected failures, 5 existing skips), Web and desktop typechecks passed, `server/internal/service` tests passed, and React Doctor again reported 0 issues.
