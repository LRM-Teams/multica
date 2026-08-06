CREATE INDEX IF NOT EXISTS idx_member_workspace_created_at
  ON member(workspace_id, created_at ASC);

CREATE INDEX IF NOT EXISTS idx_agent_workspace_active_created_at
  ON agent(workspace_id, created_at ASC)
  WHERE archived_at IS NULL;
