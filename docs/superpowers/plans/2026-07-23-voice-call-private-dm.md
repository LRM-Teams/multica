# Private agent voice call entry

## Goal

Expose realtime voice calls only in agent direct messages and connect the
existing controller to the reusable call panel.

## Completed

- [x] Added an agent-only phone action to the DM header.
- [x] Passed the current workspace, DM channel, and agent identity to call
      creation.
- [x] Kept active calls open until server hang-up succeeds.
- [x] Wired mute, retry, and browser autoplay recovery actions.
- [x] Added call duration tracking that survives mute and reconnect phases.
- [x] Kept human direct messages unchanged.

## Verification

- [x] Focused DM call tests: 6 passed.
- [x] Views package suite: 259 files, 2526 passed, 5 skipped.
- [x] Monorepo typecheck: 6 tasks passed; final views typecheck also passed.
- [x] Views lint: 0 errors; 4 pre-existing warnings outside this change.
- [x] React Doctor: 0 issues after moving the duration formatter out of the
      component module.
