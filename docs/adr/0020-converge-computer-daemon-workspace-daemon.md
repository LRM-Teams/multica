---
status: accepted
---

# Converge Computer, Daemon, and WorkspaceDaemon ownership

The local execution hierarchy is:

```text
ComputerCore
└── DaemonCore                         local only; no Server WebSocket
    ├── WorkspaceDaemonCore A          one OS child per Workspace Binding
    └── WorkspaceDaemonCore B
        └── workspaceSession           private state for its one WS connection
```

`ComputerCore` owns machine lifecycle, upgrade/restart execution, work journal,
local service control, and durable diagnostic writers. `DaemonCore` owns the
desired-vs-actual Workspace set, child process identity fences, crash policy,
shared Agent process capacity, and local IPC dispatch. Each
`WorkspaceDaemonCore` owns one Workspace's Agent execution state and one Server
WebSocket. `workspaceSession` is private connection state, not another domain
component.

The canonical WebSocket endpoint is
`GET /api/workspace/daemon/connect`. Its route is referenced through
`protocol.WorkspaceDaemonConnectPath`. The existing
`GET /api/daemon/connect?workspace_id=...` branch remains only as a released
Computer upgrade bridge: an old Computer must stay reachable long enough to
receive `computer:upgrade`. Remove that branch only after the minimum supported
Computer version uses the new endpoint and fleet evidence shows that no released
Computer still depends on the bridge. The runtime/task form of
`/api/daemon/connect` remains a separate legacy transport.

Computer operations do not require another Computer WebSocket. A server
`computer:*` envelope may travel over the current WorkspaceDaemon connection,
then through generation-fenced local IPC to `ComputerCore`. The WebSocket is a
carrier only; WorkspaceDaemonCore does not own machine-operation execution,
journaling, idempotency, or attestation.

Gorilla WebSocket permits one concurrent writer. The Server therefore gives
each connection one writer goroutine and a bounded priority/normal outbox.
Outbox limits are memory-safety budgets, never a function of Agent count.
Agent reconcile bursts queue on the normal lane; rare `computer:*` and liveness
frames use the priority lane. Queue pressure rejects the new enqueue and must
not unregister or close a healthy socket. Replacement, authentication failure,
I/O failure, or the inbound liveness watchdog may close it.

WorkspaceDaemon diagnostics are forwarded over local IPC. `ComputerCore`'s
per-Workspace `diagnosticLoggers` map is the only production owner of durable
diagnostic writers; WorkspaceDaemonCore must not open a parallel diagnostic
store.

This ADR is the sole component-ownership contract. `Workspace Runner`,
`BindingSupervisor`, and `Host` are not current component names.
