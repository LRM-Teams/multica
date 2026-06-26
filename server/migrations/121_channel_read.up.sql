-- Per-user read state for channels. unread = messages created after the
-- user's last_read_at (excluding their own). A missing row means "never read"
-- (all messages unread).
CREATE TABLE channel_read (
  channel_id   UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  user_id      UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  last_read_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (channel_id, user_id)
);
