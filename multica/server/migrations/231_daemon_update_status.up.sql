CREATE TABLE daemon_update_status (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    daemon_id TEXT NOT NULL,
    session_id UUID NOT NULL,
    revision BIGINT NOT NULL CHECK (revision > 0),
    observed_at TIMESTAMPTZ NOT NULL,
    auto_update_effective_enabled BOOLEAN NOT NULL,
    config_source TEXT NOT NULL CHECK (config_source IN (
        'official_host_default',
        'self_host_default',
        'env_enabled',
        'env_disabled',
        'cli_disabled'
    )),
    ineligible_reason TEXT CHECK (
        ineligible_reason IS NULL OR
        ineligible_reason IN ('desktop_managed', 'non_release_build')
    ),
    check_interval_seconds BIGINT NOT NULL CHECK (check_interval_seconds > 0),
    phase TEXT NOT NULL CHECK (phase IN (
        'disabled',
        'waiting',
        'checking',
        'updating',
        'restart_pending'
    )),
    attempt_source TEXT CHECK (
        attempt_source IS NULL OR attempt_source IN ('auto', 'server')
    ),
    last_attempt_at TIMESTAMPTZ,
    last_outcome TEXT NOT NULL CHECK (last_outcome IN (
        'never_checked',
        'up_to_date',
        'busy',
        'fetch_failed',
        'update_failed',
        'verification_failed',
        'update_succeeded',
        'interrupted'
    )),
    target_version TEXT,
    error_code TEXT CHECK (
        error_code IS NULL OR error_code IN (
            'daemon_restarted_during_update',
            'release_fetch_failed',
            'download_update_failed',
            'updated_binary_verification_failed',
            'desktop_managed'
        )
    ),
    error_message TEXT,
    staged_version TEXT,
    activation_generation BIGINT CHECK (
        activation_generation IS NULL OR activation_generation >= 0
    ),
    payload_hash TEXT NOT NULL CHECK (length(payload_hash) = 64),
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, daemon_id)
);

CREATE INDEX daemon_update_status_workspace_idx
    ON daemon_update_status (workspace_id);
