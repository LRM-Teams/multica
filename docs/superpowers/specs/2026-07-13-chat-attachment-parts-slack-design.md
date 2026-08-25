# Chat attachments — Slack-style structured `parts` (hard cut)

- **Date:** 2026-07-13
- **Status:** draft for review
- **Product anchor:** Slack (text vs files separated; composer tray; message = body then attachment group)
- **Cut policy:** **Ship only the latest path.** No dual-write, no long-lived compatibility layer, no historical backfill guarantee. **Do not build protocol hostility** (no required 400 on leftover fields): delete old client/server write paths so nothing in-repo still *uses* them.
- **Related:**
  - Product PRD §4.1 Attachment/Files — `docs/product-conversation-model-prd.md`
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
4. **Latest only:** FE + BE + CLI/skills all send/read the parts model; **remove** chat code that scans markdown for binds, inserts images at cursor, or sends `attachment_ids` as the attachment truth. No dual path left in the product.

## Non-goals

- Issue description / issue comment markdown inline images (out of scope; keep current model).
- Historical message bulk migration or guaranteed gallery for old `![]()` bodies.
- Dual-write or a second supported write shape.
- **Protocol-level rejection theater:** no requirement that the server 400 every obsolete field for its own sake — cleanliness is “no old callers / no old implementation,” not “punish stray JSON.”
- `workspace_file_ref` (agent workspace browse) — separate contract.
- Image crop/annotate tooling.
- Full Slack unfurl / snippet engine.

## Decisions (locked)

| Topic | Decision |
|-------|----------|
| Surfaces | Channel / DM / Thread only |
| Canonical model | `parts[]` first-class `attachment` parts |
| History | No compatibility / no backfill guarantee |
| Transition | **None** — only the new path ships |
| Old write shapes | **Deleted from codebase**; not maintained; no 400 mandate |
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
| `channel_message_attachment` | Canonical many-to-many message → file-resource references | Treating one message as the resource owner |
| Response `attachments[]` | Hydration: id → metadata + usable URL | Second write-path list |

An upload creates one `attachment` resource. Sending creates a reference after workspace/uploader authorization; it does not move or clone that resource. `attachment.channel_id` remains provenance only, so the same owned id can be referenced by group, DM, thread, and later messages without another byte upload.

### 1.3 What `attachment_ids` was (and is not)

Historically, chat send accepted a **sidecar** `attachment_ids: string[]`: “bind these already-uploaded rows to this message.” The client built it by scanning whether markdown still contained upload URLs.

- It solved **DB binding**, not **layout or message structure**.
- Order/grouping still came from cursor-placed markdown.

**Chat product write path uses only attachment parts.** Binding is derived from `parts` where `type === "attachment"`. Clients and server chat handlers **stop producing/depending on** chat `attachment_ids` as attachment truth; remove those call sites.

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
| `parts` | **Canonical write source**; at least one valid text / sticker / attachment |
| `content` | Not attachment truth. Prefer omit on write; if present, never bind files from markdown URLs |
| `attachment_ids` | **Not used by any shipped chat client.** Server chat path binds from attachment **parts** only. Prefer delete/stop reading the field in chat handlers when call sites are gone; no need to special-case 400 |

**Server pipeline:**

1. Normalize parts (including `attachment`).
2. Collect attachment ids from **parts** → authorize workspace/uploader → insert message-resource associations.
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

### 4.4 “No old path” (implementation cleanliness)

| Rule | Meaning |
|------|---------|
| One write implementation | Chat FE/CLI only assemble `parts`; no URL-scan → `attachment_ids` |
| One reference implementation | Server creates attachment associations from attachment parts only |
| Delete dead code | Remove cursor-inline upload for chat surfaces, tray-less send, markdown-bind maps |
| No dual-write | Do not also embed `![](url)` in text for the same files |
| Stray fields | Not a product concern if nothing we ship sends them; no required 400 |

---

## 5. Implementation map (for planning)

Not a task plan — orientation for `writing-plans`:

| Area | Change |
|------|--------|
| `server/pkg/protocol` + `messageparts` | `attachment` part type + normalize + FallbackContent |
| `server/internal/handler` channel send/edit | Derive binds from parts; stop depending on chat `attachment_ids`; allow file-only |
| `packages/core/types/message-part.ts` + API client | Types; chat send body uses `parts` |
| `packages/views/editor` | `mediaMode: "external"` + `onExternalFiles` |
| `packages/views/channels` composer | Tray state, fill `Composer.tray`, send assembly |
| Message bubble / quote / preview | Body then attachment zone |
| Agent CLI + `builtin_skills` | Parts-only send teaching |
| Tests | Unit normalize; bind-from-parts; FE tray order; e2e paste two images + text; assert no chat URL-scan bind |

**Suggested merge-ready order (latest-only; stackable PRs, delete old paths as you go):**

1. BE: part type + bind from parts + file-only send
2. FE: tray + external media mode + send parts; **remove** chat URL-scan / inline image insert
3. FE: bubble gallery + previews
4. Agent CLI/skills
5. Sweep remaining dead chat attachment code / e2e updates

## 6. Acceptance criteria

1. Paste image A → type text → paste image B → **tray shows A|B together**; text only in editor; never A / text / B in the body stream.
2. Send → bubble is text then grouped attachments in tray order.
3. Network payload includes `parts` with `type: "attachment"`; binding does not depend on markdown URL presence.
4. **No shipped chat code** still builds `attachment_ids` from markdown URL scans or inserts images at the composer cursor.
5. Attachment-only message succeeds.
6. Agent send path documented and implemented as attachment parts, not markdown embeds.
7. Issue comment/description inline images still work (unchanged).
8. Human and Agent sends can reuse one owned id across multiple messages and conversations, including group → DM, while each message hydrates the resource normally.
9. Migration preserves old links and repairs valid same-workspace ids found in historical attachment/voice parts; invalid/deleted ids are not fabricated.

## 7. Risks

| Risk | Mitigation |
|------|------------|
| Half-migrated tree (new UI + old bind path) | Same change set removes old path; review checklist = “grep for chat attachment_ids / uploadAndInsert in channel composer” |
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
| History | Singular message ownership could strand valid parts | Backfill valid part references, then drop singular ownership |
| Transition | — | **Latest only; old paths deleted** |

## Appendix B — Open implementation choices (non-blocking for product)

These do not change product decisions; plan may pick:

1. Exact gallery grid breakpoints (1 / 2 / 3+ images) — match Slack-ish density, not pixel-perfect Slack.
2. Whether stickers stay above or inline with text block (keep current sticker rendering unless it conflicts).
3. Whether chat `content` field is omitted on write entirely vs ignored when `parts` present — either is fine if files never bind from content.
