CREATE TABLE problem_evolution_harness_registry (
    workspace_id UUID PRIMARY KEY REFERENCES workspace(id) ON DELETE CASCADE,
    harness_version_id UUID NOT NULL UNIQUE
        REFERENCES problem_evolution_harness_version(id) ON DELETE RESTRICT,
    content_hash TEXT NOT NULL,
    promoted_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, content_hash)
);
