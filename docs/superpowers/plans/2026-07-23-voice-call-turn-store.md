# Voice call provider turn store

## Goal

Persist complete provider subtitle turns exactly once while allowing a repeated
final callback to correct the transcript without creating a second turn.

## Delivery record

- [x] Added member and agent speaker types plus a provider-turn input contract.
- [x] Looked up the call exclusively by configured provider and provider task
  identity.
- [x] Added an upsert keyed by call session and provider turn identity.
- [x] Kept the original call sequence and row ID when a provider retry updates
  transcript text or interruption state.
- [x] Preserved transcript whitespace while rejecting whitespace-only turns.
- [x] Rejected invalid sequence, speaker, provider identity, task identity, and
  provider turn identity before querying PostgreSQL.
- [x] Classified an unknown provider task as `ErrCallNotFound`, allowing the
  callback endpoint to request a retry instead of acknowledging lost data.
- [x] Generated the sqlc voice-call query surface with sqlc v1.31.1.
- [x] Added unit tests for parameter mapping, validation, identity conversion,
  and missing-session classification.
- [x] Added a PostgreSQL test proving final-turn insertion, corrected retry
  upsert, distinct member/agent order, and exactly two stored rows.
- [x] Ran the database test in a disposable database and dropped it.
- [x] Committed, pushed, and opened independent ready PR
  [#1090](https://github.com/LRM-Teams/multica/pull/1090), stacked on
  [#1089](https://github.com/LRM-Teams/multica/pull/1089).

## Ordering rule

The future subtitle processor supplies a deterministic positive call sequence
from provider `roundId` and speaker. The store does not allocate with
`MAX(sequence) + 1`; that pattern can collide when two callbacks take their
statement snapshots before either insert commits.

## Generator note

The repository has pre-existing generated-code drift in unrelated query files.
Those unrelated outputs were removed from this change; the committed generated
voice-call file matches the authoritative voice-call SQL.
