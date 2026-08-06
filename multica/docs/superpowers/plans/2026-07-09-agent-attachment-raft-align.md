# Agent Attachment Raft Align — Implementation Plan

> **For agentic workers:** Execute task-by-task with TDD. Prefer worktree isolation
> (`using-git-worktrees`) before code changes. Steps use checkbox (`- [ ]`) syntax.

**Goal:** Hard-migrate Multica agent CLI attachment surface to Raft-style
`view` / `upload` / `--attachment-id` only; rewrite daemon teaching strings; leave
product HTTP/markdown contracts unchanged.

**Architecture:** CLI-only + teaching-surface change. Download becomes
`attachment view` with required file `--output`. Upload becomes first-class
`attachment upload` reusing `POST /api/upload-file` (optional `channel_id` after
local target resolve). Message/issue/comment stop accepting local path
`--attachment`; they only accept `--attachment-id`. No aliases.

**Tech Stack:** Go CLI (`server/cmd/multica`), `server/internal/cli` API client,
daemon prompt/runtime_config strings, docs mdx. Tests: `go test` under
`server/cmd/multica` and `server/internal/daemon/...`.

**Design:** `docs/superpowers/specs/2026-07-09-agent-attachment-raft-align-design.md`

## Global Constraints

- Hard cut: no `attachment download`, no path `--attachment` flags, no dual teaching.
- Do **not** rename `GET /api/attachments/{id}/download` or change web upload.
- No `attachment comments`.
- English-only code comments; conventional commits (`feat(cli)`, `fix(cli)`, `docs`, `test(cli)`).
- Do not reformat unrelated gofmt-drift files.
- When changing CLI flags taught in builtin skills, update `SKILL.md` + source-map in same PR (grep first; none expected at plan time).

## File map

| File | Responsibility |
|------|----------------|
| `server/internal/cli/client.go` | Upload helper: optional `channel_id`; structured upload result |
| `server/internal/cli/client_test.go` | Upload multipart field tests |
| `server/cmd/multica/cmd_attachment.go` | `view` + `upload`; delete `download` |
| `server/cmd/multica/cmd_attachment_test.go` | Flag validation + command registration tests (create) |
| `server/cmd/multica/cmd_message.go` | `--attachment-id` only; drop path upload |
| `server/cmd/multica/cmd_issue.go` | Drop path `--attachment` + `uploadCLIAttachments` call sites; keep `--attachment-id` |
| `server/internal/daemon/prompt.go` + tests | Teach `view` |
| `server/internal/daemon/execenv/runtime_config.go` + tests | Teach view/upload/id protocol |
| `server/internal/daemon/types.go`, `handler/daemon.go` comments | Wording only |
| `apps/docs/content/docs/cli.mdx` (+ zh/ja/ko) | Document new commands |

---

### Task 1: API client upload options (`channel_id`)

**Files:**
- Modify: `server/internal/cli/client.go`
- Modify: `server/internal/cli/client_test.go`

**Interfaces:**
- Produces:
  ```go
  type UploadFileOptions struct {
      IssueID   string // optional
      ChannelID string // optional UUID
  }

  // UploadFileOpts uploads multipart to /api/upload-file and returns attachment id.
  func (c *APIClient) UploadFileOpts(ctx context.Context, fileData []byte, filename string, opts UploadFileOptions) (string, error)

  // Prefer migrating call sites to UploadFileOpts. Keep UploadFile(ctx, data, filename, issueID)
  // as a thin wrapper: UploadFileOpts(..., UploadFileOptions{IssueID: issueID}) for one commit
  // if many call sites; delete wrapper once CLI no longer needs issueID path helper from send.
  ```

- [ ] **Step 1: Write failing test** for multipart body including `channel_id`

In `client_test.go`, add a httptest server that asserts form fields:

```go
func TestUploadFileOptsWritesChannelID(t *testing.T) {
	// httptest: require form value channel_id == "chan-uuid", file present
	// client.UploadFileOpts(ctx, []byte("hi"), "a.txt", UploadFileOptions{ChannelID: "chan-uuid"})
	// expect id from JSON {"id":"..."}
}
```

