# Voice call realtime state events

## Goal

Refresh a caller's shared call query after committed create, provider status,
and stop transitions without polling or exposing the call to other workspace
members.

## Delivery record

- [x] Added the `voice_call:updated` protocol event with only
  `workspace_id` and `call_id`.
- [x] Returned the committed session from provider status handling so the
  callback handler can route the event without a second database lookup.
- [x] Published the event after successful create, provider status, and stop
  service calls.
- [x] Scoped every event to the call session's `user_id`; a missing session
  identifier or recipient causes no publication instead of workspace fanout.
- [x] Added handler tests covering event payload, workspace routing, and the
  single-user recipient for create, provider status, and stop.
- [x] Added the event and payload to the shared TypeScript WebSocket contract.
- [x] Added exact React Query invalidation by event-supplied workspace and call
  identifiers.
- [x] Kept the event out of generic prefix invalidation to prevent a second,
  broad refresh.
- [x] Added workspace voice-call query invalidation on WebSocket reconnect and
  client replacement to recover provider transitions missed while offline.
- [x] Added tests for exact event invalidation, malformed payload rejection,
  and reconnect recovery.
- [x] Ran focused callback service, handler, and realtime synchronization
  tests.
- [x] Ran the full `@multica/core` suite: 87 files and 832 tests passed.
- [x] Ran core typecheck and lint.
- [x] Ran React Doctor: 0 issues.
- [x] Ran focused handler tests against a disposable database and relevant Go
  vet.
- [x] Committed, pushed, and opened independent ready PR
  [#1100](https://github.com/LRM-Teams/multica/pull/1100), stacked on
  [#1095](https://github.com/LRM-Teams/multica/pull/1095).

## Boundary

This change synchronizes call state only. It does not join an RTC room, capture
microphone audio, render call controls, or publish subtitle text through the
general WebSocket payload.
