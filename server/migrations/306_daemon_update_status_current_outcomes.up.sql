ALTER TABLE daemon_update_status
    DROP CONSTRAINT IF EXISTS daemon_update_status_config_source_check,
    DROP CONSTRAINT IF EXISTS daemon_update_status_ineligible_reason_check,
    DROP CONSTRAINT IF EXISTS daemon_update_status_last_outcome_check;

ALTER TABLE daemon_update_status
    ADD CONSTRAINT daemon_update_status_config_source_check CHECK (config_source IN (
        'official_host_default',
        'self_host_default',
        'env_enabled',
        'env_disabled',
        'cli_disabled',
        'deprecated_noop'
    )),
    ADD CONSTRAINT daemon_update_status_ineligible_reason_check CHECK (
        ineligible_reason IS NULL OR
        ineligible_reason IN ('desktop_managed', 'non_release_build', 'explicit_only')
    ),
    ADD CONSTRAINT daemon_update_status_last_outcome_check CHECK (last_outcome IN (
        'never_checked',
        'up_to_date',
        'busy',
        'pinned',
        'fetch_failed',
        'update_failed',
        'verification_failed',
        'update_succeeded',
        'interrupted',
        'explicit_only'
    ));
