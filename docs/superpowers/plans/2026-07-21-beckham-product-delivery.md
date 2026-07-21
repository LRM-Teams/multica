# Beckham Product Delivery Upgrade

Date: 2026-07-21

## Goal

Give Beckham enough evidence, task-authoring authority, team capability visibility, and review authority to lead product work that includes real visual assets. Keep implementation work with delivery agents. Do not represent CSS decoration as illustration or silently pretend that an unavailable image-generation capability exists.

## Scope

This change starts with the smallest complete production slice supported by existing Multica contracts:

1. expose reference attachments to Ambient review;
2. let Radar-authored issues carry acceptance criteria, source provenance, and attachments;
3. expose relevant agent capabilities so Beckham can choose a qualified assignee;
4. provide one explicit rework operation instead of a sequence the model may only partly execute;
5. document and test the visual-delivery rules Beckham must enforce.

A provider-backed image-generation module will only be added after verifying an existing provider/tool seam, credential model, cost controls, and file provenance path. The scheduler must not claim image generation exists when no configured adapter can perform it.

## Working rules

- Query existing database, service, CLI, provider, and runtime interfaces before changing them.
- Reuse authoritative issue and attachment modules instead of creating parallel write paths.
- Add regression tests that fail against the pre-change behavior before implementation.
- Treat every unexpected failure as a possible user-path defect; record why it is or is not one.
- Avoid compatibility layers for internal code and avoid partial multi-action sequences for rework.
- Record evidence, decisions, changes, and verification after each completed step.

## Step log

### Step 1 — Establish the implementation baseline

Status: complete

Evidence:

- PR #804 is merged into `dev` at merge commit `ba7a6566dff87fcbba3f1df52c1951a1b42fd55e`; all four GitHub checks passed.
- Local `dev` was fast-forwarded to that merge and work continues on `agent/beckham-product-delivery` with a clean starting tree.
- The repository has no `CONTEXT.md`, `CONTEXT-MAP.md`, `docs/adr/`, or prior `docs/agents/` configuration. Domain decisions therefore come from `CLAUDE.md`, `docs/engineering-principles.md`, existing code contracts, and the product decisions in this record.
- The architecture review skill's setup workflow would require separate issue-tracker, triage-label, and domain-layout choices. Those repository-wide configuration choices are outside this implementation and are not inferred.

Decision:

- Build on the project-scoped context from #804 rather than creating a competing context path.
- Use the existing attachment and issue modules as seams. Add depth there only where Radar currently bypasses capabilities already available to interactive users.
- Keep provider-specific image generation outside the Ambient scheduler until the repository proves a configured adapter and an auditable output path.

### Step 2 — Verify existing contracts and define tests

Status: complete

Evidence:

- Ambient issue and channel-message queries omit attachment metadata. Channel messages also omit `deleted_at IS NULL`, so a deleted message can remain in the manager's evidence window.
- The normal agent prompt already uses attachment IDs plus `multica attachment view --id <id> --output <path>`; signed URLs are intentionally not embedded. Ambient can reuse that interface.
- `IssueCreateParams` already carries source and attachment IDs, but acceptance criteria are written by the HTTP handler after the create transaction. A failed follow-up produces a successful issue without its definition of done and no corrective realtime event.
- `IssueService` links attachments after committing the issue and swallows link/list errors. A valid-but-missing attachment ID therefore reports issue creation as successful while dropping the reference.
- Radar `create_issue` defaults an omitted assignee to the Radar agent. In Ambient that agent is Beckham, so the manager receives a concrete implementation task contrary to its managed role.
- Radar issue creation omits the full `issue` broadcast payload, work-graph sync, and thread backflow used by the HTTP path. Realtime activity/subscriber consumers require `payload.issue`.
- `comment_issue` rejects terminal issues, so a completed low-quality delivery cannot be reopened and woken as one operation.
- Channel-agent context contains only ID and name. Agent description, runtime status/provider, explicitly assigned skills, and daemon-synced runtime shared skills are available without exposing secret-bearing `mcp_config`.
- Event-driven coordination plans have no server-side five-action cap or `no_action` exclusivity check. The parser also accepts `assign_issue` and `schedule_reminder` although the executor always rejects them.
- The first targeted test run failed at compile time on the new `request_rework`, acceptance/source/attachment payload, and plan-validation interfaces. This is the expected pre-implementation red state and proves the new tests do not pass against old behavior.

