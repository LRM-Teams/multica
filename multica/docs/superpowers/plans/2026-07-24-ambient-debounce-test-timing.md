# Ambient debounce test timing

## Finding

- [x] Located the backend CI failure in
      `TestTouchChannelAmbientDebouncesAndClaimsAfterDelay`.
- [x] Confirmed the production implementation was unchanged by the voice-call
      integration.
- [x] Confirmed the test used a 50 ms wall-clock deadline while performing real
      PostgreSQL operations, so a loaded runner could cross the deadline before
      the first claim assertion.

## Change

- [x] Replaced the wall-clock sleep with a deterministic deadline assertion.
- [x] Advanced only the test row's deadline before checking the due-claim path.

## Verification

- [x] Run the focused test 50 times against a database migrated from zero.
- [x] Run the complete `workgraph` package tests.
- [ ] Run diff checks and GitHub CI.
