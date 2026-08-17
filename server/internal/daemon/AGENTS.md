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
