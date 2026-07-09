-- name: CreateEnvironment :one
INSERT INTO environment (workspace_id, sandbox_ids, parent_env_id, mode, domain)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetEnvironment :one
SELECT * FROM environment
WHERE id = $1 AND workspace_id = $2;

-- name: DeleteEnvironment :exec
DELETE FROM environment WHERE id = $1 AND workspace_id = $2;

-- name: GetEnvDispatchRequest :one
SELECT * FROM env_dispatch_request
WHERE workspace_id = $1 AND idempotency_key = $2;

-- name: CreateEnvDispatchRequest :exec
INSERT INTO env_dispatch_request (workspace_id, idempotency_key, response)
VALUES ($1, $2, $3);
