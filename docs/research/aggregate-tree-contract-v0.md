# Research canvas aggregate tree contract (LRM-1278)

Authoritative BE contract for the research session canvas **display tree** and
**quality assessment** fields. FE must not invent hierarchy or bend/detour from
edge colors.

## Tree edges

| Edge type | Role |
| --- | --- |
| `leads_to` | **Only** tree edge. `from_node_id` = parent, `to_node_id` = child. |
| `supports` / `contradicts` / `supersedes` / `abandons` | Semantic only. Never used for `parent_id` / counts. |

Invariant: at most one tree parent. If multiple inbound `leads_to` exist, the
earliest by `created_at` wins for `parent_id`; others remain visible on `edges`
for diagnostics.

## Node fields (snapshot `GET …/sessions/{id}` → `nodes[]`)

| Field | Type | Source | Notes |
| --- | --- | --- | --- |
| `id` | string | column | |
| `parent_id` | string \| null | derived from `leads_to` | null = root |
| `child_ids` | string[] | derived | direct children only; `[]` when none |
| `child_count` | number | derived | `len(child_ids)` |
| `descendant_count` | number | derived | all descendants; cycle-safe |
| `theme_key` | string | payload → else `type:<node_type>` | payload keys: `theme_key`, `themeKey`, `dimension_family`, `dimensionFamily`, `family` |
| `phase` | string (optional) | `payload.phase` | omit/empty when unset; session stage stays on `session.current_stage` |
| `status` | string | column | `active` \| `done` \| `abandoned` |
| `assessment` | string | payload → normalize | always present |
| `confidence` | number \| omit | `payload.confidence` | existing LRM-806 projection |
| `reason` | string \| omit | `payload.reason` or `assessment_reason` | short UI copy |
| `evidence_summary` | string \| omit | `payload.evidence_summary` or `evidence` | short copy or count text |

### `assessment` enum

| Value | Product meaning |
| --- | --- |
| `trusted` | 高可信·有效 |
| `pending_review` | 待核·中性 (**default** when missing/illegal) |
| `detour` | 弯路·不准确 |

Aliases accepted on write/payload: `高可信`/`有效`/`high_trust`/`valid` → `trusted`;
`弯路`/`不准确`/`inaccurate`/`wrong_path` → `detour`; `待核`/`中性`/`pending`/`neutral` → `pending_review`.

FE **must** treat missing/illegal as `pending_review`. Do not infer assessment from
edge type or stroke color.

## Incremental WS (`research_session.graph_updated`)

- New node events include the same projected fields when the accompanying edge is
  available (`parent_id` set for `leads_to` children).
- `child_count` / `descendant_count` on a partial event may be incomplete for
  **ancestors**. Full snapshot remains authoritative for counts after mutations.

## Landing path (this PR)

- **No migration**: tree + assessment projected at read time from existing
  `research_graph_edge` + `payload`.
- Writers that want stable assessment should set `payload.assessment` /
  `confidence` / `reason` / `evidence_summary` when creating/updating nodes.
- Follow-up (optional): first-class columns if payload provenance becomes
  insufficient — out of this slice.

## Fixture sketch (3-node chain)

```
goal --leads_to--> subquestion --leads_to--> finding
                 \--supports--> finding   (ignored for parent/counts)
```

Expected projection:

- goal: `parent_id=null`, `child_count=1`, `descendant_count=2`, `assessment=pending_review`
- subquestion: `parent_id=goal`, `child_count=1`, `descendant_count=1`
- finding: `parent_id=subquestion`, `child_count=0`, `descendant_count=0`
