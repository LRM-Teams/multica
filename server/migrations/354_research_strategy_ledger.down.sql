DROP TRIGGER IF EXISTS research_session_strategy_pin ON research_session;
DROP FUNCTION IF EXISTS research_pin_run_strategy();

DROP TRIGGER IF EXISTS workspace_research_strategy_initialize ON workspace;
DROP FUNCTION IF EXISTS research_initialize_workspace_strategy();

DROP TRIGGER IF EXISTS research_run_strategy_assignment_append_only ON research_run_strategy_assignment;
DROP TABLE IF EXISTS research_run_strategy_assignment;

DROP TRIGGER IF EXISTS research_strategy_pointer_transition_guard ON research_strategy_pointer;
DROP FUNCTION IF EXISTS research_validate_strategy_pointer_transition();

DROP TRIGGER IF EXISTS research_strategy_decision_actor_guard ON research_strategy_promotion_decision;
DROP TRIGGER IF EXISTS research_strategy_version_actor_guard ON research_strategy_version;
DROP FUNCTION IF EXISTS research_validate_strategy_actor();

DROP TABLE IF EXISTS research_strategy_pointer;

DROP TRIGGER IF EXISTS research_strategy_decision_append_only ON research_strategy_promotion_decision;
DROP TRIGGER IF EXISTS research_strategy_evaluation_append_only ON research_strategy_evaluation;
DROP TRIGGER IF EXISTS research_strategy_version_append_only ON research_strategy_version;
DROP FUNCTION IF EXISTS research_reject_strategy_ledger_mutation();

DROP TABLE IF EXISTS research_strategy_promotion_decision;
DROP TABLE IF EXISTS research_strategy_evaluation;
DROP TABLE IF EXISTS research_strategy_version;
