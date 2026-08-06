# Voice call PostgreSQL store

## Goal

Implement the lifecycle store against migration 215 with workspace/member
scoping and atomic state decisions.

## Query contract

- Create inserts `starting` and relies on the partial unique index to reject a
  second non-terminal call for the same workspace/member/Agent.
- Get is scoped by call, workspace, and member.
- Connecting only transitions from `starting`.
- Failed only transitions from a non-terminal status and records `ended_at`.
- Begin-ending locks the current row and decides in one statement:
  - a non-terminal/non-ending call becomes `ending`;
  - an `ending` call still requires provider Stop retry;
  - an `ended` or `failed` call does not call the provider.
- Ended only transitions from `ending`.

## Steps

- [x] Add sqlc query definitions and generate the query surface.
- [x] Qualify an existing ambiguous interaction-DAG column that blocked sqlc;
  this does not change query behavior.
- [x] Keep only the new voice-call generated query/model surface; discard
  unrelated drift caused by previously stale generated files.
- [x] Add failing UUID-mapping, workspace-scope, and real PostgreSQL state tests.
- [x] Implement the PostgreSQL lifecycle store.
- [x] Run unit tests, optional database integration tests, vet, and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1047](https://github.com/LRM-Teams/multica/pull/1047), stacked on #1043.

## Verification

- The first targeted test failed because no PostgreSQL store existed.
- Unit tests cover UUID validation before querying, database row conversion,
  and workspace/member scoping for create, get, connecting, failed, ending,
  and ended operations.
- The PostgreSQL integration test applies migration 215 in an isolated schema
  and verifies active-pair uniqueness, repeated ending, terminal idempotency,
  and legal transitions when `DATABASE_URL` is available.
- This machine has no `DATABASE_URL`, so that integration test reports an
  explicit skip; it does not report a false pass.
- `go test ./internal/service/voicecall ./pkg/db/generated -count=1`,
  `go vet ./internal/service/voicecall ./pkg/db/generated`, and
  `git diff --check` pass.
