-- LRM-1000 knowledge wiki queries (implemented in knowledge_wiki_manual.sql.go).

-- name: InsertTeamKnowledgeItem :one
INSERT INTO team_knowledge_item (
  workspace_id, kind, title, content, source_candidate_ids, status, metadata
) VALUES (
  @workspace_id, @kind, @title, @content, '{}'::uuid[], 'active', @metadata
)
RETURNING id, workspace_id, kind, title, content, status, metadata, created_at, updated_at;

-- name: GetTeamKnowledgeItemByID :one
SELECT id, workspace_id, kind, title, content, status, metadata, created_at, updated_at
FROM team_knowledge_item
WHERE workspace_id = @workspace_id AND id = @id;

-- name: ArchiveTeamKnowledgeItem :exec
UPDATE team_knowledge_item
SET status = 'archived', updated_at = now()
WHERE workspace_id = @workspace_id AND id = @id;

-- name: InsertTeamKnowledgeEdge :one
INSERT INTO team_knowledge_edge (
  workspace_id, edge_type, from_kind, from_id, to_kind, to_id, metadata, created_by_type, created_by_id
) VALUES (
  @workspace_id, @edge_type, @from_kind, @from_id, @to_kind, @to_id, @metadata, @created_by_type, @created_by_id
)
ON CONFLICT (workspace_id, edge_type, from_kind, from_id, to_kind, to_id) DO UPDATE
SET metadata = EXCLUDED.metadata
RETURNING id, workspace_id, edge_type, from_kind, from_id, to_kind, to_id, metadata, created_by_type, created_by_id, created_at;

-- name: ListTeamKnowledgeEdgesForNode :many
SELECT id, workspace_id, edge_type, from_kind, from_id, to_kind, to_id, metadata, created_by_type, created_by_id, created_at
FROM team_knowledge_edge
WHERE workspace_id = @workspace_id
  AND (
    (from_kind = @node_kind AND from_id = @node_id)
    OR (to_kind = @node_kind AND to_id = @node_id)
  )
ORDER BY created_at DESC, id;

-- name: ListTeamKnowledgeSeedPagesForExecution :many
-- Hop-0 seeds: context/decision scoped to channel/project, or about-linked to task subjects.
SELECT DISTINCT k.id, k.kind, k.title, k.content, k.metadata, k.updated_at
FROM team_knowledge_item k
WHERE k.workspace_id = @workspace_id
  AND k.status = 'active'
  AND (
    (
      k.kind = 'context'
      AND @channel_id::text <> ''
      AND (
        k.metadata->>'subject_id' = @channel_id::text
        OR k.metadata->'applies'->>'channel_id' = @channel_id::text
        OR EXISTS (
          SELECT 1 FROM jsonb_array_elements_text(COALESCE(k.metadata->'applies'->'channel_ids', '[]'::jsonb)) cid
          WHERE cid = @channel_id::text
        )
      )
    )
    OR (
      k.kind = 'decision'
      AND @project_id::text <> ''
      AND (
        k.metadata->>'subject_id' = @project_id::text
        OR k.metadata->'applies'->>'project_id' = @project_id::text
        OR EXISTS (
          SELECT 1 FROM jsonb_array_elements_text(COALESCE(k.metadata->'applies'->'project_ids', '[]'::jsonb)) pid
          WHERE pid = @project_id::text
        )
      )
    )
    OR EXISTS (
      SELECT 1 FROM team_knowledge_edge e
      WHERE e.workspace_id = k.workspace_id
        AND e.edge_type = 'about'
        AND e.from_kind = 'team_knowledge' AND e.from_id = k.id
        AND (
          (@issue_id::text <> '' AND e.to_kind = 'issue' AND e.to_id::text = @issue_id::text)
          OR (@channel_id::text <> '' AND e.to_kind = 'channel' AND e.to_id::text = @channel_id::text)
          OR (@project_id::text <> '' AND e.to_kind = 'project' AND e.to_id::text = @project_id::text)
        )
    )
  )
  AND NOT EXISTS (
    SELECT 1 FROM team_knowledge_edge s
    WHERE s.workspace_id = k.workspace_id
      AND s.edge_type = 'supersedes'
      AND s.to_kind = 'team_knowledge' AND s.to_id = k.id
  )
ORDER BY k.updated_at DESC, k.id
LIMIT 12;

-- name: ListTeamKnowledgeNeighborPageIDs :many
-- One-hop undirected neighbors that are active team_knowledge pages.
SELECT DISTINCT (CASE
         WHEN e.from_kind = 'team_knowledge' AND e.from_id = @page_id THEN e.to_id
         ELSE e.from_id
       END)::uuid AS neighbor_id
FROM team_knowledge_edge e
WHERE e.workspace_id = @workspace_id
  AND (
    (e.from_kind = 'team_knowledge' AND e.from_id = @page_id AND e.to_kind = 'team_knowledge')
    OR (e.to_kind = 'team_knowledge' AND e.to_id = @page_id AND e.from_kind = 'team_knowledge')
  )
  AND e.edge_type IN ('derived_from', 'about', 'shared_to', 'supersedes', 'owned_by');

-- name: ListTeamKnowledgePagesByIDs :many
SELECT k.id, k.kind, k.title, k.content, k.metadata, k.updated_at
FROM team_knowledge_item k
WHERE k.workspace_id = @workspace_id
  AND k.status = 'active'
  AND k.id = ANY(@ids::uuid[])
  AND NOT EXISTS (
    SELECT 1 FROM team_knowledge_edge s
    WHERE s.workspace_id = k.workspace_id
      AND s.edge_type = 'supersedes'
      AND s.to_kind = 'team_knowledge' AND s.to_id = k.id
  )
ORDER BY k.updated_at DESC, k.id;
