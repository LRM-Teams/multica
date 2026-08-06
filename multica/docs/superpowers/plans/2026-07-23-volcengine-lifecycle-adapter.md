# Volcengine voice call lifecycle adapter

## Goal

Connect the provider-neutral voice call lifecycle to the typed Volcengine
configuration builder, room token signer, and Start/Stop client without
duplicating provider rules in handlers.

## Provider boundary

- The room token is signed before `StartVoiceChat`, so entropy or token
  construction failures cannot leave a billable provider task.
- `StartVoiceChat` uses the same AppId, RoomId, and human UserId as the signed
  room token.
- A structured provider rejection is definitive and does not trigger an
  invented retry.
- A transport failure after the request may have reached Volcengine is
  ambiguous. The adapter issues `StopVoiceChat` with the same room/task IDs.
- If the compensating stop succeeds, Start returns a normal failure and the
  lifecycle may mark the session failed.
- If the compensating stop also fails, Start returns
  `ProviderStartUncertainError`; the lifecycle keeps the session non-terminal
  for recovery.

## Steps

- [x] Add failing ordering, exact-identity, and ambiguous-failure tests.
- [x] Expose the token signer's AppId as the single AppId source.
- [x] Implement the Volcengine lifecycle adapter.
- [x] Run package tests, vet, and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1043](https://github.com/LRM-Teams/multica/pull/1043), stacked on #1041.

## Verification

- The first targeted test failed because the adapter and AppId contract did not
  exist.
- Tests prove token signing happens before Start, configuration/token failures
  never call Start, and the signed token and Start/Stop requests share exact
  identities.
- Structured provider rejections skip compensation. Ambiguous transport
  failures compensate with a bounded context detached from request
  cancellation; failed compensation returns an uncertain error.
- `go test ./internal/service/voicecall ./internal/integrations/... -count=1`,
  `go vet ./internal/service/voicecall ./internal/integrations/...`, and
  `git diff --check` pass.
