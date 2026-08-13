ALTER TABLE note_worker_job
  DROP COLUMN IF EXISTS channel_message_id,
  DROP COLUMN IF EXISTS channel_id;
