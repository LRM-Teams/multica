-- Chapter D §4.8/§15.6: the operator-facing session rescan must cover every
-- registered typed relationship parser, including parsers added after 355.

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
  v_scanned INTEGER;
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
    SELECT id FROM research_report
    WHERE workspace_id=p_workspace_id AND session_id=p_session_id
  LOOP
    v_total := v_total + research_artifact_scan_research_report_migration_diagnostics(
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

  IF to_regprocedure(
    'research_artifact_scan_research_graph_node_migration_diagnostics(uuid,uuid,uuid)'
  ) IS NOT NULL THEN
    FOR v_owner_id IN
      SELECT id FROM research_graph_node
      WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    LOOP
      EXECUTE 'SELECT research_artifact_scan_research_graph_node_migration_diagnostics($1,$2,$3)'
        INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total := v_total + v_scanned;
    END LOOP;
  END IF;

  IF to_regprocedure(
    'research_artifact_scan_research_legacy_source_migration_diagnostics(uuid,uuid,uuid)'
  ) IS NOT NULL THEN
    FOR v_owner_id IN
      SELECT id FROM research_source
      WHERE workspace_id=p_workspace_id AND session_id=p_session_id
    LOOP
      EXECUTE 'SELECT research_artifact_scan_research_legacy_source_migration_diagnostics($1,$2,$3)'
        INTO v_scanned USING p_workspace_id,p_session_id,v_owner_id;
      v_total := v_total + v_scanned;
    END LOOP;
  END IF;

  RETURN v_total;
END;
$$;
