ALTER TABLE channel_message
  ADD COLUMN IF NOT EXISTS thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE;

ALTER TABLE chat_message
  ADD COLUMN IF NOT EXISTS channel_thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_channel_message_thread_root_created
  ON channel_message(channel_id, thread_root_message_id, created_at, id)
  WHERE thread_root_message_id IS NOT NULL;

CREATE TABLE IF NOT EXISTS channel_thread_state (
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  root_message_id UUID NOT NULL REFERENCES channel_message(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  last_read_at TIMESTAMPTZ,
  followed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (root_message_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_channel_thread_state_user_unread
  ON channel_thread_state(user_id, followed_at, last_read_at);
