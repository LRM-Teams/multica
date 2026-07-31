# Voice call audible-evidence repair

## Goal

Make the DM voice-call UI report Beckham as connected only after the expected
call-scoped agent stream has produced remote audio and browser playback has
started.

## Acceptance criteria

- A local RTC connection-state event cannot mark an unanswered provider task
  as connected.
- Audio from an unexpected remote user cannot mark Beckham as connected.
- The expected `voice-agent-<call nonce>` stream still stops ringback and marks
  the call connected after playback starts.
- Existing provider activation timeout and autoplay recovery remain intact.

## Non-goals

- Do not add browser speech synthesis, provider retry, or a synthetic greeting.
- Do not change voice-message behavior.
- Do not change production provider credentials.

## Evidence

- Production session `b09df972-63be-4a3f-bb21-d466fe05a873` started at
  `2026-07-31T02:59:50Z`, ended by the caller after 15 seconds, and retained
  `connected_at = NULL`, zero input/output audio, no error code, and no turns.
- The deployed UI nevertheless showed the call as connected.
- The controller currently maps a late local RTC `connected` event to the
  user-visible connected phase after provider startup.
- The remote-audio callback currently accepts any non-empty remote user ID.
- Volcengine `ListCallDetail` for the exact room shows the expected agent
  published about 32 Kbps of audio. The member received 20–40 Kbps from that
  agent with playback volume 90.31, zero packet loss, and zero stall duration.
  The remaining unowned boundary is the SDK's internal browser renderer:
  Multica treats its resolved `play()` promise as audible output without
  owning the `HTMLAudioElement` or its `MediaStreamTrack`.

## Work record

- [x] Read repository and voice-call rules.
- [x] Capture the reported production database session.
- [x] Query provider room/audio evidence for the exact call.
- [x] Add failing controller/media-session regressions.
- [x] Implement the smallest state/identity repair.
- [x] Run focused tests, full views tests, typecheck, lint, and React Doctor.
- [ ] Review diff, push a ready PR to `dev`, merge, and verify deployment.

## Verification

- Focused voice-call tests: 34 passed.
- Full `@multica/views` suite: 3,052 passed, 5 skipped after rebasing onto
  `dev`; the rebase exposed and this branch repairs two pre-existing locale
  parity failures from missing Japanese and Korean Honor labels.
- TypeScript: passed.
- ESLint: zero errors; five pre-existing warnings outside changed files.
- React Doctor: zero issues in changed React source.
- Production Next.js build: passed.
