# Aliyun voice secret repair work log

Date: 2026-07-23

## Goal and boundary

- Restore ASR/TTS after the deployment migration to `101.200.210.144`.
- Keep the change independent from voice-message product work.
- Inspect server state read-only; do not edit server code or configuration.
- Never print or commit the speech API key.

## Step 1 — Match the user-visible failure to server evidence

Completed.

- The backend logged `POST /api/voice/asr` with HTTP 503 at the same time as
  the UI reported “语音消息处理失败”.
- The request completed in 1.29 ms.
- `server/internal/handler/voice.go` returns this 503 before contacting Doubao
  when the provider is not configured; provider request failures use HTTP 502.

## Step 2 — Inspect configuration boundaries

Completed.

- The running backend container had an empty `DOUBAO_SPEECH_API_KEY`.
- `/data/multica/.env` contained no speech key.
- The old `s89` GitHub Environment still had the secret.
- The active deploy job uses the new `aliyun-dev` Environment and previously
  injected no speech secret.
- The migration therefore deployed healthy containers with voice disabled.

## Step 3 — Repair the deployment contract

Completed.

- Added `DOUBAO_SPEECH_API_KEY` to the `aliyun-dev` Environment without reading
  or changing the old secret.
- Injected the protected Environment secret into Compose.
- Added a preflight that aborts before runtime changes when the key is empty.
- Added post-restart verification that checks only whether the backend
  container received a non-empty key; it never logs the value.

## Step 4 — Verification

Completed.

- `git diff --check`: passed.
- `actionlint v1.7.7 .github/workflows/deploy.yml`: passed.
- `SELFHOST_CONFIG_STATIC_ONLY=true bash scripts/selfhost-config.test.sh`:
  passed.
- `bash scripts/selfhost-config.test.sh`: passed, including rendered Aliyun
  Compose configuration.
- The readiness check tolerates the expected interval where a replaced backend
  container does not yet exist, then requires the running container to contain
  a non-empty speech key.
