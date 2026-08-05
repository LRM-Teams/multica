# Allow user-confirmed Agent Workspace deletion on a selected machine

A user first selects a specific machine in the frontend and may then explicitly
request deletion of one Agent's local working directory. After confirming the
destructive action, the service delivers a scoped deletion operation only to
the selected Machine Service, which stops that Agent runtime and removes
`~/.multica/workspaces/<workspace_id>/agents/<agent_id>`.

Deletion removes the complete Agent Workspace, including local memory,
sessions, skills, runtime state, and working files. These are not retained as
subdirectories after this local-data deletion operation completes. Deleting the
server-side Agent is a different operation and does not imply this deletion.
The frontend confirmation names this scope before submitting the operation.

This operation does not delete the Multica Workspace, does not delete sibling
Agent Workspaces, and is not broadcast to other machines that have a Binding
for the same Workspace.

The deletion operation is distinct from membership and Binding lifecycle. It
identifies the target machine, Workspace, and Agent by immutable IDs and is
safe to retry by operation ID. A revoke, unlink, sign-out, network failure, or
ordinary Runner or Agent stop never implies this deletion intent.
