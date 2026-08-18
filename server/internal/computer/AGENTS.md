# Computer package guidance

## Process boundary

Computer and each Binding Runner/Daemon are separate OS processes. Their
control plane uses Raft-style local IPC:

- Unix domain socket on Unix;
- Windows named pipe on Windows;
- 4-byte big-endian length-prefixed JSON RPC frames;
- operation + args → typed result or structured error.

Production Computer↔Runner control must dispatch RPC operations directly. Do
not convert an IPC frame into `http.Request`, `http.ResponseWriter`, or an HTTP
adapter.

## Scope and responsibilities

- `internal/computer` owns machine/service supervision, workspace admission,
  upgrade coordination, generation fences, orphan cleanup, and service/runner
  control handlers.
- Cloud Server communication remains HTTP/WebSocket and is outside this local
  IPC migration.
- Credential Proxy remains loopback HTTP and is a separate protocol and
  responsibility.
- Preserve `computer_generation`, `runner_generation`, split PID/state/
  connected evidence, and the single successor-handoff upgrade journal.

Use TDD for each RPC operation: add the failing operation-seam test first,
then implement the smallest handler and caller change. Do not reintroduce
per-Binding lease/attest polling or persisted lifecycle state.

## Go typing

Prefer concrete request and result structs at RPC operation seams. Avoid `any`
and `interface{}` whenever the payload shape is known; define the operation's
request/result type instead. Use a generic JSON value only at an intentionally
open-ended boundary that cannot have a meaningful concrete type.

## Provider global and agent-workspace state

The Computer package owns machine identity, bindings, resident lifecycle, and
host control. It does not own provider homes, provider-global skills, or
agent-workspace skill materialization; those belong to the daemon's execenv
layer.

Keep the two scopes separate:

- Provider-global configuration, authentication, caches, sessions, and skills
  stay in the inherited user/provider home (`CODEX_HOME`, `~/.codex/`,
  `~/.agents/`, or the equivalent provider home). Computer code must not copy,
  symlink, redirect, or clean them into an agent directory.
- Agent-specific skills are installed by the daemon below the provider
  process's `workingDirectory`, using the provider's workspace-native path.

In particular, Computer must never create or prescribe an agent-scoped
`codex-home` or `CODEX_HOME`. Changes to these paths must be implemented and
tested in `server/internal/daemon/execenv`, not in this package.
