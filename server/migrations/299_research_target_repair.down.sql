DROP TRIGGER IF EXISTS research_target_repair_decision_immutable_guard
  ON research_target_repair;
DROP FUNCTION IF EXISTS research_target_repair_decision_immutable();
DROP TABLE IF EXISTS research_target_repair;
DROP FUNCTION IF EXISTS research_repair_action_allowed(TEXT, TEXT);
