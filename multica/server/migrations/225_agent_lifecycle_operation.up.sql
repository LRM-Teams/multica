BEGIN;

-- Agent lifecycle operations are the durable, user-visible control ledger for
-- restart/reset actions. The daemon may execute these only after D6 advertises
-- agent_lifecycle_actions_v1; until then the API reports the runtime as
-- unsupported and never creates an operation.
CREATE TABLE agent_lifecycle_operation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    actor_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    idempotency_key UUID NOT NULL,
    action_kind TEXT NOT NULL
        CHECK (action_kind IN (
            'restart',
            'reset_session_restart',
            'full_reset_restart'
        )),
    status TEXT NOT NULL
        CHECK (status IN ('scheduled', 'running', 'succeeded', 'failed')),
    execution_mode TEXT NOT NULL
        CHECK (execution_mode IN ('immediate', 'after_current_run')),
    step TEXT NOT NULL DEFAULT '',
    reason_code TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    started_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, idempotency_key),
    CHECK (
        (status = 'scheduled' AND started_at IS NULL AND finished_at IS NULL)
        OR (status = 'running' AND started_at IS NOT NULL AND finished_at IS NULL)
        OR (status IN ('succeeded', 'failed') AND started_at IS NOT NULL AND finished_at IS NOT NULL)
    )
);

CREATE UNIQUE INDEX agent_lifecycle_operation_one_active_per_agent_idx
    ON agent_lifecycle_operation (agent_id)
    WHERE status IN ('scheduled', 'running');

CREATE INDEX agent_lifecycle_operation_latest_idx
    ON agent_lifecycle_operation (agent_id, created_at DESC, id DESC);

COMMIT;
