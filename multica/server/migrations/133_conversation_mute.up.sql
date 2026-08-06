ALTER TABLE channel_member
    ADD COLUMN IF NOT EXISTS muted_at TIMESTAMPTZ;

ALTER TABLE dm_peer_state
    ADD COLUMN IF NOT EXISTS muted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_channel_member_user_muted
    ON channel_member (workspace_id, member_type, member_id, muted_at);

CREATE INDEX IF NOT EXISTS idx_dm_peer_state_user_muted
    ON dm_peer_state (workspace_id, user_id, muted_at);
