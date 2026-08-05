# Isolate local execution by workspace-machine binding

Each Workspace-machine Execution Binding has at most one active Workspace
Runner. A Workspace Runner owns only the runtime and Agent execution assigned
to that binding; it is not the Workspace's global execution owner, so the same
Workspace may run through bindings on multiple machines. This preserves
multi-machine execution and Agent migration while making credentials, inbox
state, crashes, and restarts independently containable per binding.
