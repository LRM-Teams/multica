# Canvas recombination contract v0 (LRM-1317)

Authoritative BE contract for **node four content faces**, **abandon reason**,
and **utterance match decisions**. Aligns product freeze on
[LRM-1310](https://github.com/LRM-Teams/multica/issues)（对话→节点匹配）and
parent [LRM-1308](https://github.com/LRM-Teams/multica/issues)（节点四内容面）.

FE must not invent content faces, abandon reasons, match scores, edge colors, or
tree rewiring when fields are missing.

## Naming reconciliation

| Product (LRM-1310 / Chinese) | API / column | Notes |
| --- | --- | --- |
| 目标 / 操作思路 / 调研思路 / 调研结果 | `content.goal` / `operation_approach` / `research_approach` / `result` | Nested under `content` so it does not collide with `session.goal` or `node_type=goal` |
| 废弃态 | `status = "abandoned"` | **Already** on `research_graph_node.status` (`active` \| `done` \| `abandoned`) |
| `deprecate` action / `deprecate_reason` | node `abandon_reason`; decision `action: "deprecate"` | Column stays `abandoned`; reason field follows issue AC `abandon_reason`. Writers may set payload `deprecate_reason` (alias) |
| `matched_node_ids` + `action` + … | `match_decision` envelope (below) | Message- or session-scoped; not inferred by FE |

**废弃 ≠ 弯路：** `status=abandoned` / decision `deprecate` is dialogue淘汰.
`assessment=detour` (LRM-1278 / 1268) is path quality. Both may appear on the
same node; FE must keep copy/visual distinct. Do not map one to the other.

## Node fields (snapshot `GET …/sessions/{id}` → `nodes[]`)

Additive projection on top of [aggregate-tree-contract-v0.md](./aggregate-tree-contract-v0.md).

| Field | Type | Source | Missing / empty |
| --- | --- | --- | --- |
| `content.goal` | string | `payload.content.goal` or `payload.goal_text` | `""` — FE 中性占位，不编造 |
| `content.operation_approach` | string | `payload.content.operation_approach` or aliases below | `""` |
| `content.research_approach` | string | `payload.content.research_approach` or aliases | `""` |
| `content.result` | string | `payload.content.result` or aliases | `""` |
| `status` | string | column | existing enum |
| `abandon_reason` | string \| omit | `payload.abandon_reason` or `payload.deprecate_reason` | omit when empty; **never** invent from `assessment` / edge color |

### Payload write aliases (accepted)

- `content` object preferred.
- Flat aliases (first non-empty wins):
  - goal: `content.goal`, `goal_text`, `node_goal`
  - operation_approach: `content.operation_approach`, `operation_approach`, `ops_approach`
  - research_approach: `content.research_approach`, `research_approach`
  - result: `content.result`, `research_result`, `result_summary`
- abandon reason: `abandon_reason`, `deprecate_reason`

`content` is **always** present on projected nodes (four keys, possibly empty).
`abandon_reason` is omitted unless non-empty.

## Match decision (utterance-scoped)

Product trigger: **human** research-session utterance only (not agent finding).
Output is a list FE can animate from — do not recompute matching client-side.

### Envelope (storage = `research_message.meta.match_decision`; LRM-1330)

Also projected top-level on `messages[]` as `match_decision` when present.
**Omit** the key when absent — never ship a fake empty list as if matching ran.

```json
{
  "utterance_id": "msg-uuid",
  "confidence": 0.82,
  "primary_anchor_node_id": "node-uuid",
  "matched_node_ids": ["node-uuid"],
  "decisions": [
    {
      "node_id": "node-uuid",
      "action": "continue",
      "reason": "用户要求继续挖竞品定价；命中定价支"
    },
    {
      "node_id": "other-uuid",
      "action": "deprecate",
      "reason": "「刚才方向错了」— 与监管合规新需求不符"
    }
  ]
}
```

Writer (agent / fleet): `PUT /api/agent/research/sessions/{id}/messages/{messageId}/match-decision`
with body `{ "match_decision": { ... } }` on a **human** utterance row.

| Field | Type | Notes |
| --- | --- | --- |
| `utterance_id` | string | Human message / utterance id |
| `confidence` | number 0..1 or omit | Product also allows enum high\|mid\|low on write; API normalizes to number when possible |
| `primary_anchor_node_id` | string \| omit | Anchor for `continue` / `branch_after` |
| `matched_node_ids` | string[] | May be empty |
| `decisions` | `MatchDecisionItem[]` | **Issue AC `match_decision` list** |

### `decisions[].action` enum (LRM-1310)

| action | Tree effect |
| --- | --- |
| `continue` | Continue research on matched node (same branch) |
| `branch_after` | Attach new node **after** best-matching anchor |
| `deprecate` | Mark node `status=abandoned` + require human-readable reason |
| `pending_confirm` | Do **not** mutate tree; UI neutral 待确认 |

One utterance may emit **multiple** `deprecate` items + **at most one** primary
attach (`continue` or `branch_after`). Low confidence → `pending_confirm` only.

## Example: node with faces + abandoned

```json
{
  "id": "22222222-2222-2222-2222-222222222222",
  "node_type": "subquestion",
  "title": "竞品定价",
  "summary": "…",
  "status": "abandoned",
  "content": {
    "goal": "摸清头部竞品公开价带",
    "operation_approach": "官网+招股书交叉",
    "research_approach": "先横向再纵向",
    "result": "价带已粗分；监管维度未覆盖"
  },
  "abandon_reason": "用户「刚才方向错了，改成监管合规」— 定价支与新需求不符",
  "assessment": "detour",
  "parent_id": "11111111-1111-1111-1111-111111111111",
  "child_ids": [],
  "child_count": 0,
  "descendant_count": 0,
  "theme_key": "pricing"
}
```

Note: `assessment=detour` and `status=abandoned` coexist; FE must not collapse them.

## Example: missing faces (neutral)

```json
{
  "id": "33333333-3333-3333-3333-333333333333",
  "status": "active",
  "content": {
    "goal": "",
    "operation_approach": "",
    "research_approach": "",
    "result": ""
  }
}
```

No `abandon_reason` key. FE shows empty/neutral placeholders; must not invent copy or colors.

## FE prohibitions

1. Do not guess `content.*` from `title`/`summary` when empty.
2. Do not invent `abandon_reason` from `assessment`, edge stroke, or layout.
3. Do not invent `match_decision` / confidence / rewiring; wait for BE envelope.
4. Do not treat semantic edges (`abandons`, `supports`, …) as tree parents (see aggregate-tree contract).
5. Do not map `assessment=detour` ↔ `status=abandoned`.

## Landing path (this slice)

| Piece | Status |
| --- | --- |
| `content` four faces projected on `nodes[]` | **Implemented** (read-time from payload; no migration) |
| `abandon_reason` projected when present | **Implemented** (payload; no migration) |
| Writers populate `payload.content` / reasons | **GAP** — fleet / matcher not yet writing faces |
| Persist + return `match_decision` per human utterance | **Implemented** (meta + top-level projection; PUT writer) |
| Matcher that emits continue/branch_after/deprecate | **GAP** — algorithm + tree mutation path |
| First-class columns for faces / reason | Optional follow-up if payload provenance insufficient |

### Match-decision storage (v0 landed)

1. **v0:** `research_message.meta.match_decision` on the human utterance row; snapshot/WS project via `messages[].match_decision`.
2. New `research_match_decision` table — deferred unless meta provenance proves insufficient.
3. Session-level ring buffer on `research_session` — discouraged (harder to audit).

Do **not** ship a fake empty `match_decisions: []` on every snapshot as if matching ran.

## Out of scope

- FE/UI visual specs (LRM-1309 / 1315 / 1311)
- Side panel 1306 → see [thought-strategy-panel-contract-v0.md](./thought-strategy-panel-contract-v0.md) (LRM-1318)
- Aggregate tree / assessment (LRM-1278) — unchanged
- Auth / permissions
