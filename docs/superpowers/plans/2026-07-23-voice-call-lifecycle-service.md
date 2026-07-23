# Voice call lifecycle service

## Goal

Own the provider-neutral start, get, and stop sequence for one-to-one Beckham
voice calls without exposing HTTP routes or embedding database/provider
implementations in the orchestration logic.

## Invariants

- Authorization runs before persistence or provider work.
- A starting session reserves the active member/Agent pair before context or
  provider work begins.
- Provider room, task, and Agent IDs derive from one server-generated nonce.
- The context builder, not an API caller, supplies Beckham's identity and
  conversation context.
- Context or provider start failure records a terminal failed session with a
  bounded error code.
- If provider start and its compensating stop both have uncertain outcomes, the
  session remains non-terminal for recovery.
- If the provider starts but the database cannot mark the call connecting, the
  service stops the provider before marking the session failed.
- If that compensating stop fails, the session remains non-terminal so a
  recovery worker can find and stop the provider task.
- Explicit stop is idempotent. An `ending` session retries the provider stop;
  an `ended` or `failed` session does not.
- Provider stop uses a bounded context detached from browser cancellation so
  closing the page cannot cancel billing cleanup.

## Steps

- [x] Add failing orchestration, compensation, idempotency, and cancellation
  tests.
- [x] Implement the provider-neutral lifecycle service.
- [x] Preserve the original session identity when the connecting transition
  fails so compensation can update the correct row.
- [x] Preserve starting state when the provider reports an uncertain start
  outcome.
- [x] Run package tests, vet, and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1041](https://github.com/LRM-Teams/multica/pull/1041), stacked on #1040.

## Verification

- The first test run failed because the lifecycle service did not exist.
- The next run exposed a zero-value session overwrite on a failed connecting
  transition; the regression test now proves provider stop happens before the
  correct session is marked failed.
- Context/provider failure, failed compensation, active/ending/terminal stop,
  uncertain provider start, repeated stop, and canceled browser request paths
  have deterministic tests.
- `go test ./internal/service/... -count=1`,
  `go vet ./internal/service/...`, and `git diff --check` pass.
