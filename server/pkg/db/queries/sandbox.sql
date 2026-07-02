-- name: CreateSandboxNode :one
INSERT INTO sandbox_node (node_key, name, capabilities, max_concurrency, metadata)
VALUES (@node_key, @name, @capabilities, @max_concurrency, @metadata)
RETURNING *;

-- name: UpsertSandboxNodeRegistration :one
INSERT INTO sandbox_node (node_key, name, status, capabilities, max_concurrency, metadata, last_seen_at)
VALUES (@node_key, @name, 'online', @capabilities, @max_concurrency, @metadata, now())
ON CONFLICT (node_key)
DO UPDATE SET
    name = EXCLUDED.name,
    status = 'online',
    capabilities = EXCLUDED.capabilities,
    max_concurrency = EXCLUDED.max_concurrency,
    metadata = EXCLUDED.metadata,
    last_seen_at = now(),
    updated_at = now()
RETURNING *;

-- name: ListSandboxNodes :many
SELECT * FROM sandbox_node
ORDER BY created_at ASC;

-- name: GetSandboxNode :one
SELECT * FROM sandbox_node
WHERE id = $1;

-- name: GetSandboxNodeByKey :one
SELECT * FROM sandbox_node
WHERE node_key = $1;

-- name: TouchSandboxNodeHeartbeat :one
UPDATE sandbox_node
SET status = 'online', last_seen_at = now(), updated_at = now(), metadata = @metadata
WHERE id = @id
RETURNING *;

-- name: SetSandboxNodeOffline :exec
UPDATE sandbox_node
SET status = 'offline', updated_at = now()
WHERE id = $1;

-- name: CreateSandboxNodeToken :one
INSERT INTO sandbox_node_token (node_id, name, token_hash, token_prefix, expires_at, created_by)
VALUES (@node_id, @name, @token_hash, @token_prefix, @expires_at, @created_by)
RETURNING *;

-- name: GetSandboxNodeTokenByHash :one
SELECT * FROM sandbox_node_token
WHERE token_hash = $1 AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at > now());

-- name: RevokeSandboxNodeToken :exec
UPDATE sandbox_node_token
SET revoked_at = now()
WHERE id = $1;

-- name: UpsertSandboxWorkspaceBinding :one
INSERT INTO sandbox_workspace_binding (workspace_id, node_id, enabled, policy, created_by)
VALUES (@workspace_id, @node_id, true, @policy, @created_by)
ON CONFLICT (workspace_id, node_id)
DO UPDATE SET enabled = true, policy = EXCLUDED.policy, updated_at = now()
RETURNING *;

-- name: ListSandboxWorkspaceBindings :many
SELECT swb.*, sn.node_key, sn.name, sn.status, sn.capabilities, sn.max_concurrency, sn.last_seen_at
FROM sandbox_workspace_binding swb
JOIN sandbox_node sn ON sn.id = swb.node_id
WHERE swb.workspace_id = $1
ORDER BY swb.created_at ASC;

-- name: GetEnabledSandboxBinding :one
SELECT * FROM sandbox_workspace_binding
WHERE workspace_id = @workspace_id AND node_id = @node_id AND enabled = true;

-- name: DisableSandboxWorkspaceBinding :exec
UPDATE sandbox_workspace_binding
SET enabled = false, updated_at = now()
WHERE workspace_id = @workspace_id AND node_id = @node_id;

-- name: PickAvailableSandboxNodeForWorkspace :one
SELECT sn.*
FROM sandbox_workspace_binding swb
JOIN sandbox_node sn ON sn.id = swb.node_id
WHERE swb.workspace_id = $1 AND swb.enabled = true AND sn.status = 'online'
ORDER BY sn.last_seen_at DESC NULLS LAST, sn.created_at ASC
LIMIT 1;

-- name: CreateSandboxInstance :one
INSERT INTO sandbox_instance (workspace_id, creator_user_id, node_id, status, template, limits, metadata)
VALUES (@workspace_id, @creator_user_id, @node_id, @status, @template, @limits, @metadata)
RETURNING *;

-- name: ListSandboxInstancesByWorkspace :many
SELECT si.*, sn.node_key, sn.name AS node_name, sn.status AS node_status
FROM sandbox_instance si
JOIN sandbox_node sn ON sn.id = si.node_id
WHERE si.workspace_id = $1
ORDER BY si.created_at DESC;

-- name: GetSandboxInstanceForWorkspace :one
SELECT si.*, sn.node_key, sn.name AS node_name, sn.status AS node_status
FROM sandbox_instance si
JOIN sandbox_node sn ON sn.id = si.node_id
WHERE si.id = @id AND si.workspace_id = @workspace_id;

-- name: UpdateSandboxInstanceStatus :one
UPDATE sandbox_instance
SET status = @status, error = @error, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: CompleteSandboxInstanceCreate :one
UPDATE sandbox_instance
SET status = 'running', local_ref = @local_ref, endpoint_info = @endpoint_info, error = NULL, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: MarkSandboxInstanceFailed :one
UPDATE sandbox_instance
SET status = 'failed', error = @error, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: MarkSandboxInstanceStopped :one
UPDATE sandbox_instance
SET status = 'stopped', error = NULL, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: MarkSandboxInstanceRunning :one
UPDATE sandbox_instance
SET status = 'running', error = NULL, updated_at = now()
WHERE id = @id
RETURNING *;

-- name: DeleteSandboxInstance :exec
DELETE FROM sandbox_instance
WHERE id = @id;

-- name: CreateSandboxJob :one
INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, instance_id, type, status, payload)
VALUES (@workspace_id, @initiator_user_id, @node_id, @instance_id, @type, 'queued', @payload)
RETURNING *;

-- name: ClaimSandboxJobsForNode :many
WITH next_jobs AS (
    SELECT id
    FROM sandbox_job
    WHERE node_id = @node_id AND status = 'queued'
    ORDER BY created_at ASC
    LIMIT @limit_count
    FOR UPDATE SKIP LOCKED
)
UPDATE sandbox_job sj
SET status = 'dispatched', lease_until = now() + make_interval(secs => @lease_seconds::double precision), updated_at = now()
FROM next_jobs
WHERE sj.id = next_jobs.id
RETURNING sj.*;

-- name: SetSandboxJobRunning :one
UPDATE sandbox_job
SET status = 'running', started_at = COALESCE(started_at, now()), updated_at = now()
WHERE id = $1 AND status IN ('dispatched', 'running')
RETURNING *;

-- name: SetSandboxJobToken :exec
UPDATE sandbox_job
SET job_token_hash = @job_token_hash, job_token_expires_at = @job_token_expires_at, updated_at = now()
WHERE id = @id;

-- name: GetSandboxJobByTokenHash :one
SELECT * FROM sandbox_job
WHERE job_token_hash = $1 AND job_token_expires_at > now();

-- name: CompleteSandboxJob :one
UPDATE sandbox_job
SET status = 'completed', result = @result, error = NULL, completed_at = now(), updated_at = now()
WHERE id = @id AND status IN ('dispatched', 'running')
RETURNING *;

-- name: FailSandboxJob :one
UPDATE sandbox_job
SET status = 'failed', error = @error, completed_at = now(), updated_at = now()
WHERE id = @id AND status IN ('queued', 'dispatched', 'running')
RETURNING *;

-- name: ListSandboxJobsByInstance :many
SELECT * FROM sandbox_job
WHERE instance_id = $1
ORDER BY created_at DESC;
