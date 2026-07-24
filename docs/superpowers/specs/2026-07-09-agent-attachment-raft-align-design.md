# Agent attachment CLI — Raft-aligned hard migration

- Date: 2026-07-09
- Status: draft for review
- Source: agent attachment UX comparison vs `@botiverse/raft-daemon@0.72.2` / `raft` CLI; product decision to borrow Raft’s **agent file-tool surface** while keeping Multica’s **product attachment HTTP / markdown** contracts
- Scope boundary: **Agent CLI + daemon teaching surfaces only**. No rewrite of web/desktop/mobile upload paths or durable markdown download URLs.

## Problem

Raft’s agent-facing attachment surface is a small, predictable file protocol:

```bash
raft attachment view   --id <uuid> --output /tmp/a.png
raft attachment upload --path ./a.png --target '#channel'
raft message send --target '#channel' --attachment-id <uuid> --message '...'
```

Multica’s agent surface is inconsistent and harder for coding agents:

| Concern | Multica today | Raft |
|---------|---------------|------|
| Download | `attachment download <id> -o <dir>` (server picks filename) | `attachment view --id --output <filepath>` (agent picks path) |
| Upload | Buried in `message send --attachment <path>` / `issue … --attachment <path>` | First-class `attachment upload` → id |
| Link on send | Local path flags mixed with id flags | `--attachment-id` only on send |
| Teaching | `prompt.go` / `runtime_config.go` teach `download` | Prompts teach `view` / `upload` / `--attachment-id` |

Agents need a **stable path after download** and a **reusable attachment id after upload**. The reusable-id promise requires two product layers: `attachment` is the workspace file resource, while `channel_message_attachment` records every message reference to it. `channel_id` on the resource is upload provenance only. The same owned id may be sent to group, DM, thread, and multiple messages without re-uploading bytes. The CLI gap remains the id-first shape; the earlier singular `attachment.channel_message_id` model did not satisfy this promise and was removed by migration 224.

## Design principles

1. **Borrow Raft where it is better for agents**: fixed output path (`view`), id-first protocol (`upload` + `--attachment-id`).
2. **Keep Multica where it is better as a product**: metadata JSON, `markdown_url` vs expiring `download_url`, CloudFront/presign/proxy download modes, multi-surface ownership (issue / comment / chat / channel).
3. **Hard cut on agent CLI**: no dual names, no aliases for removed flags/commands. Deployed agent prompts and skills must move in the same change.
4. **Do not invent attachment-scoped comments**: Raft’s `attachment comments` has no Multica product counterpart; out of scope.

## Non-goals

- Renaming or removing `GET /api/attachments/{id}/download` (persisted in issue/comment markdown; web/desktop/mobile load it as a resource URL).
- Changing `POST /api/upload-file` for the frontend / mobile clients.
- Changing `GET /api/attachments/{id}` JSON metadata contract for UI consumers.
- Implementing `attachment comments`.
- Raft draft/`--send-draft` send flow.
- Compatibility aliases: no `download`, no `message send --attachment <path>`, no silent dual-flag acceptance.

## Final agent CLI surface

### `multica attachment view`

```bash
multica attachment view <attachmentId> --output <filepath>
multica attachment view --id <attachmentId> --output <filepath>
```

| Rule | Behavior |
|------|----------|
| id | Positional **or** `--id`, exactly one required; both → error |
| `--output` | **Required** local **file** path (not a directory) |
| Write | Download bytes to that path; parent dir must exist (fail closed) |
| stderr | `Downloaded: <absolute-path>` |
| stdout | JSON `{ "id", "filename", "path", "size" }` (Multica CLI machine-readable convention) |

**Delete** `multica attachment download` entirely (including `-o` / `--output-dir`).

Implementation note: may still use existing HTTP (metadata + follow `download_url` / `/download`, including redirects). External contract is one command with a fixed file path; callers must not be taught the two-step protocol.

