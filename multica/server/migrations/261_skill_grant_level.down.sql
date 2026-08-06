DROP TABLE IF EXISTS skill_promotion;

DROP INDEX IF EXISTS idx_skill_workspace_grant_level;
DROP INDEX IF EXISTS idx_skill_channel_id;

ALTER TABLE skill DROP CONSTRAINT IF EXISTS skill_grant_channel_consistency_check;
ALTER TABLE skill DROP CONSTRAINT IF EXISTS skill_grant_level_check;
ALTER TABLE skill DROP COLUMN IF EXISTS channel_id;
ALTER TABLE skill DROP COLUMN IF EXISTS grant_level;
