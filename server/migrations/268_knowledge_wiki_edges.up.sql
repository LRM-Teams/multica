-- LRM-1000: Wiki pages (context/decision) + explicit knowledge edges.
-- No Neo4j/Graphiti — Postgres adjacency only.

ALTER TABLE team_knowledge_item
  DROP CONSTRAINT IF EXISTS team_knowledge_item_kind_check;

ALTER TABLE team_knowledge_item
  ADD CONSTRAINT team_knowledge_item_kind_check
  CHECK (kind IN (
    'memory', 'pattern', 'skill', 'policy', 'troubleshooting',
    'context', 'decision'
  ));

CREATE TABLE IF NOT EXISTS team_knowledge_edge (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  edge_type TEXT NOT NULL CHECK (edge_type IN (
    'derived_from', 'about', 'shared_to', 'supersedes', 'owned_by'
  )),
  from_kind TEXT NOT NULL CHECK (from_kind IN (
    'team_knowledge', 'agent_memory', 'skill', 'issue', 'channel', 'project', 'agent', 'member'
  )),
  from_id UUID NOT NULL,
  to_kind TEXT NOT NULL CHECK (to_kind IN (
    'team_knowledge', 'agent_memory', 'skill', 'issue', 'channel', 'project', 'agent', 'member'
  )),
  to_id UUID NOT NULL,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_by_type TEXT NOT NULL CHECK (created_by_type IN ('member', 'agent', 'system')),
  created_by_id UUID,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (workspace_id, edge_type, from_kind, from_id, to_kind, to_id)
);

CREATE INDEX IF NOT EXISTS idx_team_knowledge_edge_from
  ON team_knowledge_edge (workspace_id, from_kind, from_id);
CREATE INDEX IF NOT EXISTS idx_team_knowledge_edge_to
  ON team_knowledge_edge (workspace_id, to_kind, to_id);
CREATE INDEX IF NOT EXISTS idx_team_knowledge_edge_type
  ON team_knowledge_edge (workspace_id, edge_type);
