-- The migration runner executes each SQL file as one batch, so these indexes
-- cannot use CREATE INDEX CONCURRENTLY here. The current table is small enough
-- for bounded regular DDL; the predicates preserve activity-feed semantics.
CREATE INDEX IF NOT EXISTS idx_channel_message_active_parts_gin
  ON channel_message USING GIN (parts jsonb_path_ops)
  WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_channel_message_active_ws_author_thread
  ON channel_message (workspace_id, author_id, thread_root_message_id)
  WHERE deleted_at IS NULL
    AND author_type = 'user'
    AND thread_root_message_id IS NOT NULL;
