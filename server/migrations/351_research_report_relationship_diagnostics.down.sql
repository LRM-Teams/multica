-- Roll back structured Report migration diagnostics.

DROP FUNCTION IF EXISTS research_artifact_scan_session_report_migration_diagnostics(UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_scan_research_report_migration_diagnostics(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_diagnose_report_source_reference(UUID, UUID, UUID, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_report_local_reference(UUID, UUID, UUID, TEXT, TEXT, TEXT, BIGINT);

DELETE FROM research_artifact_migration_diagnostic
WHERE owner_kind = 'report_revision';

CREATE OR REPLACE FUNCTION research_artifact_migration_diagnostic_reason_allowed(reason TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT reason IN (
    'malformed_uuid',
    'unresolved_reference',
    'cross_scope_reference',
    'invalid_match_decision',
    'unknown_schema'
  );
$$;

CREATE OR REPLACE FUNCTION research_artifact_migration_relationship_parser_allowed(parser TEXT)
RETURNS BOOLEAN
LANGUAGE sql
IMMUTABLE
AS $$
  SELECT parser IN (
    'research_message_match_decision',
    'research_decision_inputs',
    'research_run_event_payload'
  );
$$;
