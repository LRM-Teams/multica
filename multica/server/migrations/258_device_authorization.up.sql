CREATE TABLE device_authorization (
    id                   UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_code_hash     TEXT NOT NULL,
    user_code            TEXT NOT NULL,
    client_hint          TEXT NOT NULL DEFAULT '',
    status               TEXT NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'approved', 'denied')),
    approved_by_user_id  UUID REFERENCES "user"(id),
    issued_token_id      UUID REFERENCES personal_access_token(id),
    last_polled_at       TIMESTAMPTZ,
    claimed_at           TIMESTAMPTZ,
    created_at           TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at           TIMESTAMPTZ NOT NULL
);

CREATE UNIQUE INDEX idx_device_authorization_code_hash ON device_authorization(device_code_hash);
CREATE UNIQUE INDEX idx_device_authorization_user_code ON device_authorization(user_code);
CREATE INDEX idx_device_authorization_expires_at ON device_authorization(expires_at);
