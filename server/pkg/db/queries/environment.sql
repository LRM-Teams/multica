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

-- name: ListOwnedEnvDispatchResources :one
-- Returns the derived resources owned by an env-dispatch binding (keyed by
-- env_id + source_agent_id) for idempotent cleanup: sandbox_instance_id,
-- runtime_id, derived_agent_id, daemon_id, and training session identity.
SELECT id, env_id, agent_id, source_agent_id, derived_agent_id,
       sandbox_instance_id, runtime_id, daemon_id, training_session_id,
       training_session_ref, credential_kind, model_config_owner_agent_id,
       status
FROM environment_agent_sandbox
WHERE env_id = $1 AND source_agent_id = $2;
