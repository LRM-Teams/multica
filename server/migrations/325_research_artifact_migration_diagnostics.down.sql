-- Roll back Chapter D1h migration diagnostics.

DROP FUNCTION IF EXISTS research_artifact_scan_session_message_migration_diagnostics(UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_scan_research_message_migration_diagnostics(UUID, UUID, UUID);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_message_reference(UUID, UUID, UUID, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_diagnose_scoped_graph_node_reference(UUID, UUID, UUID, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_clear_owner_migration_diagnostics(UUID, UUID, TEXT, UUID);
DROP FUNCTION IF EXISTS research_artifact_record_migration_diagnostic(UUID, UUID, TEXT, UUID, TEXT, TEXT, TEXT, TEXT);
DROP FUNCTION IF EXISTS research_artifact_reference_uuid_valid(TEXT);
DROP INDEX IF EXISTS research_artifact_migration_diagnostic_owner_field_uidx;
ALTER TABLE research_artifact_migration_diagnostic
  DROP CONSTRAINT IF EXISTS research_artifact_migration_diagnostic_reference_value_check,
  DROP CONSTRAINT IF EXISTS research_artifact_migration_diagnostic_reason_check;
DROP FUNCTION IF EXISTS research_artifact_bounded_reference_value(TEXT);
DROP FUNCTION IF EXISTS research_artifact_migration_relationship_parser_allowed(TEXT);
DROP FUNCTION IF EXISTS research_artifact_migration_diagnostic_reason_allowed(TEXT);
