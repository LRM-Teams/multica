DROP TABLE IF EXISTS team_knowledge_item;
DROP TABLE IF EXISTS agent_memory_curation_candidate;

ALTER TABLE memory_curation_watermark
  DROP CONSTRAINT IF EXISTS memory_curation_watermark_stage_check,
  ADD CONSTRAINT memory_curation_watermark_stage_check
    CHECK (stage IN ('l1_daily', 'l2_review', 'l3_promote', 'l4_curator'));

ALTER TABLE memory_curation_run
  DROP CONSTRAINT IF EXISTS memory_curation_run_stage_check,
  ADD CONSTRAINT memory_curation_run_stage_check
    CHECK (stage IN ('l1_daily', 'l2_review', 'l3_promote', 'l4_curator', 'all'));

ALTER TABLE memory_curator_profile
  DROP COLUMN IF EXISTS team_curation_enabled,
  DROP COLUMN IF EXISTS self_review_enabled;
