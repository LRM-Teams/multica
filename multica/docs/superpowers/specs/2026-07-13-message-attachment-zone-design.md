# Message attachment zone — long-term design

- **Date:** 2026-07-13
- **Status:** draft for review
- **Scope:** **Read path only** — channel / DM / thread **message bubble** (and shared compact previews that reuse the same zone).  
  **Out of scope this doc:** composer tray (separate), issue comments, agent workspace file refs.
- **Depends on:** `docs/superpowers/specs/2026-07-13-chat-attachment-parts-slack-design.md` (parts contract is settled).
- **Product anchor:** Slack message file presentation — not pixel-perfect clone; same *information architecture*.

## Problem

Parts contract is correct (`type: "attachment"` under body). Presentation is not yet a product system:

1. **Images and files share one `flex-wrap` bag** — no gallery grid rules; mixed media looks random.
2. **Non-image tiles use `w-full max-w-[340px]`** — forces **one file per row** (vertical stack) even when the row has room. Feels like a document list, not a chat attachment group.
3. **Reuses generic `<Attachment>` / `AttachmentCard`** built for issue/editor contexts — hover chrome, max widths, and spacing fight chat density.
4. **Compact surfaces** (thread root, quote) only height-cap; no true summary mode.
5. **Order** is parts order (good) but **visual grouping** (all images, then files vs interleaved) is undefined.

Users correctly reject vertical stacks of same-class chips when peers sit on one line in Slack.

## Goals

1. One **MessageAttachmentZone** contract used everywhere a chat message shows files.
2. **Images** → Slack-ish **gallery** (count-based layout, bounded size, lightbox).
3. **Non-images** → **horizontal chip row** (wrap or scroll), never full-width vertical list by default.
4. **Parts order** remains source of truth for *sequence within a kind group*; define **kind grouping** explicitly.
5. **Density modes**: `default` | `compact` | `summary` for bubble / thread root / quote.
6. No second content truth — still hydrate only via `attachment_id` → `attachments[]`.

## Non-goals

- Composer tray redesign (follow-up; should **reuse** the same visual primitives).
- Issue description/comment markdown images.
- Historical `![]()` migration.
- Video scrubber / PDF page strip (use existing preview modal).
- Drag-reorder of sent attachments (edit flow later).

---

## 1. Information architecture (locked)

```
Message bubble
├── Header (author, time, …)
├── Body          text + stickers only   (MessagePartsRenderer)
└── AttachmentZone                      ← this design
      ├── ImageGallery   (0..N images)
      └── FileChipRow    (0..N non-images)
```

### 1.1 Kind split (render grouping)

| Kind | Detection | Container |
|------|-----------|-----------|
| **image** | `content_type` starts with `image/` | `ImageGallery` |
| **file** | everything else with a resolved record (pdf, zip, sh, …) | `FileChipRow` |
| **missing** | part id not in `attachments[]` | placeholder chip (no filename leak) |

**Grouping rule (default):**

1. Walk `parts` in order; partition into image parts vs file parts **preserving relative order within each partition**.
2. Render **ImageGallery first** (if any), then **FileChipRow** (if any).

Rationale: Slack visually groups media; mixed “image, pdf, image” as three separate full rows is worse than gallery + chip row.  
**Parts order within each group** still matches send order among that kind.

Alternative considered and **rejected for v1:** strict single stream interleaved by parts order (image thumb, file chip, image thumb) — harder to layout, noisier.

### 1.2 Data in / data out

```ts
// inputs (unchanged contract)
parts?: MessagePart[] | null;       // attachment parts = truth for *which* + order
attachments?: Attachment[] | null;  // hydrate by id

// pure resolver (extend existing message-attachment-zone-items)
type ZoneModel = {
  images: ResolvedAttachmentItem[];  // only kind:record images + missing that were image-hinted? missing always file-style placeholder
  files: ResolvedAttachmentItem[];
};
```

Missing parts: no content_type → treat as **file-style** placeholder (generic unavailable chip).

---

## 2. ImageGallery (long-term)

### 2.1 Layout by count (Slack-ish)

| Count | Layout |
|-------|--------|
| 1 | Single image, max width `min(100%, 22.5rem)`, max height `22.5rem`, `object-fit: contain`, rounded |
| 2 | Two equal columns, gap `0.375rem`, each max height `12rem`, `object-fit: cover` crop |
| 3 | First large left (or top on narrow), two stacked right — **or** simple 3-up equal if easier; prefer equal 3-up on mobile |
| 4 | 2×2 grid |
| 5+ | 2×2 (or 3-up) of first 3–4 + **“+N”** overlay on last cell; click opens lightbox at index 0 or that cell |

Exact CSS can match design tokens; rules above are the **product contract**.

### 2.2 Interaction

- Click image → existing lightbox / `Attachment` preview path.
- Keyboard: cells focusable buttons/links with filename in accessible name.
- No nested scroll inside gallery; bubble scrolls.

### 2.3 Bounds

- Gallery max width: `min(100%, 22.5rem)` (align existing `.message-surface` image cap).
- Never expand bubble past message column.

---

## 3. FileChipRow (long-term)

### 3.1 Layout

```
[ chip ][ chip ][ chip ] …   ← flex-row, flex-wrap OR single-row + overflow-x-auto
```

**Default (recommended):**  
`flex flex-row flex-wrap items-center gap-1.5`  
each chip `w-fit max-w-[12rem] shrink-0`  
→ multiple `.sh` / pdf sit **on one line** when space allows; wrap only when needed.

