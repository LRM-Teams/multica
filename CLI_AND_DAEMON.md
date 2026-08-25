# CLI and Computer (agent daemon) Guide

> **Computer generation:** the local execution surface is now one machine-wide
> **Computer** (`multica computer start/stop/restart/status/logs`), run as a
> detached resident — not a profile-scoped `daemon` and not an OS service. The
> hidden `multica daemon ...` commands are deprecated aliases that delegate to
> the same Computer. `multica setup self-host` (custom-origin CLI setup) is
> retired; setup connects the Computer through https://leagent.me.

The `multica` CLI connects your local machine to Multica. It handles authentication, workspace management, issue tracking, and runs the machine-wide Computer (resident) that executes AI tasks locally.

## Installation

### Install Script (macOS/Linux)

```bash
curl -fsSL https://cdn.leagent.me/computer/install.sh | bash
```

### Build from Source

```bash
git clone https://github.com/LRM-Teams/multica.git
cd multica
make build
mkdir -p "$HOME/.local/bin"
cp server/bin/multica "$HOME/.local/bin/multica"
export PATH="$HOME/.local/bin:$PATH"
```

`$HOME/.local/bin/multica` is the only supported macOS/Linux install target.
If `command -v multica` still resolves to an older path, put
`$HOME/.local/bin` first on `PATH` and have the machine owner remove the old
binary through the normal software-management process.

If a daemon was already started from that older path, it keeps the old
executable. Do not interrupt active agent work. When the affected profile is
idle, adopt the canonical binary explicitly and confirm the daemon-reported
version:

```bash
"$HOME/.local/bin/multica" daemon restart
"$HOME/.local/bin/multica" daemon status
"$HOME/.local/bin/multica" version

# Named profile:
"$HOME/.local/bin/multica" --profile staging daemon restart
"$HOME/.local/bin/multica" --profile staging daemon status
```

Adoption is complete when `daemon status` reports the same `Version` as the
canonical `multica version`.

### Computer Upgrade

```bash
multica computer upgrade
# Recovery only: install one exact immutable release
multica computer upgrade --target-version v0.5.0-rc.2
```

The command routes through a running Computer's owner-only local control
surface, so that resident owns download, verification, handoff, and convergence.
If no resident owns the machine, it installs the verified release into
VersionStore for the next Computer start. If resident ownership exists but the
control surface is unreachable, it fails closed with
`upgrade_service_unreachable` and leaves Active unchanged. There is no separate
top-level `multica update` command.

## Quick Start

```bash
# Connect this Computer to Multica Cloud (leagent.me): authenticate + start the resident Computer
multica setup /<workspace>
```

Or step by step:

```bash
# 1. Authenticate (opens browser for login)
multica login

# 2. Start the machine-wide resident Computer
multica computer start

# 3. Done — agents in your watched workspaces can now execute tasks on your machine
```

`multica login` automatically discovers all workspaces you belong to and adds them to the daemon watch list.

## Authentication

### Browser Login

```bash
multica login
```

Opens your browser for OAuth authentication, creates a 90-day personal access token, and auto-configures your workspaces.

### Token Login

```bash
multica login --token <mul_...>
```

Authenticate using a personal access token directly. Useful for headless environments. Pass `--token=` with an empty value to be prompted interactively (so the token never lands in shell history).

### Check Status

```bash
multica auth status
```

Shows your current server, user, and token validity.

### Logout

```bash
multica auth logout
```

Removes the stored authentication token.

## Agent Daemon

The daemon is the local agent runtime. It detects available AI CLIs on your machine, registers them with the Multica server, and executes tasks when agents are assigned work.

### Start

```bash
multica computer start
```

By default, the Computer runs detached in the background and logs to `~/.multica/daemon.log`.

To run in the foreground (useful for debugging):

```bash
multica computer start --foreground
```

### Stop

```bash
multica daemon stop
```

### Status

```bash
multica daemon status
multica daemon status --output json
```

