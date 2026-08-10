---
name: multica-working-on-issues
description: "Use when working on a Multica issue after the runtime has provided the trigger context — to apply the product contracts the runtime brief does not encode: how PR linking differs from close intent, how to read a linked PR's real state via the pull-requests CLI, which metadata keys are high-signal, what status changes trigger on the server, and how sub-issue create status (todo vs backlog) controls whether assigned agents start immediately."
user-invocable: false
allowed-tools: Bash(multica *), Bash(git *), Bash(gh *)
---

# Working on Multica issues

Product contracts the runtime brief does not fully encode: PR linking vs close
intent, reading linked-PR state, metadata keys, status side effects, and
sub-issue enqueue behavior.

For building mention links, load `multica-mentioning` instead — not this skill.

Every contract below is traced to source in
`references/working-on-issues-source-map.md`.

## Choose the lightest coordination layer

The assignment runtime requires one decision before substantive execution:

- `DIRECT`: do the work in the current Issue when it is tightly coupled and can
  be completed coherently in one bounded context.
- `ISSUE_DAG`: for bounded parallel or staged work, atomically create child
  Issues with `multica issue decompose <issue-id> --plan-file <path>
  --idempotency-key <uuid>`. Use this for ordinary development, review, or a
  one-off multi-source investigation. It creates no Work Graph.
- `GOAL_GRAPH`: only while an explicit active channel Goal exists, and only for
  a manager/coordinator. Use `multica issue graph create` with
  `anchor_kind=channel_goal` when the work needs repeated evidence-driven
  replanning, independent verification, epochs, or a long-running loop.

Do not use task length by itself as the trigger. A greeting, one tool call, or a
small edit stays direct. An Issue DAG declares `depends_on`, starts all roots,
and promotes downstream Issues after all prerequisites become reviewable. The
same Agent may own multiple roots: they use independent Issue sessions and
execution roots and can run in parallel up to its concurrency cap.

Use `worker_mode: derived_agent` only when the node needs strong identity or
memory isolation: independent candidate implementations, blind/adversarial
  review, replication, or experiments
whose observations must not mutate the source Agent. A derived node requires a
concrete `clone_reason`. It copies approved configuration and skills plus a
point-in-time memory snapshot, inherits no credentials or sessions, writes no
memory back to the source automatically, and remains available for review
rework until its child Issue becomes `done` or `cancelled`, when it is archived.
Ordinary parallelism stays `reuse_agent` (the default).

Goal Graph nodes are stricter. A successful task or an Issue status is not
completion. Workers must register a durable artifact; verifier nodes must also
submit PASS evidence. Only kernel `effective_completion=satisfied` unlocks the
next frontier. Do not manually promote graph-managed Issues.
After delegating a bounded deliverable, the planner coordinates and integrates
it rather than implementing the same deliverable again.

Each plan node requires `temp_id`, `role`, and either an existing backlog
`issue_id` or `title` plus `assignee_id`. Optional fields include `description`,
`objective`, `completion_contract`, `context_policy`, `depends_on`, and `budget`.
The Goal Graph plan root supplies `anchor_kind: channel_goal`, the Goal ID as
`anchor_id`, `admission_decision: GRAPH`, `reason`, and optional `budget_policy`.

For a logically continuous Goal, operate bounded epochs:

```bash
multica issue graph epoch start <graph-id> --input-file epoch-contract.json
multica issue graph epoch finish <graph-id> <epoch-id> --input-file evaluation.json
```

Every epoch ends with a recorded decision such as `CONTINUE`, `WAIT`,
`ASK_HUMAN`, `REPLAN_NEW_AXIS`, `STOP_CONVERGED`, `STOP_NO_GAIN`, or
`STOP_BUDGET`. Never implement an unbounded process without epoch budgets and
stop decisions.

## PR linking and close intent are two distinct contracts

The GitHub webhook runs two separate scans over an incoming PR. They are not the
same gate and they read different fields.

**Linking** scans the PR **title, body, OR branch** for a routable issue key
(`PREFIX-NUMBER`, e.g. `MUL-2759`). Each match writes an issue ↔ PR link row.
This is the link that `multica issue pull-requests` reads back.

```text
MUL-2759: add built-in issue working skill        # title prefix → links
agent/matt/mul-2759-working-on-issues             # branch ref   → links
```

**Close intent** is stricter and is a separate scan over **title or body only —
never the branch**. It fires only for a key placed immediately after a closing
keyword (`Closes` / `Fixes` / `Resolves`, optional `:` then whitespace). That
adjacency is what sets the link row's close-intent flag, the gate that
auto-advances the issue to `done` when the PR merges.

