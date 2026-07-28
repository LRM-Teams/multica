ALTER TABLE agent
  DROP CONSTRAINT agent_workspace_role_check;

ALTER TABLE agent
  DROP COLUMN workspace_role;
