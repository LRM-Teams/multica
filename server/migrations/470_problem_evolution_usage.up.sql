CREATE TABLE problem_evolution_usage (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source_event_id UUID REFERENCES problem_evolution_event(id) ON DELETE SET NULL,
    provider TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    model_calls INTEGER NOT NULL DEFAULT 0 CHECK (model_calls >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    cost NUMERIC(12, 6) NOT NULL DEFAULT 0 CHECK (cost >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (run_id, provider, model)
);
CREATE INDEX problem_evolution_usage_workspace_idx
    ON problem_evolution_usage(workspace_id, created_at DESC);
