# Voice call DM authorizer

## Goal

Bind every one-to-one Beckham voice call to the authenticated member's existing
canonical human-Agent DM and reuse the current private-Agent access rules.

## Authorization contract

- The caller must still be a workspace member and a member of the DM.
- The channel must be a live `dm`, not a group, missing, or archived channel.
- The canonical DM name and resolved peer must both match the requested Agent.
- The Agent must exist, belong to the workspace, and not be archived.
- Private Agents use the existing owner/admin/group-manager access predicate.
- Scope failures are typed as not found, forbidden, or unavailable so the HTTP
  layer can map them without matching error strings.

## Steps

- [x] Add failing tests for canonical Agent binding, archived DMs, and private
  Agent access.
- [x] Implement a `voicecall.Authorizer` adapter around existing handler
  membership, DM peer, and private-Agent checks.
- [x] Run the database regressions in a fresh isolated PostgreSQL database.
- [x] Run voice-call unit tests, vet, and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1049](https://github.com/LRM-Teams/multica/pull/1049), stacked on #1047.

## Verification

- The first targeted run failed because the authorizer and typed scope errors
  did not exist.
- The repository's reused local test database cannot replay migration 204
  because it is in an unrelated partial migration state. No migration was
  changed to mask that condition.
- A fresh temporary PostgreSQL database applied the full migration history and
  passed all three authorizer regressions.
- `go test ./internal/service/voicecall -count=1`,
  `go vet ./internal/handler ./internal/service/voicecall`, and
  `git diff --check` pass.
