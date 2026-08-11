-- name: CountAgentMemoryWriteEventsByAgent :one
SELECT COUNT(*)::bigint AS count
FROM agent_memory_write_event
WHERE agent_id = $1;

-- name: HasRecentAgentMemoryWrite :one
SELECT EXISTS(
    SELECT 1
    FROM agent_memory_write_event
    WHERE agent_id = $1
      AND rel_path = $2
      AND content_hash = $3
      AND created_at > $4
) AS exists;

-- name: InsertAgentMemoryWriteEvent :one
INSERT INTO agent_memory_write_event (
    workspace_id, agent_id, runtime_id, task_id,
    rel_path, scope_type, file_key, content_hash, delta_chars
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9
)
RETURNING *;

-- name: UpsertAgentMemoryWriteDaily :one
INSERT INTO agent_memory_write_daily (workspace_id, agent_id, write_date, write_count)
VALUES ($1, $2, $3, 1)
ON CONFLICT (agent_id, write_date) DO UPDATE
SET write_count = agent_memory_write_daily.write_count + 1,
    updated_at = now()
RETURNING *;
