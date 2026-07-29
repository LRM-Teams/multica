-- env_checkpoint_lane serves two purposes at once, and both are the database's
-- job rather than the application's. The UNIQUE (checkpoint_id, lane_key) index
-- IS the per-lane idempotency mechanism, so concurrent resume of the same lane
-- needs no application lock: one claim inserts, the other gets no row back. The
-- status column IS the crash-recovery mechanism, so a sandbox orphaned midway
-- through materialization still has a discoverable owner.
--
-- The per-step ids are filled in as materialization advances, which lets a lane
-- interrupted partway be continued from its first unfilled step instead of
-- being redone or duplicated. They are nullable for exactly that reason.
--
-- The conversation ids (channel, chat session, source message) are per-lane and
-- not shared with the source: lanes that posted into one channel would not be
-- independent continuations. They are recorded here rather than derived, because
-- a lane interrupted between copying its channel and starting its run would
-- otherwise copy a second channel on recovery.
CREATE TABLE IF NOT EXISTS env_checkpoint_lane (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    checkpoint_id UUID NOT NULL REFERENCES env_checkpoint(id) ON DELETE CASCADE,
    workspace_id UUID NOT NULL,
    lane_key TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'provisioning'
        CHECK (status IN ('provisioning', 'ready', 'failed')),
    instance_id UUID,
    project_id UUID,
    runtime_id UUID,
    task_id UUID,
    env_id UUID,
    channel_id UUID,
    chat_session_id UUID,
    source_message_id UUID,
    error TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (checkpoint_id, lane_key)
);

-- Supports the sweeper that finds lanes stuck provisioning. Partial, because
-- only provisioning lanes are ever swept.
CREATE INDEX IF NOT EXISTS env_checkpoint_lane_provisioning_idx
    ON env_checkpoint_lane (updated_at)
    WHERE status = 'provisioning';
