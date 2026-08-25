# Revoke a user's Workspace Binding when membership ends

When a user loses Workspace membership, the service revokes every Workspace
Execution Binding that the user established for that Workspace. The affected
WorkspaceDaemon stops accepting work and its execution credential becomes
invalid. Membership loss, Binding revocation, and Runner shutdown are explicit
authorization events rather than inferred from a transient login or network
failure.

Revocation does not delete `~/.multica/workspaces/<workspace_id>` or any Agent
Root below it. Persistent data removal remains a separate, explicitly
user-confirmed destructive operation. That operation may be initiated in the
frontend and delivered through the service to the target Machine Service; it is
not a side effect of membership loss, Binding revocation, Runner shutdown, or
sign-out.
