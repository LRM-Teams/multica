-- name: CreateTaskMessage :one
INSERT INTO task_message (
    task_id, seq, type, tool, content, input, output,
    visibility
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
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

-- name: PageTaskMessagesInRange :many
-- Keyset-paginated messages within a seq range for one task. lastSeq=0 means
-- "start at the first message >= startSeq". Ordered by (seq, id) so the tie-
-- breaker is stable across pages. maxDiagnosisSegmentTurns pages are bounded
-- to 20 turns; the LIMIT here matches that cap.
SELECT id, task_id, seq, type, tool, content, input, output, created_at, visibility FROM task_message
WHERE task_id::text = $1::text AND seq BETWEEN $2 AND $3 AND (seq > $4 OR (seq = $4 AND id > $5))
ORDER BY seq ASC, id ASC
LIMIT $6;

-- name: CountTaskMessagesInRange :one
-- Identical range predicates to PageTaskMessagesInRange so ExpectedCount cannot
-- disagree with page membership. No cursor condition — counts the full range.
SELECT COUNT(*)::integer AS count FROM task_message
WHERE task_id::text = $1::text AND seq BETWEEN $2 AND $3;

-- name: GetMaxTaskMessageSeq :one
-- Returns the highest task_message.seq for a task, or 0 when none exist. Used by
-- CloseSegmentForEvent to compute the closing segment's end_seq. Both sides are
-- text so the caller passes the text agent_run_id (= task.ID) without UUID
-- parsing; the underlying task_id column is UUID (index not used on this path).
SELECT COALESCE(MAX(seq), 0)::integer AS max_seq
FROM task_message
WHERE task_id::text = $1::text;
