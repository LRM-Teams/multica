ALTER TABLE agent
  ADD COLUMN workspace_role TEXT NOT NULL DEFAULT 'member';

ALTER TABLE agent
  ADD CONSTRAINT agent_workspace_role_check
  CHECK (workspace_role IN ('member', 'admin'));
