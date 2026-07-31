-- Xbox-style honor: secret badges, public unlock hints, profile showcase slots.

ALTER TABLE honor_badge_def
    ADD COLUMN secret BOOLEAN NOT NULL DEFAULT false,
    ADD COLUMN unlock_rule TEXT NOT NULL DEFAULT '';

ALTER TABLE user_honor
    ADD COLUMN showcase_badge_ids TEXT[] NOT NULL DEFAULT '{}';

UPDATE honor_badge_def SET unlock_rule = 'Register before August 1, 2026' WHERE id = 'founding';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 3' WHERE id = 'stardust';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 5' WHERE id = 'mercury';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 8' WHERE id = 'venus';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 10' WHERE id = 'earth';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 12' WHERE id = 'mars';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 15' WHERE id = 'jupiter';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 18' WHERE id = 'saturn';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 20' WHERE id = 'veteran';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 22' WHERE id = 'uranus';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 26' WHERE id = 'neptune';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 30' WHERE id = 'pluto';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 35' WHERE id = 'red_dwarf';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 40' WHERE id = 'blue_giant';
UPDATE honor_badge_def SET unlock_rule = 'Reach honor level 50' WHERE id = 'quasar';
UPDATE honor_badge_def SET unlock_rule = 'Delivery pillar tier 4+' WHERE id = 'builder';
UPDATE honor_badge_def SET unlock_rule = 'Community pillar tier 3+' WHERE id = 'collaborator';

-- Late-game badges stay hidden until unlocked.
UPDATE honor_badge_def SET secret = true WHERE id IN ('blue_giant', 'quasar', 'red_dwarf');
