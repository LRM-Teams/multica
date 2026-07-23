# Agent voice synthesis frontend contract work log

Date: 2026-07-23

## Goal and boundary

- Expose the server-owned Agent TTS lifecycle in the shared frontend type.
- Keep this protocol change independent from playback UI behavior.

## Step 1 — Type contract

Completed.

- Voice parts accept `pending`, `completed`, and `failed`
  `synthesis_status` values.
- The field remains optional for human recordings and historical Agent voice
  messages.

## Step 2 — Verification

Completed.

- Focused message-part test: 1 file, 4 tests passed.
- `pnpm --filter @multica/core typecheck`: passed.
