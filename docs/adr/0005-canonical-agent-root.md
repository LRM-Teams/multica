# Use one canonical Agent Root under its Workspace

Persistent local Agent data lives at
`~/.multica/workspaces/<workspace_id>/agents/<agent_id>`. Workspace and Agent
IDs are immutable directory keys; names, slugs, profiles, and runtime providers
must not participate in the path. Runtime-specific layouts such as
`.pi/agents/<agent_id>` are compatibility inputs during migration, not
alternative identities or write roots.

Machine control state remains under `~/.multica/computer`, separate from Agent
data. Production uses the canonical `~/.multica/workspaces` root. A
WorkspacesRoot override may be used by development and tests but is not a
normal user preference.

Binding and Runner lifecycle operations do not own persistent Agent data.
Unbinding a Workspace, stopping its Runner, signing out, or rebuilding Machine
Service state must preserve the corresponding Workspace and Agent roots. Data
removal requires a separate explicit destructive operation with its own user
confirmation.
