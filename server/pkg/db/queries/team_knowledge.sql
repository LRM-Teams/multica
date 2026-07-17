-- name: ListActiveTeamKnowledgeForExecution :many
SELECT id, kind, title, content, metadata, updated_at
FROM team_knowledge_item
WHERE workspace_id = $1 AND status = 'active'
ORDER BY updated_at DESC, id
LIMIT 24;
