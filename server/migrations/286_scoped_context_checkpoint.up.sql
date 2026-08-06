CREATE TABLE scoped_context_generation (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID NOT NULL REFERENCES agent_runtime(id) ON DELETE CASCADE,
    scope_kind TEXT NOT NULL CHECK (scope_kind IN ('channel', 'dm', 'standalone_chat', 'issue', 'goal', 'ephemeral')),
    scope_id UUID NOT NULL,
    generation BIGINT NOT NULL CHECK (generation >= 1),
    provider TEXT NOT NULL,
    model TEXT NOT NULL DEFAULT '',
    provider_session_id TEXT NOT NULL CHECK (btrim(provider_session_id) <> ''),
    state TEXT NOT NULL CHECK (state IN ('active', 'archived', 'poisoned', 'invalidated')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at TIMESTAMPTZ,
    rollover_reason TEXT,
    context_tokens_before BIGINT CHECK (context_tokens_before IS NULL OR context_tokens_before >= 0),
    context_tokens_after BIGINT CHECK (context_tokens_after IS NULL OR context_tokens_after >= 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (agent_id, runtime_id, scope_kind, scope_id, generation)
);

CREATE UNIQUE INDEX scoped_context_generation_one_active
    ON scoped_context_generation (agent_id, runtime_id, scope_kind, scope_id)
    WHERE state = 'active';

CREATE TABLE scoped_context_checkpoint (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    source_generation_id UUID NOT NULL REFERENCES scoped_context_generation(id) ON DELETE RESTRICT,
    target_generation_id UUID REFERENCES scoped_context_generation(id) ON DELETE SET NULL,
    structured_state JSONB NOT NULL,
    summary_text TEXT NOT NULL DEFAULT '',
    source_refs JSONB NOT NULL DEFAULT '[]'::jsonb,
    covered_until_seq BIGINT,
    authority_epoch BIGINT NOT NULL DEFAULT 1 CHECK (authority_epoch >= 1),
    validation_state TEXT NOT NULL DEFAULT 'unverified' CHECK (validation_state IN ('unverified', 'validated', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (jsonb_typeof(structured_state) = 'object'),
    CHECK (jsonb_typeof(source_refs) = 'array')
);

CREATE INDEX scoped_context_checkpoint_source
    ON scoped_context_checkpoint (source_generation_id, created_at DESC);

CREATE INDEX scoped_context_checkpoint_target_generation
    ON scoped_context_checkpoint (target_generation_id);
