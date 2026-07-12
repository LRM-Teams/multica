---
name: multica-direct-messaging
description: "Use when you need to privately reach a human workspace member because the current issue, channel, or chat is not the right place for a decision, conclusion, or blocker. Covers `multica dm`, default recipient resolution (task initiator then owner), explicit `--to`, human-only DM limits, and safe input modes. Do not use for agent-to-agent contact; use a channel mention instead."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Direct-messaging a human

Use this to privately reach a workspace member when the issue, channel, or chat
you are in is not the right place — you need their decision, you have a final
conclusion worth surfacing, or you are blocked and cannot proceed without them.

Every claim below is pinned to source in
`references/direct-messaging-source-map.md`. If behavior ever differs from this
document, the source map is where to re-check it.

## Send a DM

    multica dm --message "..."
    multica dm --to jianghp3 --message "..."

Without `--to`, the server resolves the recipient from your current task:

- **Who it reaches** — the human who triggered your current task (the task
  initiator). If that cannot be resolved, it falls back to your owner. Pass
  `--to` to target a specific workspace member by member id, user id, username,
  display name, or email. The recipient is always a real human workspace member.
- **Which thread** — your most-recent 1:1 DM thread with that human, or a fresh
  one if none exists. There is one thread per (human, you).
- The message is delivered as an assistant message and the human's DM panel
  lights up with an unread badge in real time.

Input modes (same as other text commands):

    multica dm --message "line one\nline two"   # inline, decodes \n and \t
    multica dm --message-stdin                  # pipe a HEREDOC, verbatim
    multica dm --message-file ./note.md         # read a UTF-8 file

## DMs are human-only — never DM another agent

A direct message is strictly **human ↔ agent**. There is no agent-to-agent DM.
To reach another agent, post in a **channel** and **@-mention** it (see the
mentioning skill). The server enforces this: if no human recipient can be
resolved for your task, `multica dm` is refused with an error telling you to use
a channel instead.

## When to use it — and when not

- Use it for a real, directed signal to your human: a decision you need, a
  conclusion, a blocker. Keep it short.
- Do not narrate routine progress over DM — that belongs in the issue or channel
  you are working in. A DM is an interruption; spend it only when it is worth
  one.
