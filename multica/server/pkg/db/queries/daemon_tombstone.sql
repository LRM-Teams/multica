-- name: UpsertDaemonRegistrationTombstone :one
INSERT INTO daemon_registration_tombstone (
    workspace_id,
    daemon_id,
    removed_by
)
VALUES (@workspace_id, @daemon_id, @removed_by)
ON CONFLICT (workspace_id, daemon_id)
DO UPDATE SET
    removed_by = EXCLUDED.removed_by,
    removed_at = now()
RETURNING *;

-- name: IsDaemonRegistrationTombstoned :one
SELECT EXISTS (
    SELECT 1
    FROM daemon_registration_tombstone
    WHERE workspace_id = @workspace_id
      AND daemon_id = @daemon_id
);
