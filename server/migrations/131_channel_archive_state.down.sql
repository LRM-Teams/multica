DROP INDEX IF EXISTS idx_channel_member_user_state;
DROP INDEX IF EXISTS idx_channel_workspace_kind_archive_updated;

ALTER TABLE channel_member
  DROP COLUMN IF EXISTS manual_unread_at,
  DROP COLUMN IF EXISTS pinned_at;

ALTER TABLE channel
  DROP COLUMN IF EXISTS archived_by,
  DROP COLUMN IF EXISTS archived_at;
