---
name: multica-stickers
description: "Use when a chat, channel, or direct message would land better with a little emotion — a sticker to react, celebrate, agree, commiserate, or lighten the tone. Documents the `multica sticker search` command, which searches the embedded sticker library by mood or keyword (Chinese or English) and prints a token for each match. Drop a printed token — :sticker:<id>: — into the content of any message you send (a `multica dm`, a channel post, an issue comment) and it renders as the sticker for the human. Stickers are a garnish: at most one per message, only when it genuinely fits, never on routine status updates. Traced to server/cmd/multica/cmd_sticker.go, server/internal/stickers/, and the :sticker: token renderer in packages/views/common/markdown.tsx."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Using stickers

Stickers let you add a touch of human warmth or humour to a message — to react,
celebrate a win, agree, say thanks, commiserate over a bug, or soften a "no".
They are the cute/funny kind, like the stickers people send in chat apps.

Every claim below is pinned to source in
`references/stickers-source-map.md`. If behavior ever differs from this
document, the source map is where to re-check it.

## Find a sticker

    multica sticker search <keyword>

Search by **mood or keyword, in Chinese or English**. The library is bundled
into the CLI, so search is instant and works offline:

    multica sticker search 开心
    multica sticker search celebrate
    multica sticker search 谢谢

Each match prints a **token**, a name, and a mood:

    TOKEN                 NAME           MOOD
    :sticker:tada:        撒花 / Tada      celebrate
    :sticker:clap:        鼓掌 / Clap      celebrate

`multica sticker list` prints the whole library. If a search finds nothing, it
lists the available moods so you can pick one.

## Send a sticker — put the token in your message

There is **no separate "send sticker" command**. You send a sticker by putting
its token into the **content of a message you were already sending** — a
`multica dm`, a channel message, or an issue comment. The frontend turns the
token into the sticker image for the human.

    multica dm --message "搞定了，已经上线 :sticker:tada:"

    multica dm --message ":sticker:thumbs-up: 收到，这就去做"

The token is `:sticker:<id>:` exactly as printed — `:sticker:tada:`,
`:sticker:thumbs-up:`. An unknown id simply renders as nothing, so always use a
token you got from `multica sticker search`.

## Use them sparingly

A sticker is a garnish, not a signature.

- **At most one sticker per message**, and only when it genuinely fits the
  moment — a finished task, a shared frustration, a friendly greeting.
- **Never** decorate routine progress updates or task logs with stickers, and
  never send a sticker on its own as a reply when words are what's needed.
- When in doubt, leave it out. Overused stickers read as noise; a well-placed
  one reads as warmth.
