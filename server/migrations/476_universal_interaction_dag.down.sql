-- Restore the legacy interaction DAG schema only when no canonical live data exists.
-- Refusal happens before any catalog mutation so rollback is fail closed.

SELECT pg_advisory_xact_lock(hashtext('migration-454-universal-interaction-dag')::bigint);

LOCK TABLE interaction_dag_segment, interaction_dag_edge, task_message,
  interaction_dag_task_cursor, interaction_dag_publish_outbox,
  interaction_dag_universal_provider_call
IN ACCESS EXCLUSIVE MODE;

DO $$
BEGIN
  IF EXISTS (
    SELECT 1
    FROM interaction_dag_segment
    WHERE content_status <> 'legacy_unverified'
  ) OR EXISTS (
    SELECT 1 FROM interaction_dag_publish_outbox
  ) OR EXISTS (
    SELECT 1 FROM interaction_dag_universal_provider_call
  ) OR EXISTS (
    SELECT 1 FROM interaction_dag_task_cursor
  ) OR EXISTS (
    SELECT 1 FROM interaction_dag_edge
  ) THEN
    RAISE EXCEPTION 'migration 454 down refused canonical universal DAG data';
  END IF;
END;
$$;

DROP TRIGGER interaction_dag_shared_provider_owner_guard
  ON interaction_dag_universal_provider_call;
DROP TRIGGER interaction_dag_universal_provider_call_validate
  ON interaction_dag_universal_provider_call;
DROP TABLE interaction_dag_universal_provider_call;
DROP FUNCTION validate_universal_dag_shared_provider_owner();
DROP FUNCTION validate_universal_dag_provider_call();

DROP TRIGGER interaction_dag_edge_validate ON interaction_dag_edge;
DROP FUNCTION validate_universal_dag_edge();

DROP TRIGGER task_message_universal_dag_trigger_guard ON task_message;
DROP FUNCTION guard_universal_dag_trigger_message_mutation();
DROP INDEX task_message_task_id_seq_454_uidx;

DROP INDEX interaction_dag_edge_trigger_message_fk_idx;
DROP INDEX interaction_dag_edge_target_fk_idx;
DROP INDEX interaction_dag_edge_source_fk_idx;

ALTER TABLE interaction_dag_edge
  DROP CONSTRAINT interaction_dag_edge_trigger_message_fk,
  DROP CONSTRAINT interaction_dag_edge_target_fk,
  DROP CONSTRAINT interaction_dag_edge_source_fk,
  DROP CONSTRAINT interaction_dag_edge_workspace_seq_unique,
  DROP CONSTRAINT interaction_dag_edge_trigger_shape_check,
  DROP CONSTRAINT interaction_dag_edge_type_check,
  DROP CONSTRAINT interaction_dag_edge_seq_check,
  DROP CONSTRAINT interaction_dag_edge_workspace_fk,
  ADD CONSTRAINT interaction_dag_edge_type_check
    CHECK (type IN ('delegation', 'mention', 'completion')),
  ALTER COLUMN project_id SET NOT NULL,
  DROP COLUMN trigger_message_id,
  DROP COLUMN edge_seq,
  DROP COLUMN workspace_id;

DROP TABLE interaction_dag_edge_sequence;

DROP TRIGGER interaction_dag_outbox_segment_guard
  ON interaction_dag_publish_outbox;
DROP TRIGGER interaction_dag_segment_outbox_guard
  ON interaction_dag_segment;
DROP TRIGGER interaction_dag_publish_outbox_lifecycle_validate
  ON interaction_dag_publish_outbox;
DROP FUNCTION validate_universal_dag_publish_outbox_lifecycle();
DROP FUNCTION validate_universal_dag_outbox_segment();
DROP FUNCTION validate_universal_dag_segment_outbox();
DROP TABLE interaction_dag_publish_outbox;

DROP TRIGGER interaction_dag_task_cursor_validate
  ON interaction_dag_task_cursor;
