# Volcengine RTC API version compatibility fix

## Goal

Allow deployments to select the Volcengine `StartVoiceChat` API version that
matches the configured AppID. Keep `2025-06-01` as the default and explicitly
support the legacy conversational-AI AppID contract `2024-12-01`.

## Evidence

- Production returned `NoPermissionForApp` and stated that the configured AppID
  is not an AI-agent application.
- Volcengine's FAQ maps legacy “实时对话式 AI” AppIDs to `2024-12-01` and new
  “AI 音视频互动方案” AppIDs to `2025-06-01`.
- The official `2024-12-01` API explorer requires the same top-level
  `AppId`, `RoomId`, `TaskId`, `Config`, and `AgentConfig` request shape used by
  this integration.

## Progress

- [x] Re-read production failure and compare it with the official version/AppID
  mapping.
- [x] Confirm the repository currently hard-codes `2025-06-01`.
- [x] Add failing tests for explicit legacy-version selection and unsupported
  versions.
- [x] Implement validated API-version configuration.
- [x] Pass the setting through server wiring and deployment configuration.
- [x] Run focused tests and repository checks.
- [x] Push isolated pull request
  [#1181](https://github.com/LRM-Teams/multica/pull/1181).
- [x] Configure the `aliyun-dev` environment for `2024-12-01`.
- [ ] Verify the provider response after merge and deployment.

## Verification

- `go test ./internal/integrations/volcenginertc ./internal/service/voicecall`
- `go test ./cmd/server -run '^(TestLoadVoiceCallRuntimeConfig|TestConfigureVoiceCallService)' -count=1`
- `docker compose -f docker-compose.selfhost.yml config --quiet`
- `git diff --check`

The full `./cmd/server` package run also exercised unrelated database-backed
integration tests and reproduced an existing `TestAgentsThroughRouter` 500.
The failure persists with voice calling unconfigured and is outside this
version-selection path; it is not included in this isolated fix.
