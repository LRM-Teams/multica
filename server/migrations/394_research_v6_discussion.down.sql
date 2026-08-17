ALTER TABLE research_integration_round DROP COLUMN IF EXISTS output_insight_version_id, DROP COLUMN IF EXISTS discussion_id_v6,
 DROP COLUMN IF EXISTS status_v6, DROP COLUMN IF EXISTS mode, DROP COLUMN IF EXISTS input_set_hash,
 DROP COLUMN IF EXISTS branch_scope_hash, DROP COLUMN IF EXISTS goal_version_v6, DROP COLUMN IF EXISTS work_item_attempt_id;
DROP TABLE IF EXISTS research_discussion_vote;
DROP TABLE IF EXISTS research_discussion_turn;
DROP TABLE IF EXISTS research_discussion_participant;
DROP TABLE IF EXISTS research_discussion_input;
DROP TABLE IF EXISTS research_discussion;