### `multica attachment upload`

```bash
multica attachment upload --path <filepath> --target '#channel'
multica attachment upload --path <filepath> --target 'dm:@handle'
multica attachment upload --path <filepath> --target '#channel:<message-id>'
# optional
multica attachment upload --path <filepath> --target '#channel' --mime-type image/png
```

| Rule | Behavior |
|------|----------|
| `--path` | Required; must be a non-empty regular file |
| `--target` | Required for channel/DM/thread binding; same grammar as `message send --target` |
| `--mime-type` | Optional override; otherwise sniff + extension (same spirit as current upload) |
| Size | Enforce existing server max upload size; reject 0-byte files client-side |
| HTTP | `POST /api/upload-file` multipart: `file` + `channel_id` after target resolve |
| stdout | JSON including at least `id`, `filename`, `size` / `size_bytes`, and any useful urls the API already returns |
| stderr / text tip | Instruct: use `multica message send --attachment-id <id> …` |

**Target resolution:** Prefer resolve-then-upload-with-`channel_id` (server already accepts `channel_id` on upload and gates membership/writability). Do not rely on unbound upload + send-time link as the primary agent path (send-time link remains server capability for other clients).

**Issue/comment surfaces:** Agents that need issue-bound attachments use:

```bash
multica attachment upload --path f --target …   # or a documented issue bind if added later
multica issue create … --attachment-id <id>
multica issue comment add … --attachment-id <id>
```

If channel target is wrong for pure issue work, allow upload without channel by binding `issue_id` when a dedicated flag is needed — **only if** implementation discovers agents cannot use unbound upload + `--attachment-id` on issue create. Default preferred path: unbound or channel-bound upload is fine as long as `LinkAttachmentsToIssue` / existing issue create attachment_ids path works. Spec decision:

- **Channel / DM / thread:** `--target` required on `attachment upload`.
- **Issue / comment:** prefer `attachment upload` without channel when issue create accepts pre-uploaded ids; if upload currently requires workspace membership only (no channel), support:

  ```bash
  multica attachment upload --path <filepath>
  ```

  (no `--target`) for workspace-scoped unbound attachments that issue/comment flows link later.  
  **Hard rule:** either `--target` **or** no target (unbound workspace upload) — never a second parallel “path flag on send”.

### `multica message send` (and issue create / comment add)

| Remove | Keep / add |
|--------|------------|
| `--attachment <local-path>` (repeatable file paths) | `--attachment-id <id>` (repeatable) |
| Implicit upload inside send/create | Agents must call `attachment upload` first |

`message send` body continues to send `attachment_ids` to `/api/agent/messages/send` (already supported).

Same hard cut for:

- `multica issue create --attachment <path>` → only `--attachment-id`
- `multica issue comment add --attachment <path>` → only `--attachment-id`

### Out of scope commands

- `attachment comments` — no Multica model
- Any `download` alias

## HTTP / product contracts (unchanged)

| Endpoint | Role after this work |
|----------|----------------------|
| `POST /api/upload-file` | Unchanged; CLI upload uses it (with `channel_id` when target resolves) |
| `GET /api/attachments/{id}` | Unchanged JSON metadata for UI / optional CLI metadata |
| `GET /api/attachments/{id}/download` | Unchanged durable/browser download; still valid in markdown |
| `GET /api/attachments/{id}/content` | Unchanged text preview proxy |
| Frontend `packages/core` upload helpers | Unchanged |

No agent-only parallel HTTP surface is required for V1; CLI is the agent boundary.

## Daemon teaching surface (full rewrite, no dual track)

Update all agent-facing strings to the new commands only:

