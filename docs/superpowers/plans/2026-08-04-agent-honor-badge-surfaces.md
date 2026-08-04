# Agent honor badge surface migration

Date: 2026-08-04

## Goal

Replace every compact Agent honor badge that still renders the legacy fleet-class medal with the 30-level Agent honor artwork. Preserve fleet-class chips where they describe fleet status rather than honor level.

## Evidence and decisions

- [x] Reproduced from the supplied Agents page screenshot: `AgentRailRow` renders `FleetRankBadge` with `medal` instead of using `agent.honor_level`.
- [x] Audited legacy medal callers: Agents rail, member profile's created-Agent rows, and the shared `ActorStyledName` fallback.
- [x] Confirmed `Agent.honor_level` already exists in the list API projection and was added for compact identity surfaces. No API extension is needed.
- [x] Confirmed channel messages and thread previews already request `getAgentHonorLevel`; their remaining risk is the shared fallback to the old fleet medal when the level is absent.
- [x] Decided that an unavailable honor level renders no honor icon. It must not render a different concept (fleet class) as a visual substitute.
- [x] Fleet-class chips in Agent honor/detail pages remain unchanged because those labels communicate fleet status, not honor level.

## Implementation record

- [x] Replace Agents rail and member-created-Agent legacy medals with `AgentHonorLevelIcon`.
- [x] Remove the legacy fleet-medal branch from shared actor identity rendering.
- [x] Pass authoritative Agent honor levels into profile, DM header, detail, contact, and picker identity surfaces that display badges.
- [x] Remove the now-unused fleet medal mode and its tone helper.
- [x] Add regression coverage for the shared identity renderer and representative Agent surfaces.
- [ ] Run focused tests, typecheck, lint, React Doctor, and the repository verification pipeline.
- [ ] Submit a non-draft PR to `dev`, merge after checks pass, and verify deployment.

## Pull request and deployment

- PR: [#2128](https://github.com/LRM-Teams/multica/pull/2128) (non-draft, base `dev`)
- Merge commit: pending
- Deployment: pending

## Verification record

- [x] Shared identity, member panel, Agent icon, channel message, and thread preview tests: 113 passed, 2 skipped.
- [x] Agent side panel, DM list, chat contacts, chat Agent picker, and Agent detail overview tests: 78 passed.
- [x] `@multica/ui` and `@multica/views` TypeScript checks passed.
- [x] UI and views lint passed with zero errors; 11 warnings are pre-existing files outside this change.
- [x] React Doctor scanned 18 changed React files and reported 0 issues.
- [x] First full test run exposed four failures in one DM test file: its complete `useActorName` mock omitted the newly consumed `getAgentHonorLevel` method. Production always provides the method, so only the test contract was corrected; no production fallback was added.
