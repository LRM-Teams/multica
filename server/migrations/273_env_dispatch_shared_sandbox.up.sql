-- shared_sandbox rollouts (spec 003-env-dispatch-shared-sandbox) attach every
-- squad member's binding to the sample's single shared sandbox_instance and
-- agent_runtime, so the per-binding UNIQUE constraints inherited from
-- migration 186 must not apply to shared bindings. Replace them with partial
-- unique indexes that keep the 1:1 binding↔sandbox/runtime invariant for
-- non-shared bindings exactly as before, and exclude bindings whose
-- sandbox_config carries the shared marker.

ALTER TABLE environment_agent_sandbox
    DROP CONSTRAINT IF EXISTS environment_agent_sandbox_sandbox_instance_id_key,
    DROP CONSTRAINT IF EXISTS environment_agent_sandbox_runtime_id_key;

CREATE UNIQUE INDEX IF NOT EXISTS environment_agent_sandbox_sandbox_instance_uidx
    ON environment_agent_sandbox (sandbox_instance_id)
    WHERE sandbox_instance_id IS NOT NULL
      AND COALESCE(sandbox_config->>'shared', 'false') <> 'true';

CREATE UNIQUE INDEX IF NOT EXISTS environment_agent_sandbox_runtime_uidx
    ON environment_agent_sandbox (runtime_id)
    WHERE runtime_id IS NOT NULL
      AND COALESCE(sandbox_config->>'shared', 'false') <> 'true';
