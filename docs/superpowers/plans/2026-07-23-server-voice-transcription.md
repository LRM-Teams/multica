# Server-side voice transcription work log

Date: 2026-07-23

## Goal and boundary

- Accept a recorded voice message before ASR finishes.
- Persist the audio message first, then transcribe on the backend and only then
  dispatch the message to group, DM, or thread Agents.
- Keep this backend capability in its own PR. The browser switch to upload-only
  delivery is a separate follow-up PR.
- Keep the original WAV attachment as the user-visible and replayable message.

## Step 1 — Verify the existing behavior

Completed.

- `VoiceInputButton` decoded the recording, uploaded WAV, and called
  `/api/voice/asr` in parallel.
- It invoked channel send only after ASR returned a non-empty transcript.
- Any ASR error deleted the uploaded attachment, so no voice message reached
  the conversation.
- Channel and thread handlers dispatched Agent work immediately after message
  creation; they had no pending-transcription state.

## Step 2 — Define the persisted state

Completed.

- Added server-owned voice-part states: `pending`, `completed`, and `failed`.
- A transcript-free voice part is accepted only when it contains exactly one
  recorded attachment.
- Added `channel_voice_transcription` with claim, retry, transcript, and error
  state. The message row and transcription task are committed in the same
  transaction.
- Attachment/channel/workspace/message ownership is checked before queue
  insertion.

## Step 3 — Implement backend ASR and dispatch

Completed.

- The HTTP handler publishes and acknowledges the pending voice message before
  starting ASR.
- The backend reads the already-bound attachment from configured storage.
- WAV parsing requires RIFF/WAVE, 16 kHz, mono, PCM16, complete even-sized
  samples, and the existing 2 MiB PCM limit.
- Successful ASR updates the same message with a canonical text part, publishes
  the updated message, then runs the existing group/DM/thread dispatch path.
- The transcript passes through the existing destination-scoped mention and
  issue-reference resolver before dispatch. Mentioned thread members are
  followed after enrichment, matching typed-message behavior.
- Invalid/no-speech recordings become `failed`. Provider/storage failures retry
  at 5 and 30 seconds, with three bounded attempts.

## Step 4 — Make processing recoverable

Completed.

- New sends attempt processing immediately after the HTTP response.
- A DB-backed scheduler scans every 30 seconds.
- Stale processing and dispatch claims are reclaimed after two minutes.
- `transcribed` is a durable boundary before Agent dispatch, so a process exit
  cannot force ASR to run again merely because dispatch had not started.
- Existing idempotent Agent inbox/coalescing paths handle the narrow
  at-least-once dispatch boundary.

## Step 5 — Verification

Completed for the changed surfaces.

- `go test ./internal/messageparts`: passed.
- `go test ./internal/voiceaudio`: passed.
- `go test ./internal/scheduler -run TestChannelVoiceTranscriptionJobContract`:
  passed.
- `go test ./cmd/server -run TestNonexistent`: compile passed.
- `go test -c ./internal/handler`: compile passed.
- `go vet ./internal/voiceaudio ./internal/messageparts ./internal/handler
  ./internal/scheduler ./cmd/server`: passed.
- Migration 213 was applied to the local PostgreSQL database, queried, then
  rolled back successfully.
- The existing local handler test database cannot run handler tests because its
  pre-existing migration ledger is missing `204_system_general_channel` while
  later migrations are already recorded. This is local test-state damage, not
  an application path; no product code is being changed to hide it.
- A full scheduler package run reaches unrelated Radar integration tests, then
  fails because the same local database is missing `workspace_radar_run_scan`,
  `change_version`, and Radar functions. The focused new scheduler contract
  test passes.
