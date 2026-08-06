-- name: UpsertIssueWorkNode :one
INSERT INTO work_node (
    workspace_id,
    kind,
    title,
    description,
    owner_type,
    owner_id,
    status,
    primary_channel_id,
    linked_issue_id
) VALUES (
    sqlc.arg('workspace_id'),
    'issue',
    sqlc.arg('title'),
    sqlc.arg('description'),
    sqlc.arg('owner_type'),
    sqlc.narg('owner_id'),
    sqlc.arg('status'),
    sqlc.narg('primary_channel_id'),
    sqlc.arg('issue_id')
)
ON CONFLICT (workspace_id, linked_issue_id)
WHERE kind = 'issue' AND linked_issue_id IS NOT NULL
DO UPDATE SET
    title = EXCLUDED.title,
    description = EXCLUDED.description,
    owner_type = EXCLUDED.owner_type,
    owner_id = EXCLUDED.owner_id,
    status = EXCLUDED.status,
    primary_channel_id = EXCLUDED.primary_channel_id,
    updated_at = now()
RETURNING *;

-- name: GetWorkNodeByIssue :one
SELECT *
FROM work_node
WHERE workspace_id = $1
  AND kind = 'issue'
  AND linked_issue_id = $2;

-- name: GetWorkNodeByID :one
SELECT *
FROM work_node
WHERE id = $1;

-- name: UpsertOpenWaitsOnEdge :one
INSERT INTO work_edge (
    workspace_id,
    from_node_id,
    to_node_id,
    kind,
    status,
    evidence
) VALUES (
    sqlc.arg('workspace_id'),
    sqlc.arg('from_node_id'),
    sqlc.arg('to_node_id'),
    'waits_on',
    'open',
    sqlc.arg('evidence')
)
ON CONFLICT (workspace_id, from_node_id, to_node_id, kind)
WHERE status = 'open'
DO UPDATE SET
    evidence = EXCLUDED.evidence,
    updated_at = now()
RETURNING *;

-- name: ResolveWaitsOnEdge :many
UPDATE work_edge
SET status = 'resolved',
    updated_at = now()
WHERE workspace_id = sqlc.arg('workspace_id')
  AND from_node_id = sqlc.arg('from_node_id')
  AND to_node_id = sqlc.arg('to_node_id')
  AND kind = 'waits_on'
  AND status = 'open'
RETURNING *;

-- name: ListOpenWaitsOnFromNode :many
SELECT *
FROM work_edge
WHERE workspace_id = $1
  AND from_node_id = $2
  AND kind = 'waits_on'
  AND status = 'open'
ORDER BY created_at ASC;

-- name: CountOpenUnresolvedWaitsOn :one
SELECT count(*)
FROM work_edge edge
JOIN work_node prerequisite ON prerequisite.id = edge.to_node_id
WHERE edge.workspace_id = $1
  AND edge.from_node_id = $2
  AND edge.kind = 'waits_on'
  AND edge.status = 'open'
  AND prerequisite.status NOT IN ('done', 'cancelled');

-- name: HasAnyWaitsOnEdge :one
SELECT EXISTS (
    SELECT 1
    FROM work_edge
    WHERE workspace_id = $1
      AND from_node_id = $2
      AND kind = 'waits_on'
);

-- name: ListResolvedWaitsOnPrerequisiteIDs :many
SELECT DISTINCT to_node_id
FROM work_edge
WHERE workspace_id = $1
  AND from_node_id = $2
  AND kind = 'waits_on'
  AND status = 'resolved'
ORDER BY to_node_id ASC;

-- name: TouchWorkNodeWendyNudge :one
UPDATE work_node
SET last_wendy_nudge_at = now(),
    last_wendy_nudge_kind = sqlc.arg('nudge_kind'),
    updated_at = now()
WHERE id = sqlc.arg('id')
  AND workspace_id = sqlc.arg('workspace_id')
RETURNING *;
