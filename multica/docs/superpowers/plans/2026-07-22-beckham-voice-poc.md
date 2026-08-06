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

- Added `POST /api/voice/tts`: one strict JSON object containing `text`, at most 32 KiB / 4,096 Unicode code points; returns self-describing 24 kHz mono PCM WAV. The endpoint originally returned MP3; the production voice-bubble fix switched to WAV after the provider MP3 was verified valid but the browser playback path produced noise.
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

### Step 6 — Define the product voice-message contract

Status: complete

Evidence:

- Inspected the production host through its configured `river-v2` SSH alias. The active backend is started by the Actions workspace with `docker-compose.selfhost.yml` plus `docker-compose.s89.yml`; the running pre-merge container correctly has no `DOUBAO_*` variables yet. Server inspection was read-only.
- Fetched `origin/dev` at `055b37124` and merged its three new commits into the feature branch before changing the product UI.
- The shared `Composer` renders the group, DM, and thread inputs; channel `parts` are already the durable, idempotency-protected structured-message field; `multica message send` is the only supported visible Agent reply path. These are the interfaces extended by this slice.

Decision:

- A transcribed human recording is stored as ordinary visible text plus a structured `voice` part. The original compressed recording is not uploaded or retained in this slice.
- A voice-origin message tells the Agent, through its task prompt, to reply with `multica message send --voice`. For typed messages, the Agent applies the user's semantics: ordinary typed requests use normal text sends, while an explicit request for spoken output uses `--voice`. No client keyword list decides intent.
- `--voice` persists the same structured `voice` part on the Agent message. Clients synthesize that canonical response text through the authenticated backend TTS endpoint, attempt playback only for a reply created after a local send unlocked audio, and always expose an explicit replay control.
- Browser capture is decoded locally, downmixed, resampled, and converted to the backend's fixed signed 16-bit little-endian mono 16 kHz PCM contract. Recording, decode, ASR, and TTS failures remain visible and retryable; they do not silently switch providers or change the message modality.
- The existing GitHub Environment secret and Compose injection remain the only credential path. No speech credential is sent to the browser or added to source.

### Step 7 — Implement the shared message voice experience

Status: complete

Evidence:

- Added one microphone control to the shared Composer, so group channels, DMs, and their threads use the same capture and send implementation on web and desktop. It records at most 60 seconds, decodes in the browser, downmixes/resamples to the server's exact PCM contract, calls authenticated ASR, and sends the transcript as text plus a structured voice part through the existing idempotent mutation.
- Added `multica message send --voice` to the existing Agent delivery command. The runtime brief defines when to use it, and voice-origin prompts reinforce that instruction for directed, ambient, unread, and thread execution paths. Typed content is not classified with server or frontend keyword matching.
- Added a shared Agent voice control that synthesizes the stored transcript on demand, exposes loading/play/stop/retry states, and attempts autoplay only after the local user initiated a send. A human recording renders its transcript and recorded duration; the original recording is intentionally not retained.
- The voice protocol requires a non-empty text part and caps its transcript at the TTS limit. Human and Agent voice direction is labelled accurately in Agent history context.
- Added macOS `NSMicrophoneUsageDescription` to the packaged desktop app. Electron's existing default media-permission handling remains unchanged; no broad permission handler was added.

Unexpected issue review:

- A malformed-ASR JSON guard was initially applied to an unrelated cloud-runtime response while editing the large API client. Review moved it to the ASR boundary and restored the unrelated method exactly.
- Autoplay eligibility originally remained pending after an Agent text reply or a failed recording, allowing a later unrelated voice message to play. The scope is now cancelled on capture/transcription/send failure and consumed by the first new Agent reply regardless of modality.
- Agent voice history was initially labelled as “voice input.” The formatter now distinguishes human voice input from Agent voice reply, with a regression test.
- A malformed successful ASR response initially degraded to the same empty string as a valid “no speech” result. It now raises a controlled voice-service error, so the UI reports transcription failure instead of blaming the recording.
- React lifecycle review found that depending on the complete `voice` part object could stop active playback when an equivalent React Query refresh recreated message parts. Autoplay now depends only on stable message fields and voice-part presence; component tests prove an equivalent refresh does not interrupt playback and unmount still stops it.

### Step 8 — Rebase-equivalent merge and final product verification

Status: complete

Evidence:

