---
name: multica-stickers
description: "Use for short social chat beats that should be a sticker instead of text: greetings, ok/收到, thanks, praise, welcome, laughter, or 'on it'. Covers stable sticker ids, standalone-chat final sticker output, channel/DM/thread `multica message send --sticker`, combining one sticker with substantive text, and when not to use stickers. Do not use for substantive-only answers or issue comments."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Using stickers

When your reply is just a short social beat — a greeting, an acknowledgement,
praise, agreement, thanks — **send a sticker instead of typing it.** The sticker
IS the reply.

When you also need to explain something, send **one message** with a sticker plus
your text: acknowledgement sticker first, explanation second.

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

    multica message send --target <target> --sticker hi
    multica message send --target <target> --sticker got-it --message "这个问题是因为 xxx，我建议 xxx"

After `multica message send` succeeds, leave final assistant output empty so the
platform does not duplicate the reply.

## Need something more specific?

Run exactly **one** search (Chinese or English). Do not list the whole library,
do not read any files:

    multica sticker search 害怕
    multica sticker search celebrate

Use the printed id with `--sticker`.

## When to use it — and when not

- **Do** replace a short canned reply (hi / ok / 收到 / 谢谢 / 赞 / 厉害 / 安排)
  with a single sticker when that is the whole reply.
- **Do** combine `--sticker` and `--message` when you acknowledge first and then
  explain or answer substantively.
- **Don't** stick one on a substantive message that carries real information only —
  a status report, a code explanation, an actual answer with no social beat. At most
  one sticker per message, and never as filler on top of real content.
- **Don't** paste legacy `:sticker:<id>:` tokens.
- **Don't** use a JSON action envelope in a DM/channel/thread transport run, or
  use `multica message send` for the current reply in a standalone session.
- **Don't** treat a standalone-session `not a channel task` error as a CLI outage;
  switch to final-output delivery instead of diagnosing Multica.
- **Don't** embed chat files as markdown images (`![](url)`). Upload with
  `multica attachment upload`, then send with
  `multica message send --attachment-id <id>` (optionally with `--message` /
  `--sticker`). The CLI turns attachment ids into structured message parts.
