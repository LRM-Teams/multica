DROP INDEX IF EXISTS idx_workspace_onboarding_agent_id;
ALTER TABLE workspace DROP COLUMN IF EXISTS onboarding_agent_id;
