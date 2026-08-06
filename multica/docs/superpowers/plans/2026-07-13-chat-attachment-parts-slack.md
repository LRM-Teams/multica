# Chat attachment parts (Slack-style) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use **ultracode Workflow** for implementation (project override of superpowers plan-execution skills). Work in a git worktree. Steps use checkbox (`- [ ]`) syntax for tracking. TDD still applies inside each task.

**Goal:** Channel / DM / Thread send and render attachments like Slack — composer tray, structured `parts` with `type: "attachment"`, bubble always body → attachment zone — with only the latest path left in code (no dual-write, no required 400 theater).

**Architecture:** Attachments leave the Tiptap document model on chat surfaces. Upload yields `attachment_id` into a React pending tray; send assembles `parts[]` (text + attachment parts). Server normalizes attachment parts and binds `attachment` rows from those ids. Message UI hydrates `attachments[]` and renders a grouped zone under text. Issue description/comment keep inline markdown media.

**Tech Stack:** Go (Chi handlers, `messageparts`, sqlc attachment link), TypeScript monorepo (`@multica/core` types/API, `@multica/views` channels + editor), Vitest, `go test`, Playwright e2e.

**Spec:** `docs/superpowers/specs/2026-07-13-chat-attachment-parts-slack-design.md`
**Preview:** `docs/superpowers/specs/2026-07-13-chat-attachment-parts-slack-preview.html`

## Global Constraints

- **Surfaces:** Channel / DM / Thread only. Do not change issue description/comment inline image model.
- **Latest only:** Delete chat code that scans markdown URLs to build `attachment_ids` or inserts image/fileCard at the composer cursor. No dual-write of `![](url)` for the same files.
- **No 400 mandate:** Do not add server 400 solely to reject leftover `attachment_ids`; bind from parts; remove old call sites.
- **History:** No bulk migration / no guarantee old interleaved markdown becomes a gallery.
- **Comments in code:** English only.
- **i18n:** New user-visible strings go through `packages/views/locales/` + conventions glossary.
- **Worktree:** Never implement on bare `main` checkout; use project worktree flow + env symlinks per `CLAUDE.local.md`.

---

## File map

| Path | Responsibility |
|------|----------------|
| `server/pkg/protocol/messages.go` | `MessagePartTypeAttachment`, fields on `MessagePart` |
| `server/internal/messageparts/messageparts.go` | Normalize attachment parts; `FallbackContent` for attachment-only |
| `server/internal/messageparts/messageparts_test.go` | Unit tests for normalize |
| `server/internal/handler/channel.go` | Send/edit: collect bind ids from parts; allow empty content when parts have attachment/sticker; stop relying on chat `attachment_ids` as bind source |
| `server/internal/handler/channel_test.go` | Handler tests for parts bind + file-only |
| `packages/core/types/message-part.ts` | TS `MessagePart` union + attachment variant |
| `packages/core/api/client.ts` | Chat send helpers send `parts`; drop chat client assembly of `attachment_ids` |
| `packages/core/channels/mutations.ts` | Mutation vars use `parts` |
| `packages/views/editor/content-editor.tsx` | `mediaMode: "inline" \| "external"` + `onExternalFiles` |
| `packages/views/editor/extensions/file-upload.ts` | External mode: paste/drop call out, no insert |
| `packages/views/channels/hooks/use-composer-pending-attachments.ts` | Tray state + upload (new) |
| `packages/views/channels/components/composer-attachment-tray.tsx` | Tray UI (new) |
| `packages/views/channels/components/channels-page.tsx` | Wire tray, external editor, send parts (channel + thread) |
| `packages/views/channels/components/dm-conversation.tsx` (and any sibling composers) | Same wire-up |
| `packages/views/channels/components/message-body.tsx` | Prefer parts; attachment zone |
| `packages/views/channels/components/message-attachment-zone.tsx` | Gallery + file tiles (new) |
| `packages/views/channels/components/channel-message-bubble.tsx` | Compose body + zone; drop markdown-first image path for chat |
| `packages/views/channels/components/message-quote.tsx` / previews | Summary from parts |
| Agent CLI / `server/internal/service/builtin_skills/*` | Teach/send attachment parts; update source maps in same PR |
| `e2e/*` | Paste two images + text; assert layout / payload |

