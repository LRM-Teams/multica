# Voice call direct Ark repair

> **2026-07-30 correction:** the production evidence below only proved that
> `StartVoiceChat` accepted the request. It did not prove that the provider
> task became active. Production sessions stayed in `connecting`, had no
> `connected_at`, no audio, no turns, and no provider callback. The request
> omitted the ASR/TTS speech-service AppIDs and used an Ark display model name
> where the 2024-12-01 API requires an `EndPointId`. The corrective work and
> verification record are in
> `2026-07-30-voice-call-provider-configuration.md`.

## Goal

Restore low-latency RTC calls by keeping ASR, LLM, and TTS inside the
Volcengine realtime pipeline. A live speech turn must not wait for the durable
Cursor development queue.

## Evidence and execution log

- [x] Production `StartVoiceChat` accepted the request and persisted provider
  task/room IDs. This is not evidence that the provider task became active.
- [x] Public `leagent.me` HTTP is intercepted by Alibaba's ICP compliance page;
  HTTPS closes before a ServerHello. Host-local Caddy TLS and application
  routes are healthy.
- [x] Production call sessions never receive a provider callback and never
  receive a CustomLLM request.
- [x] Current CustomLLM dispatch creates an ordinary `agent_inbox_event` and
  waits up to 90 seconds for the exact Cursor completion.
- [x] Production voice-message evidence shows that the same per-agent queue can
  wait more than 150 seconds before execution, so that path cannot satisfy a
  realtime call.
- [x] The existing architecture rule already assigns live speech to a
  low-latency spoken model and reserves the durable Agent queue for development
  actions.
- [x] Volcengine's current RTC contract supports `LLMConfig.Mode=ArkV3` with a
  model name or endpoint ID, bounded output tokens, disabled thinking, supplied
  system messages, and RTC-managed history.
- [x] Restore ArkV3 configuration and remove CustomLLM from the required
  deployment contract.
- [x] Configure the `aliyun-dev` environment with one Ark model selector; no
  credential value is copied into the repository or host dotenv.
- [x] Run focused configuration, provider, wiring, and deployment tests:
  - `go test ./internal/integrations/volcenginertc ./internal/service/voicecall`
  - `go test ./cmd/server -run 'TestLoadVoiceCallRuntimeConfig|TestConfigureVoiceCallService'`
  - `bash scripts/validate-rtc-environment.test.sh`
  - `bash scripts/selfhost-config.test.sh`
  - `git diff --check`
- [x] Attempt `go test ./internal/handler -run VoiceCall`; the suite stopped
  before tests because the existing local test database cannot apply migration
  `204_system_general_channel` after a missing historical trigger. Production
  has already applied this migration, and this PR does not alter migrations, so
  the unrelated test-database repair is not included here.
- [x] Push one independent branch and open PR
  [#1315](https://github.com/LRM-Teams/multica/pull/1315) into `dev`.

## Boundaries

- The server still builds the call's identity and bounded context from Multica
  state before `StartVoiceChat`.
- Ark may answer conversational turns but cannot claim that code, GitHub, or
  server actions completed. Tool execution remains outside this change.
- Callback delivery still requires a reachable public HTTPS domain. Until ICP
  filing or another public ingress is available, server-side subtitles and
  provider lifecycle callbacks remain degraded even though the RTC media/LLM
  path no longer depends on that callback.
- No production source file or container is modified manually.
