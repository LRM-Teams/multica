# Voice transcript realtime work log

Date: 2026-07-23

## Goal and boundary

- Replace a pending voice message in local query caches when server ASR
  completes or fails.
- Do not emit a second-message notification for enrichment of the same row.
- Keep pending/failed visual treatment in a separate UI PR.

## Step 1 — Type the server contract

Completed.

- Added the three server-owned recording transcription states to the voice
  part type.
- Added `channel:message_updated` and its channel-message payload to the
  WebSocket event map.

## Step 2 — Update all conversation caches

Completed.

- Root messages are upserted in the channel cache.
- Thread replies are upserted in their thread cache.
- The channel list is invalidated for preview refresh.
- The update path deliberately skips the new-message notification handler.

## Step 3 — Verification

Completed.

- `pnpm --filter @multica/core typecheck`: passed.
- Focused realtime regression: 1 file and 12 tests passed.
