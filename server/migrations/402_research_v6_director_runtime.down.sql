ALTER TABLE research_director_cycle
  DROP COLUMN IF EXISTS brief_manifest,
  DROP COLUMN IF EXISTS page_count,
  DROP COLUMN IF EXISTS state_version;
DROP TABLE IF EXISTS research_v6_outbox;