---

### Task 1: Protocol + normalize `attachment` part (backend)

**Files:**
- Modify: `server/pkg/protocol/messages.go`
- Modify: `server/internal/messageparts/messageparts.go`
- Modify: `server/internal/messageparts/messageparts_test.go`

**Interfaces:**
- Produces: `protocol.MessagePartTypeAttachment = "attachment"`; part field `AttachmentID string \`json:"attachment_id,omitempty"\`` (plus optional `Filename`, `ContentType`, `SizeBytes` if already convenient); `normalizePart` accepts attachment with non-empty UUID-shaped id; `FallbackContent` returns empty or a stable non-URL label for attachment-only (e.g. filename if present, else empty — content may be empty when only attachments).

- [ ] **Step 1: Write failing tests** in `messageparts_test.go`

```go
func TestNormalizeAttachmentPart(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	content, parts, err := Normalize("", []protocol.MessagePart{{
		Type:         protocol.MessagePartTypeAttachment,
		AttachmentID: id,
	}})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if content != "" {
		// attachment-only: derived content has no markdown image URL
		t.Fatalf("content = %q, want empty", content)
	}
	if len(parts) != 1 || parts[0].Type != protocol.MessagePartTypeAttachment || parts[0].AttachmentID != id {
		t.Fatalf("parts = %+v", parts)
	}
}

func TestNormalizeAttachmentRequiresID(t *testing.T) {
	_, _, err := Normalize("", []protocol.MessagePart{{Type: protocol.MessagePartTypeAttachment}})
	if err == nil {
		t.Fatal("expected error for missing attachment_id")
	}
}
```

- [ ] **Step 2: Run tests — expect FAIL**

```bash
cd server && go test ./internal/messageparts/ -run 'TestNormalizeAttachment' -count=1
```

- [ ] **Step 3: Implement protocol + normalize**

In `messages.go`:

```go
const (
	MessagePartTypeText       = "text"
	MessagePartTypeSticker    = "sticker"
	MessagePartTypeAttachment = "attachment"
)

type MessagePart struct {
	Type         string `json:"type"`
	Text         string `json:"text,omitempty"`
	PackID       string `json:"pack_id,omitempty"`
	StickerID    string `json:"sticker_id,omitempty"`
	Alt          string `json:"alt,omitempty"`
	AttachmentID string `json:"attachment_id,omitempty"`
	Filename     string `json:"filename,omitempty"`
	ContentType  string `json:"content_type,omitempty"`
	SizeBytes    int64  `json:"size_bytes,omitempty"`
}
```

In `normalizePart`, add case for `MessagePartTypeAttachment`: trim `AttachmentID`, reject empty, clear text/sticker fields, return part.

Update `FallbackContent` so attachment-only does not invent fake body text that looks like a URL (empty is fine if sticker/text absent).

- [ ] **Step 4: Run tests — expect PASS**

```bash
cd server && go test ./internal/messageparts/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/pkg/protocol/messages.go server/internal/messageparts/
git commit -m "feat(messageparts): add attachment part type and normalize"
```

---

### Task 2: Channel send/edit bind from attachment parts

**Files:**
- Modify: `server/internal/handler/channel.go` (send top-level, thread send, edit paths that link attachments)
- Modify: `server/internal/handler/channel_test.go`
- Possibly: helper to extract attachment IDs from `[]protocol.MessagePart`

**Interfaces:**
- Consumes: Task 1 attachment parts
- Produces: `attachmentIDsFromParts(parts []protocol.MessagePart) []string` (or inline); send allows empty `content` when parts contain attachment or sticker; link SQL still uses UUID slice derived from parts

- [ ] **Step 1: Write failing handler test**

```go
// seed channel + unbound attachment owned by user, then:
// POST message with parts: [{type:attachment, attachment_id}], content ""
// expect 200/201, message.Parts has attachment, ListAttachments bound to message id
// and content does not contain markdown image URL
```

