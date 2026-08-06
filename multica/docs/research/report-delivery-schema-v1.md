# Research report delivery schema v1 (LRM-843)

Contract for the research session **report** delivery surface: envelope fields
already persisted by the server, plus the agreed shape of `structured`
(outline / sections / citations / sources), versioning, and legacy compatibility.

Authoritative code helpers: `@multica/core/research` → `normalizeReportStructured`,
`ResearchReportStructuredV1Schema`.

Mock fixtures:

- [`fixtures/report-v1.example.json`](./fixtures/report-v1.example.json) — full v1 mock
- [`fixtures/report-legacy-empty.example.json`](./fixtures/report-legacy-empty.example.json) — pre-contract row

## Envelope (`ResearchReportResp`)

Returned on `GET /api/research/sessions/:id` as `report`, and on WS
`research_session:report_updated` as `payload.report`.

| Field | Type | Notes |
| --- | --- | --- |
| `id` | UUID string | Row id |
| `session_id` | UUID string | |
| `revision` | int | **Delivery / row version** — increments on every PATCH |
| `content_md` | string | Authoritative readable Markdown body |
| `structured` | object | Opaque JSON column; v1 shape below. Legacy may be `{}` |
| `created_at` / `updated_at` | RFC3339 string | |

### Write path

`POST /api/research/sessions/:id/report` (fleet member):

```json
{
  "content_md": "# …",
  "structured": { "schema_version": 1, "...": "…" },
  "new_revision": true
}
```

Server always inserts a new report row and bumps `revision` (current behavior).
`structured` omitted / null / empty → stored as `{}`.

## `structured` schema_version = 1

```ts
{
  schema_version: 1;
  title: string;
  outline: Array<{ id; title; level: 1..6; children: string[] }>;
  sections: Array<{ id; title; level; markdown; citation_ids: string[] }>;
  citations: Array<{
    id; index: number /* 1-based */; source_id; label; quote?; locator?;
  }>;
  sources: Array<{
    source_id; title; url; credibility_weight; source_class;
  }>;
  gaps?: string[];
  conclusion?: string;
}
```

### Field semantics

- **outline** — navigation tree. `children` lists child section ids in order.
  Top-level outline nodes usually mirror `level: 1` sections.
- **sections** — chapter bodies. `citation_ids` reference `citations[].id`.
- **citations** — in-text refs. `source_id` is `research_source.id`.
  `label` is the display token (e.g. `"[1]"`).
- **sources** — denormalized snapshot for export / offline mock. Live session
  still has `sources[]` on the snapshot; resolve by `source_id` when both exist.
- **gaps / conclusion** — optional delivery aids for S4 UI; not required to render.

`source_class` is a free string matching `ResearchSource.source_class`
(default `"other"`; templates also use values like `"docs"`).

## Versioning

Two independent version axes:

1. **Row version** — top-level `revision` (int, starts at 1, bumps on PATCH).
   Use for cache invalidation, export filenames, “报告修订 rN” graph nodes.
2. **Structure version** — `structured.schema_version` (currently `1`).
   Writers MUST set `schema_version: 1` when emitting the v1 shape.

Upgrade path:

- New writers emit full v1 `structured` with `schema_version: 1`.
- Never mutate the meaning of an existing `revision` row in place — PATCH
  creates a new row with a higher `revision`.
- Future `schema_version: 2+` must remain readable via `content_md`; unknown
  versions are treated as read-only markdown (see below). Server does **not**
  hard-validate `structured` today.

## Legacy / compatibility

| Incoming `structured` | FE behavior |
| --- | --- |
| missing / `null` / `{}` | `render_mode: markdown_only` — render `content_md`; outline empty OK |
| object **without** `schema_version` | same as legacy empty (do not invent structure) |
| `schema_version: 1` (valid) | `render_mode: structured` — use outline/sections/citations |
| unknown `schema_version` or invalid v1 shape | `render_mode: readonly_markdown` — prefer `content_md`; ignore unknown fields |

Additional FE rules:

- If `content_md` is present but `sections` is empty, FE **may** derive a
  temporary outline from markdown headings for navigation — **do not write
  back** unless the user / report officer explicitly saves.
- Session-level `sources[]` remains the live source of truth for the sources
  panel; report `structured.sources` is a delivery snapshot.

Use `normalizeReportStructured(report.structured)` from `@multica/core/research`
to get `{ kind, structured, render_mode }`.
