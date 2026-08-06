-- Pending probes are separate from the replaceable Snapshot. This lets the
-- server distinguish ordinary Activity from a valid probe response and reject
-- a late response after its Runner has been fenced offline.
CREATE TABLE agent_activity_probe (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    agent_id UUID NOT NULL REFERENCES agent(id) ON DELETE CASCADE,
    daemon_id TEXT NOT NULL CHECK (length(btrim(daemon_id)) BETWEEN 1 AND 200),
    daemon_instance_id TEXT NOT NULL CHECK (length(btrim(daemon_instance_id)) BETWEEN 1 AND 200),
    launch_id TEXT NOT NULL CHECK (length(btrim(launch_id)) BETWEEN 1 AND 200),
    probe_id TEXT NOT NULL CHECK (length(btrim(probe_id)) BETWEEN 1 AND 200),
    sent_at TIMESTAMPTZ NOT NULL,
    deadline_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, agent_id),
    CHECK (deadline_at > sent_at)
);

CREATE INDEX agent_activity_probe_deadline_idx ON agent_activity_probe (deadline_at);
