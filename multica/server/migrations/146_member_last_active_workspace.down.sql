DROP INDEX IF EXISTS idx_member_user_last_active;

ALTER TABLE member
DROP COLUMN IF EXISTS last_active_at;