```text
Closes MUL-2759                                    # links AND records close intent
Fixes MUL-2759
Resolves MUL-2759
Fix login MUL-2759                                 # links only — keyword not adjacent
```

Consequence: a bare title prefix or a branch reference links the PR but does not
close the issue on merge. A closing keyword immediately adjacent to the issue key
records close intent; on merge, that close intent can move the linked issue to
`done`.

### Default for code-changing issue work

When an issue run changes code in a checked-out GitHub repo, the default handoff
is to open or update a PR before posting the final Multica issue comment, unless
the user explicitly asked for a local-only change or no PR. This is a default, not
an unconditional command: if no code changed, say no PR is needed; if PR creation
is blocked by auth, failing tests, or missing remote state, report that blocker
instead of pretending the run is complete.

Use a routable issue key in the PR title, body, or branch so the webhook can link
the PR back to the issue. If the PR should close the issue on merge, put the key
immediately after a closing keyword in the title or body, for example:

```text
MUL-2759: fix login redirect        # links only
Closes MUL-2759                     # links and records close intent
```

In the final issue comment, include the PR URL when a PR exists. If the task did
not produce a PR because no code changed or the user asked not to create one, say
that explicitly.

## Reading a linked PR's real state

For a single read of the running agent's complete work queue, use the aggregate
command instead of repeating `list` plus one `pull-requests` call per issue:

```bash
multica issue mine --with-prs --with-gates --output json
```

The result keeps the normal issue-list envelope and nests canonical
`pull_requests` on each issue. `--with-gates` requires `--with-prs`; the server
bulk-folds every linked PR in the page, so this remains one CLI/API round-trip
rather than hiding an N+1 fan-out. Without `--with-prs`, `issue mine` is the
agent-only shorthand for the existing `issue list --mine` queue.

When a step depends on PR state, query Multica's link table — do not infer it
from branch names, GitHub search, memory, or `pr_url` metadata (which can be
stale).

```bash
multica issue pull-requests <issue-id> --output json
```

Returns `{"pull_requests": [...]}`. Each element exposes:

- `number`, `html_url`, `title`
- `state` — the PR lifecycle as a **single enum**, one of `merged`, `closed`,
  `draft`, `open`. There is no separate `draft` or `merged` boolean in the
  response; the server folds them into `state` (merged wins, then closed, then
  draft, else open).
- `merged_at` — non-null once merged; a second confirmation of `state: merged`.
- `mergeable_state` — mirrors GitHub (`clean` / `dirty` surfaced; other values
  round-trip as unknown).
- `checks_conclusion` — aggregated CI: `passed`, `failed`, `pending`, or `null`
  when no check suite has been observed. Backed by `checks_passed`,
  `checks_failed`, `checks_pending` counts.

So "is it merged?" is `state == "merged"` (or `merged_at != null`); "is it still
a draft?" is `state == "draft"`; CI status is `checks_conclusion`.

The default `issue pull-requests` table exposes that same value in its `GATE`
column. It prints `none` when `checks_conclusion` is null; this is not a second
gate model.

If the command returns no linked PRs after a PR was opened, the link scanner did
not observe a routable issue key in the PR title/body/branch.

## Metadata: high-signal keys only

Metadata is durable issue state. Reading metadata is safe. Writing a metadata key
is a state mutation and should be tied to an explicit task requirement to record
that state for later readers or runs.

High-signal keys (reuse these names so queries stay consistent):

- `pr_url`
- `pr_number`
- `pipeline_status`
- `deploy_url`
- `external_issue_url`
- `waiting_on`
- `blocked_reason`
- `decision`

Not metadata: logs, summaries, files touched, timestamps, attempt counts,
investigation notes. Those belong in the result comment.

```bash
multica issue metadata set <issue-id> --key pr_url --value <url>
multica issue metadata delete <issue-id> --key <stale-key>
```

`--value` is JSON-parsed by default (bool/number are sniffed); pass `--type
string|number|bool` to force a type.

## Project is optional ownership; source is discussion provenance

An issue may belong to a project, but it is not required to. Use `--project`
when the request names a project or the current workflow is explicitly scoped
to one. Without project context, leave the flag out; the issue remains visible
as an unprojected issue instead of guessing a destination.

```bash
multica issue create --title "..." --project <project-id-or-title>
multica issue update <issue-id> --project <project-id-or-title>
multica issue update <issue-id> --project ""   # clear the project
multica issue list --project <project-id-or-title>
```

The CLI resolves a project ID or title inside the active workspace before it
sends `project_id`. The server also rejects a project from another workspace on
create or update. A child created without an explicit project inherits its
parent's project.

