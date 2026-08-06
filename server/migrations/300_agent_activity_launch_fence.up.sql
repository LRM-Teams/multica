-- The current managed launch is the server-side fence for Runner Activity.
-- It is intentionally independent from provider session and runtime generation.
CREATE TABLE agent_activity_launch (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    daemon_id TEXT NOT NULL CHECK (length(btrim(daemon_id)) BETWEEN 1 AND 200),
    daemon_instance_id TEXT NOT NULL CHECK (length(btrim(daemon_instance_id)) BETWEEN 1 AND 200),
    launch_id TEXT NOT NULL CHECK (length(btrim(launch_id)) BETWEEN 1 AND 200),
    status TEXT NOT NULL CHECK (status IN ('active', 'inactive')),
    last_client_sequence BIGINT NOT NULL DEFAULT 0 CHECK (last_client_sequence >= 0),
    last_producer_fact_id TEXT NOT NULL DEFAULT '' CHECK (length(last_producer_fact_id) <= 200),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id)
);

CREATE INDEX agent_activity_launch_runner_idx
    ON agent_activity_launch (workspace_id, daemon_id, daemon_instance_id, launch_id);
