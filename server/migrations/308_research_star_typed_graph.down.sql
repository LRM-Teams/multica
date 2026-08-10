-- LRM-1505 down：撤销 308_research_star_typed_graph。

DROP TABLE IF EXISTS research_graph_command;
ALTER TABLE research_session DROP COLUMN IF EXISTS graph_version;

ALTER TABLE research_graph_edge DROP CONSTRAINT IF EXISTS research_graph_edge_edge_type_check;
ALTER TABLE research_graph_edge ADD CONSTRAINT research_graph_edge_edge_type_check
  CHECK (edge_type IN ('leads_to', 'supports', 'contradicts', 'supersedes', 'abandons'));

ALTER TABLE research_graph_node DROP CONSTRAINT IF EXISTS research_graph_node_status_check;
ALTER TABLE research_graph_node ADD CONSTRAINT research_graph_node_status_check
  CHECK (status IN ('active', 'done', 'abandoned'));

ALTER TABLE research_graph_node DROP CONSTRAINT IF EXISTS research_graph_node_node_type_check;
ALTER TABLE research_graph_node ADD CONSTRAINT research_graph_node_node_type_check
  CHECK (node_type IN (
    'goal', 'subquestion', 'probe', 'finding', 'conflict', 'dead_end',
    'refuted', 'pivot', 'roster_change', 'stage_gate', 'agent_activity'
  ));

DROP INDEX IF EXISTS research_graph_node_round_idx;
DROP INDEX IF EXISTS research_graph_node_cluster_idx;

ALTER TABLE research_graph_node
  DROP COLUMN IF EXISTS invalidated_at,
  DROP COLUMN IF EXISTS superseded_at,
  DROP COLUMN IF EXISTS invalidated_by,
  DROP COLUMN IF EXISTS restart_of,
  DROP COLUMN IF EXISTS superseded_by,
  DROP COLUMN IF EXISTS merged_from,
  DROP COLUMN IF EXISTS derived_from,
  DROP COLUMN IF EXISTS goal_version_id,
  DROP COLUMN IF EXISTS conclusion_count,
  DROP COLUMN IF EXISTS document_count,
  DROP COLUMN IF EXISTS confidence,
  DROP COLUMN IF EXISTS cluster_id,
  DROP COLUMN IF EXISTS round,
  DROP COLUMN IF EXISTS level;

DROP TABLE IF EXISTS research_graph_cluster;
