---
name: multica-attachments
description: "Share a local file (image, screenshot, chart, report, log excerpt, generated artifact) directly in chat instead of just describing it or pasting its path. Run `multica send --attachment <path>` to upload and attach it to the message you send. Combine with --message for context: `multica send --message \"here's the chart\" --attachment ./chart.png`. Repeat --attachment for multiple files. Only local file paths are supported — never pass a URL. This is for real file content, not social-beat replies (use the multica-stickers skill for those)."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Sending file attachments

When you have a local file worth sharing directly — a chart or screenshot you
generated, a report, a log excerpt, a diff, a config file — **attach it to
your message** instead of only describing it in text or dropping its path.

Every claim below is pinned to source in
`references/attachments-source-map.md`. If behavior ever differs from this
document, the source map is where to re-check it.

## How to send

Attachment with context:

    multica send --message "这是刚跑出来的图表" --attachment ./chart.png

Attachment only (no text needed):

    multica send --attachment ./report.pdf

Multiple files in one message — repeat `--attachment`:

    multica send --message "before/after" --attachment ./before.png --attachment ./after.png

`--target`, `--sticker`, and `--show-in-channel` all work the same way they do
for a plain `multica send` text message — see `multica send --help`.

## Rules

- **Only local file paths.** `--attachment` uploads the file's bytes; it does
  not accept `http://`/`https://` URLs. If you want to reference something
  already online, put the URL in `--message` text instead.
- **Don't attach what you can just say.** A one-line status update or a short
  answer stays plain text. Attach when the *file itself* is the useful
  artifact — an image, a rendered document, structured output too long or
  too lossy to paste as text.
- **Not for canned social replies.** A sticker (hi/ok/收到/thanks/…) is a
  different tool — see the `multica-stickers` skill. Don't attach an image
  file to stand in for a sticker.
- Large or slow uploads are fine — `multica send` automatically extends its
  timeout when `--attachment` is present.
