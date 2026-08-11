BEGIN;

UPDATE daemon_update_status
SET config_source = CASE WHEN config_source = 'auto_detect' THEN 'deprecated_noop' ELSE config_source END,
    last_outcome = CASE WHEN last_outcome = 'update_available' THEN 'never_checked' ELSE last_outcome END,
    updated_at = now()
WHERE config_source = 'auto_detect' OR last_outcome = 'update_available';

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
        'deprecated_noop'
    )),
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

COMMIT;
