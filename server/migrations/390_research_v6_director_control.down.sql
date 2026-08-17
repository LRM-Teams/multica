DROP TRIGGER IF EXISTS research_v6_team_cap_guard ON research_team_membership;
DROP FUNCTION IF EXISTS research_v6_team_cap_guard_fn();
DROP TABLE IF EXISTS research_team_membership;
DROP TABLE IF EXISTS research_steering_assessment;
DROP TABLE IF EXISTS research_director_brief_page;
DROP TABLE IF EXISTS research_director_cycle;
ALTER TABLE research_session DROP CONSTRAINT IF EXISTS research_session_current_v6_director_fk;
DROP TABLE IF EXISTS research_director_assignment;
ALTER TABLE research_session
  DROP COLUMN IF EXISTS current_director_assignment_id,
  DROP COLUMN IF EXISTS director_state_version,
  DROP COLUMN IF EXISTS v6_projection_version;
ALTER TABLE research_session DROP CONSTRAINT IF EXISTS research_session_status_check;
ALTER TABLE research_session ADD CONSTRAINT research_session_status_check
  CHECK (status IN ('drafting','running','awaiting_user_confirm','completed','archived','paused','failed','cancelled'));
ALTER TABLE research_session ALTER COLUMN fleet_id SET NOT NULL;
