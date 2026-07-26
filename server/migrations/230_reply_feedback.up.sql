-- Message-level reply preference (👍/👎) for agent final answers.
-- Distinct from social channel_message_reaction emoji reactions.
CREATE TABLE reply_feedback (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  message_kind TEXT NOT NULL CHECK (message_kind IN ('channel', 'chat')),
  message_id UUID NOT NULL,
  task_id UUID,
  agent_id UUID,
  actor_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  value SMALLINT NOT NULL CHECK (value IN (1, -1)),
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (message_kind, message_id, actor_user_id)
);

CREATE INDEX idx_reply_feedback_message
  ON reply_feedback(message_kind, message_id);

CREATE INDEX idx_reply_feedback_workspace_agent
  ON reply_feedback(workspace_id, agent_id)
  WHERE agent_id IS NOT NULL;