- [ ] **Step 2: Run test — expect fail** (symbol missing)

```bash
cd server && go test ./internal/cli/ -run TestUploadFileOptsWritesChannelID -count=1
```

- [ ] **Step 3: Implement `UploadFileOpts` + thin `UploadFile` wrapper**

Write `channel_id` / `issue_id` form fields when non-empty. Reuse existing timeout/error handling from `UploadFile`.

- [ ] **Step 4: Run tests — pass**

```bash
cd server && go test ./internal/cli/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/internal/cli/client.go server/internal/cli/client_test.go
git commit -m "feat(cli): upload helper accepts channel_id"
```

---

### Task 2: `attachment view` (replace `download`)

**Files:**
- Rewrite: `server/cmd/multica/cmd_attachment.go`
- Create: `server/cmd/multica/cmd_attachment_test.go`

**Interfaces:**
- Produces CLI:
  - `multica attachment view [attachmentId] --output <path>`
  - `multica attachment view --id <attachmentId> --output <path>`
- Consumes: `GET /api/attachments/{id}` → `download_url` → `client.DownloadFile` (existing)

- [ ] **Step 1: Failing tests for validation**

```go
// TestAttachmentViewRequiresOutput
// TestAttachmentViewRejectsBothPositionalAndID
// TestAttachmentViewRequiresID
// Register: attachmentCmd has "view", does not have "download"
```

Use cobra command construction patterns from `cmd_issue_test.go` / `cmd_compat_test.go`
(construct root or attach flags and call `RunE` with mocked client if available; if pure
flag validation is easier, test helper `validateAttachmentViewArgs` extracted for purity).

Prefer extracting pure helpers for TDD:

```go
func resolveAttachmentViewID(positional string, flagID string) (string, error)
// error if both or neither

func requireOutputPath(output string) (string, error)
// error if empty
```

- [ ] **Step 2: Run — fail**

```bash
cd server && go test ./cmd/multica/ -run 'TestAttachmentView|TestAttachmentCmd' -count=1
```

- [ ] **Step 3: Implement `view`; delete `download`**

`runAttachmentView`:
1. Resolve id (positional xor `--id`)
2. Require `--output` file path
3. `GetJSON /api/attachments/{id}`
4. `DownloadFile` on `download_url` (relative URLs already handled)
5. `os.WriteFile(output, data, 0o644)` — do **not** join with server filename
6. stderr `Downloaded: <abs>`; stdout JSON `{id, filename, path, size}`

Remove `attachmentDownloadCmd` and `--output-dir`.

- [ ] **Step 4: Tests pass**

```bash
cd server && go test ./cmd/multica/ -run 'TestAttachment' -count=1
```

- [ ] **Step 5: Commit**

```bash
git add server/cmd/multica/cmd_attachment.go server/cmd/multica/cmd_attachment_test.go
git commit -m "feat(cli): attachment view replaces download"
```

---

### Task 3: `attachment upload`

**Files:**
- Modify: `server/cmd/multica/cmd_attachment.go`
- Modify: `server/cmd/multica/cmd_attachment_test.go`

**Interfaces:**
- CLI: `multica attachment upload --path <filepath> [--target <target>]`
- Target resolve (client-side, V1):
  - empty target → unbound upload (`UploadFileOpts` with empty ChannelID)
  - `#name` or `name` → `GET /api/channels`, match `name` case-sensitive first then case-insensitive; ambiguous/missing → error
  - raw UUID → use as `channel_id`
  - `dm:@…` / thread forms `#ch:msg` → **error with message**: use unbound upload then `message send --target … --attachment-id` (send-time `LinkOwnedAttachmentsToChannelMessage` already handles bind). Do not half-implement DM resolve in V1 unless an existing helper is trivial to reuse.

- [ ] **Step 1: Failing tests**

