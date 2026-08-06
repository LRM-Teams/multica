BEGIN;

CREATE TABLE channel_voice_transcription (
  message_id UUID PRIMARY KEY REFERENCES channel_message(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  attachment_id UUID NOT NULL REFERENCES attachment(id) ON DELETE CASCADE,
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'retry', 'transcribed', 'dispatching', 'completed', 'failed')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  transcript TEXT NOT NULL DEFAULT '',
  last_error_code TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX channel_voice_transcription_due_idx
  ON channel_voice_transcription (next_attempt_at, created_at, message_id)
  WHERE status IN ('pending', 'retry', 'processing', 'transcribed', 'dispatching');

COMMIT;
