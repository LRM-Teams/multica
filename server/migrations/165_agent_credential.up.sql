CREATE TABLE IF NOT EXISTS agent_credential (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  token_hash TEXT NOT NULL,
  token_prefix TEXT NOT NULL,
  agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
  workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
  user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
  expires_at TIMESTAMPTZ,
  revoked_at TIMESTAMPTZ,
  disabled_at TIMESTAMPTZ,
  last_used_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CHECK (expires_at IS NULL OR expires_at > created_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_agent_credential_hash
  ON agent_credential(token_hash);

CREATE INDEX IF NOT EXISTS idx_agent_credential_agent_active
  ON agent_credential(agent_id, created_at DESC)
  WHERE revoked_at IS NULL AND disabled_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_agent_credential_workspace_user
  ON agent_credential(workspace_id, user_id, created_at DESC);
