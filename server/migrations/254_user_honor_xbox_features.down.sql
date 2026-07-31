ALTER TABLE user_honor DROP COLUMN IF EXISTS showcase_badge_ids;
ALTER TABLE honor_badge_def DROP COLUMN IF EXISTS unlock_rule;
ALTER TABLE honor_badge_def DROP COLUMN IF EXISTS secret;
