# Honor Center V2

## Goal

Replace the flat honor settings screen with a progression-focused achievement center that:

- makes the next attainable rewards obvious;
- gives unlocked, rare, and showcased badges distinct visual weight;
- keeps usernames readable in chat while allowing a richer profile treatment;
- turns unlock notifications into a recognizable achievement ceremony;
- works in both web and desktop through shared packages.

## Boundaries

- Reuse the current honor API, WebSocket event, badge catalog, equip, and showcase contracts.
- Do not add a second state owner or app-specific duplicate.
- Do not change the XP economy or migrate the database in this PR.
- Do not reveal titles, descriptions, or rules for locked secret badges.
- Publish the existing level curve through level 60 so clients can calculate exact progress for every supported level.
- Treat level 60 as terminal so the UI reports max level instead of progress toward a nonexistent level 61.
- Respect reduced-motion preferences and preserve keyboard access.

## Design decisions

- The primary hierarchy is: current level and progress → next three attainable badges → collection → showcase and history.
- Use one cyan/violet accent system with gold reserved for rare achievements.
- Use generated key art only for the hero background. Badge marks remain code-native SVG.
- Treat `unlock_pct <= 9` as rare, matching the existing Xbox-inspired rarity convention.
- Replace animated rainbow username effects with readable, restrained nameplates; animation is reserved for unlock state changes.
- Format dates and percentages through `Intl`, not string slicing or raw `toFixed` calls in components.

## Design source

- Figma: https://www.figma.com/design/KsSaJgC7ptzwHTO1FAEvPJ
- Hero asset: `packages/views/settings/components/assets/honor-center-orbit.webp`
- Hero prompt: `Premium dark orbital code foundry and achievement constellation, visual energy on the right, negative space on the left, cyan/violet/gold lighting, no text, no logo, no brand.`

## Implementation slices

1. Pure presentation helpers for rarity, progress, sorting, and formatting.
2. Honor hero, next-up rail, filterable achievement collection, showcase, and activity feed.
3. Custom unlock toast with ordinary and rare variants.
4. Badge crest/frame and username style polish.
5. Honor discovery card on Account and honor-aware styling for the editable self profile name.
6. Server-side secret-rule redaction and complete level-threshold publication.
7. Wire every published XP rule to its durable user action: Issue advancement, collaborator invitation, and completed research.
8. Loading, empty, error, pending, keyboard, and reduced-motion states.

## Verification

- Add failing tests for next-up ordering, secret-badge redaction, rarity classification, and filter behavior before implementation.
- Run focused Vitest suites for changed honor components.
- Run `pnpm typecheck`, lint for affected packages, and React Doctor.
- Run `make check`.
- Start the app and inspect the honor screen at desktop and narrow widths with the in-app browser.
- If the in-app browser is unavailable, record the limitation and rely on component tests, local build/E2E, and deployed health checks.
- Confirm PR CI, merge to `dev`, and wait for the Aliyun deployment workflow and health checks.

## Local verification record

- TypeScript typecheck, affected-package lint, React Doctor, focused Vitest, focused Go honor tests, and the production web build pass.
- The full TypeScript suite passes: 313 files, 3059 tests, 5 skipped.
- `make check` reaches the Go handler suite and is blocked only by
  `TestAgentInboxDrainSerializesSameAgentAndKeepsDifferentAgentsConcurrent`.
  The same pass/fail/pass sequence reproduces on untouched `origin/dev`, so this is a pre-existing concurrency flake rather than an honor regression.
- The in-app browser is unavailable in this environment; deployed health and provenance checks remain required after merge.
