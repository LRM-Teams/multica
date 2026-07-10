-- name: CreateTaskMessage :one
INSERT INTO task_message (task_id, seq, type, tool, content, input, output)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING *;

-- name: ListTaskMessages :many
SELECT * FROM task_message
WHERE task_id = $1
ORDER BY seq ASC;

-- name: ListTaskMessagesSince :many
SELECT * FROM task_message
WHERE task_id = $1 AND seq > $2
ORDER BY seq ASC;

-- name: DeleteTaskMessages :exec
DELETE FROM task_message
WHERE task_id = $1;

-- name: GetMaxTaskMessageSeq :one
-- Returns the highest task_message.seq for a task, or 0 when none exist. Used by
-- CloseSegmentForEvent to compute the closing segment's end_seq. Both sides are
-- text so the caller passes the text agent_run_id (= task.ID) without UUID
-- parsing; the underlying task_id column is UUID (index not used on this path).
SELECT COALESCE(MAX(seq), 0)::integer AS max_seq
FROM task_message
WHERE task_id::text = $1::text;
