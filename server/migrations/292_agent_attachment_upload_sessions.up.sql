CREATE TABLE agent_attachment_upload_session (
  id UUID PRIMARY KEY,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
  thread_root_message_id UUID REFERENCES channel_message(id) ON DELETE CASCADE,
  context_target TEXT NOT NULL,
  object_key TEXT NOT NULL UNIQUE,
  filename TEXT NOT NULL,
  content_type TEXT NOT NULL,
  size_bytes BIGINT NOT NULL CHECK (size_bytes > 0),
  upload_mode TEXT NOT NULL CHECK (upload_mode IN ('local', 'presigned')),
  state TEXT NOT NULL CHECK (state IN ('pending', 'completed')),
  expires_at TIMESTAMPTZ NOT NULL,
  attachment_id UUID UNIQUE REFERENCES attachment(id) ON DELETE RESTRICT,
  completed_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (expires_at > created_at),
  CHECK (
    (state = 'pending' AND attachment_id IS NULL AND completed_at IS NULL)
    OR
    (state = 'completed' AND attachment_id IS NOT NULL AND completed_at IS NOT NULL)
  )
);

CREATE INDEX idx_agent_attachment_upload_session_attachment
  ON agent_attachment_upload_session(workspace_id, agent_id, attachment_id)
  WHERE attachment_id IS NOT NULL;

CREATE INDEX idx_agent_attachment_upload_session_agent
  ON agent_attachment_upload_session(agent_id);

CREATE INDEX idx_agent_attachment_upload_session_target
  ON agent_attachment_upload_session(workspace_id, agent_id, channel_id, thread_root_message_id, expires_at);
