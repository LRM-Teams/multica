---
status: accepted
---

# Computer service supervises one OS runner per Binding

The Computer owns the desired Binding set. Each wanted Binding has at most one
identity-fenced, cancelable runner OS process supervised by the resident. It
is the real execution owner, not a lifetime sentinel. The executable
`computer __run` is the production runner entrypoint.

```text
Computer process
├── Host (internal/computer)
├── Binding execution A (internal/daemon)
│   ├── WorkspaceDaemon A
│   ├── AgentProcessManager
│   ├── Inbox / MessageCoordinator
│   ├── Activity
│   └── provider runtime processes
└── Binding execution B (internal/daemon)
    └── same isolated execution owners
```

This aligns Raft v1.0.17's ownership and reconcile model.
Raft launches one child per attached Server; Multica launches one OS runner per
execution per Workspace Binding. One machine owner still supervises N real
execution owners, matching Raft's service/runner process boundary while
preserving Binding as Multica's domain term.

## Responsibility boundary

`internal/computer` owns all machine-scoped policy:

- desired Binding reconciliation and runner process start/stop;
- Runner child-reported `daemonInstanceId`/PID fence, Ready transition, crash budget and backoff;
- machine-wide provider-process capacity admission;
- authenticated child control and diagnostic aggregation routing;
- cross-child environment-switch and Machine Upgrade prepare/release;
- Machine Upgrade accept, journal, stage, verify, activation, successor
  re-registration, and attestation.

`internal/daemon` owns one Binding child's execution behavior:

- Workspace authentication and `WorkspaceDaemon.Run`;
- Agent Process Manager and canonical provider runtime pool;
- Inbox, MessageCoordinator, Activity, Attachments and Reminder execution;
- child-local Credential Proxy and child-local durable execution state;
- its own claim barrier, drain, provider termination, and Runtime
  registration.

The public resident path lives in `cmd/multica/cmd_computer_resident.go` and is
`runComputerResident → computer.NewHost → Host.RunProcess`; it does not construct
or call `daemon.Daemon`. An executable
architecture test enforces both directions: the Computer resident has no
`daemon.*` dependency, while production daemon files cannot expose a resident
`Run`, health/machine-attestation owner, restart/update executor, Machine
Upgrade journal, takeover, stage, or successor lifecycle. The only
Machine-Upgrade-related daemon behavior is child-local prepare/release and
Runtime re-registration requested by the Computer.

The CLI is composition only. A Computer Host must not construct provider
runtimes, Inbox/Activity owners, Agent Restart executor, Attachment registry,
Reminder cache, or Binding draft/outbox state.

## Bootstrap, Ready, and local control

The service constructs one immutable bootstrap value for each runner
execution. It includes Workspace identity, environment, roots,
server URL, and the local service IPC endpoint. It deliberately excludes
a Host-minted process ticket and execution credentials; the Binding
execution generates `daemonInstanceId` itself and reads its scoped
credential from the permission-restricted Binding store. The executable
fallback serializes the same value over stdin.

The service records the OS process handle before activating the runner.
The child reports `daemonInstanceId` on Ready. The execution therefore
does not need to poll or register back through the Unix socket or
Windows named pipe. It publishes Ready only after it has:

1. validated the current Binding credential and bootstrap identity;
2. registered its provider Runtime set and reported it to the Host;
3. constructed its child-local execution owners;
4. opened the Workspace Runner transport and emitted the real Runner Ready
   frame.

Ready returns the runner IPC endpoint and the child-generated
`daemonInstanceId`. The service uses that endpoint only with the exact
`daemonInstanceId`/PID identity and the machine control token.

### Local control transport decision

The Computer ↔ Binding Runner/Daemon control plane uses Raft-style local IPC:
Unix domain sockets on Unix and named pipes on Windows, carrying a
length-prefixed JSON RPC envelope. The RPC boundary is operation-oriented
(`runner-drain`, `runner-ready`, `service-status`, `upgrade-start`, and so on)
with typed arguments, typed results, and structured errors. The local control
server must dispatch these operations directly; it must not convert an IPC
frame into `http.Request`/`http.ResponseWriter` or retain an HTTP adapter as
the production control path.

This decision is deliberately narrow. Computer ↔ Cloud Server remains HTTP and
WebSocket, and the child Credential Proxy remains loopback HTTP because those
are different protocols and responsibilities. A loopback HTTP endpoint must
not be described as the Computer control IPC endpoint, and Credential Proxy
traffic must not be routed through the control RPC.

The migration scope is only the local control operations owned by Computer and
the Binding child. Existing domain logic stays in `internal/computer` and
`internal/daemon`; only the process-boundary handlers and callers move from
HTTP-shaped adapters to typed RPC handlers. Tests must be written red-first at
the operation seam before each handler migration.

## Process identity fence and crash policy

Each resident start generates an opaque `serviceGeneration`. Each managed
runner child generates an opaque `daemonInstanceId` and reports it on Ready.
An exit, Ready, control request, Runtime report, diagnostic, or capacity lease
must match the current Workspace + `daemonInstanceId` + PID; a stale process is
rejected or ignored.

Host reconciliation follows the Raft Computer policy:

- reconcile every 5 seconds;
- crash restart backoff of 2 seconds;
- 3 crashes inside 60 seconds enters `degraded` and stops automatic restart;
- desired-set removal is a graceful stop, not `unlinked` / degraded;
- removing one Binding does not restart or mutate its siblings.

The service supervises runner process handles. Binding
executions do not acquire a per-Binding OS lease and do not periodically attest
service liveness. The supervisor registers each execution before spawning its
process, so there is no startup registration request or liveness heartbeat.

## Machine Upgrade

Machine Upgrade remains one Computer operation. The Host first asks every
current child to close and drain its child-local execution admission barrier.
If one child rejects preparation, the Host releases every sibling already
prepared. Re-registration and rollback convergence are also sent to the child;
the Host never probes provider CLIs or creates Runtime execution objects.

A successful process replacement stops all Binding executions with the
Computer process. The successor Computer reconstructs the accepted managed
Workspace set, waits for every real execution Ready, then performs service and
managed-set attestation.

The permission-restricted Host journal is a single Raft-style successor
handoff marker. It records the request, source version, target version, start
time, schema version, `sourceServicePid`, old runner PIDs, and the accepted
managed Workspace set/revision immediately before activation; it is not a
persisted multi-phase state machine. A successor reads that journal after
restart and proves its exact target version, target PID, source PID, non-empty
`serviceGeneration`, and complete managed set over framed local IPC. The first
valid proof records `observedTargetGeneration` and `targetServicePid`; a
replacement target must be observed again. The journal is cleared only after
the source service and every old runner PID are dead.
An environment switch uses a distinct child barrier that waits for admitted
work naturally; it does not reuse Machine Upgrade's terminate-and-drain path.

## Consequences

- A child crash contains Workspace execution without taking down sibling
  Bindings or the Computer Host.
- Canceling one supervised Binding deletes its complete execution behavior
  without terminating sibling Bindings or the Computer process.
- Per-Binding durable execution state lives under
  `binding-children/<environment>/<workspace-id>`; machine identity and the
  explicit Binding store remain machine-wide.
- Normal Computer shutdown drains and stops every runner; crash recovery uses
  process-identity-fenced reconciliation rather than in-process lifetime coupling.
- Message delivery and Activity keep their different contracts: durable
  delivery responsibility is not inferred from best-effort Activity.
