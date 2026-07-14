DROP INDEX IF EXISTS agent_managed_role_idx;

ALTER TABLE channel
  DROP COLUMN IF EXISTS group_manager_agent_id;

ALTER TABLE agent
  DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent
  DROP COLUMN IF EXISTS managed_role;
