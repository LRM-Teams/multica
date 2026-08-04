# Research session presence contract v2 (LRM-1377)

Authoritative BE contract for `GET /api/research/sessions/:id/presence`.
Old clients that only read `activity` + `updated_at` remain valid; new fields
are additive.

## Response shape

```json
{
  "session_id": "<uuid>",
  "presence": {
    "<agent_id>": {
      "activity": "string",
      "updated_at": 0,
      "phase": "idle",
      "role": "lead",
      "fleet_member_id": "<uuid>",
      "task_id": null,
      "node_id": null,
      "branch_id": null,
      "stale_reason": null
    }
  }
}
```

### Invariants

1. **Full roster**: every **active** workspace fleet member (one per role, same
   dedupe as `GET /api/research/fleet`) appears as a key — even with no graph
   activity (reporter/lead idle case → `phase=idle`, empty `activity`).
2. **Phase enum** (stable): `idle` | `queued` | `running` | `done` | `failed` | `stale`.
3. **Associations**: `task_id` / `node_id` / `branch_id` come only from real
   event payload fields (`task_id`, `details.task_id`, `node_id`,
   `target_node(_id)`, `branch_id`). Missing → JSON `null` (never invented).
4. **Stale**: `queued` / `running` whose latest signal is older than **15m**
   become `phase=stale` with `stale_reason="presence_expired"`. Terminal
   phases are not stale-rewritten.

## Merge priority (source map)

| Priority | Signal | Source nodes | Notes |
| --- | --- | --- | --- |
| Highest (clears started) | Terminal | `finding` + `event_type=task_result_accepted` → `done`; `dead_end` + `task_attempt_failed` / `task_blocked` → `failed` | Newer than non-terminal → replaces generic “开始执行” |
| High | Explicit presence | `agent_activity` with `payload.phase=presence` | Concrete caption; not overwritten by newer generic titles |
| Mid | Specific activity | other non-generic `agent_activity` titles | |
| Low | Generic lifecycle | titles `调研任务已分派` / `Agent 开始执行调研任务`, or `event_type` `task_dispatching` / `task_started` | May enrich `phase` / `task_id` onto a still-valid specific caption |
| None | — | no signals for member | `idle` |

Implementation: `server/internal/handler/research_presence.go`
(`buildResearchPresenceRoster`). Handler entry: `GetResearchPresence` in
`research_ops.go`.

## WS note

`POST …/presence` still publishes `{agent_id, activity, updated_at}` for live
patches. Clients that need full v2 fields should refetch GET (or treat WS as a
partial patch and keep roster keys from the last GET).

## Fixture sketch (true-sample 5→5)

Fleet: lead, domain_a–c, reporter. Graph has activity only for domain_a–c.

Expected: 5 keys; lead+reporter `idle`; domain with `phase=presence` keeps
concrete `activity` even if a later `task_started` node exists; associations
null unless payload carried them.
