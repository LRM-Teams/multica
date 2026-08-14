-- Chapter D §15.6: materialize the migration diagnostics that 325/328 made
-- computable. The per-owner scanners are authoritative; this wrapper makes
-- them discoverable without requiring an operator to know every owner UUID.

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
    SELECT m.id
    FROM research_message m
    WHERE m.workspace_id = p_workspace_id
      AND m.session_id = p_session_id
      AND (
        m.meta ? 'match_decision'
        OR EXISTS (
          SELECT 1
          FROM research_artifact_migration_diagnostic d
          WHERE d.workspace_id = m.workspace_id
            AND d.session_id = m.session_id
            AND d.owner_kind = 'research_message'
            AND d.owner_id = m.id
        )
      )
  LOOP
    v_total := v_total + research_artifact_scan_research_message_migration_diagnostics(
      p_workspace_id, p_session_id, v_owner_id
    );
  END LOOP;

  FOR v_owner_id IN
    SELECT d.id
    FROM research_decision d
    WHERE d.workspace_id = p_workspace_id
      AND d.session_id = p_session_id
      AND (
        d.inputs <> '{}'::jsonb
        OR EXISTS (
          SELECT 1
          FROM research_artifact_migration_diagnostic diagnostic
          WHERE diagnostic.workspace_id = d.workspace_id
            AND diagnostic.session_id = d.session_id
            AND diagnostic.owner_id = d.id
            AND diagnostic.owner_kind IN ('method_decision', 'evaluation_decision')
        )
      )
  LOOP
    v_total := v_total + research_artifact_scan_research_decision_migration_diagnostics(
      p_workspace_id, p_session_id, v_owner_id
    );
  END LOOP;

  FOR v_owner_id IN
    SELECT e.id
    FROM research_run_event e
    WHERE e.workspace_id = p_workspace_id
      AND e.session_id = p_session_id
      AND (
        e.payload <> '{}'::jsonb
        OR EXISTS (
          SELECT 1
          FROM research_artifact_migration_diagnostic d
          WHERE d.workspace_id = e.workspace_id
            AND d.session_id = e.session_id
            AND d.owner_kind = 'run_event'
            AND d.owner_id = e.id
        )
      )
  LOOP
    v_total := v_total + research_artifact_scan_research_run_event_migration_diagnostics(
      p_workspace_id, p_session_id, v_owner_id
    );
  END LOOP;

  RETURN v_total;
END;
$$;

DO $$
DECLARE
  v_session RECORD;
BEGIN
  FOR v_session IN
    SELECT s.workspace_id, s.id AS session_id
    FROM research_session s
  LOOP
    PERFORM research_artifact_scan_session_migration_diagnostics(
      v_session.workspace_id, v_session.session_id
    );
  END LOOP;
END;
$$;
