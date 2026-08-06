# Voice call callback state

## Goal

Apply authenticated Volcengine conversation-status callbacks to persisted call
sessions without allowing retries, late delivery, or startup races to regress a
session.

## Delivery record

- [x] Added a callback service that maps provider stages 1 through 5 to call
  activity and stage 0 to a namespaced provider error code.
- [x] Added provider/task-scoped SQL operations; callback state never trusts a
  workspace or member identity from the public request.
- [x] Made activity and failure callbacks idempotent.
- [x] Preserved `ended` and `failed` terminal sessions when late callbacks
  arrive.
- [x] Made local failure and end writes terminally idempotent, including the
  race where a provider error arrives while a user hangup is waiting for
  `StopVoiceChat`.
- [x] Handled the race where the first provider callback arrives after
  `StartVoiceChat` starts but before the member request writes `connecting`.
  The callback records connection evidence and the start transition promotes
  that row to `active`; the opposite ordering also reaches `active`.
- [x] Returned a retryable processing failure for an unknown provider task
  instead of acknowledging and discarding an event that may have raced
  persistence.
- [x] Wired the callback processor and configured shared signature into the
  runtime atomically with the member call service.
- [x] Added unit tests for all six provider stages, storage error propagation,
  provider identity mapping, and runtime wiring.
- [x] Added PostgreSQL tests for callback/start ordering, duplicate activity,
  duplicate failure, late activity, late failure, and failure during hangup.
- [x] Ran the database tests in disposable databases and dropped them.
- [x] Committed, pushed, and opened independent ready PR
  [#1087](https://github.com/LRM-Teams/multica/pull/1087), stacked on
  [#1086](https://github.com/LRM-Teams/multica/pull/1086).

## Verification note

`sqlc` v1.31.1 regenerated the voice-call query surface. The repository has
pre-existing generated-code drift in unrelated query files; those unrelated
outputs were removed from this change. The committed generated voice-call file
matches the authoritative voice-call SQL.
