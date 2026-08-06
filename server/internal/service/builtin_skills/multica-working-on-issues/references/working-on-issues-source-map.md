# working-on-issues source map

Evidence layer for `SKILL.md`. Every contract the skill states is traced to a
current `file:line` here. Lines were re-derived against `feat/builtin-skills`
after the latest `main` merge; the prior skill cited pre-merge lines that have
since moved (see the "drifted" column). Re-confirm with the verification command
at the bottom before relying on an exact line.

## Work decomposition admission

| Behavior | Source |
| --- | --- |
| Assignment runs receive the `DIRECT / GRAPH / PROPOSE_GRAPH` decision boundary before substantive execution | `server/internal/daemon/execenv/runtime_config.go`, assignment-triggered branch and `Work Decomposition Gate` |
| Runtime advertises the shipped atomic graph CLI but not an unavailable `issue verify` command | `server/internal/daemon/execenv/runtime_config_test.go`, `TestAssignmentBriefIncludesWorkDecompositionGate` |
| Atomic graph CLI and stable idempotency key | `server/cmd/multica/cmd_issue_graph.go`; `server/internal/handler/agent_work_graph.go`; `server/internal/workgraph/runtime.go` |
| Canonical DAG validation, ready calculation and downstream invalidation | `server/internal/workgraph/runtime.go`; `server/internal/workgraph/runtime_test.go`; `server/internal/workgraph/runtime_postgres_test.go` |
| `todo` starts an agent-assigned child and `backlog` parks it | `server/internal/handler/issue.go`, `shouldEnqueueAgentTask`; detailed citations below |

The runtime Prompt makes the semantic admission decision. The canonical Work
Graph store owns dependency readiness and calls the existing Issue task service
only for newly ready nodes; prose dependencies are never authoritative.

## `multica issue pull-requests` — read PR links from Multica

| Behavior | File:line | Drifted from |
|---|---|---|
| CLI command `pull-requests <id>` (alias `prs`) | `server/cmd/multica/cmd_issue.go:110` | `:105` |
| `runIssuePullRequests` handler | `server/cmd/multica/cmd_issue.go:665` | `:507` |
| Calls `GET /api/issues/<id>/pull-requests` | `server/cmd/multica/cmd_issue.go:680` | `:522` |
| Default table `GATE` projection | `server/cmd/multica/cmd_issue.go:706,721` | new citation |
| API route registration | `server/cmd/server/router.go:860` | `:480` |
| Handler `ListPullRequestsForIssue` → `Queries.ListPullRequestsByIssue` | `server/internal/handler/github.go:590` | `:466` |
| Row → response mapper `issuePullRequestRowToResponse` | `server/internal/handler/github.go:155` | `:149` |

The CLI resolves the issue ref, GETs the endpoint, and (for `--output json`)
prints the raw `{"pull_requests": [...]}` body. Only `--output` is accepted; the
default `table` shows `NUMBER STATE GATE TITLE URL`; `GATE` is the canonical
`checks_conclusion`, with null rendered as `none`.

## `multica issue mine --with-prs --with-gates` — aggregate my work

| Behavior | File:line |
|---|---|
| Agent-only `issue mine` command | `server/cmd/multica/cmd_issue.go:96,560` |
| Adds `with_prs` / `with_gates` to the single issue-list request | `server/cmd/multica/cmd_issue.go:474` |
| Issue+PR table renderer | `server/cmd/multica/cmd_issue.go:574` |
| Opt-in list handler; default response stays unchanged | `server/internal/handler/issue.go:779` |
| Bulk fold into issue responses | `server/internal/handler/github.go:214` |
| Workspace-guarded current-head bulk query | `server/pkg/db/queries/github.sql:150` |

`--with-gates` requires `--with-prs`. The JSON result keeps the normal issue-list
envelope and nests canonical `pull_requests`; the server issues one bulk PR query
for the page rather than one query per issue.

## PR response shape

`GitHubPullRequestResponse` struct: `server/internal/handler/github.go:57`. JSON
fields the agent can read off each element of `pull_requests`:

