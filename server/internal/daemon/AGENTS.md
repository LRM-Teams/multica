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
supervision, generation fencing, sibling coordination, orphan cleanup, and
upgrade policy. Cloud Server HTTP/WebSocket is not part of this migration.

Use TDD at the RPC operation seam before changing handlers or callers. Preserve
`computer_generation` and `runner_generation`; do not restore per-Binding
lease/attest polling or persisted lifecycle state.
