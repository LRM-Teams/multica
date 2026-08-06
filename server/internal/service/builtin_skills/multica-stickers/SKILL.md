---
name: multica-stickers
description: "Use for short social chat beats in standalone chat sessions: greetings, ok/收到, thanks, praise, welcome, laughter, or 'on it'. Covers stable sticker ids and standalone-chat final sticker output. Agent channel/DM/thread message sends do not accept sticker Parts; use a reaction or short text there. Do not use for substantive-only answers or issue comments."
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
| 你好 / hi / 在吗 | `hi` |
| 好的 / ok / 没问题 | `ok` |
| 收到 / 明白 / 懂了 | `got-it` |
| 同意 / 对 / 点头 | `nod-yes` |
| 赞 / 点赞 | `thumbs-up` |
| 厉害 / 牛逼 / 绝了 | `impressive` |
| 完美 / 没毛病 | `perfect` |
| 谢谢 | `thanks` |
| 比心 / 爱了 | `heart-hands` |
| 鼓掌 / 恭喜 | `applause` |
| 安排 / 这就办 | `on-it` |
| 哈哈 / 笑死 | `huaji` |

## How to reply

Follow the current delivery contract. Delivery depends only on `ChannelID`.

### No `ChannelID`

The final assistant output is delivered back to the current session
automatically. Do not run `multica message send`.
Do not search for a DM/channel target.

Sticker only (for example user says "hi"):

    {"action":"message_send","parts":[{"type":"sticker","sticker_id":"hi"}]}

Sticker plus explanation (for example user assigns work and you need to answer):

    {"action":"message_send","parts":[{"type":"sticker","sticker_id":"got-it"},{"type":"text","text":"这个问题是因为 xxx，我建议 xxx"}]}

Return exactly one JSON object as final output, with no surrounding commentary.

If you mistakenly run `multica message send` without `ChannelID` and the
CLI returns `agent task is not a channel task` (403): **stop immediately**.
That error means there is no channel transport target, not that Multica CLI is broken.
Do not chase help pages, env vars, transcripts, or daemon status — reply with
final output (sticker JSON or text) instead.

### `ChannelID` present

Use the task-scoped transport with the explicit target supplied by the current
surface:

    multica message react --message-id <triggering-message-id> --emoji "✅"
    printf '%s\n' '收到，我会处理。' | multica message send --target <target>

After `multica message send` succeeds, leave final assistant output empty so the
platform does not duplicate the reply.

## Need something more specific?

Run exactly **one** search (Chinese or English). Do not list the whole library,
do not read any files:

    multica sticker search 害怕
    multica sticker search celebrate

Use a searched sticker only in the standalone final-output envelope.

## When to use it — and when not

- **Do** use a single sticker envelope for a short canned reply (hi / ok / 收到 /
  谢谢 / 赞 / 厉害 / 安排) when that is the whole reply in a standalone session.
- **Do** use a reaction or short text for the same beat in a ChannelID-backed run.
- **Don't** put a sticker on a substantive message that carries real information —
  a status report, a code explanation, or an actual answer.
- **Don't** paste legacy `:sticker:<id>:` tokens.
- **Don't** use a JSON action envelope in a DM/channel/thread transport run, or
  use `multica message send` for the current reply in a standalone session.
- **Don't** treat a standalone-session `not a channel task` error as a CLI outage;
  switch to final-output delivery instead of diagnosing Multica.
- **Don't** embed chat files as markdown images (`![](url)`). Upload with
  `multica attachment upload --path <file> --target <target>`, then pipe a
  non-empty body to `multica message send --target <target> --attachment-id <id>`.
  The Server, not the Agent CLI, turns attachment ids into canonical message Parts.
  If a direct upload is interrupted, retry the same verified session with
  `multica attachment upload --path <file> --resume-session <session-id>`;
  cancel an abandoned pending session with
  `multica attachment upload --cancel-session <session-id>`.
