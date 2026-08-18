-- Persist Computer-level metadata independently of provider Runtime rows so a
-- connected zero-Agent Computer still has a hostname, OS, and CLI version.

ALTER TABLE computer_identity_owner
    ADD COLUMN IF NOT EXISTS device_name TEXT,
    ADD COLUMN IF NOT EXISTS os TEXT,
    ADD COLUMN IF NOT EXISTS cli_version TEXT;