DROP FUNCTION validate_universal_dag_task_cursor();
DROP TABLE interaction_dag_task_cursor;

DROP TRIGGER interaction_dag_segment_validate ON interaction_dag_segment;
DROP FUNCTION validate_universal_dag_segment();

DROP TABLE interaction_dag_segment_generation_sequence;

ALTER TABLE agent_inbox_event
  DROP CONSTRAINT agent_inbox_event_workspace_id_id_454_key;

DROP INDEX interaction_dag_segment_canonical_range_guard_idx;
DROP INDEX interaction_dag_segment_workspace_segment_uidx;
DROP INDEX interaction_dag_segment_workspace_visible_action_uidx;
DROP INDEX interaction_dag_segment_workspace_generation_uidx;

ALTER TABLE interaction_dag_segment
  DROP CONSTRAINT ck_segment_source_valid,
  ADD CONSTRAINT ck_segment_source_valid CHECK (
    (trajectory_source = 'areal_tensor' AND trajectory_id IS NOT NULL AND tensor_ref IS NOT NULL)
    OR
    (trajectory_source = 'task_messages' AND trajectory_id IS NULL AND tensor_ref IS NULL)
  );

ALTER TABLE interaction_dag_segment
  DROP CONSTRAINT interaction_dag_segment_run_identity_check,
  DROP CONSTRAINT interaction_dag_segment_provider_correlation_check,
  DROP CONSTRAINT interaction_dag_segment_provider_version_check,
  DROP CONSTRAINT interaction_dag_segment_provider_identity_check,
  DROP CONSTRAINT interaction_dag_segment_provider_status_check,
  DROP CONSTRAINT interaction_dag_segment_retracted_metadata_check,
  DROP CONSTRAINT interaction_dag_segment_published_metadata_check,
  DROP CONSTRAINT interaction_dag_segment_publish_seq_check,
  DROP CONSTRAINT interaction_dag_segment_publish_status_check,
  DROP CONSTRAINT interaction_dag_segment_content_status_check,
  DROP CONSTRAINT interaction_dag_segment_close_action_check,
  DROP CONSTRAINT interaction_dag_segment_range_check,
  DROP CONSTRAINT interaction_dag_segment_generation_check,
  DROP CONSTRAINT interaction_dag_segment_run_agent_fk,
  DROP CONSTRAINT interaction_dag_segment_run_fk,
  DROP CONSTRAINT interaction_dag_segment_channel_event_fk,
  DROP CONSTRAINT interaction_dag_segment_project_event_fk,
  DROP CONSTRAINT interaction_dag_segment_agent_run_fk,
  DROP CONSTRAINT interaction_dag_segment_workspace_fk;

ALTER TABLE interaction_dag_segment
  ALTER COLUMN agent_run_id TYPE text USING agent_run_id::text,
  ALTER COLUMN project_id SET NOT NULL,
  DROP COLUMN updated_at,
  DROP COLUMN retracted_at,
  DROP COLUMN published_at,
  DROP COLUMN run_agent_id,
  DROP COLUMN run_id,
  DROP COLUMN provider_capture_correlation_key,
  DROP COLUMN provider_capture_version,
  DROP COLUMN provider_capture_id,
  DROP COLUMN provider_capture_status,
  DROP COLUMN policy_version,
  DROP COLUMN sanitizer_version,
  DROP COLUMN publish_seq,
  DROP COLUMN content_status,
  DROP COLUMN publish_status,
  DROP COLUMN trainable_eligible,
  DROP COLUMN derivative,
  DROP COLUMN visible_action_key,
  DROP COLUMN canonical_action_id,
  DROP COLUMN close_action_kind,
  DROP COLUMN graph_projection_eligible_at_event,
  DROP COLUMN memory_type_at_event,
  DROP COLUMN route_generation_at_event,
  DROP COLUMN channel_id_at_event,
  DROP COLUMN project_id_at_event,
  DROP COLUMN generation,
  DROP COLUMN workspace_id;
