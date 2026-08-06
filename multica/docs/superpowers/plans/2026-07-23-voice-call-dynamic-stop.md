# Voice call dynamic stop

## Goal

Allow a caller to stop the call returned by the create request immediately,
including the failure path before React has rendered the new call ID.

## Completed

- [x] Changed `useStopVoiceCall` to accept the call ID when the mutation runs.
- [x] Kept optimistic state, rollback, replacement, and invalidation scoped to
      that exact call.
- [x] Updated existing mutation tests for the runtime call ID.
- [x] Added coverage proving a call created after render can be stopped.

## Verification

- [x] Focused voice-call mutation tests: 4 passed.
- [x] Core package test suite: 87 files, 833 tests passed.
- [x] TypeScript typecheck: 6 tasks passed.
- [x] Lint: 8 tasks passed, no errors (8 unrelated existing warnings).
- [x] React Doctor: 0 issues in changed files.
