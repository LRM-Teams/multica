---
name: multica-stickers
description: "Send a sticker (表情包) instead of typing a short reply. When your whole reply would be a short social beat — hi/你好, ok/好的/没问题, 收到/明白, 谢谢, 赞/厉害/牛逼, 完美, 欢迎, 哈哈, 安排/这就办 — return a send action with structured parts JSON such as {\"action\":\"send\",\"parts\":[{\"type\":\"sticker\",\"sticker_id\":\"hi\"}]}. Sticker ids you can use right now with zero lookup: hi, ok, got-it, nod-yes, thumbs-up, impressive, perfect, thanks, applause, on-it, huaji. For anything else run `multica sticker search <mood>` ONCE and use only the id, never paste a :sticker:<id>: token as your final message. Don't put stickers on substantive/technical messages."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Using stickers

When your reply is just a short social beat — a greeting, an acknowledgement,
praise, agreement, thanks — **send a sticker instead of typing it.** The sticker
IS the reply. Return a `send` action with structured message
parts JSON, not a text token:

```json
{"action":"send","parts":[{"type":"sticker","sticker_id":"got-it"}]}
```

## Fast path — common replies (use these tokens directly, no command)

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

Put the id into `parts[]` inside the `send` action. That's it:
no command, no file read.

## Need something more specific?

Run exactly **one** search (Chinese or English). Do not list the whole library,
do not read any files:

    multica sticker search 害怕
    multica sticker search celebrate

If the command prints a `:sticker:<id>:` token, extract only the `<id>` and use
that id in structured parts JSON inside `send`. Do not paste
the token as your final message.

## When to use it — and when not

- **Do** replace a short canned reply (hi / ok / 收到 / 谢谢 / 赞 / 厉害 / 安排)
  with a single sticker. That's the main use.
- **Don't** stick one on a substantive message that carries real information — a
  status report, a code explanation, an actual answer. At most one sticker per
  message, and never as filler on top of real content.
- **Don't** output `:sticker:<id>:` as message content. The formal protocol is
  `send` with `parts`, for example
  `{"action":"send","parts":[{"type":"sticker","sticker_id":"hi"}]}`.
