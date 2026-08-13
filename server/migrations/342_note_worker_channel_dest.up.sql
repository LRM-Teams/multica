-- Note Worker destination: main channel/DM timeline (not standalone chat_session).
ALTER TABLE note_worker_job
  ADD COLUMN IF NOT EXISTS channel_id UUID REFERENCES channel(id) ON DELETE SET NULL,
  ADD COLUMN IF NOT EXISTS channel_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS note_worker_job_channel_idx
  ON note_worker_job(channel_id, created_at DESC)
  WHERE channel_id IS NOT NULL;
