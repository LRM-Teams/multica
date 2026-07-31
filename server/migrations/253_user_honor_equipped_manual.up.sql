-- Distinguish auto-equipped best badge from user-chosen badge in Settings → Honor.

ALTER TABLE user_honor
    ADD COLUMN equipped_badge_manual BOOLEAN NOT NULL DEFAULT false;

-- Existing rows with an equipped badge were user-visible; treat as manual so we
-- do not silently swap their choice on deploy. New users stay manual=false and
-- receive the best unlocked badge automatically.
UPDATE user_honor
SET equipped_badge_manual = true
WHERE equipped_badge_id IS NOT NULL;
