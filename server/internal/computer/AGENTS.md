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
