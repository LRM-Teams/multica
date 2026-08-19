-- name: CreateEnvCheckpoint :one
INSERT INTO env_checkpoint (
    workspace_id, project_id, event_ref, checkpoint_kind,
    env_id_map, sandbox_refs, db_snapshot, entropy_score,
    save_timeout_ms, save_status, save_error, resume_trigger,
    save_mode
) VALUES (
    @workspace_id, @project_id, @event_ref, @checkpoint_kind,
    @env_id_map, @sandbox_refs, @db_snapshot, @entropy_score,
    @save_timeout_ms, @save_status, @save_error, sqlc.narg(resume_trigger),
    @save_mode
)
RETURNING *;

-- name: GetEnvCheckpointForWorkspace :one
SELECT * FROM env_checkpoint
WHERE id = @id AND workspace_id = @workspace_id;

-- name: ListEnvCheckpointsForProject :many
SELECT * FROM env_checkpoint
WHERE workspace_id = @workspace_id AND project_id = @project_id
ORDER BY created_at DESC;

-- name: UpdateEnvCheckpointSaveStatus :one
UPDATE env_checkpoint
SET save_status = @save_status, save_error = @save_error, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: UpdateEnvCheckpointSaveMode :one
UPDATE env_checkpoint
SET save_mode = @save_mode, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: DeleteEnvCheckpoint :exec
-- Cascades the savepoint ownership rows migration 246 added and the
-- env_checkpoint_lane rows migration 247 added. The Cube templates themselves are
-- scheduled for deletion through delete_template jobs before this runs, since
-- once this row is gone nothing records that they exist.
DELETE FROM env_checkpoint
WHERE id = @id AND workspace_id = @workspace_id;
