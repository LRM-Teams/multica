BEGIN;

-- Detection remains periodic, but release mutation is explicit-only. These
-- values let the durable observation report that distinction without reusing
-- an auto-install-enabled source or outcome.
ALTER TABLE daemon_update_status
    DROP CONSTRAINT IF EXISTS daemon_update_status_config_source_check,
    DROP CONSTRAINT IF EXISTS daemon_update_status_last_outcome_check;

ALTER TABLE daemon_update_status
    ADD CONSTRAINT daemon_update_status_config_source_check CHECK (config_source IN (
        'official_host_default',
        'self_host_default',
        'env_enabled',
        'env_disabled',
        'cli_disabled',
        'deprecated_noop',
        'auto_detect'
    )),
    ADD CONSTRAINT daemon_update_status_last_outcome_check CHECK (last_outcome IN (
        'never_checked',
        'up_to_date',
        'update_available',
        'busy',
        'pinned',
        'fetch_failed',
        'update_failed',
        'verification_failed',
        'update_succeeded',
        'interrupted',
        'explicit_only'
    ));

COMMIT;
