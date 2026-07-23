# Volcengine realtime conversation configuration

## Goal

Build the exact typed `Config` and `AgentConfig` payloads needed to start a
one-to-one Beckham audio conversation on the current Volcengine
`StartVoiceChat` API.

## Verified provider contract

- OpenAPI version `2025-06-01` supports an ASR + LLM + TTS pipeline.
- Streaming ASR 2.0 uses resource `volc.seedasr.sauc.duration`; `StreamMode: 2`
  with second-pass recognition balances interim latency and final accuracy.
  The builder uses the typed direct fields `ApiResourceId` and
  `enable_nonstream` together; it does not mix them with the separate
  `Credential` and `VolcanoASRParameters` passthrough form.
- The conversational model uses `ArkV3` with exactly one custom endpoint ID or
  model name. Deep thinking is disabled for spoken-turn latency.
- Bidirectional TTS 2.0 uses provider `volcano_bidirection`, credential resource
  `seed-tts-2.0`, and a JSON-encoded `VolcanoTTSParameters` containing the
  speaker ID. Old `app` and `audio` authentication shapes are not added to the
  recommended passthrough form.
- The same public HTTPS callback URL receives captions and Agent state. The
  same signature is used for both because the current provider documentation
  requires shared callback identity and contains conflicting wording about
  whether signatures may differ.
- The human and Agent RTC user IDs are distinct and use the provider's current
  identifier alphabet.

Source:

- Volcengine OpenAPI Center Swagger for service `rtc`, action
  `StartVoiceChat`, version `2025-06-01`.
- <https://www.volcengine.com/docs/6348/1558163?lang=zh>

## Steps

- [x] Inspect the current Swagger schemas and generated request example.
- [x] Resolve the recommended TTS passthrough shape and remove legacy fields.
- [x] Add failing exact-payload and validation tests.
- [x] Implement the typed configuration builder.
- [x] Run package tests, vet, and diff checks.
- [x] Commit, push, and open independent ready PR
  [#1040](https://github.com/LRM-Teams/multica/pull/1040), stacked on #1037.

## Verification

- The first targeted test failed because no typed builder existed, then passed
  after the implementation.
- The exact JSON fixture pins ASR 2.0, Ark V3, bidirectional TTS 2.0, subtitles,
  interruption, and one-to-one Agent state callback fields.
- Validation tests reject invalid or identical RTC participants, missing or
  ambiguous Ark model references, blank system context, missing TTS voice,
  insecure callback URLs, and missing callback signatures.
- `go test ./internal/integrations/... -count=1`,
  `go vet ./internal/integrations/...`, and `git diff --check` pass.
