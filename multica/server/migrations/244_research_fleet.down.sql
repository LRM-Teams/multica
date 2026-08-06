DROP TABLE IF EXISTS research_fleet_feedback;
DROP TABLE IF EXISTS research_fleet_playbook;
DROP TABLE IF EXISTS research_message;
DROP TABLE IF EXISTS research_stage_eval;
DROP TABLE IF EXISTS research_report;
DROP TABLE IF EXISTS research_source;
DROP TABLE IF EXISTS research_graph_edge;
DROP TABLE IF EXISTS research_graph_node;
DROP TABLE IF EXISTS research_session;
DROP TABLE IF EXISTS research_fleet_member;
DROP TABLE IF EXISTS research_fleet;

ALTER TABLE agent DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role IN ('group_manager'));
