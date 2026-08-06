-- The Raft-aligned Activity contract is intentionally separate from the
-- historical agent_activity_event table. This migration does not backfill or
-- translate old rows: #2424 removes that old representation in one hard cut.

CREATE TABLE agent_activity_snapshot (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    daemon_id TEXT NOT NULL CHECK (length(btrim(daemon_id)) BETWEEN 1 AND 200),
    daemon_instance_id TEXT NOT NULL CHECK (length(btrim(daemon_instance_id)) BETWEEN 1 AND 200),
    launch_id TEXT NOT NULL CHECK (length(btrim(launch_id)) BETWEEN 1 AND 200),
    process_instance_id TEXT NOT NULL DEFAULT '' CHECK (length(process_instance_id) <= 200),
    provider_session_id TEXT NOT NULL DEFAULT '' CHECK (length(provider_session_id) <= 200),
    turn_id TEXT NOT NULL DEFAULT '' CHECK (length(turn_id) <= 200),
    runtime_generation BIGINT NOT NULL DEFAULT 0 CHECK (runtime_generation >= 0),
    client_sequence BIGINT NOT NULL CHECK (client_sequence > 0),
    producer_fact_id TEXT NOT NULL CHECK (length(btrim(producer_fact_id)) BETWEEN 1 AND 200),
    probe_id TEXT NOT NULL DEFAULT '' CHECK (length(probe_id) <= 200),
    activity_kind TEXT NOT NULL CHECK (activity_kind IN ('online', 'thinking', 'working', 'error', 'offline')),
    detail_kind TEXT NOT NULL DEFAULT '' CHECK (length(detail_kind) <= 120),
    observed_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, agent_id)
);

CREATE INDEX agent_activity_snapshot_stale_idx
    ON agent_activity_snapshot (workspace_id, activity_kind, observed_at)
    WHERE activity_kind IN ('thinking', 'working');

CREATE TABLE agent_activity_entry (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    runtime_id UUID REFERENCES agent_runtime(id) ON DELETE SET NULL,
    daemon_id TEXT NOT NULL CHECK (length(btrim(daemon_id)) BETWEEN 1 AND 200),
    daemon_instance_id TEXT NOT NULL CHECK (length(btrim(daemon_instance_id)) BETWEEN 1 AND 200),
    launch_id TEXT NOT NULL CHECK (length(btrim(launch_id)) BETWEEN 1 AND 200),
    process_instance_id TEXT NOT NULL DEFAULT '' CHECK (length(process_instance_id) <= 200),
    client_sequence BIGINT NOT NULL CHECK (client_sequence > 0),
    producer_fact_id TEXT NOT NULL CHECK (length(btrim(producer_fact_id)) BETWEEN 1 AND 200),
    entry_position INTEGER NOT NULL CHECK (entry_position >= 0 AND entry_position < 64),
    entry_kind TEXT NOT NULL CHECK (length(btrim(entry_kind)) BETWEEN 1 AND 120),
    entry_body JSONB NOT NULL CHECK (jsonb_typeof(entry_body) = 'object' AND octet_length(entry_body::text) <= 65536),
    observed_at TIMESTAMPTZ NOT NULL,
    received_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (workspace_id, agent_id, launch_id, producer_fact_id, entry_position)
);

CREATE INDEX agent_activity_entry_timeline_idx
    ON agent_activity_entry (workspace_id, agent_id, observed_at DESC, id DESC);

CREATE INDEX agent_activity_entry_launch_idx
    ON agent_activity_entry (workspace_id, agent_id, launch_id, client_sequence);
