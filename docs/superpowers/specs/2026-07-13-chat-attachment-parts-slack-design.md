# Chat attachments — Slack-style structured `parts` (hard cut)

- **Date:** 2026-07-13
- **Status:** draft for review
- **Product anchor:** Slack (text vs files separated; composer tray; message = body then attachment group)
- **Cut policy:** **Hard cut. No transition period.** No dual-write, no silent rewrite of legacy write shapes, no historical backfill guarantee.
- **Related:**
  - Product PRD §4.1 Attachment/Files — `docs/product-conversation-model-prd.md`
  - Schema target (`parts` sole truth) — `docs/superpowers/specs/2026-07-08-conversation-schema-target-design.md`
  - Agent CLI attachment surface — `docs/superpowers/specs/2026-07-09-agent-attachment-raft-align-design.md` (agent upload/id protocol; this doc owns **message shape**)

## Problem

Chat (channel / DM / thread) today treats files like **inline document nodes**:

1. Paste/upload calls `uploadAndInsertFile` → Tiptap `image` / `fileCard` at the **cursor**.
2. Send scans markdown URLs → builds sidecar `attachment_ids` → server binds attachment rows.
3. Bubble renders markdown in document order → **image / text / image** interleaving.

That is the opposite of Slack and of our own PRD (“composer → pending tray → attachment part”). Composer also caps inline images at ~9rem × 5.5rem, so terminal screenshots become unreadable black pills. `Composer` already has a `tray` mount; channel surfaces never fill it.

**Root model bug:** attachments are not first-class message structure; they are markdown accidents + a bind list.

## Goals

1. **Structured message body:** chat attachments live only as `parts[]` entries of `type: "attachment"`.
2. **Slack UX:** composer tray groups files; bubble is always **text block → attachment zone**.
3. **Single write path:** human, agent, and API share the same send contract.
4. **Hard cut:** ship FE + BE + CLI/skills together; reject old chat write shapes with 400.

## Non-goals

- Issue description / issue comment markdown inline images (out of scope; keep current model).
- Historical message bulk migration or guaranteed gallery for old `![]()` bodies.
- Dual-write or “accept `attachment_ids` and rewrite to parts” compatibility.
- `workspace_file_ref` (agent workspace browse) — separate contract.
- Image crop/annotate tooling.
- Full Slack unfurl / snippet engine.

## Decisions (locked)

| Topic | Decision |
|-------|----------|
| Surfaces | Channel / DM / Thread only |
| Canonical model | `parts[]` first-class `attachment` parts |
| History | No compatibility / no backfill guarantee |
| Transition | **None** |
| Bubble layout | Fixed: body → attachment zone |
| Product reference | Slack |

---

## 1. Data contract

### 1.1 `MessagePart` (TS + Go)

```ts
type MessagePart =
  | { type: "text"; text: string }
  | { type: "sticker"; pack_id?: string; sticker_id: string; alt?: string }
  | {
      type: "attachment";
      attachment_id: string; // UUID, required
      // Optional display hints (server may fill; clients must not trust alone)
      filename?: string;
      content_type?: string;
      size_bytes?: number;
    };
```

Go `protocol.MessagePart` and `messageparts.normalizePart` gain `attachment`:

- `attachment_id` required, valid UUID.
- On send: id must be workspace-visible and linkable to this channel/message (reuse existing ownership/link checks).
- Unknown `type` → 400.

### 1.2 Layer responsibilities

| Layer | Role | Forbidden |
|-------|------|-----------|
| `parts[]` | Sole message content truth (ordered text / sticker / attachment) | Encoding attachments as `![](url)` in text |
| `content` / future `text` column | Derived search/preview string from text (+ sticker alt if needed) | Source of truth for files |
| `attachment` table | File asset (storage, ACL, filename/mime/size, signed URLs) | Raw host paths as chat links |
| Response `attachments[]` | Hydration: id → metadata + usable URL | Second write-path list |

### 1.3 What `attachment_ids` was (and is not)

Historically, chat send accepted a **sidecar** `attachment_ids: string[]`: “bind these already-uploaded rows to this message.” The client built it by scanning whether markdown still contained upload URLs.

- It solved **DB binding**, not **layout or message structure**.
- Order/grouping still came from cursor-placed markdown.

**This design removes `attachment_ids` from the chat send/edit write contract.** Binding is derived only from `parts` where `type === "attachment"`.

Issue/comment may still use `attachment_ids` until those surfaces are redesigned — **out of scope**.

### 1.4 Validity

**Allowed messages:**

