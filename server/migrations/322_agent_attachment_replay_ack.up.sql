-- Durable command identity and acknowledgement state for Workspace Runner
-- Attachment replay.  The lifecycle sequence remains the ordering fence;
-- correlation_id is the stable identity of one replayable command.

ALTER TABLE agent_attachment_projection_event
  ADD COLUMN correlation_id UUID;

UPDATE agent_attachment_projection_event
SET correlation_id = gen_random_uuid()
WHERE correlation_id IS NULL;

ALTER TABLE agent_attachment_projection_event
  ALTER COLUMN correlation_id SET NOT NULL;

ALTER TABLE agent_attachment_projection_event
  ADD CONSTRAINT agent_attachment_projection_event_correlation_key UNIQUE (correlation_id);

CREATE TABLE agent_attachment_projection_cursor (
  runtime_id UUID PRIMARY KEY REFERENCES agent_runtime(id) ON DELETE CASCADE,
  ack_seq BIGINT NOT NULL DEFAULT 0 CHECK (ack_seq >= 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE agent_attachment_projection_receipt (
  lifecycle_seq BIGINT PRIMARY KEY REFERENCES agent_attachment_projection_event(lifecycle_seq) ON DELETE CASCADE,
  correlation_id UUID NOT NULL UNIQUE,
  receipt_type TEXT NOT NULL CHECK (receipt_type IN ('attached', 'detached')),
  acknowledged_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Migration 321 installed the projection trigger before correlation identity
-- existed. Replacing it here keeps each future command replayable by the same
-- correlation_id across a server restart.
CREATE OR REPLACE FUNCTION project_agent_attachment_projection()
RETURNS TRIGGER AS $$
DECLARE event_row agent_reminder_daemon_owner_event%ROWTYPE;
BEGIN
  IF (TG_OP = 'UPDATE' OR TG_OP = 'DELETE') AND OLD.runtime_id IS NOT NULL
     AND (TG_OP = 'DELETE' OR OLD.runtime_id IS DISTINCT FROM NEW.runtime_id
          OR (OLD.archived_at IS NULL AND NEW.archived_at IS NOT NULL)) THEN
    SELECT * INTO event_row FROM agent_reminder_daemon_owner_event
    WHERE agent_id = OLD.id AND runtime_id = OLD.runtime_id AND event_type = 'stop'
    ORDER BY seq DESC LIMIT 1;
    IF FOUND THEN
      INSERT INTO agent_attachment_projection_event
        (lifecycle_seq, agent_id, workspace_id, runtime_id, attachment_generation, event_type, correlation_id)
      VALUES (event_row.seq, OLD.id, OLD.workspace_id, OLD.runtime_id, event_row.placement_generation, 'detach', gen_random_uuid())
      ON CONFLICT (lifecycle_seq) DO NOTHING;
      DELETE FROM agent_attachment_projection
      WHERE agent_id = OLD.id AND runtime_id = OLD.runtime_id
        AND attachment_generation <= event_row.placement_generation;
    END IF;
  END IF;

  IF TG_OP <> 'DELETE' AND NEW.runtime_id IS NOT NULL AND NEW.archived_at IS NULL
     AND (TG_OP = 'INSERT' OR OLD.runtime_id IS DISTINCT FROM NEW.runtime_id
          OR (OLD.archived_at IS NOT NULL AND NEW.archived_at IS NULL)) THEN
    SELECT * INTO event_row FROM agent_reminder_daemon_owner_event
    WHERE agent_id = NEW.id AND runtime_id = NEW.runtime_id AND event_type = 'start'
    ORDER BY seq DESC LIMIT 1;
    IF FOUND THEN
      INSERT INTO agent_attachment_projection_event
        (lifecycle_seq, agent_id, workspace_id, runtime_id, attachment_generation, event_type, correlation_id)
      VALUES (event_row.seq, NEW.id, NEW.workspace_id, NEW.runtime_id, event_row.placement_generation, 'attach', gen_random_uuid())
      ON CONFLICT (lifecycle_seq) DO NOTHING;
      INSERT INTO agent_attachment_projection
        (agent_id, workspace_id, runtime_id, attachment_generation, lifecycle_seq)
      VALUES (NEW.id, NEW.workspace_id, NEW.runtime_id, event_row.placement_generation, event_row.seq)
      ON CONFLICT (agent_id) DO UPDATE SET
        workspace_id = EXCLUDED.workspace_id, runtime_id = EXCLUDED.runtime_id,
        attachment_generation = EXCLUDED.attachment_generation,
        lifecycle_seq = EXCLUDED.lifecycle_seq, updated_at = now()
      WHERE agent_attachment_projection.lifecycle_seq < EXCLUDED.lifecycle_seq;
    END IF;
  END IF;
  RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;
