# Voice call provider configuration repair

## Goal

Make the Volcengine RTC conversation task enter the active state and play
Beckham's configured welcome message after the caller joins.

## Production evidence

- [x] The reported call was created and `StartVoiceChat` returned HTTP 200.
- [x] Its database session ended with `connected_at = NULL`,
  `input_audio_ms = 0`, `output_audio_ms = 0`, and no `voice_call_turn` rows.
- [x] No provider lifecycle callback arrived for the call.
- [x] The deployed request used `VOLCENGINE_RTC_ARK_MODEL_NAME` with no Ark
  endpoint ID.
- [x] The request omitted the speech-service AppIDs required by the provider's
  ASR and TTS configuration.
- [x] The current Volcengine 2024-12-01 contract requires an Ark
  `EndPointId`; a console display model name is not a callable endpoint.
- [x] The current contract requires both the speech-service AppID and
  credential: ASR `Credential.AccessToken` and TTS `Credential.Token`.
- [x] The deployed request supplied both AppIDs but omitted both credential
  fields. This is a provider-contract defect consistent with an accepted
  OpenAPI request whose asynchronous task never entered the RTC room.

## Implementation record

- [x] Added a failing JSON contract test for ASR/TTS credential AppIDs.
- [x] Added ASR `Credential.AppId` and TTS `Credential.AppId` to the generated
  `StartVoiceChat` configuration.
- [x] Removed the Ark display-model path from the typed runtime configuration.
- [x] Added fail-closed startup and deployment validation for missing speech
  AppIDs, missing Ark endpoint ID, and obsolete display-model configuration.
- [x] Added deployment variables for the ASR and TTS AppIDs.
- [x] Corrected the incomplete credential contract: the provider request now
  sends the existing `DOUBAO_SPEECH_API_KEY` as ASR `AccessToken` and TTS
  `Token`, and startup fails closed when that credential is absent.
- [x] Added request-contract, provider-propagation, and runtime-wiring
  regression assertions for the credential.
- [x] Corrected the earlier plan's unsupported claim that request acceptance
  proved a live provider task.
- [x] Confirmed the account's speech application enables both streaming ASR
  2.0 and TTS 2.0, then configured its AppID for both provider credentials.
- [x] Confirmed the old Doubao Seed 1.6 version is retired, selected the
  already-enabled Doubao Seed 2.0 Lite version, passed `CreateEndpoint`
  dry-run validation, created a dedicated endpoint, and verified it is
  `Running`.
- [x] Stored the speech AppIDs and Ark endpoint in the `aliyun-dev` GitHub
  environment and removed the obsolete display-model secret.
- [x] Merged PR #1495 into `dev`.
- [x] Verified the deployed `sha-4f2b0b6` image contains PR #1495.
- [ ] Verify `/readyz` consistently from mainland and provider networks.
- [ ] Start a real call and verify provider active callback, non-zero audio, and
  the welcome message.

## Verification

- [x] `go test ./internal/integrations/volcenginertc`
- [x] `go test ./internal/service/voicecall`
- [x] Focused `./cmd/server` voice-call wiring tests
- [x] Re-ran all Volcengine RTC, voice-call service, and server wiring tests
  after adding the missing credential fields.
- [x] `bash scripts/validate-rtc-environment.test.sh`
- [x] `bash scripts/selfhost-config.test.sh`
- [x] `pnpm typecheck`
- [x] `pnpm test`
- [x] `git diff --check`
- [ ] `make check-worktree` could not run its database and E2E stages because
  the local Docker daemon is unavailable. It also exposed a separate
  pre-existing bug: `scripts/check.sh` reports success when PostgreSQL setup
  exits before the numbered checks. That script defect is outside this PR and
  will be fixed independently.

## Client state repair

- [x] Added a failing controller test proving that an accepted provider start
  (`connecting`) must not be shown as an active call.
- [x] Kept ringback and the connecting UI until the server reports `active`.
- [x] Mapped server `starting`, `connecting`, `active`, and `reconnecting`
  states explicitly while preserving local ending, ended, failed, muted, and
  media-reconnection states.
- [x] Stopped ringback only after the server confirms `active` or a terminal
  state.
- [x] Verified all 12 voice-call controller tests.
- [x] Verified `pnpm --filter @multica/views typecheck`.
- [x] Verified `pnpm react:doctor` reports zero issues in the changed source.
- [x] Verified `pnpm test`: 104 core files / 968 tests, 307 views files /
  2,975 passed and 5 skipped, and 14 web files / 69 tests.

## Verification-script defect found during this repair

- [x] Reproduced `scripts/check.sh` returning success and printing
  `All checks passed` when PostgreSQL setup exits non-zero.
- [x] Added a process-level regression test with a failing PostgreSQL fixture.
- [x] Set the shared check exit code before the EXIT trap runs.
- [x] Verified `scripts/check-exit-status.test.sh`,
  `scripts/selfhost-config.test.sh`, and `git diff --check`.

