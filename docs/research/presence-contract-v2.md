# Research session presence contract v2 (LRM-1377)

Authoritative BE contract for `GET /api/research/sessions/:id/presence`.
Old clients that only read `activity` + `updated_at` remain valid; new fields
are additive.

**Presence is a derived view.** Durable run `tasks` / `attempts` remain the
execution source of truth and can replay the same lifecycle without reading
presence. Graph `agent_activity` captions may enrich display text but must not
be the only lifecycle SoT for run-v2 sessions.

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
      "stage": null,
      "expires_at": null,
      "stale_reason": null
    }
  }
}
```

### Invariants

1. **Full roster**: every **active** session-bound fleet member appears as a
   key — even with no graph activity and no attempt (idle case → `phase=idle`,
   empty `activity`). Prefer session fleet (`session.fleet_id`) over workspace
   bootstrap.
2. **Phase enum** (stable): `idle` | `queued` | `running` | `done` | `failed` | `stale`.
3. **Associations**:
   - `task_id` from the current attempt/task row (or legacy event payload).
   - `node_id` for run-v2 is the deterministic canvas **task** node id
     (`runGraphNodeID(session, task, task_id)`), so FE can locate the worker.
   - `branch_id` only from real event payload fields. Missing → JSON `null`.
4. **Stage / expiry**:
   - `stage` mirrors `research_session.current_stage` when a durable run exists.
   - `expires_at` is unix ms from attempt/task start + `timeout_seconds` when
     known; otherwise `null`.
5. **Stale**:
   - `queued` / `running` past `expires_at` → `phase=stale`,
     `stale_reason="attempt_expired"`.
   - else when latest signal older than **15m** → `stale_reason="presence_expired"`.
   - Terminal phases are not stale-rewritten.

## Merge priority (source map)

| Priority | Signal | Source | Notes |
| --- | --- | --- | --- |
| Highest (clears started) | Terminal | attempt `succeeded`/`failed`/`lost`, or graph `finding`/`dead_end` lifecycle events | Newer than non-terminal → replaces generic “开始执行” |
| High | Attempt ledger | `research_task_attempt` (+ assigned task with no attempt yet) | Preferred lifecycle SoT for run-v2; supplies `task_id` / canvas `node_id` / `stage` / `expires_at` |
| High | Explicit presence | `agent_activity` with `payload.phase=presence` | Concrete caption; may decorate attempt activity text |
| Mid | Specific activity | other non-generic `agent_activity` titles | |
| Low | Generic lifecycle | titles `调研任务已分派` / `Agent 开始执行调研任务`, or `event_type` `task_dispatching` / `task_started` | May enrich `phase` onto a still-valid specific caption |
| None | — | no signals for member | `idle` |

Implementation: `server/internal/handler/research_presence.go`
(`buildResearchPresenceRosterWithRun`). Handler entry: `GetResearchPresence` in
`research_ops.go`.

## WS note

`POST …/presence` still publishes `{agent_id, activity, updated_at}` for live
patches. Clients that need full v2 fields should refetch GET (or treat WS as a
partial patch and keep roster keys from the last GET).

## Fixture sketch (true-sample 5→5)

Fleet: lead, scout, domain_*, reporter. Run ledger has attempts only for
scout/domain. Graph may have zero `agent_activity` rows (run-v2 projection).

Expected: 5 keys; idle members stay idle; scout/domain carry `task_id`,
canvas `node_id`, `stage`, and `expires_at` from the attempt/task timeout.
