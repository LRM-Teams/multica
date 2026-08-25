-- name: ClaimEnvCheckpointLane :one
-- Claims a lane for materialization. Winning the insert means this caller owns
-- the lane; losing returns no rows, and the caller then reads the existing row
-- to branch on its status. The UNIQUE (checkpoint_id, lane_key) index is what
-- makes that safe under concurrency, so there is no application lock.
--
-- workspace_id is derived from the owning checkpoint rather than passed in, so a
-- lane can never land in a different workspace than its checkpoint. The caller
-- has already loaded the checkpoint (it needs save_mode), so no rows means it
-- lost the race.
INSERT INTO env_checkpoint_lane (checkpoint_id, workspace_id, lane_key, status)
SELECT c.id, c.workspace_id, @lane_key, 'provisioning'
FROM env_checkpoint c
WHERE c.id = @checkpoint_id
ON CONFLICT (checkpoint_id, lane_key) DO NOTHING
RETURNING *;

-- name: GetEnvCheckpointLane :one
SELECT * FROM env_checkpoint_lane
WHERE checkpoint_id = @checkpoint_id
  AND lane_key = @lane_key
  AND workspace_id = @workspace_id;

-- name: ListEnvCheckpointLanes :many
SELECT * FROM env_checkpoint_lane
WHERE checkpoint_id = @checkpoint_id AND workspace_id = @workspace_id
ORDER BY created_at ASC;

-- name: UpdateEnvCheckpointLaneStep :one
-- Records one materialization step's id. COALESCE keeps already-filled steps, so
-- a continued lane never regresses to an earlier state.
UPDATE env_checkpoint_lane
SET instance_id       = COALESCE(sqlc.narg(instance_id), instance_id),
    project_id        = COALESCE(sqlc.narg(project_id), project_id),
    runtime_id        = COALESCE(sqlc.narg(runtime_id), runtime_id),
    task_id           = COALESCE(sqlc.narg(task_id), task_id),
    env_id            = COALESCE(sqlc.narg(env_id), env_id),
    channel_id        = COALESCE(sqlc.narg(channel_id), channel_id),
    chat_session_id   = COALESCE(sqlc.narg(chat_session_id), chat_session_id),
    source_message_id = COALESCE(sqlc.narg(source_message_id), source_message_id),
    updated_at        = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkEnvCheckpointLaneReady :one
UPDATE env_checkpoint_lane
SET status = 'ready', error = NULL, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: MarkEnvCheckpointLaneFailed :one
UPDATE env_checkpoint_lane
SET status = 'failed', error = @error, updated_at = now()
WHERE id = @id AND workspace_id = @workspace_id
RETURNING *;

-- name: CountProvisioningEnvCheckpointLanes :one
-- Deleting a checkpoint would cascade its lane rows away and orphan the
-- sandboxes a provisioning lane is still building, so deletion consults this
-- first and refuses while any lane is provisioning.
SELECT count(*) FROM env_checkpoint_lane
WHERE checkpoint_id = @checkpoint_id
  AND workspace_id = @workspace_id
  AND status = 'provisioning';

-- name: SweepStaleProvisioningEnvCheckpointLanes :many
-- Fails lanes abandoned mid-materialization, in one statement so the staleness
-- test and the write cannot come apart. Listing candidates and failing them in a
-- second round trip would leave a window in which a lane its owner just drove to
-- `ready` gets marked failed anyway; here the `provisioning` predicate is part of
-- the write, and Postgres re-checks it if the row changed under us.
--
-- Deliberately not workspace-scoped: the sweeper runs across all workspaces. The
-- LIMIT bounds one tick's work, and RETURNING gives the caller the swept rows
-- for the audit record. The rows keep their per-step ids so the sandboxes they
-- built stay attributable; releasing those belongs to checkpoint deletion.
UPDATE env_checkpoint_lane AS l
SET status = 'failed', error = @error, updated_at = now()
WHERE l.status = 'provisioning'
  AND l.updated_at < @stale_before
  AND l.id IN (
      SELECT c.id FROM env_checkpoint_lane c
      WHERE c.status = 'provisioning' AND c.updated_at < @stale_before
      ORDER BY c.updated_at ASC
      LIMIT @row_limit
  )
RETURNING *;
