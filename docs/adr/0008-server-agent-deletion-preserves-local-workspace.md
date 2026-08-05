# Preserve local Agent Workspaces when deleting the server-side Agent

Deleting an Agent in the frontend removes its server-side identity and prevents
new execution, but it does not immediately delete that Agent's local Workspace
files. Agent deletion and filesystem deletion are separate operations with
different targets, failure modes, and recovery value.

An Agent Workspace remains on each machine until the user selects that machine
and explicitly requests Local Agent Workspace Deletion. The machine-scoped
operation defined in ADR 0007 is the only operation that removes the complete
`~/.multica/workspaces/<workspace_id>/agents/<agent_id>` directory.
