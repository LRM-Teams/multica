-- name: ListIssueDependenciesForWorkspace :many
SELECT d.id, d.issue_id, d.depends_on_issue_id, d.type
FROM issue_dependency d
JOIN issue i ON i.id = d.issue_id
WHERE i.workspace_id = $1;

-- name: ListIssueDependenciesByIssue :many
SELECT id, issue_id, depends_on_issue_id, type
FROM issue_dependency
WHERE issue_id = $1
   OR depends_on_issue_id = $1;
