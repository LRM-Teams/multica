DROP TRIGGER IF EXISTS research_legacy_source_relationship_diagnostic_refresh ON research_source;
DROP FUNCTION IF EXISTS research_artifact_legacy_source_diagnostic_refresh_fn();
DROP TRIGGER IF EXISTS research_legacy_source_snapshot_payload_guard ON research_source;
DROP FUNCTION IF EXISTS research_artifact_legacy_source_reference_guard_fn();
DROP FUNCTION IF EXISTS research_artifact_scan_research_legacy_source_migration_diagnostics(UUID,UUID,UUID);

DELETE FROM research_artifact_migration_diagnostic WHERE owner_kind='legacy_source';

CREATE OR REPLACE FUNCTION research_artifact_migration_diagnostic_reason_allowed(reason TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT reason IN (
    'malformed_uuid','unresolved_reference','cross_scope_reference',
    'invalid_match_decision','unknown_schema','duplicate_local_key',
    'ambiguous_local_key','cyclic_local_reference','dangling_local_key'
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
    'research_graph_node_payload'
  );
$$;