Decision:

- Move acceptance criteria and attachment validation/linking into `IssueService.Create`'s transaction for every caller.
- Make omitted Radar assignee mean unassigned; reject an explicit self-assignment by a managed group manager.
- Add one `request_rework` action: allow `done` but not `cancelled`, require the current agent assignee as target, optionally replace acceptance criteria, set status to `todo`, add one visible targeted comment, and create/reuse the target task in one transaction.
- Include the latest three comments with their attachments, direct issue attachments, and live channel-message attachments. Never expose deleted-message evidence or expiring URLs.
- Treat descriptions and skills as declared capability evidence. Runtime-shared skills count only when their persisted origin runtime matches the agent runtime.
- Enforce Ambient plan shape before executing any action and stop accepting action types with no executor.

### Step 3 — Implement evidence and task-authoring contracts

Status: complete

Changes and evidence:

- Ambient context now includes the latest three comments per issue, attachment metadata on those comments, direct issue attachments, and attachment metadata on the latest forty live group messages. Deleted channel messages and their attachments are excluded.
- Context exposes only attachment UUID, filename, and content type. Beckham is instructed to use the existing `multica attachment view --id <id> --output <path>` contract and not to claim inspection unless fetch and view succeeded; storage URLs remain absent.
- `create_issue` now requires a non-empty description and at least one non-empty acceptance criterion. The persisted Radar task remains authoritative for project/channel scope; payloads cannot redirect work to another project.
- Radar source messages are canonicalized to their thread root. Requested attachments must belong to the review workspace, live review channel, and a non-deleted message. Deleted-message evidence is rejected before issue creation.
- Acceptance criteria, source anchor, and attachments are now persisted inside `IssueService.Create`'s transaction. Missing/already-bound attachments abort the create instead of producing a successful issue with silently dropped references.
- `issue:created` now carries the full issue response including acceptance criteria and attachments. Radar creation also runs the same work-graph sync and source-thread backflow used by the interactive path.
- Omitted Ambient assignee means unassigned. Ambient may explicitly assign only an eligible channel agent; member/squad assignment, managed-manager assignment, archived agents, agents without runtimes, and agents outside the channel are rejected.

Unexpected defects handled:

- A generated `GetIssueForTask` method was depended on by service code but absent from its sqlc source query. Regeneration deleted it and made the project fail compilation. The source query is now authoritative, so future generation preserves the interface.
- The first full handler run showed that direct `CreateIssue` callers such as Autopilot and onboarding passed no new acceptance-criteria argument, causing a database NOT NULL failure and user-visible HTTP 500s. SQL now maps an omitted argument to `[]`; Radar still enforces non-empty criteria at its own action boundary. The full handler package passes with these entry points covered.
- Two new database tests initially combined parameterized SQL statements in one prepared call. pgx correctly rejected the test setup; the fixtures were split into separate statements. This was test-only and did not justify a product fallback.
- The existing default local database records migration 169 while missing columns defined by that migration. The deployed dev database has the complete schema through migration 203, so this is stale local test state rather than a user-path defect. Verification uses a fresh disposable database migrated from 001 through the branch's latest migration.

### Step 4 — Implement capability-aware delegation and rework

Status: complete

Changes and evidence:

- Channel-agent context now exposes description, availability, runtime provider/status, explicitly assigned skills, and runtime-shared skills whose persisted origin runtime matches that agent. MCP configuration and other secret-bearing config are not exposed.
- Beckham's executable prompt and the durable `spec-driven-delivery` skill distinguish interface CSS from visual assets. Layout, tokens, controls, responsiveness, and simple transitions may use CSS; illustration, brand art, characters, textures, special badges, and complex motion require real SVG/PNG/WebP/Lottie/video/frame files when specified.
- Visual work may be assigned only when the agent's description or configured skills declare the needed capability. If none exists, Beckham leaves the issue unassigned and asks the PM/human owner to configure one; the product does not pretend an image-generation provider is available.
- Added `request_rework`. It verifies the current agent assignee and project/channel scope, rejects cancelled issues, optionally replaces acceptance criteria, reopens to `todo`, creates one visible targeted comment, and creates/reuses the exact assignee task in one transaction. Injected task-write failure leaves status, criteria, comments, and tasks unchanged.
- Rework source-thread backflow is factual and non-targeted because the targeted issue comment already owns the wake. This avoids creating a second channel task for the same rework.
- Ambient plans are rejected before any execution if they exceed five actions, mix `no_action` with effects, or contain an action outside the server allowlist. Parser acceptance for the unimplemented `assign_issue` and `schedule_reminder` actions was removed.

