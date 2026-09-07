-- Skill Evolution's 493-495 ledger FKs were added after the earlier agent
-- and channel teardown index migrations. PostgreSQL checks each FK child
-- relation during CASCADE/SET NULL enforcement; these indexes prevent a
-- sequential scan of the ledger tables per deleted agent, channel, approval,
-- or evaluation run. Keep this forward-only and idempotent for retries.

CREATE INDEX IF NOT EXISTS idx_skill_evolution_run_target_agent_id
    ON skill_evolution_run (target_agent_id);
CREATE INDEX IF NOT EXISTS idx_skill_evaluation_run_target_agent_id
    ON skill_evaluation_run (target_agent_id);
CREATE INDEX IF NOT EXISTS idx_skill_deployment_target_agent_id
    ON skill_deployment (target_agent_id)
    WHERE target_agent_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_skill_deployment_target_channel_id
    ON skill_deployment (target_channel_id)
    WHERE target_channel_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_skill_deployment_approval
    ON skill_deployment (workspace_id, approval_id);
CREATE INDEX IF NOT EXISTS idx_skill_approval_evaluation
    ON skill_approval (workspace_id, evaluation_id);