Project ownership and discussion provenance are orthogonal. For an issue
created from chat, still pass `--source-channel` and `--source-message` so the
issue can link back and status changes can flow to the source discussion. Do
not use the source channel as the issue's project, and do not drop the source
anchor merely because `--project` is present.

For a group-associated issue without a single originating message, use
`--channel <group-id-or-name>`. The server keeps this as the same single
canonical discussion anchor. A group association and `--project` are
independent explicit Properties: do not expect either to be inferred from the
other. Inspect the selected group's optional `project_id` with `multica channel
list --output json`, then explicitly pass the project value you intend to set.
When both matter, pass both values.

Do not guess a group. Choose one you can actually see, then use
`multica issue channel <issue-id> <group-id-or-name>` to set or change an
existing issue's association, or `multica issue channel <issue-id> --clear` to
remove it. `multica issue get <id> --output json` exposes the current
`source_refs.channel`; project ownership remains independently visible on the
issue as `project_id`.

## Status changes have server side effects

A status change is not cosmetic — the server enqueues or skips agent work based
on it. These are the contracts, not advice:

- **`backlog`** parks an agent-assigned issue: the assignee is set but no task
  fires. Moving `backlog → todo` (or any non-done/non-cancelled status) enqueues
  the assigned agent then.
- **`in_review`** is an accepted issue status. Some workflows use it while a PR
  is open and awaiting review; moving to it is an explicit mutation.
- **`done`** on a child issue posts a system comment on its parent. If a PR
  carries close intent (`Closes MUL-XXXX`), it advances the issue to `done`
  itself on merge — you do not also need to flip it manually.
- **`cancelled`** stops outstanding work; treat it as a user-driven decision.

## Sub-issues: `todo` starts work now, `backlog` parks it

On an agent-assigned issue, create status decides whether the assignee fires
immediately. A non-backlog status (e.g. `todo`) enqueues the agent at create
time; `backlog` sets the assignee without triggering.

Parallel children — all start now:

```bash
multica issue create --title "..." --parent <issue-id> --assignee <agent> --status todo
```

Strictly serial children — park later steps, promote one at a time:

```bash
multica issue create --title "Step 2: ..." --parent <issue-id> --assignee <agent> --status backlog
multica issue status <child-id> todo   # promote when the previous step is truly done
```

Creating every serial step as `todo` enqueues the whole chain at once.

## WorkOwnerLease before code ownership work

Before pushing a branch / opening a PR / editing migrations for an issue, hold
an active executor `WorkOwnerLease`. Issue task enqueue auto-acquires one for
the assignee. If another agent already owns the issue, do not invent a second
canonical branch — wait, take over via an explicit handoff, or comment only.

```bash
multica work-lease acquire --issue-id <uuid> --role executor --canonical-branch agent/<you>/<issue>
multica work-lease list --issue-id <uuid>
```

PR descriptions should mention the lease id / canonical branch. Reviewer leases
may be multiple (`--role reviewer`) and must not change code.

## Database migrations require a MigrationLease

Never invent the next migration number by listing `server/migrations/` locally
or by taking `max(N)+1` from your checkout. Open PRs routinely collide on the
same number.

Before adding `server/migrations/<N>_*.sql`:

```bash
multica migration reserve --number <N> --filename <N>_your_change.up.sql --issue-id <uuid>
# or: multica migration reserve --filename <N>_your_change.up.sql
multica migration list
```

If reserve returns conflict, pick another free number and reserve again. Release
with `multica migration release --number <N>` when abandoning the change. CI
also rejects new migrations that reuse a number already on `dev` or collide with
another new migration on the same branch.

## Incorrect → correct

PR title (link the issue):

```text
Fix login redirect                  # incorrect — no issue key, won't link
MUL-2759: fix login redirect        # correct — links the PR
```

Serial sub-issues (don't start the whole chain):

```bash
# incorrect — both fire immediately
multica issue create --title "Step 2" --parent <issue-id> --assignee <agent> --status todo
multica issue create --title "Step 3" --parent <issue-id> --assignee <agent> --status todo

# correct — parked, promote in turn
multica issue create --title "Step 2" --parent <issue-id> --assignee <agent> --status backlog
multica issue create --title "Step 3" --parent <issue-id> --assignee <agent> --status backlog
```

## References

`references/working-on-issues-source-map.md` — accurate `file:line` for every
contract above: the `pull-requests` CLI and route, the PR response field list,
`derivePRState`, the two-path link (`extractIdentifiers`) vs close-intent
(`extractClosingIdentifiers`) proof, the backlog enqueue lines, child-done
notify, and the metadata CLI. Re-derive before depending on an exact line.