- `number` (`json:"number"`, line 62)
- `html_url` (`json:"html_url"`, line 65)
- `title` (`json:"title"`, line 63)
- `state` (`json:"state"`, line 64) — the folded lifecycle enum (see below)
- `merged_at` (`json:"merged_at"`, line 69), `closed_at` (line 70)
- `mergeable_state` (`json:"mergeable_state"`, line 76) — mirrors GitHub; UI only
  surfaces `clean`/`dirty`, other values round-trip as unknown
- `checks_conclusion` (`json:"checks_conclusion"`, line 80) — aggregated
  `"passed"`/`"failed"`/`"pending"` or `null` (no observed suite)
- `checks_passed` / `checks_failed` / `checks_pending` (lines 84-86) — per-suite
  counts; `aggregateChecksConclusion` (line 250) folds them into
  `checks_conclusion`

There is **no** standalone `draft` or `merged` boolean in the response. The
PR lifecycle is encoded in the single `state` string by `derivePRState`
(`server/internal/handler/github.go:994`):

```
merged   → if PullRequest.Merged
closed   → else if PullRequest.State == "closed"
draft    → else if PullRequest.Draft
open     → otherwise
```

`derivePRState` is called when the webhook upserts the row
(`server/internal/handler/github.go:682`), so `state` is what the list endpoint
returns. "Is it merged?" = `state == "merged"` (or `merged_at != null`); "is it a
draft?" = `state == "draft"`. Combine with `checks_conclusion` for CI status.

## Two distinct webhook paths: link vs close-intent

Both run inside the `pull_request` webhook handler, gated by the workspace
auto-link flag (`workspaceAutoLinkPRsEnabled`, `github.go:1074`).

### Path 1 — link (title OR body OR branch)

- `extractIdentifiers` regex helper: `server/internal/handler/github.go:1028`
- driving regex `identifierRe` (`\b([a-z][a-z0-9]{1,9})-(\d+)\b`, case-insensitive):
  `server/internal/handler/github.go:490`
- call site: `server/internal/handler/github.go:727` —
  `extractIdentifiers(p.PullRequest.Title, p.PullRequest.Body, p.PullRequest.Head.Ref)`

Every `PREFIX-NUMBER` mention in **title, body, or branch** resolves to an issue
in the workspace and writes a link row (`LinkIssueToPullRequest`, ~`github.go:762`).
This is what `multica issue pull-requests` later reads back.

Drifted from the prior skill's `github.go:727` citation, which pointed at the old
call-site location for the link logic.

### Path 2 — close intent (title OR body only, keyword-adjacent)

- `extractClosingIdentifiers` regex helper: `server/internal/handler/github.go:1051`
- driving regex `closingIdentifierRe`
  (`\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?)[:\s]+([a-z][a-z0-9]{1,9})-(\d+)\b`):
  `server/internal/handler/github.go:501`
- call site: `server/internal/handler/github.go:736` —
  `extractClosingIdentifiers(p.PullRequest.Title, p.PullRequest.Body)` (no branch arg)

Only a `PREFIX-NUMBER` immediately after a closing keyword
(`Closes`/`Fixes`/`Resolves`, optional `:` then whitespace) sets the link row's
`close_intent` flag — the gate that auto-advances the issue to `done` on merge.
`Fix MUL-1` closes; `Fix login MUL-1` does not (adjacency). Branch names are
deliberately excluded (function doc, `github.go:1044-1050`): a branch like
`mul-1/fix-login` links but must never declare close intent.

Drifted from the prior skill's `github.go:736` citation.

Net: a bare title prefix (`MUL-2759: ...`) or a branch ref links only;
`Closes MUL-2759` links **and** records close intent.

## Status side effects (enqueue contracts)

| Behavior | File:line | Drifted from |
|---|---|---|
| Create-time: agent-assigned, non-backlog issue enqueues immediately | `server/internal/handler/issue.go:2263-2264` | new citation |
| `shouldEnqueueAgentTask` returns false for `backlog` (parking lot) | `server/internal/handler/issue.go:2644-2648` | new citation |
| Backlog → non-backlog (not done/cancelled) enqueues on update | `server/internal/handler/issue.go:2537-2540` | `:2523` |
| Same contract in batch update | `server/internal/handler/issue.go:3021-3024` | new citation |
| Child → `done` posts a system comment on the parent | `server/internal/handler/issue_child_done.go:51` (`notifyParentOfChildDone`; doc comment at `:15`) | func def `:51` |

