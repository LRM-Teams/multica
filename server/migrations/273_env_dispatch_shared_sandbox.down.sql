-- Revert to the migration-186 per-binding uniqueness. NOTE: this fails while
-- any shared_sandbox bindings still reference the same sandbox_instance or
-- agent_runtime; delete or archive those rollouts first.

DROP INDEX IF EXISTS environment_agent_sandbox_sandbox_instance_uidx;
DROP INDEX IF EXISTS environment_agent_sandbox_runtime_uidx;

ALTER TABLE environment_agent_sandbox
    ADD CONSTRAINT environment_agent_sandbox_sandbox_instance_id_key UNIQUE (sandbox_instance_id),
    ADD CONSTRAINT environment_agent_sandbox_runtime_id_key UNIQUE (runtime_id);
