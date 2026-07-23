# Upload-only voice send work log

Date: 2026-07-23

## Goal and boundary

- Send the recorded WAV immediately after upload.
- Do not call ASR in the browser or require a transcript before message send.
- Use the same behavior for group channels, DMs, and their threads.
- Keep server transcription state and realtime transcript updates in a separate
  PR.

## Step 1 — Remove browser ASR

Completed.

- `deliverVoiceRecording` now performs one upload and returns its attachment.
- A missing attachment ID remains an explicit send error.
- The caller deletes an unbound upload when the composer rejects the send.
- Upload errors report upload failure instead of transcription failure.

## Step 2 — Send the recording shape

Completed.

- Group, DM, group-thread, and DM-thread sends now use an empty content string
  plus one voice part containing duration and attachment metadata.
- The shared callback signature no longer accepts a browser transcript, so a
  surface cannot accidentally restore the old path.

## Step 3 — Keep pending recordings usable

Completed.

- A recording with empty content remains playable from its stored WAV.
- The transcript control is absent until canonical transcript text exists.
- Busy animation respects reduced motion; changing upload/playback state is
  exposed as a polite live region.

## Step 4 — Verification

Completed.

- `pnpm --filter @multica/views typecheck`: passed.
- Focused delivery, WAV, playback, and Composer suite: 4 files and 27 tests
  passed.
- `pnpm react:doctor`: 8 changed React files scanned, 0 issues.
- Web Interface Guidelines review found no missing labels, focus states, reduced
  motion handling, or transcript-empty-state violations in the changed UI.
