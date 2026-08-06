# Voice call error contract

## Goal

Give the authenticated HTTP boundary stable error categories without exposing
PostgreSQL or RTC provider details.

## Delivery record

- [x] Add `ErrCallNotFound`, `ErrCallAlreadyActive`, and
  `ErrProviderFailure` service errors.
- [x] Translate only the active member-Agent call uniqueness constraint to
  `ErrCallAlreadyActive`; unrelated uniqueness failures remain internal.
- [x] Translate member-scoped session lookup and stop misses to
  `ErrCallNotFound`.
- [x] Classify provider start, uncertain start, invalid provider response, and
  provider stop failures while retaining their original error chain for
  server-side diagnosis.
- [x] Prove context/database failures are not mislabeled as provider failures.
- [x] Run all `server/internal/service/voicecall` tests, the real migration
  store test in an isolated PostgreSQL database, `go vet`, and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1081](https://github.com/LRM-Teams/multica/pull/1081), stacked on #1079.
