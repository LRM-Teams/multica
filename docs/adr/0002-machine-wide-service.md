# Use one Machine Service per OS user environment

One Machine Service supervises all local Multica execution for an OS user and
may manage Workspace Execution Bindings for multiple Workspaces. A server URL
or login profile is configuration, not an additional Machine Service domain or
a simultaneous server attachment. This avoids competing supervisors, duplicate
upgrades, port ownership conflicts, and cross-profile races over machine-local
Agent state.
