# DM voice message summary

## Goal

Keep hidden voice transcripts out of the direct-message conversation list while
still showing who sent the latest message and how long the recording is.

## Evidence and execution log

- [x] Traced `DmConversationRow` to `formatChannelMessagePreview`. Structured
  voice messages contain a text part for accessibility, so the generic preview
  formatter exposed that transcript as ordinary list text.
- [x] Made the preview formatter recognize a structured voice part before
  projecting text, mentions, markdown, or historical envelopes.
- [x] Added a localized voice summary callback at the DM row: `Voice message`
  when duration is unavailable and `Voice message · N seconds` when present.
- [x] Added English, Simplified Chinese, Japanese, and Korean strings and kept
  locale parity.
- [x] Kept a safe generic English summary inside the formatter so a future
  reading surface cannot leak a transcript merely by omitting localization.
- [x] Added tests for localized duration rounding and the non-localized safe
  path; both assert that the transcript is absent.
- [x] Ran the focused preview and locale-parity suites: 2 files and 219 tests
  passed.
- [x] Ran `pnpm --filter @multica/views typecheck` and `git diff --check`.
- [x] Ran ESLint on the three changed TypeScript/React files.
- [x] Pushed `fix/dm-voice-summary` and opened ready-for-review PR #968 into
  `dev`: <https://github.com/LRM-Teams/multica/pull/968>.

## Boundaries

- Conversation-list rows remain text-only; the playable bubble stays inside
  the opened conversation and thread-root preview.
- Historical legacy audio without a structured voice part is unchanged because
  the DM list payload does not carry attachments needed to identify it safely.
- No server files, containers, or production data were modified.