- Text only
- Sticker only / sticker + text
- Attachment only (Slack file-only message)
- Text + attachments
- Any ordered mix of the above part types (attachments still **render** in the attachment zone after text/stickers per §3; storage order in `parts` is tray order for attachments)

**Illegal:**

- Empty parts and no meaningful content
- Attachment part with missing/invalid/unauthorized id
- Empty text part (existing normalize rule)

### 1.5 Agent symmetry

- Human: tray → upload → `attachment_id` → attachment part.
- Agent: `attachment upload` → id → send with attachment parts (CLI may sugar flags into parts; **server only persists parts**).
- Context pack: metadata only; no auto-inject of file bytes (PRD unchanged).

---

## 2. Composer (Slack tray)

### 2.1 Draft model

Per channel / DM / thread composer draft:

```ts
type ComposerDraft = {
  textMarkdown: string; // text parts only — no attachment URLs
  pending: PendingAttachment[]; // ordered tray
};

type PendingAttachment = {
  localId: string;
  status: "uploading" | "ready" | "error";
  file?: File;
  attachmentId?: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
  previewUrl?: string; // blob or signed URL for UI only
  errorMessage?: string;
};
```

**Send assembly (only path):**

```ts
parts = [
  ...(textTrimmed ? [{ type: "text", text: textTrimmed }] : []),
  // stickers if already modeled as parts on this surface — unchanged
  ...pending
    .filter((p) => p.status === "ready" && p.attachmentId)
    .map((p) => ({
      type: "attachment" as const,
      attachment_id: p.attachmentId!,
    })),
];
```

No URL scan. No `attachment_ids` field.

### 2.2 Interactions

| Action | Behavior |
|--------|----------|
| Paste image/file | Into **tray**, never at cursor |
| Drop on composer | Same |
| Paperclip | File picker → tray |
| Tray item | Thumbnail / file row; remove; retry on error |
| Multi-image | Grouped in tray (readable thumbs, ~120–160px edge guidance) |
| Body editor | Text + mentions/issue refs only |
| Send while uploading | Disabled / blocked until ready |
| Success | Clear text + tray |

Layout (existing `composer-shell` / `tray` slot):

```
[ quote / prefix ]
[ tray: [img][img][file ×] ]   ← above editor, max-height + scroll
[ text editor ]
[ 📎  #  …              Send ]
```

### 2.3 ContentEditor split

| | Issue description / comment | Channel / DM / Thread |
|--|----------------------------|------------------------|
| Media | `mediaMode: "inline"` (current) | `mediaMode: "external"` |
| Paste/drop/upload | Insert image/fileCard in doc | Call `onExternalFiles` → tray only |
| `getMarkdown()` | May contain `![]()` | Must not carry attachment URLs |

Tray state is **React state**, not ProseMirror nodes.

### 2.4 Errors / edge cases

- Upload fail: tray error + retry/delete; body still editable.
- Attachment-only send: allowed.
- Channel switch: draft key isolation; revoke blob URLs on dispose.

---

## 3. Message UI (Slack bubble)

### 3.1 Layout

```
┌──────────────────────────────┐
│ Body: text (+ stickers)      │
│                              │
│ Attachment zone              │
│  [gallery / file tiles]      │  ← attachment parts only, tray order
└──────────────────────────────┘
```

- Never interleave attachments inside body paragraphs.
- Images: bounded gallery, click → lightbox.
- Non-images: file tiles (name / type / size / download).
- Mixed: preserve **parts order** among attachment parts (simple, matches tray).
- Attachment-only: no empty body chrome.

### 3.2 Read path

```
message.parts          → structure
message.attachments[]  → hydrate by attachment_id
```

- Missing / unauthorized: tombstone or “no access” without leaking filename (PRD).
- Chat surfaces **must not** primary-render attachments by regexing `![]()` from `content`.

### 3.3 Quote / search / thread root

| Surface | Summary |
|---------|---------|
| Quote | Text clamp + `N images` / filename / `N attachments` |
| Search / list preview | Same; no full gallery |
| Thread root | Compact body + light gallery or summary |

### 3.4 Copy

- Copy text parts as plain text.
- Attachments as product-defined summary lines (or ids), **not** as the primary path of re-pasteable `![](url)` into chat (avoids recreating dual truth).

### 3.5 Edit

- Edit = change text parts + add/remove attachment parts via the same tray model.
- Save sends **parts only**.

---

## 4. API / server (hard cut)

### 4.1 Send (channel top-level + thread)

**Request (sole legal write shape):**

```json
{
  "parts": [
    { "type": "text", "text": "check s146" },
    { "type": "attachment", "attachment_id": "<uuid>" }
  ],
  "client_message_id": "…",
  "quote_message_id": "…"
}
```

