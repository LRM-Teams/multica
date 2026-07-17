CREATE TABLE sandbox_snapshot (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES sandbox_node(id) ON DELETE RESTRICT,
    instance_id UUID REFERENCES sandbox_instance(id) ON DELETE SET NULL,
    creator_user_id UUID REFERENCES "user"(id) ON DELETE SET NULL,
    cube_snapshot_id TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'creating'
        CHECK (status IN ('creating', 'ready', 'failed', 'deleting')),
    error TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sandbox_snapshot_workspace_idx
    ON sandbox_snapshot (workspace_id, created_at DESC);

CREATE INDEX sandbox_snapshot_node_idx
    ON sandbox_snapshot (node_id, created_at DESC);

CREATE UNIQUE INDEX sandbox_snapshot_cube_id_uidx
    ON sandbox_snapshot (workspace_id, cube_snapshot_id)
    WHERE cube_snapshot_id <> '';

ALTER TABLE sandbox_job
    ALTER COLUMN instance_id DROP NOT NULL;

ALTER TABLE sandbox_job
    DROP CONSTRAINT IF EXISTS sandbox_job_type_check,
    ADD CONSTRAINT sandbox_job_type_check CHECK (type IN (
        'create', 'stop', 'resume', 'delete', 'reconfigure',
        'create_template', 'delete_template', 'exec', 'message'
    ));
