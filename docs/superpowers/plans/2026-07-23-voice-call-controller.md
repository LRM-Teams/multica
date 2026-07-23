# Voice call controller

## Goal

Provide one browser-facing controller that owns the complete voice-call
lifecycle without exposing RTC credentials or provider cleanup ordering to UI
components.

## Completed

- [x] Create the server call before starting local RTC media.
- [x] Join with the short-lived media response without storing credentials in
      component state.
- [x] Expose call phases, mute, hang-up, and blocked-autoplay recovery.
- [x] Stop the server call once when RTC startup or an active provider session
      fails.
- [x] Cancel an in-flight start when the user hangs up.
- [x] Disconnect media and request server stop when the controller unmounts.
- [x] Reconcile server terminal status with local media cleanup.

## Verification

- [x] Focused controller tests: 10 passed.
- [x] Views package test suite: 257 files, 2510 passed, 5 skipped.
- [x] TypeScript typecheck: 6 tasks passed.
- [x] Lint: 8 tasks passed, no errors (8 unrelated existing warnings).
- [x] React Doctor: 0 issues in changed files.
