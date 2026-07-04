ALTER TABLE member
ADD COLUMN IF NOT EXISTS last_active_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_member_user_last_active
ON member(user_id, last_active_at DESC)
WHERE last_active_at IS NOT NULL;
