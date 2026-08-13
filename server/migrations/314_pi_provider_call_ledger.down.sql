DROP TRIGGER IF EXISTS pi_provider_call_identity_guard ON pi_provider_call;
DROP FUNCTION IF EXISTS validate_mixed_rl_provider_call_identity();
DROP TRIGGER IF EXISTS pi_message_consumption_effective_call_guard
  ON pi_message_consumption;
DROP FUNCTION IF EXISTS validate_mixed_rl_consumption_effective_call();
DROP TRIGGER IF EXISTS env_dispatch_turn_capture_batch_boundary_guard
  ON env_dispatch_turn_capture_batch;
DROP FUNCTION IF EXISTS validate_mixed_rl_capture_batch_boundary();
DROP TABLE IF EXISTS env_dispatch_run_audit_event;
DROP TABLE IF EXISTS pi_message_consumption;
DROP TABLE IF EXISTS pi_visible_action;
DROP TABLE IF EXISTS pi_provider_call;
DROP TABLE IF EXISTS env_dispatch_delivery_obligation;
DROP TABLE IF EXISTS env_dispatch_turn_capture_batch;
DROP TABLE IF EXISTS env_dispatch_resident_turn;