| Field | Rule |
|-------|------|
| `parts` | Canonical; at least one valid text / sticker / attachment |
| `content` | **Not** a write source of truth for chat; do not accept markdown-embedded files as attachments. Prefer omit; if present for transitional clients that are **not** supported, either ignore when parts present or 400 — **must not** bind files from content |
| `attachment_ids` | **Removed** from chat send/edit. If present → **400** |

**Server pipeline:**

1. Normalize parts (including `attachment`).
2. Collect attachment ids from parts → authorize → link to `channel_message_id`.
3. Persist `parts`; set `content`/`text` **derived** from text (+ sticker alt for preview), **without** attachment URLs.
4. Allow attachment-only messages (fix current “content is required” for pure-file cases).
5. Response: normalized `parts` + hydrated `attachments[]` in attachment-part order.

### 4.2 Edit

Same contract: `parts` only; re-bind from attachment parts.

### 4.3 Agent / CLI / skills

- Same message JSON shape as humans.
- CLI may accept `--attachment-id` sugar that becomes attachment parts before POST.
- Built-in skills / prompts: **delete** teaching that embeds files in markdown bodies for chat.
- Ship skill + CLI + server in the same cut.

### 4.4 Explicit rejections (no soft fallback)

| Client behavior | Server |
|-----------------|--------|
| `attachment_ids` without attachment parts | 400 |
| Only markdown images in `content`, no attachment parts | No bind as attachments; not a supported product path (FE must not produce) |
| Half-upgraded client | Breaks loudly — intentional hard cut |

---

## 5. Implementation map (for planning)

Not a task plan — orientation for `writing-plans`:

| Area | Change |
|------|--------|
| `server/pkg/protocol` + `messageparts` | `attachment` part type + normalize + FallbackContent |
| `server/internal/handler` channel send/edit | Derive binds from parts; drop chat `attachment_ids`; allow file-only |
| `packages/core/types/message-part.ts` + API client | Types; send body `parts` only for chat |
| `packages/views/editor` | `mediaMode: "external"` + `onExternalFiles` |
| `packages/views/channels` composer | Tray state, fill `Composer.tray`, send assembly |
| Message bubble / quote / preview | Body then attachment zone |
| Agent CLI + `builtin_skills` | Parts-only send teaching |
| Tests | Unit normalize; handler 400 cases; FE tray order; e2e paste two images + text |

**Suggested merge-ready order (all hard-cut; stackable PRs but no half-ship to prod):**

1. BE: part type + bind from parts + reject `attachment_ids` + file-only send  
2. FE: tray + external media mode + send parts  
3. FE: bubble gallery + previews  
4. Agent CLI/skills  
5. Delete dead chat inline-upload paths / e2e updates  

## 6. Acceptance criteria

1. Paste image A → type text → paste image B → **tray shows A|B together**; text only in editor; never A / text / B in the body stream.
2. Send → bubble is text then grouped attachments in tray order.
3. Network payload includes `parts` with `type: "attachment"`; binding does not depend on markdown URL presence.
4. Chat send with `attachment_ids` and no attachment parts → **400**.
5. Attachment-only message succeeds.
6. Agent send path documented and implemented as attachment parts, not markdown embeds.
7. Issue comment/description inline images still work (unchanged).

## 7. Risks

| Risk | Mitigation |
|------|------------|
| Desktop/web clients out of sync with server | Hard cut requires coordinated release; old clients get 400 on chat attach send |
| Users expect old interleaved layout | Product: Slack-like is intentional |
| Large tray of files | Cap + scroll (PRD tray behavior) |
| `content is required` server checks | Explicitly allow empty content when attachment/sticker parts present |

---

## Appendix A — Current vs target (one screen)

| | Today | Target |
|--|-------|--------|
| Where files live in composer | Cursor inline nodes | Tray |
| Where files live in message | Markdown order | `parts` attachment |
| Bind list | Client URL scan → `attachment_ids` | Server derives from parts |
| Multi-image UI | Interleaved / tiny inline | Slack group gallery |
| History | — | No migration promise |
| Transition | — | **None** |

## Appendix B — Open implementation choices (non-blocking for product)

These do not change product decisions; plan may pick:

1. Exact gallery grid breakpoints (1 / 2 / 3+ images) — match Slack-ish density, not pixel-perfect Slack.
2. Whether stickers stay above or inline with text block (keep current sticker rendering unless it conflicts).
3. Whether chat `content` field is omitted on write entirely vs ignored when `parts` present — either is fine if files never bind from content.
