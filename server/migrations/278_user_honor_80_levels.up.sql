ALTER TABLE user_honor
    ADD CONSTRAINT user_honor_level_max_check CHECK (level <= 80);

UPDATE honor_badge_def
SET description = 'Reached level 80 — completed the current honor progression.',
    unlock_rule = 'Reach honor level 80'
WHERE id = 'infinity_engine';