```go
// TestAttachmentUploadRequiresPath
// TestResolveChannelIDFromTarget_HashName
// TestResolveChannelIDFromTarget_UUID
// TestResolveChannelIDFromTarget_DMRejectedClearError
```

- [ ] **Step 2: Run — fail**

```bash
cd server && go test ./cmd/multica/ -run 'TestAttachmentUpload|TestResolveChannelID' -count=1
```

- [ ] **Step 3: Implement upload command**

```go
// runAttachmentUpload:
// - stat --path: must exist, regular file, size > 0
// - resolve channel id from --target if set
// - read file; UploadFileOpts(..., UploadFileOptions{ChannelID: channelID})
// - stderr tip: use message send --attachment-id
// - stdout JSON {id, filename, path, channel_id?}
```

Skip `--mime-type` in V1 (server sniffs; form field not supported today). Spec optional flag can land later without breaking hard-cut.

- [ ] **Step 4: Tests pass + commit**

```bash
cd server && go test ./cmd/multica/ -run 'TestAttachment' -count=1
git add server/cmd/multica/cmd_attachment.go server/cmd/multica/cmd_attachment_test.go
git commit -m "feat(cli): attachment upload with optional channel target"
```

---

### Task 4: Message send — `--attachment-id` only

**Files:**
- Modify: `server/cmd/multica/cmd_message.go`
- Modify: any message send tests if present (`cmd_message` tests or compat)

**Interfaces:**
- Remove `StringSlice("attachment", …)`
- Add `StringSlice("attachment-id", nil, "Attachment id to link (repeatable). Get one from multica attachment upload.")`
- Body: `attachment_ids` from flag values (no local upload)
- Required content check: sticker OR message text OR len(attachmentIDs)>0

- [ ] **Step 1: Update / add test that send body includes attachment_ids without calling upload**

Mirror `TestRunIssueCreateSendsExistingAttachmentIDs` pattern if message has httptest harness; else flag registration test + manual body construction unit if `RunE` is hard to mock.

- [ ] **Step 2: Implement message send changes**

Delete `uploadCLIAttachments` usage from message path. Keep longer timeout only if needed for network send (optional; can use default API timeout).

- [ ] **Step 3: Test + commit**

```bash
cd server && go test ./cmd/multica/ -count=1
git add server/cmd/multica/cmd_message.go
git commit -m "feat(cli): message send accepts only --attachment-id"
```

---

### Task 5: Issue create / comment — drop path `--attachment`

**Files:**
- Modify: `server/cmd/multica/cmd_issue.go`
- Modify: `server/cmd/multica/cmd_issue_test.go`

**Interfaces:**
- Remove `issueCreateCmd` / `issueCommentAddCmd` flags `StringSlice("attachment", …)`
- Keep `--attachment-id`
- Remove pre-create local file read/upload loops that used path attachments
- Delete `uploadCLIAttachments` if unused after Task 4–5 (and any URL-skip helpers only used by it)

- [ ] **Step 1: Adjust tests** that set `--attachment` paths; keep `--attachment-id` tests green

- [ ] **Step 2: Implement removal** of path attachment branches in `runIssueCreate` / comment add

- [ ] **Step 3: Test + commit**

```bash
cd server && go test ./cmd/multica/ -count=1
git add server/cmd/multica/cmd_issue.go server/cmd/multica/cmd_issue_test.go
git commit -m "feat(cli): issue create/comment use attachment-id only"
```

---

### Task 6: Daemon teaching surface hard cut

**Files:**
- Modify: `server/internal/daemon/prompt.go`
- Modify: `server/internal/daemon/prompt_test.go`
- Modify: `server/internal/daemon/execenv/runtime_config.go`
- Modify: `server/internal/daemon/execenv/runtime_config_test.go`
- Modify: `server/internal/daemon/execenv/execenv_test.go` (string asserts)
- Modify: `server/internal/daemon/types.go` (comments)
- Modify: `server/internal/handler/daemon.go` (comments only)
- Modify: `server/internal/handler/agent.go` / `chat.go` comments if they mention `download`

