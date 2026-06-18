-- channel.project_id: the project a channel is bound to. When set, every agent
-- in the channel works in that project's shared directory (resolved at task
-- claim via channel_agent_session → channel.project_id, taking precedence over
-- a per-session chat_session.project_id). Nullable; cleared (not deleted) if
-- the project is removed.
ALTER TABLE channel
  ADD COLUMN project_id UUID REFERENCES project(id) ON DELETE SET NULL;

CREATE INDEX idx_channel_project
  ON channel(project_id)
  WHERE project_id IS NOT NULL;
