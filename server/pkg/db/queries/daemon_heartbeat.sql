-- name: RecordDaemonHeartbeat :exec
INSERT INTO daemon_heartbeat (
    workspace_id,
    daemon_id,
    last_seen_at,
    updated_at
)
VALUES (@workspace_id, @daemon_id, now(), now())
ON CONFLICT (workspace_id, daemon_id)
DO UPDATE SET
    last_seen_at = now(),
    updated_at = now();

-- name: GetDaemonHeartbeat :one
SELECT *
FROM daemon_heartbeat
WHERE workspace_id = @workspace_id
  AND daemon_id = @daemon_id;

-- name: GetDaemonHeartbeatsForWorkspace :many
-- Bulk lookup for the runtime list endpoint, which serializes one
-- daemon_last_seen_at per agent_runtime row without an N+1 query per
-- distinct daemon_id.
SELECT *
FROM daemon_heartbeat
WHERE workspace_id = @workspace_id;
