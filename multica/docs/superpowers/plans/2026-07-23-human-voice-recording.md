# Human voice recording delivery

## Goal

Send a human recording as a real voice message. The sender, other members, and agents see the same playable voice bubble. The transcript remains hidden until requested, while agents receive the transcript as message context.

## Boundaries

- This change does not alter agent-generated TTS behavior.
- This change does not add a second upload or speech API.
- The stored recording is a 16 kHz mono WAV made from the same decoded PCM sent to ASR.
- Historical human voice messages without an audio attachment remain non-playable; they are not replaced with synthesized speech.
- This branch contains only this feature and will produce one pull request targeting `dev`.

## Step log

- [x] Created `fix/human-voice-recording` from the merged `origin/dev` head.
- [x] Traced capture, ASR, message-part normalization, attachment binding, optimistic rendering, and playback paths.
- [x] Confirmed the root cause: the browser recording is discarded after ASR and the structured voice part has no attachment reference.
- [x] Added failing protocol, attachment-binding, WAV, optimistic-message, and UI playback tests; each failed at the missing behavior before implementation.
- [x] Store and bind the WAV recording while retaining the ASR transcript for agent context.
- [x] Render human recordings with the shared voice bubble and on-demand transcript.
- [x] Run focused and package-wide frontend tests, backend unit tests, handler compilation, lint, and repository TypeScript checks.
- [x] Reviewed the final diff, pushed `fix/human-voice-recording`, and opened ready PR #940 to `dev`.

## Verification evidence

- `packages/views`: complete suite passed — 241 files, 2,368 tests passed and 5 skipped.
- `packages/core`: complete suite passed — 78 files and 777 tests passed.
- Repository `pnpm typecheck` passed — 6 tasks across core, UI, views, docs, web, and desktop.
- React Doctor scanned the changed React source and reported 0 issues in core/views.
- Views/core lint passed with no errors; views reported three pre-existing unrelated unused-disable warnings.
- `server/pkg/protocol` and `server/internal/messageparts` passed; `server/internal/handler` compiled with `go test -c`.
- Handler integration execution is locally blocked before test selection by the pre-existing test database migration failure in `204_system_general_channel` (`trg_journal_workspace_radar_channel` does not exist). This is a local test-database bootstrap condition; handler compilation and CI remain required before merge.
- GitHub PR #940 CI passed: frontend, backend, Ubuntu installer, and macOS installer.

## Root-cause record

- The capture path converted the browser recording to PCM for ASR, then discarded the recording and sent only transcript text plus duration.
- The message protocol had attachment fields available in Go, but voice normalization deliberately erased them and attachment binding only inspected `attachment` parts.
- The renderer treated every human voice part as a non-playable label and reserved the playable bubble for Agent TTS.
- The repaired invariant is: a new human voice part carries a bound audio attachment; the Agent still reads canonical transcript text; the UI plays the attachment bytes and never synthesizes a human recording.
