DROP TABLE IF EXISTS issue_derived_agent_assignment;
DROP TABLE IF EXISTS issue_decompose_child;
DROP TABLE IF EXISTS issue_decompose_request;
DROP TABLE IF EXISTS goal_execution_epoch;
DROP INDEX IF EXISTS work_graph_node_frontier_idx;
ALTER TABLE work_graph_node
  DROP COLUMN IF EXISTS effective_completion,
  DROP COLUMN IF EXISTS completion_authority;
