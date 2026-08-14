ALTER TABLE research_branch DROP CONSTRAINT IF EXISTS research_v6_branch_xxl_fk;
ALTER TABLE research_branch DROP CONSTRAINT IF EXISTS research_v6_branch_attempt_fk;
ALTER TABLE research_branch DROP CONSTRAINT IF EXISTS research_v6_branch_cycle_fk;
ALTER TABLE research_branch DROP COLUMN IF EXISTS current_xxl_version_id, DROP COLUMN IF EXISTS created_by_attempt_id,
 DROP COLUMN IF EXISTS created_by_director_cycle_id, DROP COLUMN IF EXISTS reason_detail, DROP COLUMN IF EXISTS reason_code,
 DROP COLUMN IF EXISTS state_version, DROP COLUMN IF EXISTS scope, DROP COLUMN IF EXISTS goal_version;
DROP TABLE IF EXISTS research_node_steward_assignment;
DROP TABLE IF EXISTS research_branch_frontier;
DROP TABLE IF EXISTS research_node_branch;
ALTER TABLE research_insight DROP CONSTRAINT IF EXISTS research_insight_current_v6_version_fk;
ALTER TABLE research_insight DROP COLUMN IF EXISTS current_version_id;
DROP TABLE IF EXISTS research_insight_version;
DROP TABLE IF EXISTS research_result_node;