Shows PID, uptime, detected agents, and watched workspaces.

### Logs

```bash
multica daemon logs              # Last 50 lines
multica daemon logs -f           # Follow (tail -f)
multica daemon logs -n 100       # Last 100 lines
```

### Supported Agents

The daemon auto-detects these AI CLIs on your PATH:

| CLI | Command | Description |
|-----|---------|-------------|
| [Claude Code](https://docs.anthropic.com/en/docs/claude-code) | `claude` | Anthropic's coding agent |
| [Codex](https://github.com/openai/codex) | `codex` | OpenAI's coding agent |
| OpenCode | `opencode` | Open-source coding agent |
| [Pi](https://pi.dev/) | `pi` | Pi coding agent |
| [Cursor Agent](https://cursor.com/) | `cursor-agent` | Cursor's headless coding agent |
| Kiro CLI | `kiro-cli` | Kiro ACP coding agent |
| [Grok](https://grok.com) | `grok` | xAI Grok coding agent |

You need at least one installed. The daemon registers each detected CLI as an available runtime.

### How It Works

1. On start, the daemon detects installed agent CLIs and registers a runtime for each agent in each watched workspace
2. It polls the server at a configurable interval (default: 2s) for claimed tasks
3. When a task arrives, it reuses the Agent's durable workspace, spawns the agent CLI with that directory as cwd, and streams results back
4. Heartbeats are sent periodically (default: 15s) so the server knows the daemon is alive
5. On shutdown, all runtimes are deregistered

### Configuration

Daemon behavior is configured via flags or environment variables:

| Setting | Flag | Env Variable | Default |
|---------|------|--------------|---------|
| Poll interval | `--poll-interval` | `MULTICA_DAEMON_POLL_INTERVAL` | `2s` |
| Heartbeat interval | `--heartbeat-interval` | `MULTICA_DAEMON_HEARTBEAT_INTERVAL` | `15s` |
| Agent timeout | `--agent-timeout` | `MULTICA_AGENT_TIMEOUT` | `0` (no cap; bounded by the watchdogs) |
| Codex semantic inactivity timeout | `--codex-semantic-inactivity-timeout` | `MULTICA_CODEX_SEMANTIC_INACTIVITY_TIMEOUT` | `10m` |
| Max concurrent tasks | `--max-concurrent-tasks` | `MULTICA_DAEMON_MAX_CONCURRENT_TASKS` | `20` |
| Daemon ID | `--daemon-id` | `MULTICA_DAEMON_ID` | hostname |
| Device name | `--device-name` | `MULTICA_DAEMON_DEVICE_NAME` | hostname |
| Runtime name | `--runtime-name` | `MULTICA_AGENT_RUNTIME_NAME` | `Local Agent` |
| Workspaces root | — | `MULTICA_WORKSPACES_ROOT` | `~/.multica/workspaces` |

Each Agent uses `<workspaces-root>/<workspace-id>/agents/<agent-id>/` as both its durable root and subprocess cwd. Multica does not create per-task workspaces or garbage-collect Agent workspace contents.

Agent-specific overrides:

| Variable | Description |
|----------|-------------|
| `MULTICA_CLAUDE_PATH` | Custom path to the `claude` binary |
| `MULTICA_CLAUDE_MODEL` | Override the Claude model used |
| `MULTICA_CLAUDE_ARGS` | Default extra arguments for Claude Code runs |
| `MULTICA_CODEX_PATH` | Custom path to the `codex` binary |
| `MULTICA_CODEX_MODEL` | Override the Codex model used |
| `MULTICA_CODEX_ARGS` | Default extra arguments for Codex runs |
| `MULTICA_OPENCODE_PATH` | Custom path to the `opencode` binary |
| `MULTICA_OPENCODE_MODEL` | Override the OpenCode model used |
| `MULTICA_PI_PATH` | Custom path to the `pi` binary |
| `MULTICA_PI_MODEL` | Override the Pi model used |
| `MULTICA_CURSOR_PATH` | Custom path to the `cursor-agent` binary |
| `MULTICA_CURSOR_MODEL` | Override the Cursor Agent model used |
| `MULTICA_KIRO_PATH` | Custom path to the `kiro-cli` binary |
| `MULTICA_KIRO_MODEL` | Override the Kiro model used |
| `MULTICA_GROK_PATH` | Custom path to the `grok` binary |
| `MULTICA_GROK_MODEL` | Override the Grok model used |

`MULTICA_CLAUDE_ARGS` and `MULTICA_CODEX_ARGS` are parsed with POSIX shellword quoting, so values such as `--model "gpt-5.1 codex" --sandbox read-only` are split like a shell command line. Agent arguments are applied in this order: hardcoded Multica defaults, daemon-wide env defaults, then per-agent `custom_args` from the task.

### Self-Hosted Server (retired CLI surface)

The `multica setup self-host` CLI surface is retired in the Computer generation
(see the top note); the resident Computer authenticates through https://leagent.me.

```bash
# (retired) this command is no longer exposed:
#   multica setup self-host
#   multica setup self-host --server-url https://api.example.com --app-url https://app.example.com
```

Or configure manually:

```bash
# Set URLs individually
multica config set server_url http://localhost:8080
multica config set app_url http://localhost:3000

# For production with TLS:
# multica config set server_url https://api.example.com
# multica config set app_url https://app.example.com

multica login
multica computer start
```

### Profiles (retired)

Per-profile daemons are retired with the machine-wide Computer (see the top note);
the Computer is no longer profile-scoped and does not accept `setup self-host --profile`.

```bash
# (retired) profile-scoped setup is no longer exposed:
#   multica setup self-host --profile staging --server-url https://...
#   multica daemon start --profile staging
```

Each profile gets its own config directory (`~/.multica/profiles/<name>/`), daemon state, health port, and workspace root.

## Workspaces

### Working with multiple workspaces

Every command runs against a single workspace. The CLI resolves which one in this order (highest priority first):

1. `--workspace-id <id>` flag on the command
2. `MULTICA_WORKSPACE_ID` environment variable
3. The default workspace stored in your current profile (set by `multica workspace switch` or `multica login`)

`multica workspace switch <id|slug>` is the day-to-day way to change the default workspace. For scripting and headless setups where you don't want any stored state, prefer the `--workspace-id` flag or the env variable. `multica config set workspace_id <id>` is the low-level equivalent of `switch` (it writes the same setting but skips the access check).

If you need full isolation between organizations or accounts — separate tokens, separate daemons, separate config dirs — use `--profile <name>` instead. Each profile keeps its own default workspace.

### List Workspaces

```bash
multica workspace list
multica workspace list --full-id
multica workspace list --output json
```

The current default workspace is marked with `*`. Table output shows short UUID prefixes — pass `--full-id` when you need the canonical UUIDs.

### Switch Default Workspace

```bash
multica workspace switch <workspace-id>
multica workspace switch <slug>
```

Verifies you have access to the workspace, then sets it as the default for the current profile. Subsequent commands without `--workspace-id` and `MULTICA_WORKSPACE_ID` target this workspace. Pair `--profile` if you want to change a non-default profile's workspace.

### Get Details

```bash
multica workspace get <workspace-id>
multica workspace get <workspace-id> --output json
```

Passing no `<workspace-id>` resolves to the current default workspace, so `multica workspace get` doubles as "what workspace am I on?".

### List Members

```bash
multica workspace member list <workspace-id>
```

## Issues

### List Issues

```bash
multica issue list
multica issue list --status in_progress
multica issue list --priority urgent --assignee "Agent Name"
multica issue list --assignee-id 5fb87ac7-23b5-4a7a-81fa-ed295a54545d
multica issue list --full-id
multica issue list --limit 20 --output json
```

Table output shows a routable issue `KEY` such as `MUL-123`; copy that key into follow-up commands like `issue get`, `issue comment list`, `issue status`, or `--parent`. Add `--full-id` when you need canonical UUIDs. Available filters: `--status`, `--priority`, `--assignee` / `--assignee-id`, `--project`, `--metadata`, `--limit`. Use `--assignee-id <uuid>` for unambiguous filtering when names overlap.

Use `--metadata key=value` (repeatable; combined with AND) to filter by per-issue metadata. The value is JSON-parsed: `true`/`false` become bool, numbers become numbers, anything else is a string. Wrap as `'"42"'` to force a string when the value would otherwise sniff as a number:

```bash
multica issue list --metadata pipeline_status=waiting_review
multica issue list --metadata pr_number=482 --metadata is_blocked=true
```

### Get Issue

```bash
multica issue get <id>
multica issue get <id> --output json
```

### Create Issue

```bash
multica issue create --title "Fix login bug" --description "..." --priority high --assignee "Lambda"
multica issue create --title "Fix login bug" --assignee-id 5fb87ac7-23b5-4a7a-81fa-ed295a54545d
```

Flags: `--title` (required), `--description`, `--status`, `--priority`, `--assignee` / `--assignee-id`, `--parent`, `--project`, `--due-date`. Pass `--assignee-id <uuid>` (mutually exclusive with `--assignee`) when scripting against the IDs returned by `multica workspace member list --output json` / `multica workspace info --agents --output json`.

### Update Issue

```bash
multica issue update <id> --title "New title" --priority urgent
```

### Assign Issue

```bash
multica issue assign <id> --to "Lambda"
multica issue assign <id> --to-id 5fb87ac7-23b5-4a7a-81fa-ed295a54545d
multica issue assign <id> --unassign
```

Pass `--to-id <uuid>` to assign by canonical UUID (mutually exclusive with `--to`); useful when names overlap across members and agents.

### Change Status

```bash
multica issue status <id> in_progress
```

Valid statuses: `backlog`, `todo`, `in_progress`, `in_review`, `done`, `blocked`, `cancelled`.

### Comments

```bash
# List comments — flat timeline, chronological. Hard cap of 2000 rows; on
# long-running issues prefer one of the thread-aware reads below to keep
# context windows tight.
multica issue comment list <issue-id>

# Single thread (root + every descendant). Anchor may be the root itself
# or any reply inside the thread — the server walks up to the root.
multica issue comment list <issue-id> --thread <comment-id>

# Single thread, capped to the N most recent replies. The thread root is
# always included (even with --tail 0), so an agent landing on a long
# thread keeps the "what is this about" context without dragging hundreds
# of replies into its prompt.
multica issue comment list <issue-id> --thread <comment-id> --tail 30

# Scroll older replies inside the same thread. --before / --before-id are
# the reply cursor that the previous response emitted on stderr as
# `Next reply cursor: --before <ts> --before-id <reply-id>`.
multica issue comment list <issue-id> --thread <comment-id> --tail 30 \
    --before <ts> --before-id <reply-id>

# Most recently active threads (root + every descendant), grouped by
# thread. Returns N complete conversational arcs, oldest-active first so
# the freshest thread sits closest to "now" in an agent prompt.
multica issue comment list <issue-id> --recent 20

# Scroll older threads. Under --recent, --before / --before-id are a
# THREAD cursor (thread last_activity_at + root id), emitted on stderr as
# `Next thread cursor: --before <ts> --before-id <root-id>`.
multica issue comment list <issue-id> --recent 20 \
    --before <ts> --before-id <root-id>

# Incremental polling. Combines with --thread or --recent; filters out
# replies created on or before <ts> from the page (the thread root is
# exempt so the agent always gets context).
multica issue comment list <issue-id> --thread <comment-id> --tail 30 \
    --since <RFC3339-timestamp>

# Add a comment
multica issue comment add <issue-id> --content "Looks good, merging now"

# Reply to a specific comment
multica issue comment add <issue-id> --parent <comment-id> --content "Thanks!"

# Delete a comment
multica issue comment delete <comment-id>
```

**`--before` / `--before-id` semantics depend on the paging mode**, by
design — same flag, different scope:

| Mode | What the cursor walks | stderr label |
| --- | --- | --- |
| `--recent N` | Older *threads* (last_activity_at, root_id) | `Next thread cursor` |
| `--thread <id> --tail N` | Older *replies* inside that thread (created_at, id) | `Next reply cursor` |

Outside those two modes (`--thread` without `--tail`, or no `--thread`
and no `--recent`) the cursor flags are rejected so they cannot silently
no-op. The server emits the cursor headers (`X-Multica-Next-Before` /
`X-Multica-Next-Before-Id`) only when an older page actually exists —
exact-boundary pages (e.g. `--tail 3` on a thread with exactly 3
replies) intentionally return no cursor so callers stop paginating.

When `--since` is combined with `--recent` or `--thread --tail`, the
server additionally suppresses the cursor once the cursor target itself
is older than `since`. Older pages walk strictly older rows, so they
cannot satisfy `> since` either — emitting a cursor there would just
hand back root-only pages until the caller reaches the start of the
thread / issue. Incremental polling stops at the first page whose
cursor target falls before the watermark.

### Metadata

Per-issue metadata is a small KV map agents use to track pipeline state (PR number, pipeline status, waiting_on, ...). Keys match `^[a-zA-Z_][a-zA-Z0-9_.-]{0,63}$`, values are primitives (string / number / bool), max 50 keys per issue, blob capped at 8KB.

The bar for writing is high: pin a value only when it is materially important to the issue AND likely to be re-read by future runs on this same issue (the PR URL, the deploy URL, what we're blocked on). Most runs write zero new keys — that's the expected case. Don't pin runtime bookkeeping like `attempts`, single-run investigation notes, large logs, secrets/tokens, or description/comment copies — see the agent runtime prompt for the full anti-pattern list.

```bash
# List every key on an issue
multica issue metadata list <issue-id>

# Read a single key
multica issue metadata get <issue-id> --key pipeline_status

# Write a single key — value auto-typed (true/false → bool, numbers → number, else string)
multica issue metadata set <issue-id> --key pipeline_status --value waiting_review
multica issue metadata set <issue-id> --key pr_number --value 482
multica issue metadata set <issue-id> --key is_blocked --value true

# Force a specific type when sniffing would pick the wrong one
multica issue metadata set <issue-id> --key code --value 42 --type string

# Remove a key
multica issue metadata delete <issue-id> --key pipeline_status
```

All writes are single-key atomic — concurrent agents writing different keys do not lose each other's updates. To query, use `multica issue list --metadata key=value` (see *List Issues* above).

### Subscribers

```bash
# List subscribers of an issue
multica issue subscriber list <issue-id>

# Subscribe yourself to an issue
multica issue subscriber add <issue-id>

# Subscribe another member or agent by name
multica issue subscriber add <issue-id> --user "Lambda"

# Unsubscribe yourself
multica issue subscriber remove <issue-id>

# Unsubscribe another member or agent
multica issue subscriber remove <issue-id> --user "Lambda"
```

Subscribers receive notifications about issue activity (new comments, status changes, etc.). Without `--user`, the command acts on the caller.

## Direct Messages

```bash
# Agent task only: proactively DM an explicit human workspace handle.
multica message send --target dm:@maozh2 --message "Welcome to the workspace!"
multica message send --target dm:@liudh16 --message-stdin <<'MSG'
Welcome to the workspace!
MSG
```

`multica message send --target dm:@<human-handle>` is intentionally agent-only. It must run inside a claimed agent task, where the daemon injects the short-lived task credential as `MULTICA_TOKEN` plus `MULTICA_AGENT_ID` and `MULTICA_TASK_ID`. The human handle is required: there is no task-initiator or owner fallback. Unknown and agent handles are rejected. Agent-to-agent DMs are not supported — use a channel and @mention the other agent. Only treat the DM as sent after the command exits 0 and its JSON response contains `message.id`; a freshness-held result is not delivery.

### Execution History

```bash
# List all execution runs for an issue
multica issue runs <issue-id>
multica issue runs <issue-id> --full-id
multica issue runs <issue-id> --output json

# View messages for a specific execution run
multica issue run-messages <task-id>
multica issue run-messages <short-task-id> --issue <issue-id>
multica issue run-messages <task-id> --output json

# Incremental fetch (only messages after a given sequence number)
multica issue run-messages <task-id> --since 42 --output json
```

The `runs` command shows all past and current executions for an issue, including running tasks. Table output uses short task UUID prefixes by default; pass `--full-id` to print canonical task UUIDs. The `run-messages` command accepts full task UUIDs directly; copied short task prefixes must be scoped with `--issue <issue-id>` so the CLI only checks that issue's runs. It shows the detailed message log (tool calls, thinking, text, errors) for a single run. Use `--since` for efficient polling of in-progress runs.

## Projects

Projects group related issues (e.g. a sprint, an epic, a workstream). Inspect
projects and their bound resources through the unified workspace snapshot:

```bash
multica workspace info --projects
multica workspace info --projects --output json
```

The legacy `multica project` command is not available.

### Adaptive channel goals

Goal Mode is opt-in per group channel. Greetings, pure questions, and trivial
one-step asks do not create a goal. When a human states a channel-level overall
goal/outcome (or explicitly asks the manager to set/start goal mode), the
channel manager should create one sustained goal if none exists:

```bash
multica goal create --channel '#launch' \
  --title 'Ship adaptive goals' \
  --objective 'Keep long-running channel work aligned across wakes' \
  --criterion 'The current goal is visible in the channel' \
  --criterion 'Agents recover the latest goal after resume'
```

Every participant can read the current goal. An agent executing a channel task
can persist a versioned checkpoint:

```bash
multica goal get --channel '#launch'
multica goal checkpoint --channel '#launch' \
  --expected-version 3 \
  --progress 'Backend and UI are connected' \
  --current-step 'Run final acceptance checks' \
  --evidence 'test:TestChannelGoalLifecycleAndCompletionGate' \
  --completed-criterion 'The current goal is visible in the channel'
```

Only an agent with channel role `manager` may revise intent or lifecycle:

```bash
multica goal update --channel '#launch' --expected-version 4 --status paused
multica goal update --channel '#launch' --expected-version 5 --status active
```

Goal writes use optimistic concurrency. On a stale-version conflict, read the
goal again and reconcile before retrying. Completing a goal is rejected until
every current success criterion is present in `completed_criteria`. Paused,
completed, and cancelled goals are not injected into later agent turns.

### Associating Issues with Projects

Use the `--project` flag on `issue create` / `issue update` to attach an issue to a
project, or on `issue list` to filter issues by project:

```bash
multica issue create --title "Login bug" --project <project-id>
multica issue create --title "Channel follow-up" --channel <group-id-or-name> --project <project-id>
multica issue update <issue-id> --project <project-id>
multica issue list --project <project-id>
```

## Setup

```bash
# Connect this Computer to Multica Cloud: authenticate + start the resident Computer
multica setup /<workspace>

# (retired) the `setup self-host` / custom-origin CLI surface is no longer exposed,
# so these are not valid commands in the Computer generation:
#   multica setup self-host
#   multica setup self-host --port 9090 --frontend-port 4000
#   multica setup self-host --server-url https://api.example.com --app-url https://app.example.com
```

`multica setup /<workspace>` connects this Computer to Multica Cloud (leagent.me): it authenticates and starts the resident Computer in one step. The `multica setup self-host` surface for connecting to custom origins is retired.

## Configuration

### View Config

```bash
multica config show
```

Shows config file path, server URL, app URL, and default workspace.

### Set Values

```bash
multica config set server_url https://api.example.com
multica config set app_url https://app.example.com
multica config set workspace_id <workspace-id>
```

`config set workspace_id <id>` is the low-level interface — it writes the value verbatim without checking that the workspace exists or that you have access. Prefer `multica workspace switch <id|slug>` for day-to-day workspace changes; it does both checks before saving.

## Autopilot Commands (retired)

**Autopilot is retired.** The `multica autopilot …` CLI, API, scheduler, and UI entry points are gone (LRM-1049/1051, task #40). Historical issue/task fields such as `origin_type=autopilot` / `autopilot_run_id` may still appear on old rows for audit only.

For recurring agent work, use **agent reminders** (`multica reminder schedule` / the reminder UI) with a message-id anchor in the target channel.

## Other Commands

```bash
multica version              # Show CLI version and commit hash
multica computer upgrade     # Upgrade the machine-wide Computer
multica workspace info --agents           # List agents in the current workspace
```

## Output Formats

Most commands support `--output` with two formats:

- `table` — human-readable table (default for list commands)
- `json` — structured JSON (useful for scripting and automation)

```bash
multica issue list --output json
multica daemon status --output json
```

## Error Messages

The CLI funnels command errors returned to the top-level handler through a
single user-facing translation layer (`server/internal/cli/errors.go`) so that
what you see on the terminal is a short, actionable sentence rather than a raw
Go error, an HTTP status line, or an internal `resolve issue: ...` chain. (A
few commands print their own output or run deliberate fast probes — for example
`setup`'s short `/health` reachability check — and don't go through this
layer.) The underlying detail is still available on demand (see `--debug`).

### What you see

- **Friendly, single-line message.** Transport failures (timeout, DNS,
  connection refused, TLS) and HTTP status failures (401/403/404/409/400·422/
  429/5xx) are each rendered as one clear sentence with a next step — for
  example a timeout suggests checking the network or raising
  `MULTICA_HTTP_TIMEOUT`, and a 401 tells you to run `multica login`.
- **Server-provided validation messages are preserved.** For a 400/422 that
  carries a message from the server, that message is shown verbatim
  (`Invalid request: <server message>`); only when there is none do you get the
  generic "check your values / run with --help" hint.
- **No leaked internals by default.** Raw URLs, status lines, JSON bodies, and
  the internal verb chain are hidden unless you ask for them.

### Language

Messages default to **English**, matching the rest of the CLI's help output.
If a Chinese locale is detected in `LC_ALL`, `LC_MESSAGES`, or `LANG` (in that
precedence order), messages switch to **Chinese**. No flag is needed; set the
locale as usual:

```bash
LANG=zh_CN.UTF-8 multica issue get MUL-9999   # 错误信息显示为中文
```

### Exit codes

The process exit code is tiered so scripts can branch on the failure class:

| Exit code | Meaning |
| --- | --- |
| `0` | success |
| `1` | generic / unclassified error |
| `2` | network error (timeout, DNS, connection refused, TLS, offline) |
| `3` | authentication / authorization (HTTP 401, 403) |
| `4` | not found (HTTP 404) |
| `5` | validation (HTTP 400, 422) |

```bash
multica issue get MUL-9999
if [ $? -eq 4 ]; then echo "no such issue"; fi
```

### Seeing the full detail (`--debug`)

Pass the global `--debug` flag (or set `MULTICA_DEBUG=1`) to print the complete
original error chain — the internal verb chain, the request method/path/status,
and the raw server body — underneath the friendly message. Use it when you need
to file a bug or understand exactly what the server returned:

```bash
multica issue list --debug
MULTICA_DEBUG=1 multica issue update MUL-1234 --title "x"
```

### Request timeout

API requests use a default timeout of 30 seconds. Override it with
`MULTICA_HTTP_TIMEOUT` when you are on a slow network; it accepts a Go duration
(`45s`, `2m`) or a plain number of seconds (`45`). Command-level deadlines are
always at least this value, so raising it takes effect across all commands.

```bash
MULTICA_HTTP_TIMEOUT=60s multica issue list
```
