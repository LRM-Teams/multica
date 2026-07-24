# Voice call panel

## Goal

Add the reusable, localized call surface before wiring it into direct messages.

## Completed

- [x] Reused Multica dialog, button, avatar, theme tokens, and Lucide icons.
- [x] Added visible states for connecting, connected, muted, reconnecting,
      ending, ended, and failed calls.
- [x] Added mute, hang-up, retry, blocked-autoplay recovery, and close actions.
- [x] Kept stop failures distinguishable so the user can retry hang-up instead
      of hiding a possibly active call.
- [x] Added English, Simplified Chinese, Japanese, and Korean copy.
- [x] Added a duration formatter for stable `m:ss` display.

## Verification

- [x] Added 10 panel/formatter tests.
- [x] Views package suite: 258 files, 2520 passed, 5 skipped.
- [x] Monorepo typecheck: 6 tasks passed.
- [x] Views lint: 0 errors; 4 pre-existing warnings outside this change.
- [x] React Doctor: 0 issues in changed packages.
- [x] Locale JSON parsed and `voice_call` key parity checked across all four
      shipped locales.
