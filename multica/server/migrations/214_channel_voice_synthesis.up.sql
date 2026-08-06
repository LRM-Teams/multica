BEGIN;

CREATE TABLE channel_voice_synthesis (
  message_id UUID PRIMARY KEY REFERENCES channel_message(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  attachment_id UUID NOT NULL DEFAULT gen_random_uuid(),
  status TEXT NOT NULL DEFAULT 'pending'
    CHECK (status IN ('pending', 'processing', 'retry', 'completed', 'failed')),
  attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
  next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  claimed_at TIMESTAMPTZ,
  last_error_code TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX channel_voice_synthesis_due_idx
  ON channel_voice_synthesis (next_attempt_at, created_at, message_id)
  WHERE status IN ('pending', 'retry', 'processing');

CREATE FUNCTION enqueue_channel_voice_synthesis()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
  IF NEW.author_type = 'agent'
     AND NEW.parts @> '[{"type":"voice","synthesis_status":"pending"}]'::jsonb THEN
    INSERT INTO channel_voice_synthesis (message_id, workspace_id, channel_id)
    VALUES (NEW.id, NEW.workspace_id, NEW.channel_id);
  END IF;
  RETURN NEW;
END;
$$;

CREATE TRIGGER channel_message_enqueue_voice_synthesis
AFTER INSERT ON channel_message
FOR EACH ROW
EXECUTE FUNCTION enqueue_channel_voice_synthesis();

COMMIT;
