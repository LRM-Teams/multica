# Persistent Agent voice synthesis work log

Date: 2026-07-23

## Goal and boundary

- Generate each Agent voice reply once on the server.
- Persist the playable WAV as a message attachment.
- Recover synthesis after process restarts without blocking text delivery.
- Keep the existing client TTS path until the server lifecycle UI lands in a
  separate PR.

## Step 1 — Current behavior diagnosis

Completed.

- Agent voice messages contain a transcript and a `voice` marker but no audio.
- Every browser independently calls `/api/voice/tts`; the WAV only lives in an
  eight-entry in-memory cache.
- Reopening an old message can call the provider again, and a later provider
  outage makes an already delivered voice reply unplayable.
- Existing attachment storage, message-update realtime events, and scheduler
  recovery can carry server-generated audio without a second storage system.

## Step 2 — Durable synthesis contract

Completed.

- Agent voice finalization removes runtime-supplied audio metadata and marks
  the canonical transcript `synthesis_status:"pending"`.
- Migration 214 creates a durable queue and an insert trigger covering every
  Agent message writer, including transactional Radar and handoff paths.
- Initial realtime publication starts synthesis without blocking message
  delivery; the scheduler retries and recovers stale claims after restarts.
- The provider PCM is wrapped as WAV, uploaded through the existing storage
  interface, and inserted as an Agent-owned attachment bound to the message.
- Attachment persistence, completed part state, and queue completion commit in
  one transaction. The same message is then republished as
  `channel:message_updated`.
- Terminal failures update the same voice part to `failed`; temporary provider
  or storage failures retry three times, while missing deployment
  configuration does not consume attempts.

## Step 3 — Verification

Completed.

- `go test ./internal/messageparts`: passed.
- Focused scheduler contract test: passed.
- Migration 214 up/trigger/down test in an isolated schema: passed.
- Handler package compile (`go test -c`): passed.
- `go vet` for handler, messageparts, scheduler, and server command: passed.
- Focused handler runtime tests are blocked before test selection by the
  pre-existing local handler database drift at migration 204; the failing
  schema is unrelated (`trg_journal_workspace_radar_channel`). The new handler
  code still compiles, and CI runs against a clean migrated database.
