# Beckham Project Context Audit

Date: 2026-07-21

## Goal

Review the project-scoped Beckham Ambient context shipped by PR #795 through the real user path: channel binding, Ambient scheduling, context assembly, Radar task claim, and action execution. Fix defects that a real user can encounter. Do not add product fallbacks for local test setup or incorrect invocation problems.

## Working rules

- Verify existing interfaces and data contracts before changing code.
- Separate product defects from local environment, test fixture, or command errors.
- Fix root causes on real user paths and add regression coverage at the affected seam.
- Record evidence, decision, change, and verification after each completed step.

## Step log

### Step 1 — Establish the review baseline

Status: complete

Evidence:

- PR #795 is merged into `dev`; all four GitHub CI checks passed.
- Local `dev` was fast-forwarded to `566d2df524e11bf19802ac96b016e241ce04160f`.
- Review work continues on `agent/beckham-context-audit` with a clean worktree.
- Existing dated engineering records live under `docs/superpowers/plans`, so this audit uses the same location.

Decision:

- Review the merged implementation rather than the obsolete feature branch.
- Use the channel-bound project as the authority throughout the path; do not trust model-copied identifiers where the server already owns the scope.

### Step 2 — Audit context assembly and action execution

Status: complete

Evidence:

- The Ambient/idle task JSON contains the authoritative channel-bound `project_id`, and the daemon revalidates that project against the task workspace before dispatch.
- `executeRadarCreateIssue` ignores that trusted task scope and uses only the model-produced `payload.project_id`. A missing value creates an unprojected issue; a different same-workspace value files it under the wrong project.
- `renderProjectContext` selects between chat and issue wording only. A Radar task therefore receives `This issue belongs to ...` despite having no issue. Quick-create and autopilot tasks have the same semantic defect.
- The Ambient work-node query filters by channel only. `ResolveSharedGroupChannel` chooses a channel from membership/manager state without considering a project, so an issue from another project can legitimately acquire the project channel as its primary channel and leak into the review.
- Recently completed issues label `issue.updated_at` as `completed_at`. The server already records the actual `status_changed -> done` time in `activity_log`; later issue edits make `updated_at` observably different from completion time.
- The most recent comment is selected without its type, author, or timestamp and is asserted to be `latest_progress` / `latest_result`. Comment rows may be `comment`, `progress_update`, `status_change`, or `system`, so those labels claim evidence the database does not provide.
- Structured JSON (`resource_ref`, acceptance criteria, dependencies) is cut at an arbitrary rune boundary by `trimAmbientContent`, producing invalid JSON for long real records.

Decision:

- Make persisted Radar task scope authoritative at action execution. Use it when the model omits `project_id`, reject a conflicting model value, and retain legacy payload behavior only when the task has no persisted project scope.
- Render task-kind-specific project wording instead of asserting that every non-chat task is an issue.
- Keep non-issue coordination nodes in the channel view, but exclude issue-backed nodes whose issue is outside the channel-bound project.
- Emit the actual completion activity time when available and state `last_updated_at` when only that value is known. Describe comment data as comment data and include its stored type/author/time.
- Bound structured values with a valid JSON truncation envelope instead of emitting malformed JSON.

### Step 3 — Implement contract fixes and regression tests

Status: complete

Changes:

- `executeRadarCreateIssue` now loads the linked Radar task, verifies its agent/run backpointers, and treats its `project_id` as authoritative. An omitted model value inherits the task project; a conflicting value is rejected before issue creation.
- The Ambient action schema no longer asks the model to copy `project_id`; it states that project scope is enforced by the server.
- Project context wording now distinguishes issue, chat, proactive Radar, quick-create, autopilot, and generic tasks.
- The channel work-node query retains channel-only coordination nodes while filtering issue-backed nodes to the bound project.
- Open/completed issue comments are emitted as `latest_comment` with stored type, author type/ID, and creation time.
- Completed issues use the most recent `status_changed -> done` activity time. If no such durable activity exists, the prompt emits the truthful `last_updated_at` label instead of inventing a completion time.
- Long structured values remain valid JSON through an explicit `{truncated, preview}` envelope.

Regression coverage:

- Missing model `project_id` inherits persisted project scope.
- Conflicting same-workspace `project_id` is rejected without creating an issue.
- A cross-project issue work node attached to the same channel is excluded, while a channel-only coordination node remains visible.
- A post-completion edit does not replace the actual completion time.
- Comment metadata and valid structured truncation are present.
- All task kinds render truthful project wording.

### Step 4 — Verify the implementation

Status: complete

Passed:

- Focused Radar prompt and runtime project-context unit tests.
- Focused Ambient markdown, structured JSON, and every `executeRadarCreateIssue` authorization/regression test.
- `go test ./... -run '^$'` for compilation of every Go package.
- `go vet ./internal/handler ./internal/radar ./internal/daemon/execenv`.
- `git diff --check`.

Environment-only failures:

- A full run of the three related packages reaches unrelated older tests whose local test database says migrations are applied while the corresponding schema is absent: `workspace_radar_run_scan`, `workspace_radar_state.change_version`, and `refresh_workspace_radar_time_signals` are missing.
- The changed and adjacent regression tests pass against the same database. No product fallback was added for the inconsistent local fixture.
- The first new project-scope test initially used an event cooldown rejected by the existing database constraint. Changing the test to the real `wendy_ambient:<channel>` form fixed it; product code was unchanged.

### Step 5 — Publish for review

Status: complete

Evidence:

- Fetched the remote before publishing and found five new `dev` commits. Reviewed their changed files and rebased without conflict onto `2504248ac` (`feat(#576): move project binding from composer to a group settings surface (#800)`). The server-side channel/project contract used here was unchanged.
- Re-ran all focused regressions and `go test ./... -run '^$'` after the rebase; all passed.
- Pushed `agent/beckham-context-audit` to `origin`.
- The GitHub connector returned `403 Resource not accessible by integration` on PR creation. The authenticated `gh` fallback created PR [#804](https://github.com/LRM-Teams/multica/pull/804) targeting `dev`; it was marked ready for review so Draft state does not block the user-managed merge.
