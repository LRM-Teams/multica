# Voice thread-root preview

## Goal

Render a voice message consistently when it becomes the fixed root preview in
an open thread: show the playable voice bubble first and reveal its transcript
only through the transcript control.

## Evidence and execution log

- [x] Compared the main channel bubble with `ThreadRootPreview`. The main bubble
  resolves voice presentation once, hides transcript parts, consumes the raw
  recording attachment, and renders `VoiceMessageAudio`; the root preview only
  consumed the attachment and rendered the transcript as compact text.
- [x] Reused `VoiceMessageAudio` in the root preview instead of adding a second
  playback implementation or duplicate TTS state.
- [x] Applied the same transcript-hiding rule as the main bubble: agent voice
  replies and human recordings start with `non-transcript` content; malformed
  human voice metadata without a playable recording keeps its accessible text.
- [x] Added a recorded human voice-root test proving the transcript is absent
  initially, the duration bubble is present, and the transcript appears after
  the user clicks the transcript control.
- [x] Ran the full `@multica/views` Vitest suite: 246 files passed, 2414 tests
  passed, and 5 tests were skipped by their existing conditions.
- [x] Ran `pnpm --filter @multica/views typecheck` and `git diff --check`.
- [x] Pushed `fix/voice-thread-root-preview` and opened ready-for-review PR #967
  into `dev`: <https://github.com/LRM-Teams/multica/pull/967>.

## CI follow-up

- [x] Reproduced the blocking React Doctor warning from PR #967 locally.
- [x] Traced it to array-index keys in this test's `MessagePartsRenderer` mock
  after the mock gained text rendering; production rendering was not the
  reported location.
- [x] Replaced index keys with stable keys derived from the rendered sticker or
  text part.
- [x] Re-ran the full views suite (246 files, 2414 passing tests, 5 existing
  skips), React Doctor (0 issues), typecheck, and diff checks before updating
  the PR.

## Boundaries

- The DM conversation-list summary is a separate UI surface and will be fixed
  in its own PR.
- Voice playback and transcript panel styling remain owned by the existing
  shared voice component.
- No server files, containers, or production data were modified.
