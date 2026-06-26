---
name: multica-stickers
description: "Send a sticker (表情包) instead of typing a short reply. When your whole reply would be a short social beat — hi/你好, ok/好的/没问题, 收到/明白, 谢谢, 赞/厉害/牛逼, 完美, 欢迎, 哈哈, 安排/这就办 — put a :sticker:<id>: token in your message and that IS the reply; do NOT also type the words. Tokens you can use right now with zero lookup: :sticker:hi: (你好), :sticker:ok: (好的/没问题), :sticker:got-it: (收到/明白), :sticker:nod-yes: (同意), :sticker:thumbs-up: (赞), :sticker:impressive: (厉害/牛逼), :sticker:perfect: (完美), :sticker:thanks: (谢谢), :sticker:applause: (鼓掌/恭喜), :sticker:on-it: (安排), :sticker:huaji: (哈哈). For anything else run `multica sticker search <mood>` ONCE — never read skill files, never run `multica sticker list`. Don't put stickers on substantive/technical messages. Traced to server/cmd/multica/cmd_sticker.go, server/internal/stickers/, and the :sticker: renderer in packages/views/common/markdown.tsx."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Using stickers

When your reply is just a short social beat — a greeting, an acknowledgement,
praise, agreement, thanks — **send a sticker instead of typing it.** The sticker
IS the reply; don't also type the words it already says (a `:sticker:got-it:`
alone is the whole "收到", no extra text needed).

## Fast path — common replies (use these tokens directly, no command)

These ids are stable; use them straight from this table — do **not** look them up.

| When you'd reply… | Send just this |
| --- | --- |
| 你好 / hi / 在吗 | `:sticker:hi:` |
| 好的 / ok / 没问题 | `:sticker:ok:` |
| 收到 / 明白 / 懂了 | `:sticker:got-it:` |
| 同意 / 对 / 点头 | `:sticker:nod-yes:` |
| 赞 / 点赞 | `:sticker:thumbs-up:` |
| 厉害 / 牛逼 / 绝了 | `:sticker:impressive:` |
| 完美 / 没毛病 | `:sticker:perfect:` |
| 谢谢 | `:sticker:thanks:` |
| 比心 / 爱了 | `:sticker:heart-hands:` |
| 鼓掌 / 恭喜 | `:sticker:applause:` |
| 安排 / 这就办 | `:sticker:on-it:` |
| 哈哈 / 笑死 | `:sticker:huaji:` |

Put the token into the message you send — e.g. `multica dm --message ":sticker:got-it:"`
or a channel reply. That's it: no command, no file read.

## Need something more specific?

Run exactly **one** search (Chinese or English). Do not list the whole library,
do not read any files:

    multica sticker search 害怕
    multica sticker search celebrate

It prints `:sticker:<id>:` tokens; pick one and put it in your message.

## When to use it — and when not

- **Do** replace a short canned reply (hi / ok / 收到 / 谢谢 / 赞 / 厉害 / 安排)
  with a single sticker. That's the main use.
- **Don't** stick one on a substantive message that carries real information — a
  status report, a code explanation, an actual answer. At most one sticker per
  message, and never as filler on top of real content.
