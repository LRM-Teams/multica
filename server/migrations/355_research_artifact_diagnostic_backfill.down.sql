-- Diagnostic rows are derived observations and may have been refreshed after
-- migration. Do not delete them on rollback because they are indistinguishable
-- from operator-triggered scans and remain valid under migrations 325/328.
DROP FUNCTION IF EXISTS research_artifact_scan_session_migration_diagnostics(UUID, UUID);
