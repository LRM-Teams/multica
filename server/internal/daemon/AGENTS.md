# Daemon Agent Start and Activity Rules

These rules are hard alignment constraints from Raft v1.0.16. Keep the names
and ownership boundaries exact; do not add wrapper concepts around them.

## Agent start

- The production start owner is `(*WorkspaceRunner).startAgentNow`.
- Server-commanded starts and idle-snapshot wakeups must converge on
  `startAgentNow`; do not create another production start-completion path.
- Replayed or rebound starts publish current status/session only. They must not
  re-enter `startAgentNow` or manufacture a new spawn Activity.

## Starting Activity

- Publish the spawn Activity through
  `(*WorkspaceRunner).broadcastActivity(..., "starting")`.
- Production code has exactly one `broadcastActivity` call site: inside
  `startAgentNow`, after the provider process exists and `active` status (and a
  present provider session) has been sent.
- One provider spawn produces exactly one `Starting…` Activity. Reconnect,
  replay, Message delivery, and runtime progress must not broadcast Starting.
- Do not reintroduce `publishManagedAgentStartActivity`,
  `observeRuntimeStarting`, `publishManagedProviderSpawn`, or an equivalent
  wrapper under another name.

## Executable checks

- `TestRaftStartingActivityHasOneBroadcastCallSite` locks the method name and
  sole production call site.
- `TestWorkspaceRunnerAcceptsScopedStartAndReturnsAckThenStatus` requires
  exactly one Starting Activity for a managed spawn.
- `TestReplayManagedStartDoesNotRepaintStarting` locks the replay behavior.
- Run `go test ./internal/daemon -count=1` from `server/` after changing this
  flow.

## Provider global and agent-workspace state

Keep provider-global state separate from the durable agent workspace. Global
configuration, authentication, caches, sessions, and global skills belong to
the inherited user/provider home and must not be copied, symlinked, or
rewritten below an agent root. An agent-specific skill belongs under the
provider's workspace discovery directory below the provider process's
`workingDirectory`.

The daemon owns the workspace side only. Its provider skill paths are:

| Provider | Agent-specific skills |
| --- | --- |
| Claude | `<workingDirectory>/.claude/skills/` |
| Codex | `<workingDirectory>/.agents/skills/` |
| OpenCode | `<workingDirectory>/.opencode/skills/` |
| Pi | `<workingDirectory>/.pi/skills/` |
| Cursor | `<workingDirectory>/.cursor/skills/` |
| Kiro | `<workingDirectory>/.kiro/skills/` |
| Grok | `<workingDirectory>/.grok/skills/` |

Do not introduce an agent-scoped `CODEX_HOME`, `codex-home`, or equivalent
provider-home directory. Codex's global `CODEX_HOME`, `~/.codex/skills/`, and
`~/.agents/skills/` remain inherited global state. The same separation applies
to every other code agent: never seed its global skills/config into the
agent-specific directory. If a provider does not support the workspace path,
keep the skill in the generated `.agent_context/` contract rather than
silently copying global state.

When changing a provider's skill path, update both `skillsDirPath` and the
rendered skill locations in `runtime_config.go`, then verify that reuse removes
only daemon-managed workspace files and leaves provider-global files alone.
# Daemon package guidance

## Computer control boundary

Each Binding Runner/Daemon is a separate OS process from Computer. Runner
control uses the Raft-style local IPC endpoint supplied in its bootstrap:

- Unix domain socket on Unix;
- Windows named pipe on Windows;
- 4-byte big-endian length-prefixed JSON RPC frames;
- operation + args → typed result or structured error.

Runner control handlers must be RPC handlers directly. Do not add an
`http.Request`/`http.ResponseWriter` adapter for Computer↔Runner control.

The child-local Credential Proxy is intentionally different: it remains
loopback HTTP for provider credential traffic and must not be routed through
the control RPC.

## Responsibility boundary

Daemon owns one Workspace Runner's execution behavior, drain barrier, provider
runtimes, Runtime registration, and child-local state. Computer owns machine
supervision, process identity fencing, sibling coordination, orphan cleanup,
and upgrade policy. Cloud Server HTTP/WebSocket is not part of this migration.

Use TDD at the RPC operation seam before changing handlers or callers. Managed
runner identity is `pid + startIdentity + role + workspace/server identity +
version`; do not restore numeric generation fences, per-Binding lease/attest
polling, or persisted lifecycle state.

## Go typing

Prefer concrete request and result structs at RPC operation seams. Avoid `any`
and `interface{}` whenever the payload shape is known; define the operation's
request/result type instead. Use a generic JSON value only at an intentionally
open-ended boundary that cannot have a meaningful concrete type.
