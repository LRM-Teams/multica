-- name: CreateSandboxNode :one
INSERT INTO sandbox_node (node_key, name, owner_user_id, capabilities, max_concurrency, metadata)
VALUES (@node_key, @name, @owner_user_id, @capabilities, @max_concurrency, @metadata)
RETURNING *;

-- name: UpsertSandboxNodeRegistration :one
INSERT INTO sandbox_node (node_key, name, owner_user_id, status, capabilities, max_concurrency, metadata, last_seen_at)
VALUES (@node_key, @name, @owner_user_id, 'online', @capabilities, @max_concurrency, @metadata, now())
ON CONFLICT (node_key)
DO UPDATE SET
    name = EXCLUDED.name,
    owner_user_id = COALESCE(sandbox_node.owner_user_id, EXCLUDED.owner_user_id),
    status = 'online',
    capabilities = EXCLUDED.capabilities,
    max_concurrency = EXCLUDED.max_concurrency,
    metadata = EXCLUDED.metadata,
    last_seen_at = now(),
    updated_at = now()
WHERE sandbox_node.deleted_at IS NULL
RETURNING *;

-- name: ListSandboxNodesByOwner :many
SELECT * FROM sandbox_node
WHERE owner_user_id = $1 AND deleted_at IS NULL
ORDER BY created_at ASC;

-- name: CountSandboxInstancesByNode :one
SELECT COUNT(*)::bigint FROM sandbox_instance
WHERE node_id = $1;

-- name: CountSandboxInstancesGroupedByNode :many
SELECT node_id, COUNT(*)::bigint AS instance_count
FROM sandbox_instance
WHERE node_id = ANY(@node_ids::uuid[])
GROUP BY node_id;

-- name: GetSandboxNodeLiveness :one
SELECT status, last_seen_at, deleted_at FROM sandbox_node
WHERE id = $1;

-- name: GetSandboxNode :one
SELECT * FROM sandbox_node
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetSandboxNodeForOwner :one
SELECT * FROM sandbox_node
WHERE id = @id AND owner_user_id = @owner_user_id AND deleted_at IS NULL;

-- name: GetSandboxNodeByKey :one
SELECT * FROM sandbox_node
WHERE node_key = $1 AND deleted_at IS NULL;

-- name: UpdateSandboxNodeNameForOwner :one
UPDATE sandbox_node
SET name = @name, updated_at = now()
WHERE id = @id AND owner_user_id = @owner_user_id AND deleted_at IS NULL
RETURNING *;

-- name: UpdateSandboxNodeDefaultTemplateForOwner :one
UPDATE sandbox_node
SET metadata = jsonb_set(
        COALESCE(metadata, '{}'::jsonb),
        '{cube_template_id}',
        to_jsonb(@cube_template_id::text),
        true
    ),
    updated_at = now()
WHERE id = @id AND owner_user_id = @owner_user_id AND deleted_at IS NULL
RETURNING *;

-- name: DeleteSandboxNodeForOwner :exec
WITH deleted AS (
    UPDATE sandbox_node
    SET deleted_at = now(), status = 'offline', updated_at = now()
    WHERE sandbox_node.id = @id AND sandbox_node.owner_user_id = @owner_user_id AND sandbox_node.deleted_at IS NULL
    RETURNING id
)
UPDATE sandbox_node_token
SET revoked_at = now()
WHERE node_id IN (SELECT id FROM deleted) AND revoked_at IS NULL;

-- name: TouchSandboxNodeHeartbeat :one
UPDATE sandbox_node
SET status = 'online', last_seen_at = now(), updated_at = now(), metadata = @metadata
WHERE id = @id AND deleted_at IS NULL
RETURNING *;

-- name: TouchSandboxNodeLiveness :exec
UPDATE sandbox_node
SET status = 'online', last_seen_at = now(), updated_at = now()
WHERE id = @id AND deleted_at IS NULL;

