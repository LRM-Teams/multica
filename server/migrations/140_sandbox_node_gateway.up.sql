CREATE TABLE sandbox_node (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_key TEXT NOT NULL UNIQUE,
    name TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'offline' CHECK (status IN ('online', 'offline')),
    capabilities JSONB NOT NULL DEFAULT '[]'::jsonb,
    max_concurrency INTEGER NOT NULL DEFAULT 1 CHECK (max_concurrency > 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_seen_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sandbox_node_token (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    node_id UUID NOT NULL REFERENCES sandbox_node(id) ON DELETE CASCADE,
    name TEXT NOT NULL DEFAULT 'default',
    token_hash TEXT NOT NULL UNIQUE,
    token_prefix TEXT NOT NULL,
    expires_at TIMESTAMPTZ,
    revoked_at TIMESTAMPTZ,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sandbox_workspace_binding (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    node_id UUID NOT NULL REFERENCES sandbox_node(id) ON DELETE CASCADE,
    enabled BOOLEAN NOT NULL DEFAULT true,
    policy JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, node_id)
);

CREATE TABLE sandbox_instance (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    creator_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    node_id UUID NOT NULL REFERENCES sandbox_node(id) ON DELETE RESTRICT,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'creating', 'running', 'failed', 'stopping', 'stopped', 'resuming')),
    template TEXT NOT NULL,
    local_ref TEXT,
    endpoint_info JSONB NOT NULL DEFAULT '{}'::jsonb,
    limits JSONB NOT NULL DEFAULT '{}'::jsonb,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE sandbox_job (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    initiator_user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE RESTRICT,
    node_id UUID NOT NULL REFERENCES sandbox_node(id) ON DELETE RESTRICT,
    instance_id UUID NOT NULL REFERENCES sandbox_instance(id) ON DELETE CASCADE,
    type TEXT NOT NULL CHECK (type IN ('create', 'stop', 'resume', 'delete', 'exec', 'message')),
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'dispatched', 'running', 'completed', 'failed', 'cancelled')),
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    error TEXT,
    lease_until TIMESTAMPTZ,
    started_at TIMESTAMPTZ,
    completed_at TIMESTAMPTZ,
    job_token_hash TEXT UNIQUE,
    job_token_expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX sandbox_workspace_binding_workspace_idx ON sandbox_workspace_binding(workspace_id) WHERE enabled;
CREATE INDEX sandbox_instance_workspace_idx ON sandbox_instance(workspace_id, created_at DESC);
CREATE INDEX sandbox_job_node_queue_idx ON sandbox_job(node_id, status, created_at);
CREATE INDEX sandbox_job_instance_idx ON sandbox_job(instance_id, created_at DESC);
