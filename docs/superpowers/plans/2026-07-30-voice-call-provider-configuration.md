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
- [ ] Merge the PR into `dev`.
- [ ] Verify the deployed image SHA and external `/readyz`.
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