### Step 5 — Verify real user paths and inspect unexpected failures

Status: complete

Verification:

- A fresh disposable PostgreSQL database was explicitly migrated from 001 through 205. Both `204_system_general_channel` and `205_beckham_product_delivery_actions` are recorded, and `go test -count=1 ./internal/handler ./internal/radar ./internal/migrations ./cmd/migrate` passes, including HTTP issue creation, Ambient context assembly, Radar issue authoring, rework success, injected transactional rollback, and migration-runner coverage. The complete backend suite also passes in final Linux CI.
- `go vet ./...` passes.
- `pnpm typecheck` passes across the monorepo.
- `pnpm test` passes all TypeScript unit-test tasks: core 733, docs 17, web 69, desktop 216, and views 2164 passed with 5 skipped.
- `git diff --check` passes.
- The deployed dev service was inspected without mutation: `http://82.157.184.89:8090/health` returned `{"status":"ok"}`; the backend container was running with restart count 0; its database has the complete workspace-Radar schema and migrations through 203. The branch's newer migrations remain intentionally undeployed until this PR is merged.
- Before publication, `origin/dev` advanced to `f4a9b09e48f19eac41740648eda5ee6db66774af` (`hot-fix`). Its files do not overlap this change. The branch was rebased onto that commit and the handler/service/Radar/migration suite passed again.
- Browser/E2E testing was not attempted because this checkout cannot run the product locally and the agreed boundary permits unit/static verification. No deployed-server files or database rows were modified.

Unexpected failures assessed:

- The full `go test ./...` run has one pre-existing macOS portability failure: `server/internal/daemon/TestSecureSkillDraftBundleDirRejectsEscapes` expects a `/var/...` path while macOS securely canonicalizes that alias to `/private/var/...`. The traversal/symlink security check itself does not fail. This is a test-environment assertion outside the changed runtime paths, so product code was not changed to make the assertion pass.
- `pnpm test` prints existing React `act`, i18n, and mock-DOM-property warnings while all test tasks pass. None originate from the backend-only implementation in this change.
- A final `make sqlc` idempotence check could not run because this development machine has no `sqlc` executable. This is a missing local generator tool, not a product path. The authoritative SQL and generated Go are both committed, compile, and pass the database-backed tests; generation previously exposed and prompted the missing-source-query repair recorded in Step 3.
- The first PR CI run exposed a flaky pre-existing daemon test, `TestPollLoopTargetsRuntimeWakeup`: its setup queued a broadcast wakeup even though runtime pollers already claim immediately, then changed counter phases as soon as those initial claims arrived. A still-buffered broadcast could therefore be counted as a targeted slow-runtime claim. The production code has separate per-runtime wakeup channels and the targeted branch only signals the selected channel, so this was not runtime cross-talk. The redundant test broadcast was removed; the corrected focused test passed 200 consecutive local runs.
- While final CI was running, `dev` added `204_system_general_channel`, so the Beckham migration moved to the next sequence number, 205. An initial concern that two numeric 204 prefixes would collide was disproved by the authoritative migration run: the version key is the full filename stem, this repository already has several shared numeric prefixes, and both distinct stems would execute. The renumbering preserves chronological order; it is not a compatibility workaround for a nonexistent loader collision. A new database was then migrated through 204 and 205 before rerunning the backend suite.
- The first fresh-database command started handler, service, and Radar test packages concurrently while relying on the handler package to bootstrap migrations. Service/Radar reached the empty database first and reported missing relations. After an explicit migration phase, Radar passed; three unrelated service sandbox-cleanup tests still assume some global user already exists and return `no rows` on a truly empty database. That is a non-hermetic test fixture, not a runtime path, and this PR does not add product waits or seed data for it. The final CI backend job runs the repository's supported setup and passes the full package set.

