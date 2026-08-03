-- name: UpsertSourceTask :one
INSERT INTO source_task (workspace_id, type, payload, content_hash)
VALUES ($1, $2, $3, $4)
ON CONFLICT (workspace_id, content_hash) DO UPDATE
SET content_hash = EXCLUDED.content_hash
RETURNING id, workspace_id, type, payload, content_hash, created_at;

-- name: GetSourceTaskForWorkspace :one
SELECT id, workspace_id, type, payload, content_hash, created_at
FROM source_task
WHERE id = $1 AND workspace_id = $2;