Creation with `--status todo` (or any non-backlog status) on an agent-assigned
issue fires the agent immediately; `--status backlog` parks it with the assignee
set but no trigger. Promoting `backlog → todo` later fires it then (update path,
line 2537).

## Optional project ownership and source provenance

| Behavior | File:line |
|---|---|
| CLI `issue create --project` flag | `server/cmd/multica/cmd_issue.go:293` |
| CLI `issue create --channel` flag | `server/cmd/multica/cmd_issue.go:294` |
| CLI `issue channel` manages the existing issue association | `server/cmd/multica/cmd_issue.go` |
| CLI create resolves project ID/title and sends `project_id` | `server/cmd/multica/cmd_issue.go:714-720` |
| CLI update resolves/clears project | `server/cmd/multica/cmd_issue.go:842-853` |
| CLI list resolves project and sends `project_id` | `server/cmd/multica/cmd_issue.go:418-424` |
| HTTP create accepts nullable `project_id` | `server/internal/handler/issue.go:2000-2009` |
| Service validates project belongs to the issue workspace | `server/internal/service/issue.go:171-193` |
| Child without explicit project inherits the parent's project | `server/internal/service/issue.go:171-185` |
| Group association and project are independent explicit create properties; omitting project keeps it empty | `server/internal/handler/issue.go` |
| `PUT /api/issues/{id}/channel` keeps the canonical 1:1 anchor | `server/internal/handler/issue_source_anchor.go` |
| HTTP update validates project belongs to the issue workspace | `server/internal/handler/issue.go:2407-2424` |
| CLI source flags remain independent of `--project` | `server/cmd/multica/cmd_issue.go:680-731` |
| Create persists project and source anchor as separate fields | `server/internal/handler/issue.go:2189-2208` |

An omitted project stays NULL. Source provenance remains in
`issue_source_message`; it is not an issue-list ownership dimension.

## Metadata CLI

| Behavior | File:line |
|---|---|
| `multica issue metadata set <issue-id> --key --value [--type]` | `server/cmd/multica/cmd_issue_metadata.go:80,109-111` |
| `multica issue metadata delete <issue-id> --key` | `server/cmd/multica/cmd_issue_metadata.go:93,113` |
| API routes (PUT/DELETE `/metadata/{key}`) | `server/cmd/server/router.go:478-479` |

`--value` is JSON-parsed by default (bool/number sniff); `--type` forces
`string`/`number`/`bool`.

## Verification command

Re-derive any line above before depending on it:

```bash
cd server
grep -n 'pull-requests <id>'                 cmd/multica/cmd_issue.go
grep -n 'var issueMineCmd\|func runIssueMine\|func printIssueMineTable\|with_prs' cmd/multica/cmd_issue.go internal/handler/issue.go
grep -n 'ListPullRequestsForIssue'           cmd/server/router.go internal/handler/github.go
grep -n 'func issuePullRequestRowToResponse\|func attachPullRequestsToIssues\|type GitHubPullRequestResponse struct\|func derivePRState\|func extractIdentifiers\|func extractClosingIdentifiers\|closingIdentifierRe' internal/handler/github.go
grep -n 'ListPullRequestsByIssues' pkg/db/queries/github.sql
grep -n 'extractIdentifiers(\|extractClosingIdentifiers(\|derivePRState(' internal/handler/github.go
grep -n 'prevIssue.Status == "backlog"\|func (h \*Handler) shouldEnqueueAgentTask' internal/handler/issue.go
grep -n 'func notifyParentOfChildDone'       internal/handler/issue_child_done.go
grep -n 'Flags().String("project"\|body\["project_id"\]\|body\["source"\]' cmd/multica/cmd_issue.go
grep -n 'GetProjectInWorkspace\|SourceChannelID\|SourceMessageID' internal/handler/issue.go internal/service/issue.go
```
