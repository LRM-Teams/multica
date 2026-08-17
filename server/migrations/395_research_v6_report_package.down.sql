DROP TRIGGER IF EXISTS research_v6_report_publish_guard ON research_report;
DROP FUNCTION IF EXISTS research_v6_report_publish_guard_fn();
DROP TABLE IF EXISTS research_report_review;
DROP TABLE IF EXISTS research_report_resource;
DROP TABLE IF EXISTS research_report_input;
DROP INDEX IF EXISTS research_v6_report_latest_published_idx;
DROP INDEX IF EXISTS research_v6_report_package_hash_idx;
ALTER TABLE research_report DROP CONSTRAINT IF EXISTS research_v6_report_director_fk;
ALTER TABLE research_report DROP CONSTRAINT IF EXISTS research_v6_report_parent_fk;
ALTER TABLE research_report DROP CONSTRAINT IF EXISTS research_v6_report_status_check;
ALTER TABLE research_report DROP COLUMN IF EXISTS reviewed_by_director_assignment_id, DROP COLUMN IF EXISTS published_at,
 DROP COLUMN IF EXISTS input_event_sequence, DROP COLUMN IF EXISTS csp_style_hashes, DROP COLUMN IF EXISTS csp_script_hashes,
 DROP COLUMN IF EXISTS input_snapshot_hash, DROP COLUMN IF EXISTS document_byte_size, DROP COLUMN IF EXISTS document_storage_generation,
 DROP COLUMN IF EXISTS document_storage_key, DROP COLUMN IF EXISTS document_content_hash, DROP COLUMN IF EXISTS package_hash,
 DROP COLUMN IF EXISTS plain_text, DROP COLUMN IF EXISTS summary, DROP COLUMN IF EXISTS title,
 DROP COLUMN IF EXISTS parent_revision, DROP COLUMN IF EXISTS parent_report_id, DROP COLUMN IF EXISTS status;
