# Human voice transcript MIME repair

## Goal

Make a browser-recorded WAV transcribable after upload, including recordings
already persisted with Go's detected `audio/wave` media type.

## Evidence

- Production message `e0d6d7c9-77bf-43be-b5d1-949bd4026ebd` was created at
  2026-07-23 16:26 CST.
- The transcription worker failed before contacting ASR:
  `recorded voice content type "audio/wave" is not audio/wav`.
- The upload handler uses `http.DetectContentType`; Go reports RIFF/WAVE bytes
  as `audio/wave`.
- The transcription reader accepted only the alias `audio/wav`.
- A read-only production query found exactly one recoverable row, the same
  message above; unrelated failed jobs are outside the migration predicate.

## Steps

- [x] Reproduce the upload/reader MIME disagreement from production evidence.
- [x] Add failing tests for upload canonicalization and persisted
  `audio/wave` recordings.
- [x] Canonicalize new `.wav` uploads to `audio/wav`.
- [x] Accept registered WAV MIME aliases at the transcription boundary.
- [x] Requeue only failed jobs caused by this exact historical MIME mismatch.
- [x] Run targeted tests, Go vet, and migration verification.
- [x] Commit, push, and open independent ready PR
  [#1030](https://github.com/LRM-Teams/multica/pull/1030).

## Validation

- `TestCanonicalUploadContentTypeNormalizesWAV` observes Go identify real RIFF
  bytes as `audio/wave`, then verifies upload canonicalization.
- `TestReadChannelVoicePCMAcceptsGoDetectedWAVMediaType` verifies the persisted
  historical type reaches the exact PCM decoder.
- `TestCreateUserChannelMessagePersistsVoiceTranscriptionJob` verifies a
  successful transcript advances to the completed dispatch state.
- `TestChannelVoiceTranscriptionMIMERepairMigration216` verifies only
  `invalid_recording` jobs with a WAV alias are requeued; provider failures and
  non-WAV files remain terminal.
- Targeted handler tests passed against a fresh temporary database.
- The complete `cmd/migrate` test package and `go vet` passed.
