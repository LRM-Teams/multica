CREATE TABLE problem_evolution_secret (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    run_id UUID REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    kind TEXT NOT NULL DEFAULT 'hidden_answer' CHECK (kind IN (
        'hidden_answer', 'hidden_cases', 'verifier_config'
    )),
    label TEXT NOT NULL DEFAULT '',
    -- Envelope encryption: `ciphertext` is sealed with a per-secret data key,
    -- which is itself sealed with the server master key named by `key_id`.
    -- Plaintext never exists in a column, a log line or an event payload.
    ciphertext BYTEA NOT NULL,
    nonce BYTEA NOT NULL,
    wrapped_key BYTEA NOT NULL,
    wrapped_key_nonce BYTEA NOT NULL,
    key_id TEXT NOT NULL,
    content_hash TEXT NOT NULL DEFAULT '',
    created_by UUID REFERENCES "user"(id) ON DELETE SET NULL,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX problem_evolution_secret_workspace_idx
    ON problem_evolution_secret(workspace_id, created_at DESC);
CREATE INDEX problem_evolution_secret_run_idx
    ON problem_evolution_secret(run_id)
    WHERE run_id IS NOT NULL;

CREATE TABLE problem_evolution_secret_capability (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    secret_id UUID NOT NULL REFERENCES problem_evolution_secret(id) ON DELETE CASCADE,
    run_id UUID NOT NULL REFERENCES problem_evolution_run(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    -- Only the hash is stored: a leaked database row must not be a usable
    -- capability, the same reason session tokens are not stored in the clear.
    token_hash TEXT NOT NULL UNIQUE,
    -- Who may redeem it. `verifier` is the only audience that ever sees
    -- plaintext; an evolver capability would defeat the whole isolation.
    audience TEXT NOT NULL DEFAULT 'verifier' CHECK (audience = 'verifier'),
    max_uses INTEGER NOT NULL DEFAULT 1 CHECK (max_uses > 0),
    uses INTEGER NOT NULL DEFAULT 0 CHECK (uses >= 0),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    issued_to TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (uses <= max_uses)
);

CREATE INDEX problem_evolution_secret_capability_run_idx
    ON problem_evolution_secret_capability(run_id, created_at DESC);
CREATE INDEX problem_evolution_secret_capability_expiry_idx
    ON problem_evolution_secret_capability(expires_at)
    WHERE revoked_at IS NULL;

CREATE TABLE problem_evolution_secret_audit (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    secret_id UUID REFERENCES problem_evolution_secret(id) ON DELETE SET NULL,
    capability_id UUID REFERENCES problem_evolution_secret_capability(id) ON DELETE SET NULL,
    run_id UUID REFERENCES problem_evolution_run(id) ON DELETE SET NULL,
    action TEXT NOT NULL CHECK (action IN (
        'created', 'issued', 'redeemed', 'denied', 'revoked', 'expired'
    )),
    -- Why a redemption was refused. Denials are the interesting rows: they are
    -- how an attempt to reach a hidden answer from the wrong side shows up.
    reason TEXT NOT NULL DEFAULT '',
    actor_type TEXT NOT NULL DEFAULT 'system',
    actor_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX problem_evolution_secret_audit_run_idx
    ON problem_evolution_secret_audit(run_id, created_at DESC);
CREATE INDEX problem_evolution_secret_audit_secret_idx
    ON problem_evolution_secret_audit(secret_id, created_at DESC);
