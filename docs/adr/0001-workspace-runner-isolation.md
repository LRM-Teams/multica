# Isolate local execution by workspace-machine binding

Each Workspace-machine Execution Binding has at most one active Workspace
Runner. A Workspace Runner owns only the runtime and Agent execution assigned
to that binding; it is not the Workspace's global execution owner, so the same
Workspace may run through bindings on multiple machines. This preserves
multi-machine execution and Agent migration while making credentials, inbox
state, crashes, and restarts independently containable per binding.

The Runner is a state-owning object whose immutable identity is the stable
Daemon, current daemon process instance, and Workspace. Runtime membership is
mutable input and is never part of Runner identity. Each Runner owns its local
Process Manager, Activity producer, and Workspace-scoped Inbox registry state.
Machine-wide Agent Attachment, Runtime capacity, Credential Proxy, and
diagnostic registries are injected references; constructing a Runner must not
copy those owners or create another global singleton.

`WorkspaceRunner.Run(ctx)` owns Workspace authentication, dial/reconnect
backoff, ready identity, connection cancellation, ping/pong, and the single
serialized frame writer. Reconnect replaces and closes the prior connection
context before installing a new writer. Socket callbacks remain private Runner
implementation details; there is no separately exposed `RunnerTransport`
module.

Each live Runner connection also owns its per-Agent Delivery dispatcher and
current-socket Message callbacks. Deliveries are FIFO for one Agent and may run
concurrently across different Agents. A callback is valid only while its exact
connection remains current; replacement fences the old callback before the new
writer becomes externally addressable. Daemon code may resolve the current
Runner by Workspace, but it does not keep a parallel transport or generation
map.