-- name: MarkStaleSandboxNodesOffline :exec
UPDATE sandbox_node
SET status = 'offline', updated_at = now()
WHERE status = 'online'
  AND deleted_at IS NULL
  AND (last_seen_at IS NULL OR last_seen_at < now() - make_interval(secs => @stale_seconds::double precision));

-- name: SetSandboxNodeOffline :exec
UPDATE sandbox_node
SET status = 'offline', updated_at = now()
WHERE id = $1 AND deleted_at IS NULL;

-- name: CreateSandboxNodeToken :one
INSERT INTO sandbox_node_token (node_id, name, token_hash, token_prefix, expires_at, created_by)
SELECT @node_id, @name, @token_hash, @token_prefix, @expires_at, @created_by
WHERE EXISTS (
    SELECT 1 FROM sandbox_node
    WHERE id = @node_id AND owner_user_id = @created_by AND deleted_at IS NULL
)
RETURNING *;

-- name: GetSandboxNodeTokenByHash :one
SELECT snt.* FROM sandbox_node_token snt
JOIN sandbox_node sn ON sn.id = snt.node_id
WHERE snt.token_hash = $1 AND snt.revoked_at IS NULL AND (snt.expires_at IS NULL OR snt.expires_at > now()) AND sn.deleted_at IS NULL;

-- name: RevokeSandboxNodeToken :exec
UPDATE sandbox_node_token
SET revoked_at = now()
WHERE id = $1;

-- name: UpsertSandboxWorkspaceBinding :one
INSERT INTO sandbox_workspace_binding (workspace_id, node_id, enabled, policy, created_by)
SELECT @workspace_id, @node_id, true, @policy, @created_by
WHERE EXISTS (
    SELECT 1 FROM sandbox_node
    WHERE id = @node_id AND owner_user_id = @created_by AND deleted_at IS NULL
)
ON CONFLICT (workspace_id, node_id)
DO UPDATE SET enabled = true, policy = EXCLUDED.policy, updated_at = now()
RETURNING *;

-- name: ListSandboxWorkspaceBindings :many
SELECT swb.*, sn.node_key, sn.owner_user_id, sn.name, sn.status, sn.capabilities, sn.max_concurrency, sn.last_seen_at
FROM sandbox_workspace_binding swb
JOIN sandbox_node sn ON sn.id = swb.node_id
WHERE swb.workspace_id = $1 AND sn.deleted_at IS NULL
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
WHERE swb.workspace_id = $1 AND swb.enabled = true AND sn.status = 'online' AND sn.deleted_at IS NULL
ORDER BY sn.last_seen_at DESC NULLS LAST, sn.created_at ASC
LIMIT 1;

-- name: PickSandboxNodeForWorkspace :one
SELECT sn.*
FROM sandbox_workspace_binding swb
JOIN sandbox_node sn ON sn.id = swb.node_id
WHERE swb.workspace_id = @workspace_id AND swb.node_id = @node_id AND swb.enabled = true AND sn.status = 'online' AND sn.deleted_at IS NULL
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

-- name: UpdateSandboxInstanceMetadata :one
UPDATE sandbox_instance
SET metadata = @metadata, updated_at = now()
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

-- name: CreateSandboxDeleteJob :one
INSERT INTO sandbox_job (workspace_id, initiator_user_id, node_id, instance_id, type, status, payload)
VALUES (@workspace_id, @initiator_user_id, @node_id, @instance_id, 'delete', 'queued', @payload)
ON CONFLICT DO NOTHING
RETURNING *;

-- name: GetActiveSandboxDeleteJob :one
SELECT * FROM sandbox_job
WHERE instance_id = @instance_id
  AND type = 'delete'
  AND status IN ('queued', 'dispatched', 'running')
ORDER BY created_at ASC
LIMIT 1;

