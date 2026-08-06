ALTER TABLE environment
    ADD COLUMN collaboration_trigger JSONB;

CREATE TABLE environment_agent_sandbox (
    env_id UUID NOT NULL REFERENCES environment(id) ON DELETE CASCADE,
    channel_id UUID NOT NULL REFERENCES channel(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'provisioning', 'ready', 'failed', 'deleting')),
    sandbox_instance_id UUID REFERENCES sandbox_instance(id) ON DELETE SET NULL,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    daemon_id UUID,
    source_sandbox_instance_id UUID REFERENCES sandbox_instance(id) ON DELETE SET NULL,
    sandbox_config JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (env_id, agent_id),
    UNIQUE (sandbox_instance_id),
    UNIQUE (runtime_id),
    CHECK (
        status <> 'ready' OR
        (sandbox_instance_id IS NOT NULL AND runtime_id IS NOT NULL AND daemon_id IS NOT NULL)
    )
);

CREATE INDEX environment_agent_sandbox_channel_idx
    ON environment_agent_sandbox(channel_id);

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure', 'clone',
        'create_template', 'delete_template', 'exec', 'message'
    ));
