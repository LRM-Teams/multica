DROP TRIGGER IF EXISTS research_run_event_relationship_diagnostic_refresh ON research_run_event;
DROP FUNCTION IF EXISTS research_artifact_run_event_diagnostic_refresh_fn();
DROP FUNCTION IF EXISTS research_artifact_run_event_relationship_schema_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_question_reference(UUID,UUID,TEXT,UUID,TEXT,TEXT);

CREATE OR REPLACE FUNCTION research_artifact_scan_research_run_event_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_event_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_payload JSONB;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'run_event',p_event_id
  );
  SELECT event.payload INTO v_payload
  FROM research_run_event event
  WHERE event.workspace_id=p_workspace_id AND event.session_id=p_session_id AND event.id=p_event_id;
  IF NOT FOUND OR v_payload IS NULL OR v_payload='{}'::jsonb THEN
    RETURN 0;
  END IF;
  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id,p_session_id,'run_event',p_event_id,'/payload/task_id',v_payload->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id,p_session_id,'run_event',p_event_id,'/payload/attempt_id',v_payload->>'attempt_id'
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
