# Voice-triggered reply modality enforcement

## Goal

Guarantee that an agent's visible text answer to a human voice message is
persisted as a playable voice reply in the same channel, DM, or thread, even
when the runtime omits `multica message send --voice`.

## Evidence and execution log

- [x] Traced the existing behavior from human voice message parts through the
  channel prompts and runtime CLI. The prompt requested `--voice`, but the
  agent transport accepted a plain text reply, so model compliance was the
  only modality guarantee.
- [x] Identified both active source mechanisms: inbox events carry
  `source_message_id`; legacy task-token runs carry the same message ID in the
  `Reaction target message id` prompt line.
- [x] Added final-write enforcement for immediate sends and saved-draft sends.
  It loads the actual human trigger and adds a voice part only when the reply
  destination is the same main timeline or the same thread.
- [x] Preserved proactive output semantics: another channel, another thread,
  agent-authored triggers, and ordinary human text do not force voice output.
- [x] Kept sticker-only acknowledgements as stickers. A normalized sticker alt
  label is not treated as a speech transcript; explicit text sent with a
  sticker remains eligible for voice delivery.
- [x] Added table tests for group main, DM main, matching thread, cross-channel,
  cross-thread, agent triggers, text triggers, sticker-only output, explicit
  text plus sticker, and already-structured voice replies.
- [x] Added a handler integration test proving that a task-token agent which
  sends plain text for a human voice trigger persists accessible text and a
  voice part.
- [x] Compiled the handler test package successfully.
- [x] The shared local handler database could not advance migration 204 because
  its expected migration-169 trigger had been removed. This is local database
  drift, not the tested product path; recent clean CI migrations pass. Created
  a new temporary local database, ran the two focused tests successfully, and
  removed only that temporary database afterward.
- [x] Ran `gofmt` and `git diff --check`.
- [x] Pushed `fix/voice-reply-modality` and opened ready-for-review PR #963 into
  `dev`: <https://github.com/LRM-Teams/multica/pull/963>.

## Boundaries

- Typed requests such as “用语音回复” still require semantic interpretation by
  the agent because they are not a structured input modality.
- Empty answers and sticker-only acknowledgements are not converted into
  fabricated speech.
- No server files, containers, or production data were modified.