Also test: text + two attachment parts → both bound in order.

- [ ] **Step 2: Run test — expect FAIL** (content required / no bind)

```bash
cd server && go test ./internal/handler/ -run 'Test.*AttachmentPart|Test.*FileOnly' -count=1
```

- [ ] **Step 3: Implement**

1. After `messageparts.Normalize`, if `content == ""` && parts include sticker or attachment → do **not** `content is required`.
2. Build bind list:

```go
func attachmentIDsFromParts(parts []protocol.MessagePart) []string {
	var ids []string
	for _, p := range parts {
		if p.Type == protocol.MessagePartTypeAttachment && p.AttachmentID != "" {
			ids = append(ids, p.AttachmentID)
		}
	}
	return ids
}
```

3. Pass that slice into existing `LinkAttachmentsToChannelMessage` / `LinkOwnedAttachmentsToChannelMessage` paths instead of (or as sole source instead of) `req.AttachmentIDs`.
4. Stop **requiring** clients to send `attachment_ids` for chat. Prefer not reading `req.AttachmentIDs` once FE is updated in Task 4–5; until then, if both present, **parts win** (latest only — do not merge dual sources). Spec: no dual-write; parts are sole bind source after FE lands — implement parts-as-source in this task; remove `AttachmentIDs` usage from chat handlers when Task 5 removes client field (or same PR stack).

5. Validate attachment ids belong to workspace / linkable (reuse existing checks around link helpers).

- [ ] **Step 4: Run handler tests — PASS**

```bash
cd server && go test ./internal/handler/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/handler/
git commit -m "feat(channels): bind message attachments from parts"
```

---

### Task 3: Core types + API client send `parts`

**Files:**
- Modify: `packages/core/types/message-part.ts`
- Modify: `packages/core/api/client.ts` (`sendChannelMessage`, thread send, edit if any)
- Modify: `packages/core/channels/mutations.ts`
- Test: add/extend unit test near API schema or a small pure helper if you extract `buildChannelSendBody`

**Interfaces:**
- Produces:

```ts
export type MessagePart =
  | { type: "text"; text: string }
  | { type: "sticker"; pack_id?: string; sticker_id: string; alt?: string }
  | {
      type: "attachment";
      attachment_id: string;
      filename?: string;
      content_type?: string;
      size_bytes?: number;
    };
```

- Chat send body: `{ parts, client_message_id?, quote_message_id?, ... }` — do not send `attachment_ids` from channel mutations once composers are updated. Keep issue/comment `attachment_ids` unchanged.

- [ ] **Step 1: Extend type + failing mutation signature**

```ts
// mutations: sendChannelMessage({ channelId, parts, clientMessageId, quoteMessageId })
// client.sendChannelMessage(channelId, { parts, clientMessageId, quoteMessageId })
```

Prefer an options object over positional `content, attachmentIds, parts` soup if a small cleanup is free; otherwise add `parts` as primary and stop passing `attachmentIds` from channel code paths.

- [ ] **Step 2: Unit test** that serialized body includes `parts` with `attachment` and omits `attachment_ids` for the new helper/call shape.

- [ ] **Step 3: Implement client + mutations**