### Step 6 — Publish for review

Status: complete

Publication:

- Implementation commit: `845b6b7017537813007606db67664bfbca964c2d` (`feat(beckham): enforce evidence-based product delivery`).
- Branch: `agent/beckham-product-delivery`. It was synchronized while `dev` advanced during PR verification and finally includes `c843c86728b525d3880459eab6b7dea304101a9c`. The incoming product changes do not overlap this implementation; after their migration 204 landed, this branch uses the next sequence number, 205.
- PR: [#815 — feat(beckham): enforce evidence-based product delivery](https://github.com/LRM-Teams/multica/pull/815), targeting `dev`.
- PR state after creation: open, ready for review, and reported mergeable by GitHub. It is not a draft, so the repository merge control is available once required checks pass.
- The GitHub App PR-creation call returned `403 Resource not accessible by integration`; authenticated `gh` with repository scope created the same PR as the documented fallback. This affected only the publication interface, not repository contents or validation.

### Step 7 — Repair deployment migration compatibility

Status: complete

Diagnosis evidence:

- Deploy run `29816385159` for merge commit `a17879f20f397da4c67bc2efbc6ce31d81a8870d` built both images, then left the backend restart-looping because migration 205 failed with `SQLSTATE 23514`. The frontend remained healthy; Caddy's backend lookup and 502 errors followed from the backend process exiting and are not an independent proxy defect.
- A read-only query against the deployed dev database found seven historical `schedule_reminder` rows and no action type outside the pre-205 constraint. Migration 205 is absent from `schema_migrations`, migration 204 remains the latest applied version, and the pre-205 check constraint is intact. PostgreSQL therefore rolled the failed migration statement back without leaving a partial schema.
- The 205 migration removed `assign_issue` and `schedule_reminder` from the table constraint while adding `request_rework`. Removing parser acceptance prevents Beckham from proposing new unimplemented actions, but rewriting a check constraint also validates persisted audit history. These are separate boundaries and cannot share the narrower set.
- `TestBeckhamProductDeliveryMigration205PreservesLegacyRadarActions` creates the pre-205 table, seeds both legacy action types, and executes the real up migration. Before the repair it fails with the same `agent_radar_action_action_type_check` violation and SQLSTATE as deployment.

Decision:

- Keep both legacy values in the database constraint so existing Radar audit rows remain valid. Continue rejecting new model-produced `assign_issue` and `schedule_reminder` plans in the parser; do not delete or rewrite deployed history.
- Validate the up and down migrations against seeded legacy history, the new `request_rework` value, and an unknown value before publication.

Repair and verification:

- Migration 205 now keeps `assign_issue` and `schedule_reminder` in the persistence constraint while adding `request_rework`. No executor, parser, proxy, container-startup, or deployed data path was changed.
- The migration regression test passes in both directions. It proves legacy rows survive up and down, `request_rework` is accepted only after up and removed by down, and unknown actions still fail with `23514`.
- The parser boundary tests pass: Beckham still rejects newly proposed `assign_issue` and `schedule_reminder` actions and accepts `request_rework`.
- A disposable local database was created, migrated from 001 through the repaired 205, and used to run `./cmd/migrate`, `./internal/radar`, and `./internal/migrations`; all passed. `go vet` for those packages also passed. The disposable database was then dropped.
- The first Radar package run against the default local database failed because that database records old migrations while missing `refresh_workspace_radar_time_signals`. A read-only production query confirmed the deployed database has the function, and the fully migrated disposable database passed both affected tests. This is stale local state, not a deployed user-path defect, so no product fallback or unrelated migration was added.

Publication:

- Repair commit: `d06a5121b30519e4b1c82fb5cf8218c672bdeec1` (`fix(beckham): preserve radar action history in migration`).
- Branch: `agent/fix-beckham-migration-history`, based on deployed failing merge commit `a17879f20f397da4c67bc2efbc6ce31d81a8870d` from `dev`.
- PR: [#817 — fix(beckham): preserve radar action history in migration](https://github.com/LRM-Teams/multica/pull/817), targeting `dev`.
- PR state after creation: open, ready for review, non-draft, and reported mergeable by GitHub. CI started on the published head.