**Optional density `scroll`:**  
`flex-nowrap overflow-x-auto` for very attachment-heavy messages (agent dumps). Product can start with wrap-only.

### 3.2 Chip anatomy (shared primitive)

```
┌──────────────────────────┐
│ [ext icon] name.ext   ✕? │  ← name truncated; size·type on second line optional in default
└──────────────────────────┘
```

- **Primary action:** open preview if previewable, else download (same rules as `AttachmentCard` a11y).
- **Secondary:** download always available (hover/focus or overflow menu on mobile).
- **No** full-width 340px card in the zone by default — that was the vertical-stack bug.
- Visual: reuse file icon + meta from `AttachmentCard` logic, but **new presentational shell** `MessageFileChip` sized for chat (padding `px-2 py-1.5`, text `text-xs`), not the large issue card.

### 3.3 Missing / denied

Same chip silhouette, dashed border, label = i18n `attachment_unavailable` only (PRD: no filename leak).

---

## 4. Density modes

| Mode | Where | Behavior |
|------|--------|----------|
| `default` | Channel / DM / thread bubble | Full gallery + chip row |
| `compact` | Thread root preview, dense lists | Gallery max-height ~4rem, max 2–3 thumbs visible; chips single line clamp + “+N files” |
| `summary` | Quote strip, search hit | **No** media render — text only: `2 images · notes.pdf` (existing quote helpers) |

`MessageAttachmentZone` API:

```ts
type MessageAttachmentZoneProps = {
  parts?: MessagePart[] | null;
  attachments?: Attachment[] | null;
  density?: "default" | "compact" | "summary";
  className?: string;
};
```

Deprecate boolean `compact` → map `compact={true}` to `density="compact"` during impl.

---

## 5. Component boundaries

```
message-attachment-zone.tsx          orchestration + density
  image-gallery.tsx                  count layout + lightbox entry
  message-file-chip.tsx              single non-image chip
  message-file-chip-row.tsx          row layout
  message-attachment-zone-items.ts   pure resolve / partition (test-heavy)
```

**Do not** import issue `AttachmentList`.  
**Do** call shared preview/download hooks / `getPreviewKind` / size formatters from editor utils.  
**Avoid** mounting full editor `Attachment` dispatcher for every file if it forces large card layout — either:

- add `variant="message-chip" | "message-image"` to `Attachment`, or  
- zone uses low-level hooks + small presentational components.

Recommendation: **`variant` on Attachment** only if cheap; else **zone-local chips** calling the same open/download handlers to keep editor surface free of chat density hacks.

---

## 6. Surfaces map

| Surface | Zone? | Density |
|---------|-------|---------|
| Channel bubble | yes | default |
| DM bubble | yes | default |
| Thread reply bubble | yes | default |
| Thread root preview | yes | compact |
| Quote preview | no media | summary string only |
| Search / inbox snippet | no media | summary string |
| Composer tray | **out of scope** | must later reuse `MessageFileChip` + image thumb primitive |

---

## 7. A11y

- Gallery: `role="group"` + `aria-label` “N images”.
- Each image control: name = filename.
- File chips: single primary control naming “Open/Download {filename} · {size} · {type}”.
- Missing: not focus-trapping; plain text status.

---

## 8. Migration / compatibility

- **New messages:** already parts-only; zone is sole visual path.
- **Old messages** with markdown images and no attachment parts: **unchanged** (no gallery promise) — out of scope.
- **No API change** required if hydrate already returns `attachments[]` ordered or unordered (zone sorts by parts).

---

## 9. Implementation phases (message only)

### P0 — Fix vertical file stack (behavior contract)

1. Partition images vs files in resolver.
2. File row: `flex-row flex-wrap` + `w-fit` chips (stop `w-full max-w-[340px]`).
3. Images: keep single/multi via wrap thumbs with shared max bounds (gallery grid can be rough).
4. Tests: two non-image attachments → container has `flex-row`; chips not `w-full`.

### P1 — Real ImageGallery

1. Count-based layouts 1 / 2 / 3 / 4 / 5+.
2. +N overflow cell.
3. Lightbox index wiring.

### P2 — Density + shared primitives

1. `density` API; quote/search summary helpers aligned.
2. Extract `MessageFileChip` for later composer reuse.
3. Optional `Attachment` variant cleanup.

---

## 10. Acceptance (message)

1. Message with 2× `.sh` → **one horizontal chip row** (side by side when width allows).
2. Message with 4 images → grid, not four full-width stacked heroes.
3. Message with 2 images + 1 pdf → gallery then pdf chip under body; **never** text between.
4. Missing attachment id → dashed unavailable chip, no filename.
5. Compact thread root does not explode height.
6. Quote shows “2 images” / filename summary, not inline media.

---

## 11. Decision log

| Topic | Decision |
|-------|----------|
| Scope | Message zone only |
| Kind grouping | Images gallery first, then files row |
| File layout | Horizontal chips, not full-width stack |
| Image layout | Count-based gallery |
| Data | parts + hydrate only |
| Composer | Later; reuse chips |

---

## 12. Open (non-blocking)

1. Wrap vs horizontal scroll for 10+ files — default wrap; measure later.
2. 3-image layout exact geometry — design polish in P1.
3. Whether `Attachment` gets `variant` or zone stays presentational — choose in P0/P1 impl for least churn.
