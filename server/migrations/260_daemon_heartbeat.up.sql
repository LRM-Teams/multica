-- "Computer online" needs a heartbeat signal that belongs to the daemon
-- itself, independent of any single agent_runtime row. Without this, the
-- frontend has been aggregating per-runtime last_seen_at into a "machine"
-- status, which answers a different question ("is any runtime on this
-- machine alive") and disagrees with a real connectivity signal whenever a
-- machine is connected but has no live runtime (task #57 follow-up,
-- 2026-08-01: the s144 "Active now / Offline" contradiction).
CREATE TABLE daemon_heartbeat (
    workspace_id UUID NOT NULL REFERENCES workspace(id) ON DELETE CASCADE,
    daemon_id TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (workspace_id, daemon_id)
);

-- daemon_connection was created in 001_init and altered in 003_task_context
-- but never wired into any query path (zero sqlc references, zero rows in
-- production as of 2026-08-01). Its shape also embeds the same mistake this
-- migration fixes: it FKs to a single agent_id, conflating "this daemon is
-- connected" with "this one agent's runtime is connected". Dropping it
-- rather than reusing it so the next person doesn't find two heartbeat
-- tables and assume one of them is already wired up.
DROP TABLE daemon_connection;
