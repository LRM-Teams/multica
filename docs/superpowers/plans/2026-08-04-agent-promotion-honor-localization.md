# Agent Promotion Notification and Honor Localization Record

Date: 2026-08-04

## Goal

- Make every Agent fleet-promotion notification identify the promoted Agent.
- Fully localize user-visible human and Agent honor copy in the Chinese UI.
- Preserve server IDs and English source data as protocol/storage values; localize only at the presentation boundary.
- Submit a non-draft PR to `dev`, merge it, and verify deployment.

## Progress

- [x] Read repository rules, engineering principles, Chinese conventions, diagnosis guidance, and React guidance.
- [x] Preserve the dirty main checkout and create isolated branch `fix/agent-promotion-notification-name` from `origin/dev` at `ffb8c707f`.
- [x] Audit the Agent promotion event from service callback through realtime payload to the toast.
- [x] Audit human and Agent honor pages, profile surfaces, unlock toasts, server-sourced catalog copy, progress labels, event reasons, metric names, and audit actions.
- [x] Add failing regression tests for anonymous promotion notifications and untranslated honor copy.
- [x] Fix the notification contract and localize every audited honor surface.
- [x] Run focused tests, repository TypeScript verification, React Doctor, and real-page Chinese visual checks.
- [ ] Open a non-draft PR to `dev`, merge it, and verify deployment/provenance/health.

## Findings

- The only Agent fleet-promotion toast is `AgentHonorUnlockListener` handling `agent_honor:fleet_class_changed`.
- The realtime payload declares `agent_name` optional. When both the event field and the Agent-directory cache are absent, the listener deliberately renders the generic subject `智能体`; this reproduces the reported ambiguity.
- The backend `audience` helper returns empty recipients and an empty name when `GetAgent` fails or the owner is absent, then still calls `publishToUsers`. Empty recipients mean workspace-wide broadcast in this event system, so the failure path can publish an anonymous promotion more broadly than intended.
- The prior regression tests cover a payload with `agent_name` and an older-server cache fallback. They do not cover the real failure condition where neither name source exists, so the generic notification remained an accepted passing behavior.
- Agent achievement titles, descriptions, categories and fleet-class labels are localized, but the recent event reason, achievement metric names and admin audit actions still display server identifiers or English source strings.
- Human honor badge titles, descriptions and unlock rules come from English database definitions. They are rendered directly in the main honor page, next-target cards, collection, activity, unlock toast, member profile summary, public honor wall and comparison groups.
- Human badge progress labels are protocol keys (`founding`, `level`, `usage`, `presence`, `delivery`, `community`) and are currently rendered directly.
- Chinese human-honor copy still contains the user-visible English word `Tier`; `XP` and Multica remain unchanged product abbreviations/brand terms.

## Regression evidence

- The listener regression reproduced `智能体晋升至护卫舰` when both the event name and Agent-list cache were absent; the new listener test now resolves `前端工程师晋升至护卫舰` from the authoritative Agent detail query and suppresses the toast if identity still cannot be confirmed.
- The backend audience regression test covers a named owned Agent, a failed Agent query, and a missing owner. The handler now publishes only the first case, preventing an empty recipient list from becoming a workspace broadcast.
- The human badge-copy regression test asserts all 51 built-in Chinese titles, representative descriptions, level and pillar unlock conditions, localized progress labels, secret redaction, and forward-compatible fallback for unknown future badge IDs.
- Public honor-wall and realtime unlock-listener regressions verify that English badge records received from the API are rendered with Chinese titles at those independent entry points.
- Agent honor regressions cover all built-in titles, locked-secret redaction, metric names, XP-event reasons, audit actions, and unknown-value compatibility.
- Locale JSON syntax validation passes for all four `settings` and `agents` bundles.

## Ranked hypotheses

1. Missing `agent_name` plus an empty Agent cache causes the generic `智能体` subject.
2. Backend Agent lookup failure publishes an empty-name event with workspace-wide recipients.
3. A long-lived browser bundle still runs the old listener after deployment.
4. A second promotion notification entry point exists outside the audited code.

Exact searches found no second toast entry point. The implementation will make hypotheses 1 and 2 impossible and retain deployment/version verification for hypothesis 3.

## Implementation

- Human honor copy now resolves by stable badge ID at the presentation boundary. All 51 built-in badge names, descriptions, unlock rules, progress labels, equipped-badge summaries, showcase entries, public-wall entries and realtime unlock notifications use the same localized source.
- Agent honor copy now localizes built-in achievements, fleet classes, event reasons, metric names and audit actions. Locked secret achievements expose neither the server title nor the stable ID.
- Fleet-promotion notifications resolve the Agent name from the event, the Agent list cache, or an authoritative Agent detail query. If identity cannot be confirmed, no anonymous notification is shown.
- The backend publishes Agent achievement and promotion events only after resolving an owned Agent with a non-empty name, so a lookup failure cannot become an anonymous workspace broadcast.

## Verification

- Focused frontend suite: 7 files, 23 tests passed.
- `packages/views` TypeScript check passed.
- Focused backend Agent honor handler test passed against the isolated worktree database.
- Views lint passed with 0 errors; 9 warnings are in pre-existing untouched files.
- React Doctor inspected the 10 changed React files and reported 0 issues.
- Locale JSON syntax validation passed for all four `settings` and `agents` bundles.
- Repository Playwright rendered a seeded Chinese honor page against the isolated backend and database. It verified representative early, middle and final badge names, rejected the English source names, and rejected `Tier`.
- The complete `packages/views` suite passed with one worker: 445 files and 3,854 tests passed, with the repository-declared 2 expected failures and 5 skips. The default all-repository run first timed out in five unrelated UI tests under local CPU contention; all 36 tests in the affected files passed together with one worker.
- The first full Go run shared its database with the still-running local development server and failed three unrelated handler integration tests while scheduler/background writes were active. After stopping the development services, all three failed tests passed together. The focused Agent honor backend regression also passed independently.
- The first local Playwright setup loaded the app before injecting its legacy token, which selected cookie-auth mode and redirected to login. This is an intentional test bootstrap ordering problem, not a production interaction; injecting the token before the first application load made the check deterministic.
- An intermediate locator selected the hidden `<title>` inside a badge SVG instead of the visible card heading. The page snapshot showed the translated card; the final check reads the rendered page text and passed.

## Follow-up split requested during delivery

- The Agent rail still renders `FleetRankBadge` through the legacy vector `FleetClassIcon`; it does not use the new 30-level Agent honor artwork shown on the honor page.
- This visible omission was confirmed from the supplied Agent-list screenshot and exact code path in `AgentRailRow`.
- Per delivery instruction, the localization and named-notification changes will ship in the first PR. The Agent rail and every other compact Agent badge surface will be audited and changed in a separate follow-up PR based on the updated `dev` branch.
