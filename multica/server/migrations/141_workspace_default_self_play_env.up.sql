-- 141_workspace_default_self_play_env.up.sql
-- Per-workspace default base env used by env-dispatch when a self_play
-- (message) dispatch is called with an empty env_id. Set out-of-band;
-- env-dispatch only reads it. ON DELETE SET NULL so deleting the referenced
-- base env clears the default rather than blocking the delete.
ALTER TABLE workspace
    ADD COLUMN default_self_play_env_id UUID NULL REFERENCES environment(id) ON DELETE SET NULL;
