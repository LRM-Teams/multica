-- Canonical server-side Agent Attachment projection. This is deliberately
-- separate from managed process launch: an attachment only assigns durable
-- local responsibility to a Workspace Runner.

CREATE TABLE agent_attachment_projection (
  agent_id UUID PRIMARY KEY REFERENCES agent(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE RESTRICT,
  attachment_generation BIGINT NOT NULL CHECK (attachment_generation > 0),
  lifecycle_seq BIGINT NOT NULL CHECK (lifecycle_seq > 0),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (agent_id, attachment_generation)
);

CREATE TABLE agent_attachment_projection_event (
  lifecycle_seq BIGINT PRIMARY KEY,
  -- Historical events survive Agent/Runtime deletion so reconnect/replay can
  -- fence an obsolete owner. Current-state integrity lives in the projection
  -- table above; this ledger deliberately keeps scalar identities.
  agent_id UUID NOT NULL,
  workspace_id UUID NOT NULL,
  runtime_id UUID NOT NULL,
  attachment_generation BIGINT NOT NULL CHECK (attachment_generation > 0),
  event_type TEXT NOT NULL CHECK (event_type IN ('attach', 'detach')),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX agent_attachment_projection_event_runtime_idx
  ON agent_attachment_projection_event (runtime_id, lifecycle_seq);

-- The existing owner-event trigger is the trusted placement transition. Its
-- sequence and placement_generation fence the Attachment projection too.
INSERT INTO agent_attachment_projection_event (
  lifecycle_seq, agent_id, workspace_id, runtime_id, attachment_generation, event_type, created_at
)
SELECT seq, agent_id, workspace_id, runtime_id, placement_generation,
       CASE event_type WHEN 'start' THEN 'attach' ELSE 'detach' END,
       created_at
FROM agent_reminder_daemon_owner_event;

INSERT INTO agent_attachment_projection (
  agent_id, workspace_id, runtime_id, attachment_generation, lifecycle_seq, updated_at
)
SELECT DISTINCT ON (agent_id)
  agent_id, workspace_id, runtime_id, placement_generation, seq, created_at
FROM agent_reminder_daemon_owner_event
WHERE event_type = 'start'
  AND EXISTS (
    SELECT 1 FROM agent
    WHERE agent.id = agent_reminder_daemon_owner_event.agent_id
      AND agent.archived_at IS NULL
      AND agent.runtime_id = agent_reminder_daemon_owner_event.runtime_id
  )
ORDER BY agent_id, seq DESC;
