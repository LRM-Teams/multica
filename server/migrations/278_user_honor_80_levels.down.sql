ALTER TABLE user_honor
    DROP CONSTRAINT IF EXISTS user_honor_level_max_check;

UPDATE user_honor
SET level = 60
WHERE level > 60;

UPDATE honor_badge_def
SET description = 'Reached level 60 — completed the current honor progression.',
    unlock_rule = 'Reach honor level 60'
WHERE id = 'infinity_engine';
