-- Chapter D §4.8: close the typed stored-graph relationship surface.

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN (
    'research_message_match_decision',
    'research_decision_inputs',
    'research_report_structured',
    'research_run_event_payload',
    'research_graph_node_payload'
  );
$$;

ALTER TABLE research_graph_node
  ADD CONSTRAINT research_graph_node_run_event_scoped_fkey
  FOREIGN KEY (workspace_id,session_id,run_event_id)
  REFERENCES research_run_event(workspace_id,session_id,id)
  NOT VALID;

ALTER TABLE research_graph_node
  VALIDATE CONSTRAINT research_graph_node_run_event_scoped_fkey;

CREATE OR REPLACE FUNCTION research_artifact_diagnose_graph_node_reference(
  p_workspace_id UUID,
  p_session_id UUID,
  p_owner_id UUID,
  p_field_path TEXT,
  p_expected_target_kind TEXT,
  p_reference_value TEXT
)
RETURNS VOID
LANGUAGE plpgsql
AS $$
DECLARE
  v_same_scope BOOLEAN := false;
  v_other_scope BOOLEAN := false;
BEGIN
  IF btrim(COALESCE(p_reference_value, '')) = '' THEN
    RETURN;
  END IF;
  IF NOT research_artifact_reference_uuid_valid(p_reference_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'graph_node',p_owner_id,p_field_path,
      p_expected_target_kind,p_reference_value,'malformed_uuid'
    );
    RETURN;
  END IF;

  CASE p_expected_target_kind
    WHEN 'legacy_source' THEN
      SELECT
        EXISTS(SELECT 1 FROM research_source source
               WHERE source.workspace_id=p_workspace_id AND source.session_id=p_session_id
                 AND source.id=p_reference_value::uuid),
        EXISTS(SELECT 1 FROM research_source source
               WHERE source.id=p_reference_value::uuid
                 AND (source.workspace_id IS DISTINCT FROM p_workspace_id
                   OR source.session_id IS DISTINCT FROM p_session_id))
      INTO v_same_scope,v_other_scope;
    WHEN 'question' THEN
      SELECT
        EXISTS(SELECT 1 FROM research_question question
               WHERE question.workspace_id=p_workspace_id AND question.session_id=p_session_id
                 AND question.id=p_reference_value::uuid),
        EXISTS(SELECT 1 FROM research_question question
               WHERE question.id=p_reference_value::uuid
                 AND (question.workspace_id IS DISTINCT FROM p_workspace_id
                   OR question.session_id IS DISTINCT FROM p_session_id))
      INTO v_same_scope,v_other_scope;
    WHEN 'task' THEN
      SELECT
        EXISTS(SELECT 1 FROM research_task task
               WHERE task.workspace_id=p_workspace_id AND task.session_id=p_session_id
                 AND task.id=p_reference_value::uuid),
        EXISTS(SELECT 1 FROM research_task task
               WHERE task.id=p_reference_value::uuid
                 AND (task.workspace_id IS DISTINCT FROM p_workspace_id
                   OR task.session_id IS DISTINCT FROM p_session_id))
      INTO v_same_scope,v_other_scope;
    ELSE
      RAISE EXCEPTION 'unknown graph-node relationship target kind %', p_expected_target_kind;
  END CASE;

  IF NOT v_same_scope THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'graph_node',p_owner_id,p_field_path,
      p_expected_target_kind,p_reference_value,
      CASE WHEN v_other_scope THEN 'cross_scope_reference' ELSE 'unresolved_reference' END
    );
  END IF;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_graph_node_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_node_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_payload JSONB;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'graph_node',p_node_id
  );
  SELECT node.payload INTO v_payload
  FROM research_graph_node node
  WHERE node.workspace_id=p_workspace_id AND node.session_id=p_session_id AND node.id=p_node_id;
  IF NOT FOUND OR v_payload IS NULL OR v_payload='{}'::jsonb THEN
    RETURN 0;
  END IF;

  PERFORM research_artifact_diagnose_graph_node_reference(
    p_workspace_id,p_session_id,p_node_id,'/payload/source_id','legacy_source',v_payload->>'source_id'
  );
  PERFORM research_artifact_diagnose_graph_node_reference(
    p_workspace_id,p_session_id,p_node_id,'/payload/question_id','question',v_payload->>'question_id'
  );
  PERFORM research_artifact_diagnose_graph_node_reference(
    p_workspace_id,p_session_id,p_node_id,'/payload/task_id','task',v_payload->>'task_id'
  );
  PERFORM research_artifact_diagnose_graph_node_reference(
    p_workspace_id,p_session_id,p_node_id,'/payload/details/question_id','question',v_payload#>>'{details,question_id}'
  );
  PERFORM research_artifact_diagnose_graph_node_reference(
    p_workspace_id,p_session_id,p_node_id,'/payload/details/task_id','task',v_payload#>>'{details,task_id}'
  );

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id=p_workspace_id AND diagnostic.session_id=p_session_id
    AND diagnostic.owner_kind='graph_node' AND diagnostic.owner_id=p_node_id;
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_graph_node_diagnostic_trigger_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM research_artifact_scan_research_graph_node_migration_diagnostics(
    NEW.workspace_id,NEW.session_id,NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_graph_node_relationship_diagnostic_guard
AFTER INSERT OR UPDATE OF workspace_id,session_id,payload
ON research_graph_node
FOR EACH ROW EXECUTE FUNCTION research_artifact_graph_node_diagnostic_trigger_fn();

DO $$
DECLARE
  v_node RECORD;
BEGIN
  FOR v_node IN SELECT workspace_id,session_id,id FROM research_graph_node LOOP
    PERFORM research_artifact_scan_research_graph_node_migration_diagnostics(
      v_node.workspace_id,v_node.session_id,v_node.id
    );
  END LOOP;
END;
$$;