- [ ] **Step 4: `pnpm --filter @multica/core exec vitest run` on touched tests**

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(core): channel send uses attachment parts"
```

---

### Task 4: Editor `mediaMode: "external"` for chat

**Files:**
- Modify: `packages/views/editor/content-editor.tsx`
- Modify: `packages/views/editor/extensions/file-upload.ts` (or create thin wrapper)
- Modify: `packages/views/editor/content-editor.test.tsx` / `file-upload.test.ts`
- Leave issue/comment callers on default `mediaMode: "inline"` (or omit prop)

**Interfaces:**
- Produces:

```ts
mediaMode?: "inline" | "external"; // default "inline"
onExternalFiles?: (files: File[]) => void;
```

When `mediaMode === "external"`:
- paste/drop/files from paperclip **must not** call `setImage` / insert `fileCard`
- invoke `onExternalFiles(dedupedFiles)` instead
- `uploadFile` imperative API on ref: either no-op + console/dev assert, or route to `onExternalFiles`

- [ ] **Step 1: Failing test** — with `mediaMode="external"` and paste image file, editor markdown has no `![](` and `onExternalFiles` called once.

- [ ] **Step 2: Implement** in `createFileUploadExtension` / `uploadAndInsertFile` branch:

```ts
if (mediaMode === "external") {
  onExternalFiles?.(Array.from(files));
  return true; // handled
}
// existing inline path
```

Thread `mediaMode` + callback via refs like existing `onUploadFileRef`.

- [ ] **Step 3: Tests pass; issue path still inserts image (regression test if missing)**

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(editor): external media mode for chat tray"
```

---

### Task 5: Composer pending tray + channel/DM/thread wire-up

**Files:**
- Create: `packages/views/channels/hooks/use-composer-pending-attachments.ts`
- Create: `packages/views/channels/hooks/use-composer-pending-attachments.test.ts`
- Create: `packages/views/channels/components/composer-attachment-tray.tsx`
- Create: `packages/views/channels/components/composer-attachment-tray.test.tsx`
- Modify: `packages/views/channels/components/composer.tsx` (already has `tray` slot — use it)
- Modify: `packages/views/channels/components/channels-page.tsx` (channel + thread composers)
- Modify: `packages/views/channels/components/dm-conversation.tsx` (and any other chat composers)
- Delete usage of: `uploadMapRef` URL→id scan in chat send

**Interfaces:**
- Produces:

```ts
type PendingAttachment = {
  localId: string;
  status: "uploading" | "ready" | "error";
  attachmentId?: string;
  filename: string;
  contentType: string;
  sizeBytes: number;
  previewUrl?: string;
  errorMessage?: string;
};

function useComposerPendingAttachments(opts: {
  upload: (file: File) => Promise<UploadResult | null>;
}): {
  pending: PendingAttachment[];
  addFiles: (files: File[]) => void;
  remove: (localId: string) => void;
  retry: (localId: string) => void;
  clear: () => void;
  hasUploading: boolean;
  readyAttachmentParts: Extract<MessagePart, { type: "attachment" }>[];
};
```

- Send:

```ts
const text = editor.getMarkdown().trim(); // no attachment URLs
const parts: MessagePart[] = [
  ...(text ? [{ type: "text", text }] : []),
  ...readyAttachmentParts,
];
// mutate with parts; block if hasUploading || parts empty
```

- [ ] **Step 1: Hook tests** — addFiles starts uploading; success → ready + attachmentId; remove drops item; readyAttachmentParts order = add order.

- [ ] **Step 2: Implement hook** (use existing `useFileUpload` / `uploadWithToast` patterns from channels-page).

- [ ] **Step 3: Tray UI** — horizontal wrap of thumbs + file chips, × remove, error state; fill `Composer` `tray={...}`.

- [ ] **Step 4: Wire ContentEditor** `mediaMode="external" onExternalFiles={addFiles}`; paperclip → `addFiles`; **remove** `onUploadFile={handleUpload}` inline path for chat.

- [ ] **Step 5: handleSend** builds `parts` only; clear tray + editor on success; drop `uploadMapRef` scan.

- [ ] **Step 6: Vitest** for hook + tray + send assembly if extracted pure function `buildChatParts(text, pending)`.

- [ ] **Step 7: Commit**

```bash
git commit -m "feat(channels): Slack-style composer attachment tray"
```

---

### Task 6: Message bubble attachment zone

**Files:**
- Create: `packages/views/channels/components/message-attachment-zone.tsx`
- Create: `packages/views/channels/components/message-attachment-zone.test.tsx`
- Modify: `packages/views/channels/components/message-body.tsx`
- Modify: `packages/views/channels/components/channel-message-bubble.tsx`
- Modify: `packages/views/channels/components/message-quote.tsx` / `message-parts-preview.ts` as needed
- Modify: `packages/views/channels/components/thread-root-preview.tsx`

**Interfaces:**
- Produces: `MessageAttachmentZone({ parts, attachments })`
  - Collect attachment parts in order
  - Hydrate via `attachments` by id
  - Images → gallery (bounded, click lightbox via existing Attachment)
  - Non-images → file tiles
  - Missing/denied → PRD-safe placeholder

- `MessageBody` / bubble: render text+sticker parts in body; **always** render zone under body for attachment parts (not interleaved in markdown).

- [ ] **Step 1: Tests** — two image attachments render two thumbs in zone; text not between them; quote summary uses counts not raw markdown images.

- [ ] **Step 2: Implement zone + integrate**

- [ ] **Step 3: Ensure chat path does not primary-render images from markdown `![]()` when attachment parts exist (latest path). YAGNI: if new messages never write markdown images, body markdown simply has no images.

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(channels): render attachments in Slack-style zone under body"
```

---

### Task 7: Agent CLI / skills alignment

**Files:**
- Modify: `server/cmd/multica/cmd_message.go` (and related) so send builds attachment parts
- Modify: `server/internal/service/builtin_skills/*` SKILL.md + `references/*-source-map.md` in **same PR** (repo rule)
- Cross-check: `docs/superpowers/specs/2026-07-09-agent-attachment-raft-align-design.md` — message shape must be parts, not markdown embeds

**Interfaces:**
- CLI sugar `--attachment-id` → append `{type:attachment, attachment_id}` to parts before POST
- Do not teach agents to put images in markdown body for chat

- [ ] **Step 1: Failing CLI test or handler test with agent-shaped body**

- [ ] **Step 2: Implement + update skills**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(cli): send chat attachments as message parts"
```

---

### Task 8: Dead-path sweep + e2e

**Files:**
- Grep and delete: channel composer URL maps, inline upload for chat only
- `e2e/` chat attachment specs
- Docs already written; no product doc required beyond skill updates in Task 7

- [ ] **Step 1: Grep gates (must be clean for chat paths)**

```bash
rg -n "uploadMapRef|attachmentIds.*content\.includes|onUploadFile=\{handleUpload\}" packages/views/channels
rg -n "attachment_ids" packages/core/api/client.ts packages/core/channels
```

Issue/comment may still mention `attachment_ids` — OK.

- [ ] **Step 2: E2E** — open channel, paste/drop two images, type text between pastes, send; assert DOM order text then two images; optional request intercept shows `parts` with two attachments.

- [ ] **Step 3: `pnpm typecheck` / targeted vitest / `cd server && go test` for touched packages; `pnpm react:doctor` if FE changed.

- [ ] **Step 4: Commit**

```bash
git commit -m "test(channels): e2e tray attachments and remove dead chat bind paths"
```

---

## Spec coverage checklist

| Spec requirement | Task |
|------------------|------|
| `MessagePart` attachment type | 1 |
| Normalize + empty content for file-only | 1–2 |
| Bind from parts only | 2 |
| FE types/API | 3 |
| Composer tray / external media | 4–5 |
| Bubble body → zone | 6 |
| Agent symmetry | 7 |
| Delete old chat paths; no dual-write | 5, 8 |
| No history migration | (explicit non-work) |
| No 400 theater | 2 notes — bind from parts, delete clients |
| Issue inline unchanged | 4 default inline; grep gate in 8 |

## Placeholder / consistency self-check

- Types use `attachment_id` (snake) on the wire, matching Go JSON tags.
- Hook name `useComposerPendingAttachments` / component `ComposerAttachmentTray` / `MessageAttachmentZone` used consistently across tasks.
- No TBD steps left.

---

## Execution handoff

Plan saved to:

`docs/superpowers/plans/2026-07-13-chat-attachment-parts-slack.md`

(in worktree `docs/chat-attachment-parts-slack-design` / path under `.worktrees/chat-attachment-parts-design/`)

**How to implement (this repo):**

1. Worktree + env symlinks (`using-git-worktrees` / project `make worktree-env`).
2. Drive tasks with **ultracode Workflow** (not superpowers `executing-plans` / `subagent-driven-development` — project CLAUDE.md override).
3. TDD inside each task; commit per task; update skills in the same change as CLI behavior.

When you want to start implementation, say so and which task to begin with (default: Task 1).
