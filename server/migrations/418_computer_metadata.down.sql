ALTER TABLE computer_identity_owner
    DROP COLUMN IF EXISTS device_name,
    DROP COLUMN IF EXISTS os,
    DROP COLUMN IF EXISTS cli_version;
