# Voice call runtime wiring

## Goal

Construct and attach the existing Volcengine RTC call stack during API server
startup while keeping deployments that have not opted in operational.

## Delivery record

- [x] Added one runtime configuration loader for the Volcengine signing
  credentials, RTC room credentials, Ark model selector, TTS voice, provider
  callback, and operation timeouts.
- [x] Kept calling disabled when no credential, model, or callback value is
  present.
- [x] Made partial opt-in a startup error for the integration instead of
  constructing a service that would fail after creating a call session.
- [x] Required exactly one Ark endpoint ID or model name.
- [x] Reused `DOUBAO_TTS_SPEAKER_ID` when a call-specific voice is absent, then
  used the existing product voice ID as the documented non-secret default.
- [x] Derived the callback URL from `MULTICA_PUBLIC_URL` when no explicit
  override exists; the resulting provider URL is
  `/api/voice-calls/callback`.
- [x] Constructed the signed Volcengine client, room token signer, provider
  adapter, PostgreSQL store, DM authorizer, context builder, and lifecycle
  service once at startup.
- [x] Exposed every setting through self-host Compose and mapped protected
  Aliyun deployment values from GitHub Environment secrets.
- [x] Added tests for disabled deployments, partial credentials, callback and
  voice reuse, strict duration/model validation, and the complete runtime
  object graph.
- [x] Ran targeted server, provider, deployment configuration, vet, and diff
  checks.
- [ ] Commit, push, and open an independent ready PR stacked on
  [#1082](https://github.com/LRM-Teams/multica/pull/1082).

## Deployment boundary

The runtime remains disabled until the Aliyun `aliyun-dev` Environment has
non-empty values for AppID, AppKey, access key ID, secret access key, one Ark
selector, and callback signature. Secret values are never copied into the
repository or host dotenv.

The callback URL is configured now because the provider rejects calls without
it. The authenticated callback handler itself is the next independent PR, so
no frontend call entry is exposed by this change.
