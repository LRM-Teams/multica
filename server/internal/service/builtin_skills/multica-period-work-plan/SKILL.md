---
name: multica-period-work-plan
description: "Use when the Notes Assistant (笔记助手) is woken as the 写汇报 collect-plan commander. Covers restating the human focus (paths / topics / aspects), assigning only needed collectors from the roster, and submit-collect-plan delivery. Do not use for writing the final Period Work Brief (multica-period-work-brief) or collecting OS work (multica-period-work-collect)."
user-invocable: false
allowed-tools: Bash(multica *)
---

# Period Work collect-plan commander

The platform woke you **before** collectors run because the human gave a
scoped 写汇报 request. Your job is to **understand that request and assign
collection tasks**. You do **not** write the Brief yet. You do **not**
collect the OS yourself.

## Audience of this wake

You are commanding Period Work collectors. The next wake (after packs
settle) is the synthesizer wake — that is a different prompt.

## What to read

1. Untrusted `<focus>` — the human request (path, topic, aspects, or
   unconstrained).
2. Untrusted `<roster>` — collectors the human already selected. You may
   skip some. You **cannot add** ids that are not on the roster.

## How to assign

- Restate the request as `summary` plus optional `paths` / `topics` /
  `aspects`.
- Assign a collector when that Computer can hold matching work.
- `skip: true` when that Computer cannot help (wrong machine, cloud-only
  request vs laptop-only, etc.).
- For each assigned collector, write a short `brief` so they scan narrowly.
- If the request is unconstrained, assign **every** roster collector with
  empty paths/topics/aspects (full `SCAN_ROOTS`).

## Deliver

```bash
multica notes period-brief submit-collect-plan --draft-page-id <draft>
```

stdin (or `--json`) must be JSON:

```json
{
  "summary": "Only notes-assistant work under ~/multica",
  "assignments": [
    {
      "collector_agent_id": "<roster uuid>",
      "skip": false,
      "paths": ["/home/you/multica"],
      "topics": ["notes assistant"],
      "aspects": ["commits", "dirty trees"],
      "brief": "Focus on feat/notes-agent. Ignore unrelated repos."
    }
  ]
}
```

After a successful response: **stop and wait**. The platform dispatches
collectors, then re-wakes you as the synthesizer.

## Forbidden

- `--note-write`
- `submit-pack`
- Collecting the OS yourself
- Writing the Period Work Brief in this wake
- Adding collector ids that are not on the roster

Source map: `references/period-work-plan-source-map.md`