-- name: ClaimSandboxJobsForNode :many
WITH next_jobs AS (
    SELECT id
    FROM sandbox_job
    WHERE sandbox_job.node_id = @node_id AND sandbox_job.status = 'queued'
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
WHERE job_token_hash = sqlc.arg('job_token_hash')::text AND job_token_expires_at > now();

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

-- name: CreateSandboxSnapshot :one
INSERT INTO sandbox_snapshot (
    workspace_id, node_id, instance_id, creator_user_id,
    cube_snapshot_id, name, description, status, metadata
)
VALUES (
    @workspace_id, @node_id, @instance_id, @creator_user_id,
    @cube_snapshot_id, @name, @description, @status, @metadata
)
RETURNING *;

-- name: ListSandboxSnapshotsByNode :many
SELECT *
FROM sandbox_snapshot
WHERE workspace_id = @workspace_id AND node_id = @node_id
ORDER BY created_at DESC;

-- name: GetSandboxSnapshotForWorkspace :one
SELECT *
FROM sandbox_snapshot
WHERE id = @id AND workspace_id = @workspace_id;

-- name: MarkSandboxSnapshotReady :one
UPDATE sandbox_snapshot
SET cube_snapshot_id = @cube_snapshot_id,
    status = 'ready',
    error = NULL,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkSandboxSnapshotFailed :one
UPDATE sandbox_snapshot
SET status = 'failed',
    error = @error,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkSandboxSnapshotDeleting :one
UPDATE sandbox_snapshot
SET status = 'deleting',
    error = NULL,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkSandboxSnapshotReadyAgain :one
UPDATE sandbox_snapshot
SET status = 'ready',
    error = @error,
    updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: DeleteSandboxSnapshot :exec
DELETE FROM sandbox_snapshot
WHERE id = @id AND workspace_id = @workspace_id;


-- name: GetSweLegoTemplateCache :one
SELECT node_id, cache_key, parent_template_id, task_template_id, status, error,
       builder_instance_id, created_at, updated_at
FROM swe_lego_template_cache
WHERE node_id = $1 AND cache_key = $2;

-- name: ClaimSweLegoTemplateBuild :one
-- Reclaim policy: failed rows are always reclaimable; building rows become
-- reclaimable once updated_at is older than the materializer's 20-minute
-- build timeout (25m margin), so a builder killed by a deploy/restart does
-- not wedge the cache key in "building" forever.
INSERT INTO swe_lego_template_cache (node_id, cache_key, parent_template_id, status)
VALUES ($1, $2, $3, 'building')
ON CONFLICT (node_id, cache_key) DO UPDATE
SET status = 'building',
    task_template_id = NULL,
    error = NULL,
    builder_instance_id = NULL,
    updated_at = now()
WHERE swe_lego_template_cache.status = 'failed'
   OR (swe_lego_template_cache.status = 'building'
       AND swe_lego_template_cache.updated_at < now() - interval '25 minutes')
RETURNING node_id, cache_key, parent_template_id, task_template_id, status, error,
          builder_instance_id, created_at, updated_at;

-- name: SetSweLegoTemplateBuildBuilder :exec
UPDATE swe_lego_template_cache
SET builder_instance_id = $3,
    updated_at = now()
WHERE node_id = $1 AND cache_key = $2 AND status = 'building';

-- name: CompleteSweLegoTemplateBuild :one
UPDATE swe_lego_template_cache
SET task_template_id = sqlc.arg('task_template_id')::text,
    status = 'ready',
    error = NULL,
    builder_instance_id = NULL,
    updated_at = now()
WHERE node_id = sqlc.arg('node_id') AND cache_key = sqlc.arg('cache_key') AND status = 'building'
RETURNING node_id, cache_key, parent_template_id, task_template_id, status, error,
          builder_instance_id, created_at, updated_at;

-- name: FailSweLegoTemplateBuild :one
UPDATE swe_lego_template_cache
SET status = 'failed',
    task_template_id = NULL,
    error = $3,
    builder_instance_id = NULL,
    updated_at = now()
WHERE node_id = $1 AND cache_key = $2 AND status = 'building'
RETURNING node_id, cache_key, parent_template_id, task_template_id, status, error,
          builder_instance_id, created_at, updated_at;
