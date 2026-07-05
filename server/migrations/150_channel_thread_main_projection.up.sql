ALTER TABLE channel_message
  ADD COLUMN IF NOT EXISTS main_timeline_visible BOOLEAN NOT NULL DEFAULT false;

CREATE INDEX IF NOT EXISTS idx_channel_message_main_projection_seq
  ON channel_message(channel_id, workspace_id, seq DESC)
  WHERE thread_root_message_id IS NULL OR main_timeline_visible;
