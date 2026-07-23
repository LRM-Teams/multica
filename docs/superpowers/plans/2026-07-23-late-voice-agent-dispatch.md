# Late group voice Agent dispatch

## Goal

Ensure a recorded group voice message reaches joined Agents after ASR finishes,
even when newer channel messages advanced their ambient cursor while ASR was
pending.

## Delivery record

- [x] Confirm production is running commit `a15a35f`, which contains merged
  voice MIME repair PR #1030 and migration 216.
- [x] Confirm the reported recording now has a completed transcript and
  completed voice message part in production.
- [x] Confirm no `agent_inbox_event` was created for the repaired recording.
- [x] Identify the cause: normal ambient dispatch treats the late transcript's
  old sequence as already read after the Agent cursor passes it.
- [x] Add a failing regression that advances an Agent beyond a pending voice
  message before ASR completes; old behavior creates zero wakes.
- [x] Dispatch a newly available transcript as the current unread item while
  retaining existing Agent membership, mute, mention, group-command, prompt,
  pending-wake merge, and response-modality rules.
- [x] Verify the regression creates one pending Agent inbox event after the
  cursor has advanced.
- [x] Run related channel voice tests and `go vet ./internal/handler` against a
  fresh fully migrated PostgreSQL database.
- [x] Commit, push, and open independent ready PR
  [#1080](https://github.com/LRM-Teams/multica/pull/1080) against `dev`.
