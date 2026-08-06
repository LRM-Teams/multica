ALTER TABLE channel
  ADD COLUMN IF NOT EXISTS archived_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS archived_by UUID REFERENCES "user"(id) ON DELETE SET NULL;

ALTER TABLE channel_member
  ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMPTZ,
  ADD COLUMN IF NOT EXISTS manual_unread_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_channel_workspace_kind_archive_updated
  ON channel (workspace_id, kind, archived_at, updated_at DESC, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_channel_member_user_state
  ON channel_member (workspace_id, member_type, member_id, pinned_at DESC, manual_unread_at);
