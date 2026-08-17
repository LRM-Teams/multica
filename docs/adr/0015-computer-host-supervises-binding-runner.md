---
status: accepted
---

# Computer host supervises one real Binding execution child

The Computer owns the desired Binding set. Each wanted Binding has at most one
generation-fenced OS child (`computer __runner --workspace-id`). That child is
the real execution owner, not a lifetime sentinel.

```text
Computer Host (internal/computer)
├── Binding child A (internal/daemon)
│   ├── WorkspaceRunner A
│   ├── AgentProcessManager
│   ├── Inbox / MessageCoordinator
│   ├── Activity
│   └── provider runtime processes
└── Binding child B (internal/daemon)
    └── same isolated execution owners
```

This intentionally differs from Raft v1.0.16 only in Binding cardinality:
Raft launches one child per attached Server, while Multica launches one child
per Workspace Execution Binding. The reusable architecture is the same:
one machine owner supervises N real execution children.

## Responsibility boundary

`internal/computer` owns all machine-scoped policy:

- desired Binding reconciliation and OS process spawn/stop;
- Runner generation, PID fence, Ready transition, crash budget and backoff;
- machine-wide provider-process capacity admission;
- authenticated child control and diagnostic aggregation routing;
- cross-child environment-switch and Machine Upgrade prepare/release;
- Machine Upgrade journal, stage, verify, activation, and successor
  completion reconciliation.

`internal/daemon` owns one Binding child's execution behavior:

- Workspace authentication and `WorkspaceRunner.Run`;
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
Upgrade journal, takeover, stage, or successor lifecycle. Daemon owns only the
child-local prepare/release behavior and the current Binding socket used by the
Computer-owned executor to publish progress and completion.

The CLI is composition only. A Computer Host must not construct provider
runtimes, Inbox/Activity owners, Agent Restart executor, Attachment registry,
Reminder cache, or Binding draft/outbox state.

## Bootstrap, Ready, and local control

The parent sends one immutable bootstrap document over stdin. It includes
`computer_generation`, `runner_generation`, Workspace identity, environment,
roots, server URL, and the loopback Host-control URL. It deliberately excludes
execution credentials; the child reads its scoped credential from the
permission-restricted Binding store.

The child publishes Ready only after it has:

1. attested its exact `(workspace, runner generation, PID)` to the Host;
2. validated the current Binding credential and Computer generation;
3. registered its provider Runtime set and reported it to the Host;
4. constructed its child-local execution owners;
5. opened the Workspace Runner transport and emitted the real Runner Ready
   frame.

Ready returns the child's loopback control URL. The Host uses that URL only
with the exact generation/PID identity and the machine control token.

## Generation fence and crash policy

`computer_generation` is the machine resident tenure. `runner_generation` is
the independent spawn tenure of one Binding slot. Each spawn increments the
Runner generation; an exit, control request, Runtime report, diagnostic, or
capacity lease from a previous generation/PID is rejected or ignored.

Host reconciliation follows the Raft Computer policy:

- reconcile every 5 seconds;
- crash restart backoff of 2 seconds;
- 3 crashes inside 60 seconds enters `degraded` and stops automatic restart;
- desired-set removal is a graceful stop, not `unlinked` / degraded;
- removing one Binding does not restart or mutate its siblings.

## Machine Upgrade

Machine Upgrade remains one Computer request. The Host first asks every
current child to close and drain its child-local execution admission barrier.
If one child rejects preparation, the Host releases every sibling already
prepared. The Host never probes provider CLIs or creates Runtime execution
objects.

A successful process replacement stops all children with the Host process.
The successor Computer reconstructs the desired set, waits for every real
child Ready, then reconciles the pending upgrade over one current Binding
socket. Before swapping the executable, the executor persists a
permission-restricted marker containing the original request ID and source and
target versions. The successor reports correlated success, or correlated
rollback when its running version differs from the target. It clears the marker
only after the Binding accepts the completion frame; otherwise startup fails
and the marker remains for retry. There is no accepted generation or complete
Runtime/Workspace snapshot in this marker, and no cloud attestation receipt.
An environment switch uses a distinct child barrier that waits for admitted
work naturally; it does not reuse Machine Upgrade's terminate-and-drain path.

## Consequences

- A child crash contains Workspace execution without taking down sibling
  Bindings or the Computer Host.
- Deleting the child process now deletes the complete Binding execution
  behavior, so the OS-process seam is deep rather than ceremonial.
- Per-Binding durable execution state lives under
  `binding-children/<environment>/<workspace-id>`; machine identity and the
  explicit Binding store remain machine-wide.
- Message delivery and Activity keep their different contracts: durable
  delivery responsibility is not inferred from best-effort Activity.
