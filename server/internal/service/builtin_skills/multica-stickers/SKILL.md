---
name: multica-stickers
description: "Use for short social chat beats in standalone chat sessions: greetings, acknowledgements, thanks, praise, welcome, laughter, or 'on it'. Covers stable sticker ids and standalone-chat final sticker output. Agent channel/DM/thread message sends do not accept sticker Parts; use a reaction or short text there. Do not use for substantive-only answers or issue comments."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Using stickers

When your reply is just a short social beat — a greeting, an acknowledgement,
praise, agreement, thanks — return a sticker only in a standalone chat session.
For a ChannelID-backed channel, DM, or thread, use a reaction or a short text
message instead: Agent transport does not accept sticker Parts.

When you also need to explain something, send ordinary text. A standalone
session may use a sticker envelope only when that is the entire reply.

## Fast path — common replies (use these ids directly, no command)

These ids are stable; use them straight from this table — do **not** look them up.

| When you'd reply… | Use this `sticker_id` |
| --- | --- |
| hello / checking in | `hi` |
| okay / sounds good | `ok` |
| acknowledged / understood | `got-it` |
| agree / yes | `nod-yes` |
| approval / like | `thumbs-up` |
| impressed / excellent | `impressive` |
| perfect / looks good | `perfect` |
| thanks | `thanks` |
| affection / love it | `heart-hands` |
| applause / congratulations | `applause` |
| on it / will do | `on-it` |
| laughter | `huaji` |

## How to reply

Follow the current delivery contract. Delivery depends only on `ChannelID`.

### No `ChannelID`

The final assistant output is delivered back to the current session
automatically. Do not run `multica message send`.
Do not search for a DM/channel target.

Sticker only (for example user says "hi"):

    {"action":"message_send","parts":[{"type":"sticker","sticker_id":"hi"}]}

Sticker plus explanation (for example user assigns work and you need to answer):

    {"action":"message_send","parts":[{"type":"sticker","sticker_id":"got-it"},{"type":"text","text":"This happened because xxx; I recommend xxx."}]}

Return exactly one JSON object as final output, with no surrounding commentary.

If you mistakenly run `multica message send` without `ChannelID` and the
CLI reports that there is no channel transport target (403): **stop immediately**.
That error means there is no channel transport target, not that Multica CLI is broken.
Do not chase help pages, env vars, transcripts, or daemon status — reply with
final output (sticker JSON or text) instead.

### `ChannelID` present

Use the durable agent-credential transport with the explicit target supplied by the current
surface:

    multica message react --message-id <triggering-message-id> --emoji "✅"
    printf '%s\n' 'Acknowledged; I will handle it.' | multica message send --target <target>

After `multica message send` succeeds, leave final assistant output empty so the
platform does not duplicate the reply.

## Need something more specific?

Run exactly **one** search (use an English intent). Do not list the whole library,
do not read any files:

    multica sticker search afraid
    multica sticker search celebrate

Use a searched sticker only in the standalone final-output envelope.

## When to use it — and when not

- **Do** use a single sticker envelope for a short canned reply (hi / ok /
  acknowledged / thanks / approval / impressed / on-it) when that is the whole reply in a standalone session.
- **Do** use a reaction or short text for the same beat in a ChannelID-backed run.
- **Don't** put a sticker on a substantive message that carries real information —
  a status report, a code explanation, or an actual answer.
- **Don't** paste legacy `:sticker:<id>:` tokens.
- **Don't** use a JSON action envelope in a DM/channel/thread transport run, or
  use `multica message send` for the current reply in a standalone session.
- **Don't** treat a standalone-session no-channel-transport error as a CLI outage;
  switch to final-output delivery instead of diagnosing Multica.
- **Don't** embed chat files as markdown images (`![](url)`). Upload with
  `multica attachment upload --path <file> --target <target>`, then pipe a
  non-empty body to `multica message send --target <target> --attachment-id <id>`.
  The Server, not the Agent CLI, turns attachment ids into canonical message Parts.
  If a direct upload is interrupted, retry the same verified session with
  `multica attachment upload --path <file> --resume-session <session-id>`;
  cancel an abandoned pending session with
  `multica attachment upload --cancel-session <session-id>`.