## Deployment-script defect found during rollout

- [x] Reproduced the Aliyun deploy failure:
  `compose_environment: unbound variable`.
- [x] Added a static regression test proving the resolved Compose environment
  remains available until the OSS credential preflight consumes it.
- [x] Moved `unset compose_environment` after that preflight.

## Server-level VoiceChat task events

- [x] Confirmed the provider's server-level callback contract from the current
  Volcengine documentation instead of reusing the binary conversation-message
  envelope.
- [x] Added the documented SHA-256 callback signature verification and verified
  it against Volcengine's published example.
- [x] Added strict decoding for `VoiceChat` task state and task error events.
- [x] Mapped `taskStart` and every documented in-progress run stage to an
  idempotent provider-active update, so a delayed or out-of-order event can
  still stop ringback.
- [x] Mapped provider task errors to terminal session failures with the actual
  Volcengine error code.
- [x] Added the callback GET connectivity check required by the RTC console.
- [x] Preserved the existing signed binary conversation status and subtitle
  callbacks.
- [x] Reproduced the old handler returning `401` for a correctly signed
  server-level `VoiceChat` event with a standalone HTTP replay.
- [x] Verified the same replay returns `200` and invokes the task-event
  processor after the repair.
- [x] Recorded that the repository's handler and `cmd/server` TestMain skip
  integration tests when local PostgreSQL is unavailable; their new regression
  tests compile locally but require CI or a database-backed environment to
  execute.

## Unanswered-call timeout

- [x] Added a failing controller test reproducing an accepted provider start
  that remains `connecting` indefinitely.
- [x] Started a 30-second activation deadline only after the browser has joined
  RTC and the provider start request has returned, so microphone setup time is
  not counted against the agent.
- [x] Forced one authoritative server read at the deadline instead of trusting
  a possibly stale React Query cache or a missed WebSocket invalidation.
- [x] Preserved a call when the final read reports `active`.
- [x] Stopped ringback, disconnected RTC, and requested an idempotent server
  stop when the provider is still non-terminal at the deadline.
- [x] Added a distinct `provider_activation_timeout` client error and
  user-facing messages in English, Simplified Chinese, Japanese, and Korean.
- [x] Added regression coverage for both timeout cleanup and the final-read
  active race.
- [x] Verified 27 controller and panel tests.
- [x] Verified `pnpm --filter @multica/views typecheck`.
- [x] Verified `pnpm react:doctor` reports zero issues in the changed source.
- [x] Verified `git diff --check`.

## Rollout verification

- [x] Merged server-level callback support in PR #1502.
- [x] Merged unanswered-call cleanup in PR #1506.
- [x] Verified the successful `dev` deployment uses image tag `sha-ae9e795`.
- [x] Verified the deployed frontend is healthy and the deployed backend logs
  `voice call integration enabled provider=volcengine`.
- [x] Verified all required RTC, Ark, ASR, TTS, and callback environment names
  are present in the backend container without reading their secret values.
- [x] Verified `https://leagent.me/healthz` returns HTTP 200 through the
  production Caddy route.
- [x] Verified `https://leagent.me/api/voice-calls/callback` returns the
  provider-required HTTP 200 `ok` response through the production route.
- [x] Verified the callback URL returns HTTP 200 from all eight Check-Host
  locations in report `45df409ak83c`, including Hong Kong.
- [ ] Complete one caller-driven browser test and confirm `taskStart`, a
  non-null `connected_at`, non-zero audio duration, and the configured welcome
  message. This step requires an authenticated browser microphone session.

## Cross-service authorization repair

- [x] Reproduced the production symptom at 15:38 CST: the call remained
  `connecting` until the 30-second client deadline, then ended with
  `connected_at = NULL`, zero input/output audio, and no turns.
- [x] Confirmed the same room had no RTC diagnostic record after the documented
  data-delay window.
- [x] Rechecked the Web SDK contract and source: `joinRoom()` resolves only
  after the RTC join request succeeds; the client was not treating a
  fire-and-forget request as a completed room join.
- [x] Opened the Volcengine RTC `跨服务授权` page and found the account in the
  unconfigured state. This omitted `VoiceChatRoleForRTC`, which the current
  Volcengine integration guide requires before RTC can invoke ASR, TTS, and
  Ark LLM services.
- [x] Enabled account-level cross-service authorization through the RTC
  console and verified the page reports `账号跨服务授权已开通`.
- [x] Added the required permissions to the `test` IAM user whose AK/SK is used
  by the production backend. Reopening the unprivileged-user list returns no
  remaining user.
- [x] Created an isolated diagnostic RTC room with a short-lived
  room-and-user-scoped token; no production conversation data was reused.
- [x] Joined a real Web SDK user to that room and observed the successful RTC
  connection state.
