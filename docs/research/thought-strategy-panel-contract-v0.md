# Thought / strategy panel contract v0 (LRM-1318)

Authoritative BE contract for the canvas-side **思路 / 策略** panel designed in
[LRM-1306](https://github.com/LRM-Teams/multica/issues). FE must not invent
`rationale` / `expected_outcome` / strategy revisions from node title, summary,
colors, or layout.

## Snapshot field

`GET /api/research/sessions/{id}` (and agent twin) → `thought_strategies[]`

Always present (may be `[]`). Each row:

| Field | Type | Notes |
| --- | --- | --- |
| `node_id` | string | Must equal an existing `nodes[].id`. FE disables linkage if missing. |
| `rationale` | string | 「怎么想」— short; empty only when `state=drafting` |
| `expected_outcome` | string | 「要达到什么」— same |
| `strategy_label` | string \| omit | Optional chip ≤20 CJK |
| `strategy_revision` | string \| omit | Stable version or timestamp; **only real changes** may bump |
| `state` | `drafting` \| `active` \| `settled` | Panel state machine |
| `updated_at` | string \| omit | Node `updated_at` echo |

### Inclusion rules

1. **Both** `rationale` and `expected_outcome` non-empty → include (default `state=active` if unset).
2. Explicit `state=drafting` with partial faces → include as drafting (empty face stays `""`).
3. Otherwise **omit** the node from the list.
4. Never synthesize faces from `title` / `summary` / `content.*` / assessment.

### Payload write aliases (first non-empty wins)

Preferred nested object: `payload.thought_strategy` (aliases `thoughtStrategy`, `strategy`).

| API field | Accepted keys |
| --- | --- |
| rationale | `rationale`, `how_thinking` |
| expected_outcome | `expected_outcome`, `expectedOutcome` |
| strategy_label | `strategy_label`, `strategyLabel` |
| strategy_revision | `strategy_revision`, `strategyRevision` |
| state | `state`, `strategy_state`, `strategyState` |

If `strategy_revision` is absent but the row is included and a label or both faces
exist, projection falls back to the node’s `updated_at` string so FE can diff
without inventing client-side clocks.

### Example (fixture-shaped)

```json
{
  "thought_strategies": [
    {
      "node_id": "22222222-2222-2222-2222-222222222222",
      "rationale": "先横向挂牌价再纵向促销",
      "expected_outcome": "欧洲挂牌价对照表",
      "strategy_label": "价带扫描",
      "strategy_revision": "v3",
      "state": "active",
      "updated_at": "2026-08-04T04:00:00Z"
    }
  ]
}
```

## Out of scope

- UI / motion / panel wiring (FE after this contract)
- Matcher / canvas recombination (`match_decision` — LRM-1330)
- Node four content faces (LRM-1317 `content.*`) — different surface
