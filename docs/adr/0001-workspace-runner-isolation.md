# Isolate local execution by workspace-machine binding

Each Workspace-machine Execution Binding has at most one active Workspace
Runner. A Workspace Runner owns only the runtime and Agent execution assigned
to that binding; it is not the Workspace's global execution owner, so the same
Workspace may run through bindings on multiple machines. This preserves
multi-machine execution and Agent migration while making credentials, inbox
state, crashes, and restarts independently containable per binding.

The Runner is a state-owning object whose immutable identity is the stable
Computer, current Computer process instance, and Workspace. Runtime membership is
mutable input and is never part of Runner identity. Each Runner owns its local
Process Manager, Activity producer, and Workspace-scoped Inbox registry state.
Machine-wide Runtime capacity and diagnostic registries are injected references;
constructing a Runner must not copy those owners or create another global
singleton. Credential Proxy callers resolve the unique Runner and invoke its
Message operations without receiving Inbox internals.

The Runner never retains `*Daemon`. Construction wires explicit machine-wide
owners and narrow callbacks only. `Daemon` owns Runner creation, teardown, and
lookup; Message delivery/recovery, coverage, Activity observation, and
Agent process lifecycle transitions stay behind Runner methods. Machine-local
callers may resolve an unambiguous Runner, but never receive its Inbox,
Process Manager, or Activity producer internals.

`WorkspaceRunner.Run(ctx)` owns Workspace authentication, dial/reconnect
backoff, ready identity, connection cancellation, ping/pong, and the single
serialized frame writer. Reconnect replaces and closes the prior connection
context before installing a new writer. Socket callbacks remain private Runner
implementation details; there is no separately exposed `RunnerTransport`
module.

A capable current Runner also owns the management heartbeat and consumes its
acknowledgements on that same fenced socket. It derives the current Runtime set
from its Workspace binding on every send; Server authorization rechecks both
Workspace and Computer ownership for each Runtime independently; it does not
require equality with every historical Runtime row retained for the Computer,
because an offline row for an uninstalled provider is not current Runner
membership. Reminder reconnect requests a complete snapshot for every current
Runtime, while live mutations use direct version-fenced upsert/cancel commands.
The legacy runtime-multiplexed socket remains a data, Reminder,
task-wakeup, and liveness transport, and the current daemon does not start the
HTTP heartbeat loop. Those legacy Server endpoints continue to accept older
released daemons during rolling deployment, but they are not a second control
carrier inside a capable daemon process.

Each live Runner connection also owns its per-Agent Delivery dispatcher and
current-socket Message callbacks. Deliveries are FIFO for one Agent and may run
concurrently across different Agents. A callback is valid only while its exact
connection remains current; replacement fences the old callback before the new
writer becomes externally addressable. Daemon code may resolve the current
Runner by Workspace, but it does not keep a parallel transport or generation
map.

`InboxRegistry` is Runner-owned in-memory state with one immutable Workspace
scope. It accepts an Inbox only from that Runner's current Agent Process Manager
`agent:start` admission and refuses delivery without that accepted launch.
Delivery, recovery pages, reconnect recovery, and scoped close all route through
this registry. A Runner reconnect retains its registry; a Runner shutdown
detaches it from lookup and closes only that Runner's coordinators.
Legacy machine-local callers that have only an Agent ID can resolve exactly one
current Inbox, but an absent or ambiguous result fails closed rather than
selecting a Workspace.

`AgentProcessManager` and `AgentActivityProducer` are likewise created once by
the Runner and never indexed separately by the Daemon. A socket reconnect only
replaces Activity transport, preserving launch identity and Activity sequence.
Binding teardown releases that Runner's process, Activity, and Inbox state.
