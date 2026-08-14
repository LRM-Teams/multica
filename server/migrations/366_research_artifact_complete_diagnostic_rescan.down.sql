-- Restore migration 355's original three-family session rescan.

CREATE OR REPLACE FUNCTION research_artifact_scan_session_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_owner_id UUID;
  v_total INTEGER := 0;
BEGIN
  FOR v_owner_id IN
    SELECT id FROM research_message
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total := v_total + research_artifact_scan_research_message_migration_diagnostics(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;

  FOR v_owner_id IN
    SELECT id FROM research_decision
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total := v_total + research_artifact_scan_research_decision_migration_diagnostics(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;

  FOR v_owner_id IN
    SELECT id FROM research_run_event
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total := v_total + research_artifact_scan_research_run_event_migration_diagnostics(
      p_workspace_id,p_session_id,v_owner_id
    );
  END LOOP;

  RETURN v_total;
END;
$$;
