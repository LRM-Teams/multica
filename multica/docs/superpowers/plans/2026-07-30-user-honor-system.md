# User honor system implementation plan

**Goal:** Ship platform-global levels, badges, and name styles per `docs/superpowers/specs/2026-07-30-user-honor-system-design.md`.

## Global constraints

- Global scope on `user`, not `member`
- Founding cutoff: `2026-08-01T00:00:00Z`
- Others see level + badges only; XP on Settings honor tab
- Name style auto-highest; one equipped badge (user choice)
- Web + Desktop settings parity

## Tasks

- [x] Migration 251 + sqlc honor queries + models
- [x] HonorService + rules + founding + XP hooks
- [x] HTTP routes + profile/member embed
- [x] FE types/API/schemas + honor tab + account summary
- [x] ActorIdentityRow + channel author honor styling
- [x] Badge SVG + CSS tokens
- [ ] Handler integration tests with DB
- [ ] CoreProvider presence heartbeat
- [x] Profile popover honor wall
- [ ] Ops grant endpoint
- [ ] `pnpm react:doctor` on current FE diff
