# Volcengine RTC voice client

## Goal

Add a backend-only client for the current Volcengine RTC AI audio/video
interaction OpenAPI. This slice signs and sends Start/Update/Stop requests; it
does not expose Multica HTTP routes or start user calls.

## Verified provider contract

- OpenAPI version: `2025-06-01`.
- Endpoint: `https://rtc.volcengineapi.com`.
- Region/service for signing: `cn-north-1` / `rtc`.
- Actions: `StartVoiceChat`, `UpdateVoiceChat`, `StopVoiceChat`.
- Start requires `AppId`, `RoomId`, `TaskId`, `Config`, and `AgentConfig`.
- `AgentConfig.TargetUserId` accepts exactly one user; the AI `UserId` must be
  distinct.
- `Config` and `AgentConfig` remain validated raw JSON objects in this transport
  slice. A later typed builder will own the ASR, LLM, TTS, token, and callback
  fields after those product choices are wired.
- Update exposes only the verified `interrupt` command. Fields required by
  other commands stay absent until those commands are implemented and tested.
- Stop only removes the AI Bot. The RTC client must still leave the room and
  destroy its engine when the later UI slice hangs up.

The field set came from the Volcengine OpenAPI Center Swagger response for
service `rtc`, version `2025-06-01`, not from the older demo default.

## Steps

- [x] Fetch and inspect current Start/Update/Stop Swagger.
- [x] Add failing protocol, signing, validation, and provider-error tests.
- [x] Reuse the official Volcengine Go SDK signer (`v1.2.43`).
- [x] Implement bounded Start/Update/Stop calls.
- [x] Run unit tests, vet, and dependency review.
- [x] Commit, push, and open independent ready PR
  [#1033](https://github.com/LRM-Teams/multica/pull/1033).

## Validation

- The Start fixture pins the exact method, endpoint, `2025-06-01` query,
  request JSON, payload SHA-256, signed headers, and HMAC signature.
- Update and Stop fixtures pin their action names and request bodies.
- Provider errors cover both `ResponseMetadata.Error` and the documented
  top-level `code` / `message` response.
- Invalid one-to-one agent config and unknown Update commands fail before the
  transport runs.
- Signed requests do not follow redirects.
- Request and response caps both have explicit 1 MiB regression tests.
- `go test ./internal/integrations/... -count=1`, package vet, module
  verification, and diff checks passed.
