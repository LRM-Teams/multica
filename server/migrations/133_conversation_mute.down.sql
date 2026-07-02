DROP INDEX IF EXISTS idx_dm_peer_state_user_muted;
DROP INDEX IF EXISTS idx_channel_member_user_muted;

ALTER TABLE dm_peer_state
    DROP COLUMN IF EXISTS muted_at;

ALTER TABLE channel_member
    DROP COLUMN IF EXISTS muted_at;
