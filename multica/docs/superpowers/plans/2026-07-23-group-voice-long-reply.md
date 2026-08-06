# Group voice long-reply failure

Date: 2026-07-23

## Goal

Make an Agent's long voice reply in a group channel produce the same playable,
persisted voice bubble as a short reply or a direct-message reply.

## Constraints

- Change code locally and deploy through a pull request.
- Use the production server only for read-only evidence.
- Do not replace failed server synthesis with browser TTS.
- Keep the voice transcript hidden until the user requests it.

## Investigation log

### 1. Reproduced the UI state from code

`VoiceMessageAudio` renders the observed disabled bubble plus `Voice unavailable`
only when an Agent message owns server synthesis, synthesis is no longer pending,
and no recording attachment exists. The screenshot is one failure state, not two
voice components.

### 2. Checked the production backend log

The backend recorded:

```text
channel voice synthesis exhausted
message_id=7db301c4-f7e8-4346-8ce3-ae51919417c6
attempt=1
error_code=invalid_audio
error="voice provider returned invalid PCM audio"
```

### 3. Checked the production row without modifying it

The message belongs to a `group`, contains 319 characters, has a voice part with
`synthesis_status=failed`, and has no generated attachment because synthesis was
rejected before persistence.

### 4. Isolated the code defect

The provider client accepts up to 32 MiB of synthesized audio. The HTTP TTS path
also encodes the provider result without a 2 MiB limit. Only the persisted
channel-synthesis path rejects PCM above `maxVoicePCMBytes`.

That constant is the inbound ASR recording-body limit. At 24 kHz, signed 16-bit
mono PCM, 2 MiB represents about 43.7 seconds. Reusing it for outbound TTS makes
long group reports fail even though the provider returned valid audio.

### 5. Regression test

Added a focused test that passes valid 24 kHz PCM just above the recording upload
limit through the shared synthesized-audio encoder. It must produce a WAV longer
than 43 seconds.

Status before implementation: expected compile failure because the shared
encoder and the recording-specific constant name do not exist yet.

## Implementation and verification

### 6. Implementation

- Renamed the 2 MiB constant to `maxVoiceRecordingPCMBytes`, so its inbound ASR
  scope is explicit.
- Added `encodeSynthesizedPCM16WAV` as the common validator/encoder for both
  direct HTTP TTS and persisted channel TTS.
- Removed the recording-upload limit from channel TTS. The provider client's
  existing 32 MiB output bound remains the single synthesized-audio size limit.

### 7. Verification

- Red phase: the focused handler regression did not compile because
  `maxVoiceRecordingPCMBytes` and `encodeSynthesizedPCM16WAV` were absent.
- `go test ./internal/voiceaudio -count=1`: passed.
- `go test ./internal/integrations/doubaospeech -count=1`: passed.
- `go test -c ./internal/handler`: passed, including compilation of the handler
  regression test.
- `go vet ./internal/handler ./internal/voiceaudio
  ./internal/integrations/doubaospeech`: passed.
- `git diff --check`: passed for the server changes.

The handler test binary cannot execute locally because its shared local database
is partially migrated: migration `204_system_general_channel` attempts to drop
the absent trigger `trg_journal_workspace_radar_channel`. This occurs in
`TestMain` before the selected test runs. It is unrelated to this change;
production already has migrations through 214. No migration code was changed to
hide that local fixture problem.

## Result

The production failure was caused before attachment persistence, so the
existing failed message remains failed. New long Agent voice replies will retain
the provider output, receive a WAV attachment, publish
`channel:message_updated`, and render as playable voice bubbles.
