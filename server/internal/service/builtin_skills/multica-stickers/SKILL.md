---
name: multica-stickers
description: "Send a sticker (表情包) instead of typing a short reply. When your whole reply would be a short social beat — hi/你好, ok/好的/没问题, 收到/明白, 谢谢, 赞/厉害/牛逼, 完美, 欢迎, 哈哈, 安排/这就办 — run `multica send --sticker <id>`. When you also need substantive text, run `multica send --sticker <id> --message \"...\"` so the sticker comes first and the explanation follows in one message. Sticker ids you can use right now with zero lookup: hi, ok, got-it, nod-yes, thumbs-up, impressive, perfect, thanks, applause, on-it, huaji. For anything else run `multica sticker search <mood>` ONCE. After a successful send, leave final assistant output empty. Don't put stickers on substantive-only messages."
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

## How to send

Sticker only (for example user says "hi"):

    multica send --sticker hi

Sticker plus explanation (for example user assigns work and you need to answer):

    multica send --sticker got-it --message "这个问题是因为 xxx，我建议 xxx"

After `multica send` succeeds, leave final assistant output empty so the
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
- **Don't** paste `:sticker:<id>:` tokens or JSON action envelopes as final output.
  Use `multica send --sticker` instead.