Replacement strings (use consistently):

```
multica attachment view --id <id> --output <path>
multica attachment upload --path <filepath> [--target '#channel']
multica message send --attachment-id <id>
```

Issue create teaching: remove `--attachment <path>`; document upload then `--attachment-id`.

For chat attachment block in `prompt.go`, replace:

```
Use `multica attachment download <id>` ...
```

with:

```
Use `multica attachment view --id <id> --output <path>` to fetch each file locally before referring to it.
```

- [ ] **Step 1: Update tests first to expect new substrings (they fail on old code)**

- [ ] **Step 2: Update production strings**

- [ ] **Step 3: Grep gate**

```bash
rg -n 'attachment download|message send --attachment |issue create.*--attachment ' \
  server/cmd/multica server/internal/daemon server/internal/handler \
  --glob '*.go' || true
# Expect only historical comments in design docs outside these trees, or zero hits in teaching paths
```

- [ ] **Step 4: Test + commit**

```bash
cd server && go test ./internal/daemon/... ./cmd/multica/ -count=1
git add server/internal/daemon server/internal/handler/daemon.go
git commit -m "feat(daemon): teach raft-aligned attachment view/upload"
```

---

### Task 7: Public CLI docs

**Files:**
- Modify: `apps/docs/content/docs/cli.mdx`
- Modify: `apps/docs/content/docs/cli.zh.mdx`
- Modify: `apps/docs/content/docs/cli.ja.mdx`
- Modify: `apps/docs/content/docs/cli.ko.mdx`
- Modify: `docs/docs-outline.md` if it lists `attachment download`

Replace download row with:

| Command | Description |
|---------|-------------|
| `multica attachment view --id <id> --output <path>` | Download attachment bytes to a local file path |
| `multica attachment upload --path <file> [--target '#channel']` | Upload a file; use returned id with `--attachment-id` |

- [ ] **Step 1: Edit four locales + outline**
- [ ] **Step 2: Commit**

```bash
git add apps/docs/content/docs/cli.mdx apps/docs/content/docs/cli.zh.mdx \
  apps/docs/content/docs/cli.ja.mdx apps/docs/content/docs/cli.ko.mdx docs/docs-outline.md
git commit -m "docs(cli): document attachment view and upload"
```

---

### Task 8: Verification + grep gate

- [ ] **Step 1: Full targeted Go tests**

```bash
cd server && go test ./cmd/multica/ ./internal/cli/ ./internal/daemon/... -count=1
```

- [ ] **Step 2: Repo grep for regressions**

```bash
rg -n 'attachment download' server/cmd/multica server/internal/daemon apps/docs/content/docs/cli*.mdx
rg -n 'Flags\(\)\.StringSlice\("attachment"' server/cmd/multica
# both should be empty
```

- [ ] **Step 3: Manual smoke (if local server available)**

```bash
# view
multica attachment view --id <real-id> --output /tmp/multica-att-test.bin
# upload + send (agent task context)
multica attachment upload --path /tmp/multica-att-test.bin --target '#<channel>'
multica message send --target '#<channel>' --attachment-id <id> --message 'attachment smoke'
```

- [ ] **Step 4: Final commit only if fixes needed; else done**

---

## Spec coverage checklist

| Spec requirement | Task |
|------------------|------|
| `attachment view` + `--output` file path | T2 |
| Delete `download` | T2 |
| `attachment upload --path [--target]` | T3 |
| `message send --attachment-id` only | T4 |
| Issue/comment path `--attachment` removed | T5 |
| Daemon prompts/runtime_config hard cut | T6 |
| Docs updated | T7 |
| No HTTP markdown path change | (explicit non-goal; no task) |
| No comments command | (non-goal) |

## Execution notes

- Worktree: create from `origin/dev` (or current integration base) before coding; symlink env per project local instructions.
- Project Claude.md: prefer ultracode Workflow for multi-task execution if available; otherwise subagent-driven or inline execution of this plan.
- Do not run full `make check` unless user asks; use targeted `go test` per task.
