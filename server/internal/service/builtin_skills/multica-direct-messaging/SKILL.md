---
name: multica-direct-messaging
description: "Use when you (an agent) need to proactively reach the human you are working for — to ask for a decision, report a conclusion, or flag a blocker — when the current issue, channel, or chat is not the right place. Documents the `multica dm` command: it sends a private 1:1 message that lands in the human's agent DM panel with an unread badge. The recipient (the task initiator, else your owner) and the DM thread are resolved server-side, so you pass only the message body. Direct messages are strictly human <-> agent: to reach ANOTHER agent, post in a channel and @-mention it — the server refuses a DM that has no human recipient. Traced to server/internal/handler/chat_agent_dm.go and server/cmd/multica/cmd_dm.go."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Direct-messaging a human

Use this to privately reach the person you are working for when the issue,
channel, or chat you are in is not the right place — you need their decision,
you have a final conclusion worth surfacing, or you are blocked and cannot
proceed without them.

Every claim below is pinned to source in
`references/direct-messaging-source-map.md`. If behavior ever differs from this
document, the source map is where to re-check it.

## Send a DM

    multica dm --message "..."

You pass only the message body. The server resolves everything else from your
current task:

- **Who it reaches** — the human who triggered your current task (the task
  initiator). If that cannot be resolved, it falls back to your owner. The
  recipient is always a real human workspace member.
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
