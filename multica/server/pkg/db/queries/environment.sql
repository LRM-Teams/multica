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

-- name: CreateEnvDispatchRun :exec
-- Persists the durable dispatch root row for a project (one row per project,
-- keyed by project_id). Created after the project exists, carrying workspace_id
-- and training_mode. root_task_id starts NULL and is bound later via
-- BindEnvDispatchRootTask. Upsert on conflict so a re-dispatch of the same
-- project refreshes workspace_id/training_mode without orphaning the row.
INSERT INTO env_dispatch_run (project_id, workspace_id, training_mode)
VALUES ($1, $2, $3)
ON CONFLICT (project_id) DO UPDATE SET
  workspace_id = EXCLUDED.workspace_id,
  training_mode = EXCLUDED.training_mode;

-- name: BindEnvDispatchRootTask :exec
-- Binds the enqueued leader task as the dispatch root
-- (env_dispatch_run.root_task_id = rootTaskID). Called immediately after the
-- leader task is enqueued. No-op (0 rows affected) when no run row exists yet;
-- callers treat an unbound root as in_progress.
UPDATE env_dispatch_run
SET root_task_id = $2
WHERE project_id = $1;

-- name: GetEnvDispatchRootTaskStatus :one
-- Resolves the status of the dispatch's bound root task for the GET /dag
-- readiness decision. Readiness is derived EXCLUSIVELY from this row, not from
-- training_dispatch. The INNER JOIN yields no rows (pgx.ErrNoRows) when no
-- env_dispatch_run exists for the (project_id, workspace_id) pair or when
-- root_task_id is NULL (rollout not started / root not enqueued); the caller
-- treats both as "in_progress" (keep polling). A non-terminal status is also
-- in_progress; a terminal status (completed/failed/cancelled) triggers DAG
-- assembly.
SELECT atq.status
FROM env_dispatch_run r
JOIN agent_inbox_event atq ON atq.id = r.root_task_id
WHERE r.project_id = $1 AND r.workspace_id = $2;
