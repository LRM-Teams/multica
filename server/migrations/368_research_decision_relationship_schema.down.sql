DROP TRIGGER IF EXISTS research_decision_relationship_diagnostic_refresh ON research_decision;
DROP FUNCTION IF EXISTS research_artifact_decision_diagnostic_refresh_fn();
DROP FUNCTION IF EXISTS research_artifact_decision_relationship_schema_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_decision_reference_array(UUID,UUID,TEXT,UUID,TEXT,TEXT,JSONB);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_branch_reference(UUID,UUID,TEXT,UUID,TEXT,TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_decision_reference(UUID,UUID,TEXT,UUID,TEXT,TEXT);

-- Migration 367 owns the shared question-reference helper; do not drop it.

CREATE OR REPLACE FUNCTION research_artifact_scan_research_decision_migration_diagnostics(
  p_workspace_id UUID,p_session_id UUID,p_decision_id UUID
)
RETURNS INTEGER LANGUAGE plpgsql AS $$
DECLARE v_inputs JSONB; v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(p_workspace_id,p_session_id,'evaluation_decision',p_decision_id);
  PERFORM research_artifact_clear_owner_migration_diagnostics(p_workspace_id,p_session_id,'method_decision',p_decision_id);
  SELECT decision.inputs INTO v_inputs FROM research_decision decision
  WHERE decision.workspace_id=p_workspace_id AND decision.session_id=p_session_id AND decision.id=p_decision_id;
  IF NOT FOUND OR v_inputs IS NULL OR v_inputs='{}'::jsonb THEN RETURN 0; END IF;
  PERFORM research_artifact_diagnose_scoped_task_reference(
    p_workspace_id,p_session_id,
    CASE WHEN (SELECT decision_kind FROM research_decision WHERE id=p_decision_id)='research_method'
      THEN 'method_decision' ELSE 'evaluation_decision' END,
    p_decision_id,'/inputs/task_id',v_inputs->>'task_id'
  );
  PERFORM research_artifact_diagnose_scoped_attempt_reference(
    p_workspace_id,p_session_id,
    CASE WHEN (SELECT decision_kind FROM research_decision WHERE id=p_decision_id)='research_method'
      THEN 'method_decision' ELSE 'evaluation_decision' END,
    p_decision_id,'/inputs/attempt_id',v_inputs->>'attempt_id'
  );
  PERFORM research_artifact_diagnose_scoped_report_reference(
    p_workspace_id,p_session_id,
    CASE WHEN (SELECT decision_kind FROM research_decision WHERE id=p_decision_id)='research_method'
      THEN 'method_decision' ELSE 'evaluation_decision' END,
    p_decision_id,'/inputs/report_id',v_inputs->>'report_id'
  );
  SELECT count(*)::int INTO v_count FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id=p_workspace_id AND diagnostic.session_id=p_session_id
    AND diagnostic.owner_id=p_decision_id;
  RETURN v_count;
END;
$$;
