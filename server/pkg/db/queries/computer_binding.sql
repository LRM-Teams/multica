-- name: GetActiveBindingOwnerForRuntime :one
-- LRM-1570: ownership is machine-level. Given a runtime, return the user id
-- of the active computer_workspace_bindings row for its daemon in the same
-- workspace. Exactly one active binding is expected (the machine owner).
SELECT b.user_id
FROM computer_workspace_bindings b
WHERE b.daemon_id = @daemon_id
  AND b.workspace_id = @workspace_id
  AND b.active = TRUE
LIMIT 1;

-- name: GetActiveBindingOwnersForRuntime :many
-- LRM-1570: all active binding owners for a runtime's daemon in a workspace
-- (used for membership validation and audit attribution).
SELECT b.user_id
FROM computer_workspace_bindings b
WHERE b.daemon_id = @daemon_id
  AND b.workspace_id = @workspace_id
  AND b.active = TRUE;
