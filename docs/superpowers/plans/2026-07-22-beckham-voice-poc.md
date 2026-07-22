# Beckham Voice POC

Date: 2026-07-22

## Goal

Prove the voice boundary needed by Beckham with the existing Multica backend as the only conversation brain: speech audio enters through Doubao streaming ASR, Beckham continues to own context and tool use, and response text leaves through Doubao streaming TTS. Keep LiveKit and Tavus outside this first provider-connectivity slice.

## Working rules

- Never persist provider credentials in source, fixtures, logs, command output, or Git history.
- Query and test the current provider protocol instead of copying a legacy AppID/Access Token integration into the new API Key account.
- Keep provider transport behind a server-owned interface so later LiveKit and Tavus integration does not duplicate vendor protocol code.
- Treat provider failures as explicit product errors. Do not silently substitute browser speech, another model, or generated audio.
- Record evidence, decisions, changes, and verification after each completed step.

## Step log

### Step 1 — Establish the implementation baseline

Status: complete

Evidence:

- Local `dev` was fast-forwarded to `3c3b48dc5ebff621298dc16ea8901becf224b113` from `origin/dev`.
- Work continues on the clean branch `agent/beckham-voice-poc`.
- The repository has no existing LiveKit, Tavus, ASR, TTS, microphone, or voice-session implementation to extend. Existing audio support is limited to attachment preview and media MIME handling.
- The customer account has active Doubao Speech 2.0 services: streaming ASR hourly quota and TTS character quota. The credential is a new-console API Key, not the legacy AppID/Access Token pair.
- The chosen stock TTS speaker is `zh_male_m191_uranus_bigtts`.

Decision:

- Implement and verify a reusable server-side Doubao ASR/TTS boundary first.
- Do not add LiveKit, Tavus, or a permanent product UI until the provider round trip is proven and the voice-session privacy/context surface is specified.
- Add authenticated connectivity endpoints only when they exercise the same production client used by the later voice session; do not ship a parallel test script as the integration.

### Step 2 — Implement and prove the provider transport

Status: complete

Evidence:

- Added a server-owned Doubao Speech WebSocket client with bounded frame parsing, gzip handling, protocol sequence/event validation, deadlines, and sanitized provider errors.
- TTS uses the current API-key protocol and resource `seed-tts-2.0`; ASR 2.0 hourly billing uses `volc.seedasr.sauc.duration`. The earlier candidate `volc.bigasr.sauc.duration` belongs to ASR 1.0 and is intentionally not retried.
- Unit tests pin provider headers, exact resource IDs, TTS session events, ASR sequence/final-frame semantics, malformed frames, and API-key redaction.
- An opt-in live test synthesized “你好，我是贝克汉姆。” as 16 kHz PCM with the chosen speaker, most recently received 63,294 bytes, and ASR returned the same sentence. The credential was passed only through the process environment.
- The 2 MiB PCM request ceiling represents about 65.5 seconds at the fixed audio format; the provider deadline is 90 seconds so every accepted request can finish instead of timing out by construction. Voice responses use `Cache-Control: no-store`.
- ASR reads provider partials concurrently with paced audio writes, preventing a long recording from deadlocking when the receive buffer fills. TTS rejects cumulative provider audio above 32 MiB.

Unexpected issue review:

- The shared local test database records migration 169 as applied but is missing its channel radar trigger, so handler TestMain cannot apply migration 204. A fresh isolated database applies the migrations and runs the new handler tests successfully. This is local schema/ledger drift, not a reproducible clean-install or production migration defect; no migration was changed.

### Step 3 — Expose the production client through bounded HTTP endpoints

Status: complete

Evidence:

- Added `POST /api/voice/tts`: one strict JSON object containing `text`, at most 32 KiB / 4,096 Unicode code points; returns 24 kHz MP3.
- Added `POST /api/voice/asr`: `audio/pcm`, signed 16-bit little-endian mono at 16 kHz, at most 2 MiB; an explicit MIME `rate` must equal 16000; returns the final transcript as JSON.
- Both endpoints are behind the existing authentication chain, workspace membership, and `RequireHumanActor`, preventing task tokens, agent credentials, and cloud PATs from consuming the account's speech quota.
- Provider credentials remain in backend environment variables. Unconfigured deployments return coded 503 responses; upstream failures return sanitized coded 502 responses with diagnostic provider code/log ID kept in server logs only.
- Self-host Compose passes the server-only credential and defaults the non-secret speaker ID; the s89 deploy workflow accepts the API key from its Environment secret.
- Handler tests cover successful audio/transcript responses, strict JSON, fixed media type/sample contract, odd PCM rejection, unconfigured state, provider error sanitization, and `no-store` response caching.

### Step 4 — Verify the rebased implementation and deployment inputs

Status: complete

Evidence:

- Fetched and rebased the implementation without conflict from the original baseline onto final `origin/dev` commit `2b69c609c307389831c6ccad96b5aede86e778ed`.
- `go test -race ./internal/integrations/doubaospeech -count=1` passed.
- The live TTS-to-ASR round trip passed again after the rebase and required the transcript to contain the spoken name, preventing an arbitrary non-empty result from counting as success.
- On a newly created isolated database, `go test ./internal/handler -run Voice -count=1` passed after applying the complete migration history; `go test ./cmd/server -run '^$' -count=1` compiled the router package. The temporary database was removed afterward.
- `go vet ./internal/integrations/doubaospeech ./internal/handler ./cmd/server` passed.
- `docker compose --env-file .env.example -f docker-compose.selfhost.yml -f docker-compose.s89.yml config --quiet` passed.
- The s89 GitHub Environment now contains `DOUBAO_SPEECH_API_KEY`; listing its metadata confirms the secret exists without exposing its value. A repository scan confirms the supplied credential is absent from all working-tree files.
- No React/Next.js file changed, so React Doctor and browser UI verification are not applicable to this provider/API slice.

### Step 5 — Publish for the dev deployment workflow

Status: complete

Evidence:

- Pushed branch `agent/beckham-voice-poc` to `origin` with only the reviewed voice implementation, tests, deployment inputs, and this record.
- Opened GitHub PR #868 against `dev`: `feat(beckham): add Doubao ASR/TTS voice transport`.
- Marked the PR ready for review rather than leaving it as a draft; GitHub reports the branch mergeable with CI checks running.
- Deployment is intentionally not triggered from the feature branch. The existing workflow deploys after the user merges the PR into `dev`.
