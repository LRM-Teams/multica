DROP TABLE IF EXISTS research_v6_activation_evidence;
DROP TRIGGER IF EXISTS research_v6_branch_xxl_guard ON research_branch;
DROP FUNCTION IF EXISTS research_v6_branch_xxl_guard_fn();
DROP TRIGGER IF EXISTS research_v6_steering_append_only ON research_steering_assessment;
DROP TRIGGER IF EXISTS research_v6_report_review_append_only ON research_report_review;
DROP TRIGGER IF EXISTS research_v6_discussion_turn_append_only ON research_discussion_turn;
DROP TRIGGER IF EXISTS research_v6_absorption_append_only ON research_node_absorption;
DROP TRIGGER IF EXISTS research_v6_result_node_append_only ON research_result_node;
DROP FUNCTION IF EXISTS research_v6_append_only_guard_fn();