- Merged final `origin/dev` commit `0f3e669f4b6784c92d8f378daa29fad548ddd70f` into the feature branch without conflicts, then reviewed the shared group, DM, message-bubble, and Composer wiring that changed on both sides.
- `pnpm typecheck` passed all six executable workspace typecheck tasks. `pnpm lint` passed all eight tasks with zero errors; the seven warnings are unchanged files outside this feature.
- `pnpm test` passed all eight workspace tasks after the merge. The affected packages report Core 757/757 and Views 2,281 passed with five pre-existing skips; web and desktop also passed.
- Voice-specific API, PCM conversion, autoplay eligibility, Composer, group/DM send, message rendering, and lifecycle tests passed. The new lifecycle tests cover Agent autoplay, equivalent message refresh, and stopping playback on unmount.
- `go test ./internal/messageparts ./cmd/multica ./internal/daemon/execenv -count=1` passed. The three voice handler tests passed against a newly created isolated PostgreSQL database, which was removed afterward.
- React Doctor scanned the ten changed React files and reported zero issues. `git diff --check`, Compose resolution, and the packaged desktop `NSMicrophoneUsageDescription` assertion passed.
- GitHub Environment `s89` contains `DOUBAO_SPEECH_API_KEY`, and the workflow maps it into the backend container. Only the secret name and update timestamp were inspected; its value was not read or logged.

Boundary:

- Local automated tests cannot exercise a real browser/desktop microphone, speaker autoplay policy, or the deployed provider network path. Those checks must run after this PR is merged and the existing CD workflow deploys it; no server file was modified directly.

### Step 9 — Diagnose and remove the shared-database CI race

Status: complete

Evidence:

- The first post-push frontend CI failure was a transient HTTP/2 `INTERNAL_ERROR` while the GitHub Runner downloaded `go-redis` from `proxy.golang.org`; application compilation had not failed. No product fallback or dependency change was added.
- The backend failure occurred while a handler test inserted a radar binding for a random temporary workspace. At the same time, the scheduler package's concurrency test calls the production-wide binding repair, which scans every eligible Wendy workspace and can insert the same row.
- `go test ./...` runs packages in parallel while CI gives every package the same PostgreSQL database. The colliding workspace UUID was random and package-local, proving the second writer came from another package rather than duplicate fixture creation within the failing test.
- Production Wendy binding serializes on the workspace row and persists with `ON CONFLICT`; the collision came from a test-only raw insert racing a different package. The CI and `make test` commands now use `go test -p 1 ./...`, preserving test behavior while preventing cross-package mutation of a shared integration database.
- On a fresh isolated PostgreSQL database, `go test -p 1 ./internal/handler ./internal/scheduler -count=1` passed and the temporary database was removed. The workflow YAML parsed, the Make target dry-run resolved to the serial command, and `git diff --check` passed.
- The first serial CI run then exposed three `internal/service` sandbox tests that selected an arbitrary pre-existing user with `FROM "user" LIMIT 1`. They had passed only while another package left a user visible. The tests now create unique users and issues inside their own rollback transactions.
- On a second fresh isolated database, all three sandbox cleanup tests passed, followed by serial `internal/handler`, `internal/scheduler`, and `internal/service` package tests. The temporary database was removed.

### Step 10 — Integrate the latest dev changes before handoff

Status: complete

Evidence:

- Fetched `origin/dev` twice after the first all-green CI run because it advanced during verification. Merged twelve upstream commits through final `origin/dev` commit `ae54c2132`, including Beckham private visibility, channel sidebar cleanup, full-surface identity, runtime reachability, and consecutive-message grouping.
- Reviewed both overlapping channel changes. Sidebar cleanup leaves the shared Composer and voice routing untouched. Consecutive-message grouping changes the voice message bubble/list, so the Agent voice regression now renders its replay control in the new compact group layout.
- The first merge passed five focused Composer, voice audio/playback, Agent voice message, and channel sidebar files (20/20). After the grouping merge, seven message bubble/list/grouping, voice, and Composer files passed 105 tests with three existing skips. `@multica/views` TypeScript checking passed after both merges.
- Reusing an old local handler database exposed an inconsistent test-only migration history: migration 204 was absent while a trigger it expected to remove was already absent. No production migration was changed for that dirty local state.
- Created a new isolated database, applied the full migration history through both migration 205 files and `206_beckham_private`, then passed all handler tests matching voice, Beckham, and group-manager behavior after the first merge.
- Repeated the clean migration after the second merge and passed the complete `internal/handler` package. Both temporary databases were removed afterward.
