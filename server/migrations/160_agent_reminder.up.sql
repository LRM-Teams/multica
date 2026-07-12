CREATE TABLE agent_reminder (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  title TEXT NOT NULL CHECK (char_length(title) BETWEEN 1 AND 500),
  anchor_channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  anchor_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  anchor_thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE SET NULL,
  fire_at TIMESTAMPTZ NOT NULL,
  status TEXT NOT NULL DEFAULT 'scheduled'
    CHECK (status IN ('scheduled', 'firing', 'fired', 'cancelled')),
  fired_task_id UUID REFERENCES agent_task_queue(id) ON DELETE SET NULL,
  snooze_count INTEGER NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  fired_at TIMESTAMPTZ
);

CREATE INDEX idx_agent_reminder_due
  ON agent_reminder(fire_at)
  WHERE status = 'scheduled';

CREATE INDEX idx_agent_reminder_agent_active
  ON agent_reminder(workspace_id, agent_id)
  WHERE status = 'scheduled';
