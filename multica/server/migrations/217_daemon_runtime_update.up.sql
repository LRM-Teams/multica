BEGIN;

CREATE TABLE daemon_runtime_update (
    id TEXT PRIMARY KEY,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending'
        CHECK (status IN (
            'pending',
            'running',
            'ready_to_apply',
            'completed',
            'failed',
            'timeout'
        )),
    target_version TEXT NOT NULL CHECK (btrim(target_version) <> ''),
    output TEXT NOT NULL DEFAULT '',
    error TEXT NOT NULL DEFAULT '',
    run_started_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX daemon_runtime_update_one_active_per_runtime_idx
    ON daemon_runtime_update (runtime_id)
    WHERE status IN ('pending', 'running', 'ready_to_apply');

CREATE INDEX daemon_runtime_update_pending_idx
    ON daemon_runtime_update (runtime_id, created_at, id)
    WHERE status = 'pending';

CREATE INDEX daemon_runtime_update_latest_idx
    ON daemon_runtime_update (runtime_id, updated_at DESC, created_at DESC, id DESC);

COMMIT;
