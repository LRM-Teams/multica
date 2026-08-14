-- Chapter D §4.8: bind the legacy Source payload snapshot reference to its
-- canonical relational Source Snapshot pointer without rewriting history.

CREATE OR REPLACE FUNCTION research_artifact_migration_diagnostic_reason_allowed(reason TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT reason IN (
    'malformed_uuid','unresolved_reference','cross_scope_reference',
    'invalid_match_decision','unknown_schema','duplicate_local_key',
    'ambiguous_local_key','cyclic_local_reference','dangling_local_key',
    'mismatched_reference'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN (
    'research_message_match_decision','research_decision_inputs',
    'research_report_structured','research_run_event_payload',
    'research_graph_node_payload','research_legacy_source_payload'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_scan_research_legacy_source_migration_diagnostics(
  p_workspace_id UUID,
  p_session_id UUID,
  p_source_id UUID
)
RETURNS INTEGER
LANGUAGE plpgsql
AS $$
DECLARE
  v_payload JSONB;
  v_snapshot_id UUID;
  v_payload_value TEXT;
  v_count INTEGER;
BEGIN
  PERFORM research_artifact_clear_owner_migration_diagnostics(
    p_workspace_id,p_session_id,'legacy_source',p_source_id
  );
  SELECT source.payload,source.source_snapshot_id
  INTO v_payload,v_snapshot_id
  FROM research_source source
  WHERE source.workspace_id=p_workspace_id AND source.session_id=p_session_id AND source.id=p_source_id;
  IF NOT FOUND OR v_payload IS NULL OR NOT (v_payload ? 'snapshot_id') THEN
    RETURN 0;
  END IF;

  v_payload_value := v_payload->>'snapshot_id';
  IF jsonb_typeof(v_payload->'snapshot_id') <> 'string'
     OR NOT research_artifact_reference_uuid_valid(v_payload_value) THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'legacy_source',p_source_id,
      '/payload/snapshot_id','source_snapshot',COALESCE(v_payload_value,(v_payload->'snapshot_id')::text),
      'malformed_uuid'
    );
  ELSIF v_snapshot_id IS NULL THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'legacy_source',p_source_id,
      '/payload/snapshot_id','source_snapshot',v_payload_value,'unresolved_reference'
    );
  ELSIF v_payload_value::uuid <> v_snapshot_id THEN
    PERFORM research_artifact_record_migration_diagnostic(
      p_workspace_id,p_session_id,'legacy_source',p_source_id,
      '/payload/snapshot_id','source_snapshot',v_payload_value,'mismatched_reference'
    );
  END IF;

  SELECT count(*)::int INTO v_count
  FROM research_artifact_migration_diagnostic diagnostic
  WHERE diagnostic.workspace_id=p_workspace_id AND diagnostic.session_id=p_session_id
    AND diagnostic.owner_kind='legacy_source' AND diagnostic.owner_id=p_source_id;
  RETURN v_count;
END;
$$;

CREATE OR REPLACE FUNCTION research_artifact_legacy_source_reference_guard_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
  v_payload_value TEXT;
BEGIN
  IF NEW.payload IS NULL OR NOT (NEW.payload ? 'snapshot_id') THEN
    RETURN NEW;
  END IF;
  v_payload_value := NEW.payload->>'snapshot_id';
  IF jsonb_typeof(NEW.payload->'snapshot_id') <> 'string'
     OR NOT research_artifact_reference_uuid_valid(v_payload_value)
     OR NEW.source_snapshot_id IS NULL
     OR v_payload_value::uuid <> NEW.source_snapshot_id THEN
    RAISE check_violation
      USING CONSTRAINT='research_legacy_source_snapshot_payload_guard';
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_legacy_source_snapshot_payload_guard
BEFORE INSERT OR UPDATE OF workspace_id,session_id,source_snapshot_id,payload
ON research_source
FOR EACH ROW EXECUTE FUNCTION research_artifact_legacy_source_reference_guard_fn();

CREATE OR REPLACE FUNCTION research_artifact_legacy_source_diagnostic_refresh_fn()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  PERFORM research_artifact_scan_research_legacy_source_migration_diagnostics(
    NEW.workspace_id,NEW.session_id,NEW.id
  );
  RETURN NEW;
END;
$$;

CREATE TRIGGER research_legacy_source_relationship_diagnostic_refresh
AFTER INSERT OR UPDATE OF workspace_id,session_id,source_snapshot_id,payload
ON research_source
FOR EACH ROW EXECUTE FUNCTION research_artifact_legacy_source_diagnostic_refresh_fn();

DO $$
DECLARE
  v_source RECORD;
BEGIN
  FOR v_source IN SELECT workspace_id,session_id,id FROM research_source LOOP
    PERFORM research_artifact_scan_research_legacy_source_migration_diagnostics(
      v_source.workspace_id,v_source.session_id,v_source.id
    );
  END LOOP;
END;
$$;
