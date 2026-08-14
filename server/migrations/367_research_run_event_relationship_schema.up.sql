-- Chapter D §4.8: close the Run Event payload schema registry and resolve the
-- remaining artifact-valued question_id field.

CREATE OR REPLACE FUNCTION research_artifact_run_event_relationship_schema_allowed(event_type TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT event_type IN (
    'budget_exhausted','control_task_created','execution_circuit_transition',
    'goal_steered','inquiry_graph_created','inquiry_state_changed','inquiry_status_updated',
    'node_command_continue','node_command_fork','node_command_reassign','node_command_retry',
    'run_archived','run_awaiting_confirmation','run_cancelled','run_completed',
    'run_failed','run_paused','run_resumed','run_started','selective_steering_applied',
    'target_repair_decided',
    'task_attempt_cancelling','task_attempt_failed','task_blocked','task_dispatched',
    'task_dispatching','task_inquiry_targets_bound','task_result_accepted','task_started',
    'task_waiting_for_execution_target'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_scoped_question_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_kind TEXT,
  p_owner_id UUID,
  p_field_path TEXT,
  p_reference_value TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
  IF btrim(COALESCE(p_reference_value,''))='' THEN
    RETURN;
  END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'question',p_reference_value,'malformed_uuid'
    );
    RETURN;
  END IF;
  IF NOT EXISTS(
    SELECT 1 FROM research_question question
    WHERE question.workspace_id=p_workspace_id AND question.session_id=p_session_id
      AND question.id=p_reference_value::uuid
  ) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,p_owner_kind,p_owner_id,p_field_path,
      'question',p_reference_value,
      CASE WHEN EXISTS(
        SELECT 1 FROM research_question question
        WHERE question.id=p_reference_value::uuid
          AND (question.workspace_id IS DISTINCT FROM p_workspace_id
            OR question.session_id IS DISTINCT FROM p_session_id)
      ) THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_run_event_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_event_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_event_type TEXT;
  v_payload JSONB;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'run_event',p_event_id
  );
  SELECT event.event_type,event.payload INTO v_event_type,v_payload
  FROM research_run_event event
  WHERE event.workspace_id=p_workspace_id AND event.session_id=p_session_id AND event.id=p_event_id;
  IF NOT FOUND THEN
    RETURN 0;
  END IF;
  IF NOT research_artifact_run_event_relationship_schema_allowed(v_event_type) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'run_event',p_event_id,'/event_type',
      'run_event_schema',v_event_type,'unknown_schema'
    );
  END IF;
  v_payload := COALESCE(v_payload,'{}'::jsonb);
  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id,p_session_id,'run_event',p_event_id,'/payload/task_id',v_payload->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id,p_session_id,'run_event',p_event_id,'/payload/attempt_id',v_payload->>'attempt_id'
  );
  PERFORM research_artifact_diagnose_scoped_question_reference(
    p_workspace_id,p_session_id,'run_event',p_event_id,'/payload/question_id',v_payload->>'question_id'
  );
  PERFORM research_artifact_diagnose_scoped_report_reference(
    p_workspace_id,p_session_id,'run_event',p_event_id,'/payload/report_id',v_payload->>'report_id'
  );
  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id=p_workspace_id AND diagnostic.session_id=p_session_id
    AND diagnostic.owner_kind='run_event' AND diagnostic.owner_id=p_event_id;
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_run_event_diagnostic_refresh_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM research_artifact_scan_research_run_event_migration_diagnostics(
    NEW.workspace_id,NEW.session_id,NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_run_event_relationship_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,event_type,payload
ON research_run_event
FOR EACH ROW EXECUTE FUNCTION research_artifact_run_event_diagnostic_refresh_fn();

DO $$
DECLARE
  v_event RECORD;
BEGIN
  FOR v_event IN SELECT workspace_id,session_id,id FROM research_run_event LOOP
    PERFORM research_artifact_scan_research_run_event_migration_diagnostics(
      v_event.workspace_id,v_event.session_id,v_event.id
    );
  END LOOP;
END;
$$;
