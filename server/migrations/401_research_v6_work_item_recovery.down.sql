DROP INDEX IF EXISTS research_v6_work_item_recovery_idx;
DROP TABLE IF EXISTS research_v6_work_submission;
ALTER TABLE research_work_catalog_page DROP COLUMN IF EXISTS page;
ALTER TABLE research_work_item_attempt DROP CONSTRAINT IF EXISTS research_v6_attempt_request_hash_check;
ALTER TABLE research_work_item_attempt DROP COLUMN IF EXISTS request_content_hash, DROP COLUMN IF EXISTS manifest;
ALTER TABLE research_work_item DROP COLUMN IF EXISTS state_version;
