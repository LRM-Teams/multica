-- name: CreateEnvCheckpoint :one
INSERT INTO env_checkpoint (
    workspace_id, project_id, event_ref, checkpoint_kind,
    env_id_map, sandbox_refs, db_snapshot, entropy_score,
    save_timeout_ms, save_status, save_error, resume_trigger
) VALUES (
    @workspace_id, @project_id, @event_ref, @checkpoint_kind,
    @env_id_map, @sandbox_refs, @db_snapshot, @entropy_score,
    @save_timeout_ms, @save_status, @save_error, sqlc.narg(resume_trigger)
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
