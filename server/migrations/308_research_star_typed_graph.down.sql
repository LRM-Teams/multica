-- LRM-1505 down：撤销 308_research_star_typed_graph。

DROP TABLE IF EXISTS research_graph_command;
ALTER TABLE research_session DROP COLUMN IF EXISTS graph_version;

-- 收敛前先把持有被移除取值的行重映射到仍保留的取值，否则真库回滚会因
-- CHECK 约束变窄而失败（lint：check_constraint_lint_test 强制）。
UPDATE research_graph_edge
SET edge_type = 'supports'
WHERE edge_type IN ('deepens', 'derived_from', 'merged_from', 'superseded_by', 'restart_of', 'invalidated_by');

UPDATE research_graph_node
SET status = 'abandoned'
WHERE status IN ('superseded', 'invalidated', 'restarted', 'deprecated');

UPDATE research_graph_node
SET node_type = 'finding'
WHERE node_type = 'conclusion';

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
