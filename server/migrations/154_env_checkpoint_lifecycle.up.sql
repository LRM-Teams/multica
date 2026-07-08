CREATE TABLE IF NOT EXISTS env_checkpoint (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id uuid NOT NULL,
    project_id uuid NOT NULL,
    event_ref text NOT NULL,
    checkpoint_kind text NOT NULL,
    env_id_map jsonb NOT NULL DEFAULT '{}'::jsonb,
    sandbox_refs jsonb NOT NULL DEFAULT '[]'::jsonb,
    db_snapshot jsonb NOT NULL DEFAULT '{}'::jsonb,
    entropy_score double precision,
    save_timeout_ms integer NOT NULL,
    save_status text NOT NULL,
    save_error text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS env_checkpoint_project_created_idx
    ON env_checkpoint (workspace_id, project_id, created_at DESC);
