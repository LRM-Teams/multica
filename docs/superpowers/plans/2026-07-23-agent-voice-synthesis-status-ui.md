# Agent voice synthesis status UI work log

Date: 2026-07-23

## Goal and boundary

- Consume the server-owned Agent TTS lifecycle in the shared voice bubble.
- Do not issue a second browser TTS request for server-owned messages.
- Preserve on-demand TTS playback for historical messages without lifecycle
  metadata.

## Step 1 — Pending and failed states

Completed.

- Pending synthesis renders `正在生成语音`, disables playback, retains the
  transcript action, and does not consume the pending autoplay gesture.
- Terminal failure renders `语音生成失败`, keeps the transcript available,
  and does not retry through the browser TTS endpoint.

## Step 2 — Completed attachment transition

Completed.

- The `channel:message_updated` transition to `completed` exposes the
  server-bound WAV through the existing voice bubble.
- Autoplay eligibility is claimed only after that attachment is playable.
- Historical Agent messages with no `synthesis_status` keep the existing
  on-demand TTS path during rollout.

## Step 3 — Verification

Completed.

- Focused voice bubble regression: 1 file, 15 tests passed.
- `pnpm --filter @multica/views typecheck`: passed.
- React Doctor scanned the changed core/view source and reported 0 issues.
- Web Interface Guidelines review found no changed-line violation; the pending
  status uses a native live output and the spinner honors reduced motion.
- The first typecheck caught two server-only linkage fields in the new
  attachment fixture. They were removed from the frontend fixture; production
  code was not changed to accept fields outside the shared API type.
