DROP INDEX IF EXISTS research_v6_report_review_cycle_idx;
ALTER TABLE research_report_resource DROP CONSTRAINT IF EXISTS research_v6_report_resource_upload_fk;
DROP TABLE IF EXISTS research_report_upload_session;
ALTER TABLE research_report_review DROP CONSTRAINT IF EXISTS research_v6_report_review_revision_fk;
ALTER TABLE research_report_resource DROP CONSTRAINT IF EXISTS research_v6_report_resource_revision_fk;
ALTER TABLE research_report_input DROP CONSTRAINT IF EXISTS research_v6_report_input_revision_fk;
ALTER TABLE research_report_input DROP CONSTRAINT IF EXISTS research_v6_report_input_artifact_version_fk;
ALTER TABLE research_report DROP CONSTRAINT IF EXISTS research_v6_report_id_revision_unique;
ALTER TABLE research_report DROP COLUMN IF EXISTS citations, DROP COLUMN IF EXISTS outline;
