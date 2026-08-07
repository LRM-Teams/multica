UPDATE daemon_update_status
SET config_source = CASE
        WHEN config_source = 'deprecated_noop' THEN 'cli_disabled'
        ELSE config_source
    END,
    ineligible_reason = CASE
        WHEN ineligible_reason = 'explicit_only' THEN NULL
        ELSE ineligible_reason
    END,
    last_outcome = CASE
        WHEN last_outcome IN ('pinned', 'explicit_only') THEN 'never_checked'
        ELSE last_outcome
    END
WHERE config_source = 'deprecated_noop'
   OR ineligible_reason = 'explicit_only'
   OR last_outcome IN ('pinned', 'explicit_only');

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
        'cli_disabled'
    )),
    ADD CONSTRAINT daemon_update_status_ineligible_reason_check CHECK (
        ineligible_reason IS NULL OR
        ineligible_reason IN ('desktop_managed', 'non_release_build')
    ),
    ADD CONSTRAINT daemon_update_status_last_outcome_check CHECK (last_outcome IN (
        'never_checked',
        'up_to_date',
        'busy',
        'fetch_failed',
        'update_failed',
        'verification_failed',
        'update_succeeded',
        'interrupted'
    ));