- [x] Started a `StartVoiceChat` task with the same API version and
  ASR/TTS/Ark configuration shape used by production.
- [x] Observed the configured bot user join, publish audio, and deliver
  `onRemoteAudioFirstFrame`, proving that the welcome-message path works after
  authorization.
- [x] Stopped the diagnostic VoiceChat task, left the RTC room, destroyed the
  browser engine, and closed the temporary HTTPS test service.
- [x] No business-code change was made: the failure was an account permission
  prerequisite, and adding another client/server fallback would have hidden
  that configuration error without granting RTC access to ASR, TTS, or LLM.

## Provider-answer detection repair

- [x] Rechecked the production failure after cross-service authorization:
  the bot joins the RTC room and publishes the welcome-message audio, but the
  application remains `connecting` and stops the task after 30 seconds.
- [x] Opened the RTC application's `功能配置 -> 回调设置` page and confirmed
  that no server-level event subscription exists. Per-task
  `ServerMessageURLForRTS` configuration does not subscribe the application to
  the server-level `VoiceChat` event.
- [x] Attempted to add a `VoiceChat` subscription for
  `https://leagent.me/api/voice-calls/callback`. The RTC console rejected the
  address as invalid during its connectivity check.
- [x] Reproduced the network-dependent ingress behavior: a Chromium request to
  `https://leagent.me/healthz` returns 200, while a non-browser TLS client is
  reset and plain HTTP receives Alibaba `Non-compliance ICP Filing` 403 before
  reaching Caddy. This corrects the earlier Check-Host-only conclusion that
  the callback endpoint was generally reachable.
- [x] Added a failing media-session test for the SDK's
  `onRemoteAudioFirstFrame` signal and a controller test proving that a call
  must not time out after the remote agent's audio is decoded.
- [x] Wired `onRemoteAudioFirstFrame` through the media driver and session.
  This event is direct evidence that the remote bot joined, published, and
  delivered playable audio to the caller.
- [x] Stopped ringback, cleared the unanswered-call deadline, and moved the UI
  to the connected state when that signal arrives, including the race where
  audio arrives before the provider-start HTTP response returns.
- [x] Preserved the 30-second failure and cleanup path when no remote agent
  audio arrives. A local RTC room join alone still does not count as an
  answered call.
- [x] Verified 31 focused media-session and controller tests.
- [x] Verified the complete views suite: 306 files, 2,992 passed and 5 skipped.
- [x] Verified the views TypeScript project with `tsc --noEmit`.
- [x] Verified the views lint task has no errors; its five warnings are in
  pre-existing files outside this change.
- [x] Verified React Doctor reports zero issues in the changed source.
- [x] Verified `git diff --check`.
- [x] Merged the repair in PR #1516 with merge commit `bc378b7`.
- [x] Verified Deploy run `30527088960` completed successfully.
- [x] Verified production frontend and backend use image tag `sha-bc378b7`.
- [x] Verified the production backend reports the Volcengine voice-call
  integration enabled and `/readyz` reports both database and migrations OK.
- [ ] Complete one authenticated caller microphone test on the deployed web
  client and confirm ringback stops on the welcome-message audio instead of
  the unreachable server callback.

## Audible remote playback repair

- [x] Captured the reported production call
  `796ef84e-5fdf-483d-b9cd-723897903729` instead of inferring from an older
  session.
- [x] Confirmed `StartVoiceChat` returned HTTP 200 in 200 ms and the provider
  bot remained in the RTC room until the caller hung up.
- [x] Queried Volcengine `ListCallDetail` after its documented data-delay
  window. The agent sent 32 Kbps audio, the caller received 20–40 Kbps from
  that agent, playback volume was 90.31, packet loss was 0–1.01%, and audio
  stall duration was 0 ms.
- [x] Ruled out an empty welcome message, failed TTS generation, a missing
  remote stream, and transport loss for this call. The remaining defect was
  between the decoded first frame and browser playback.
- [x] Added failing media-session tests proving that a decoded frame must not
  count as an answered call until the SDK's explicit remote `play()` promise
  succeeds.
- [x] Added a failing regression test for browsers that reject playback:
  the UI must request a user gesture and must not report an audible answer.
- [x] Explicitly start remote audio playback on the first decoded agent frame,
  deduplicate concurrent attempts, and report the answer only after playback
  succeeds.
- [x] Reuse the same playback confirmation path when the member clicks the
  resume-audio control after an autoplay rejection.
- [x] Clear a stale autoplay warning when playback is subsequently confirmed.
- [x] Verified 32 focused media-session and controller tests.
- [x] Verified the complete views suite (306 files, 2,993 passing tests and 5
  skips), TypeScript, lint (zero errors; five pre-existing warnings outside
  the changed files), React Doctor (zero findings), and whitespace checks.
- [ ] Merge the independent repair PR to `dev`, verify deployment, and repeat
  the RTC quality query for the caller's acceptance call.
