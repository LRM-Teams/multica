CREATE TABLE web_push_subscription (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES "user"(id) ON DELETE CASCADE,
    endpoint TEXT NOT NULL,
    p256dh TEXT NOT NULL,
    auth TEXT NOT NULL,
    expiration_time TIMESTAMPTZ,
    device_id TEXT,
    user_agent TEXT,
    last_active_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error TEXT,
    revoked_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE(endpoint)
);

CREATE INDEX idx_web_push_subscription_recipient
    ON web_push_subscription(user_id)
    WHERE revoked_at IS NULL;

CREATE INDEX idx_web_push_subscription_endpoint
    ON web_push_subscription(endpoint);
