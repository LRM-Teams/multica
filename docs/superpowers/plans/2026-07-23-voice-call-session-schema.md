# Voice call session schema

Date: 2026-07-23

## Goal

Add durable PostgreSQL state for one-to-one Beckham voice calls before exposing
provider credentials, callbacks, APIs, or UI.

## Scope

- `voice_call_session`
- `voice_call_turn`
- allowed status transitions
- one non-terminal call per workspace/user/Agent pair
- idempotent provider task and turn identities
- timestamp, duration, speaker, and sequence constraints

This PR does not start a provider session and does not add an HTTP endpoint.

## TDD log

### Cycle 1: one non-terminal call per pair

Test: insert one `starting` session, reject a second `connecting` session for
the same workspace/user/Agent pair, finish the first session, then allow a new
session.

Status: RED pending.

Result:

- RED: a completed provider task ID could be reused by another call.
- GREEN: added provider-scoped unique indexes for task and room IDs.

### Cycle 7: reversible migration

Test: apply the down migration and confirm both tables and the transition
function are absent.

Status: coverage added; the existing down order passed without modification.

## Verification

- `go test ./cmd/migrate -run 'TestVoiceCallSessionMigration215' -count=1`
  passed.
- `go test ./cmd/migrate -count=1` passed.
- `git diff --check` passed.
- Refetched `origin/dev`; migration 214 is still the latest upstream number, so
  migration 215 has no current numbering conflict.

## Result

The database now has a provider-neutral call and final-turn state model. No
provider session can be started by this PR; the next slice must add authenticated
server APIs and a provider interface against these constraints.

Result:

- RED: all inconsistent turn facts were accepted.
- GREEN: constrained final turns to positive sequences, `member|agent`
  speakers, non-blank transcripts, complete ordered timestamps, non-negative
  spoken duration, and non-blank provider IDs when present.

### Cycle 6: provider session idempotency

Test: retain one terminal provider session, then reject reuse of either its
provider task ID or provider room ID.

Status: RED pending.

Result:

- RED: every case except the already bounded status value was accepted.
- GREEN: added provider, terminal timestamp, timestamp ordering, non-negative
  duration, and update-time checks.

### Cycle 5: final turn fact consistency

Test: reject sequence zero, an unknown speaker, a blank final transcript,
missing turn time, an end before start, and negative spoken duration.

Status: RED pending.

Result:

- RED: `voice_call_turn` did not exist.
- GREEN: added durable final turns with a per-call sequence key and a
  per-call/provider-turn unique key.

### Cycle 4: session fact consistency

Test: reject blank providers, unknown statuses, terminal calls without
`ended_at`, non-terminal calls with `ended_at`, connection before start, and
negative audio durations.

Status: RED pending.

Result:

- RED: migration file was absent.
- GREEN: added the session record and a partial unique index covering every
  status except `ended` and `failed`.

### Cycle 2: legal status transitions

Test: walk a call through
`starting -> connecting -> active -> reconnecting -> active -> ending -> ended`,
then reject an attempt to restart the terminal call.

Result:

- RED: the terminal session could be changed back to `active`.
- GREEN: added a bounded status check and a transition trigger.

The trigger also permits `starting`, `connecting`, or `reconnecting` to enter
`ending`. A user must be able to hang up before media connects or while the
provider reconnects; restricting hang-up to `active` would make the stop API
non-idempotent in those states.

### Cycle 3: durable turn idempotency and ordering

Test: store the first final member turn, reject a provider callback retry with
the same provider turn ID, reject a different turn at the same sequence, then
store the next Agent turn.

Status: RED pending.
