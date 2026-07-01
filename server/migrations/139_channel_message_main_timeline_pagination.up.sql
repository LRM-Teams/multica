CREATE INDEX IF NOT EXISTS idx_channel_message_main_timeline_page
  ON channel_message(channel_id, workspace_id, created_at DESC, id DESC)
  WHERE thread_root_message_id IS NULL;
