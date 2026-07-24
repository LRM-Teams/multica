# Voice call React Query state

## Goal

Give shared web/desktop call UI one server-state contract for loading, creating,
and stopping a call without duplicating it into Zustand or caching RTC
credentials as durable state.

## Delivery record

- [x] Added workspace-and-call-scoped query keys.
- [x] Disabled detail queries until both identifiers are available.
- [x] Kept the detail query non-polling so the planned realtime invalidation is
  the sole freshness mechanism.
- [x] Added a create mutation that writes only `{ call }` into the detail
  query; media credentials remain in the mutation result needed for the
  immediate room join.
- [x] Added a stop mutation with an optimistic `ending` state.
- [x] Cancelled detail refetches before the optimistic stop update.
- [x] Restored the prior call on stop failure.
- [x] Replaced the optimistic state with the authoritative stop response and
  invalidated the detail query on settle.
- [x] Accepted workspace IDs as hook arguments instead of reading workspace
  context internally.
- [x] Added package exports for the shared query and mutation surface.
- [x] Added tests for query isolation, disabled fetches, credential-free cache,
  optimistic hang-up, rollback, and terminal replacement.
- [x] Observed the tests fail before the query/mutation modules existed.
- [x] Ran the full `@multica/core` suite: 87 files and 830 tests passed.
- [x] Ran core typecheck and lint.
- [x] Ran React Doctor: 0 issues.
- [x] Committed, pushed, and opened independent ready PR
  [#1095](https://github.com/LRM-Teams/multica/pull/1095), stacked on
  [#1093](https://github.com/LRM-Teams/multica/pull/1093).

## Boundary

This change does not invalidate calls from WebSocket events and does not render
the call surface. The query deliberately does not poll; realtime invalidation
must land before the UI relies on provider callback state changes.