| Location | Change |
|----------|--------|
| `server/internal/daemon/prompt.go` | Teach `attachment view --id … --output …`; remove `download` |
| `server/internal/daemon/execenv/runtime_config.go` | Attachments section + issue/comment examples: upload + `--attachment-id` |
| `server/internal/daemon/types.go` comments | Reference `view` |
| Matching `*_test.go` | Assert new substrings only |
| `apps/docs/content/docs/cli.mdx` (+ zh/ja/ko) | Document `view` / `upload`; remove `download` |
| Builtin skills under `server/internal/service/builtin_skills/*` if they mention attachment download or `--attachment` paths | Update `SKILL.md` **and** `references/*-source-map.md` in the same PR |

Message history formatting that lists attachments for agents (if any Multica equivalent of Raft’s “use raft attachment view to download” suffix) should say `multica attachment view`.

## Migration / breakages (intentional)

| Old | New |
|-----|-----|
| `multica attachment download <id>` | `multica attachment view --id <id> --output <path>` |
| `multica attachment download <id> -o /tmp` | Must choose full file path in `--output` |
| `multica message send --attachment ./f` | `attachment upload` then `message send --attachment-id` |
| `multica issue create --attachment ./f` | `attachment upload` then `issue create --attachment-id` |
| `multica issue comment add --attachment ./f` | same |

No deprecation window. CI and unit tests that invoke old flags must be rewritten, not shimmed.

## Acceptance criteria

1. `multica attachment view --id <id> --output /tmp/x.bin` writes bytes and prints JSON with absolute `path`.
2. `multica attachment upload --path <file> --target '#<joined-channel>'` returns an id; `message send --attachment-id <id>` attaches it to a channel message.
3. `multica attachment download` is not a registered command.
4. `message send` / `issue create` / `issue comment add` reject or simply do not define `--attachment` path flags.
5. Daemon prompt + runtime_config + docs contain **zero** occurrences of `attachment download` and agent-taught `--attachment <path>` for local files.
6. Historical markdown images using `/api/attachments/<id>/download` still load in web/desktop/mobile (no HTTP path rename).
7. One uploaded attachment id can be sent to a group and then a DM (and reused in later messages); every response hydrates the same metadata and storage object, with one association per message and no re-upload.
8. Historical message parts that name an existing same-workspace resource are backfilled into associations; illegal or missing ids remain unavailable and never produce invented rows.

## Test plan

- Unit tests for CLI flag validation (`view` id mutual exclusion, missing `--output`, empty upload path, 0-byte upload).
- CLI integration or handler-level tests: upload with `channel_id`; send with `attachment_ids` (existing transport tests extended).
- Prompt/runtime_config string tests updated.
- No requirement to change Playwright markdown image e2e unless CLI e2e covers attachment commands (add focused Go/CLI tests first).

## Implementation sketch (for planning, not binding)

1. Extend `APIClient` upload helper to pass `channel_id` (and keep issue_id where needed for non-agent callers until their call sites migrate).
2. Rewrite `cmd_attachment.go`: `view` + `upload`; delete `download`.
3. Strip path `--attachment` from `cmd_message.go`, `cmd_issue.go`; ensure `--attachment-id` on message send.
4. Target resolve for upload: reuse agent transport / channel resolution patterns already used by `message send` if available from CLI; otherwise resolve via existing channel list/API used elsewhere in CLI.
5. Update daemon prompts, runtime_config, docs, skills, tests.
6. Grep gate in review: no remaining `attachment download` in server/cmd/multica or daemon teaching paths.

## Open questions (resolved)

| Question | Decision |
|----------|----------|
| Keep `download` as alias? | **No** |
| Keep `message --attachment <path>` sugar? | **No** |
| Align public HTTP to Raft paths? | **No** — product/markdown contracts stay |
| Implement `attachment comments`? | **No** |
| Scope | Agent CLI + teaching surfaces hard cut |

## Summary

Align Multica’s **agent attachment protocol** with Raft’s superior agent UX (`view` path control, first-class `upload`, id-only send), via a hard CLI migration. Leave Multica’s stronger **product attachment system** (durable download URLs, multi-surface ownership, CDN/proxy modes) intact under the CLI.
