# Use one canonical Agent Root under its Workspace

Persistent local Agent data lives at
`~/.multica/workspaces/<workspace_id>/agents/<agent_id>`. Workspace and Agent
IDs are immutable directory keys; names, slugs, profiles, and runtime providers
must not participate in the path. Runtime-specific roots such as
`.pi/agents/<agent_id>` are obsolete and are neither read nor migrated. The
hard cut leaves old files untouched at their old locations.

Machine control state remains under `~/.multica/computer`, separate from Agent
data. Production uses the canonical `~/.multica/workspaces` root. A
WorkspacesRoot override may be used by development and tests but is not a
normal user preference.

Binding and Runner lifecycle operations do not own persistent Agent data.
Unbinding a Workspace, stopping its Runner, signing out, or rebuilding Machine
Service state must preserve the corresponding Workspace and Agent roots.
There is no background retention or garbage-collection job for AgentRoot.
Only an explicit full reset may hard-delete and recreate the exact root; it
force-interrupts the runtime and does not wait for quiescence. Separately, the
Computer owner may explicitly delete a listed on-disk AgentRoot through the
confirmed Computer storage action; that manual choice is not inferred from
unbinding or Runner state.
