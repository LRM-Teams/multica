DROP TRIGGER IF EXISTS interaction_dag_shared_producer_owner_guard
  ON interaction_dag_segment_provider_call;
DROP FUNCTION IF EXISTS validate_mixed_rl_shared_producer_owner();
-- Removes the INSERT/UPDATE/DELETE lifecycle guard, including frozen
-- capture-gap immutability, before dropping its trigger function.
DROP TRIGGER IF EXISTS env_dispatch_run_audit_event_lifecycle_guard
  ON env_dispatch_run_audit_event;
DROP FUNCTION IF EXISTS enforce_mixed_rl_audit_event_lifecycle();
DROP TRIGGER IF EXISTS interaction_dag_causal_edge_freeze_guard
  ON interaction_dag_causal_edge;
DROP TRIGGER IF EXISTS interaction_dag_segment_provider_call_freeze_guard
  ON interaction_dag_segment_provider_call;
DROP TRIGGER IF EXISTS interaction_dag_run_segment_freeze_guard
  ON interaction_dag_run_segment;
DROP TRIGGER IF EXISTS pi_message_consumption_freeze_guard
  ON pi_message_consumption;
DROP TRIGGER IF EXISTS pi_visible_action_freeze_guard
  ON pi_visible_action;
DROP TRIGGER IF EXISTS env_dispatch_run_agent_freeze_guard
  ON env_dispatch_run_agent;
DROP TRIGGER IF EXISTS pi_provider_call_freeze_guard
  ON pi_provider_call;
DROP FUNCTION IF EXISTS enforce_mixed_rl_graph_mutability();
DROP TRIGGER IF EXISTS interaction_dag_frozen_snapshot_immutable
  ON interaction_dag_frozen_snapshot;
DROP FUNCTION IF EXISTS reject_frozen_snapshot_update();
DROP TABLE IF EXISTS interaction_dag_causal_edge;
DROP TABLE IF EXISTS interaction_dag_segment_provider_call;
DROP TABLE IF EXISTS interaction_dag_run_segment;
ALTER TABLE env_dispatch_run_audit_event
  DROP CONSTRAINT IF EXISTS env_dispatch_run_audit_event_snapshot_fk;
ALTER TABLE env_dispatch_run
  DROP CONSTRAINT IF EXISTS env_dispatch_run_frozen_snapshot_fk;
DROP TABLE IF EXISTS interaction_dag_frozen_snapshot;
