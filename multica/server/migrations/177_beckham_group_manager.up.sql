-- 贝克汉姆 (Beckham) group-manager agent — foundation.
--
-- Beckham is a brand-new, per-group agent (one and only one per group) that owns
-- proactive group behavior (ambient review, coordination handoffs). It is fully
-- independent of Wendy.
--
-- managed_role marks an agent's managed nature so resolution never relies on the
-- display name (name is free to change). group_manager_agent_id binds a group
-- channel to its single Beckham, giving a fast, unambiguous one-per-group lookup.

ALTER TABLE agent
  ADD COLUMN IF NOT EXISTS managed_role TEXT;

ALTER TABLE agent
  DROP CONSTRAINT IF EXISTS agent_managed_role_check;
ALTER TABLE agent
  ADD CONSTRAINT agent_managed_role_check
  CHECK (managed_role IS NULL OR managed_role IN ('group_manager'));

ALTER TABLE channel
  ADD COLUMN IF NOT EXISTS group_manager_agent_id UUID REFERENCES agent(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS agent_managed_role_idx
  ON agent (workspace_id, managed_role)
  WHERE managed_role IS NOT NULL;
